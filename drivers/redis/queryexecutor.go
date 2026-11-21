// Translator from orm.Query to Redis.
//
// Redis has neither secondary indexes nor a query language, so WHERE is
// emulated as a deliberate trade-off (chosen explicitly, see the discussion
// in the README): every "table" keeps a SET of all its ids, and every
// Select iterates over ALL ids of the table, filtering in the application,
// i.e. O(n) per query. That is fine for dev and small datasets and
// fundamentally unsuitable for production load with large tables; a
// production setup needs hand-maintained secondary indexes (a SET/SORTED SET
// per indexed field), which this version does not include.
//
// Storage layout:
//
//	<table>:<id>       -> JSON document of the record (map[string]any keyed by db tags)
//	<table>:__ids__     -> Redis SET of all ids in the table (for iteration)
//	<table>:__seq__      -> Redis STRING counter (INCR) for auto-increment ids
package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"orm"
)

func recordKey(table, id string) string { return table + ":" + id }
func idsKey(table string) string        { return table + ":__ids__" }
func seqKey(table string) string        { return table + ":__seq__" }

// ---- orm.QueryExecutor ----

func (c *Conn) Select(ctx context.Context, q *orm.Query, dest any) error {
	records, err := c.scanMatching(ctx, q.Table, q.Wheres)
	if err != nil {
		return err
	}

	sortRecords(records, q.OrderBy)
	records = applyLimitOffset(records, q.Limit, q.Offset)

	v := reflect.ValueOf(dest)
	if v.Kind() != reflect.Ptr {
		return fmt.Errorf("redis: Select expects a pointer, got %T", dest)
	}
	if v.Elem().Kind() == reflect.Slice {
		return orm.FillSlice(dest, records)
	}
	if len(records) == 0 {
		return orm.ErrNotFound
	}
	return orm.FillStruct(dest, records[0])
}

func (c *Conn) Insert(ctx context.Context, q *orm.Query) (orm.Result, error) {
	record := map[string]any{}
	for k, v := range q.Values {
		record[k] = v
	}
	// A key set by the application is kept and names the record (the
	// counter moves past a numeric one); otherwise the table's counter
	// assigns one. The record's "id" is the name of its Redis key.
	key := q.KeyColumn()
	var id int64
	idStr := ""
	if v, ok := record[key]; ok && v != nil && !reflect.ValueOf(v).IsZero() {
		idStr = idString(v)
		if n, ok := intKey(v); ok {
			if err := raiseSeq.Run(ctx, c.client, []string{seqKey(q.Table)}, n).Err(); err != nil && err != goredis.Nil {
				return orm.Result{}, err
			}
		}
	} else {
		next, err := c.client.Incr(ctx, seqKey(q.Table)).Result()
		if err != nil {
			return orm.Result{}, err
		}
		id, idStr = next, strconv.FormatInt(next, 10)
		record[key] = id
	}
	if key != "id" {
		record["id"] = idStr
	}
	normalizeTimes(record)

	data, err := json.Marshal(record)
	if err != nil {
		return orm.Result{}, err
	}

	if err := c.client.Set(ctx, recordKey(q.Table, idStr), data, 0).Err(); err != nil {
		return orm.Result{}, err
	}
	if err := c.client.SAdd(ctx, idsKey(q.Table), idStr).Err(); err != nil {
		return orm.Result{}, err
	}
	return orm.Result{LastInsertID: id, RowsAffected: 1}, nil
}

func (c *Conn) Update(ctx context.Context, q *orm.Query) (orm.Result, error) {
	records, err := c.scanMatching(ctx, q.Table, q.Wheres)
	if err != nil {
		return orm.Result{}, err
	}

	for _, record := range records {
		for k, v := range q.Values {
			record[k] = v
		}
		normalizeTimes(record)
		data, err := json.Marshal(record)
		if err != nil {
			return orm.Result{}, err
		}
		idStr := idString(record["id"])
		if err := c.client.Set(ctx, recordKey(q.Table, idStr), data, 0).Err(); err != nil {
			return orm.Result{}, err
		}
	}
	return orm.Result{RowsAffected: int64(len(records))}, nil
}

func (c *Conn) Delete(ctx context.Context, q *orm.Query) (orm.Result, error) {
	records, err := c.scanMatching(ctx, q.Table, q.Wheres)
	if err != nil {
		return orm.Result{}, err
	}

	for _, record := range records {
		idStr := idString(record["id"])
		if err := c.client.Del(ctx, recordKey(q.Table, idStr)).Err(); err != nil {
			return orm.Result{}, err
		}
		if err := c.client.SRem(ctx, idsKey(q.Table), idStr).Err(); err != nil {
			return orm.Result{}, err
		}
	}
	return orm.Result{RowsAffected: int64(len(records))}, nil
}

// scanMatching is the honest O(n) scan: it reads the table's id set and
// fetches the JSON document for each id, filtering by WHERE in the
// application (not on the Redis side).
func (c *Conn) scanMatching(ctx context.Context, table string, wheres []orm.Condition) ([]map[string]any, error) {
	if err := checkOps(wheres); err != nil {
		return nil, err
	}

	ids, err := c.client.SMembers(ctx, idsKey(table)).Result()
	if err != nil && err != goredis.Nil {
		return nil, err
	}

	var matched []map[string]any
	for _, id := range ids {
		raw, err := c.client.Get(ctx, recordKey(table, id)).Result()
		if err == goredis.Nil {
			continue // the record was deleted between SMEMBERS and GET; skip it
		}
		if err != nil {
			return nil, err
		}

		var record map[string]any
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return nil, err
		}
		if matchesWhere(record, wheres) {
			matched = append(matched, record)
		}
	}
	return matched, nil
}

// idString returns the record id as used in the "<table>:<id>" key. JSON yields
// numbers as float64, and fmt.Sprint(1e6) == "1e+06" would not match the real key.
func idString(v any) string {
	switch n := v.(type) {
	case float64:
		return strconv.FormatFloat(n, 'f', -1, 64)
	case json.Number:
		return n.String()
	default:
		return fmt.Sprint(v)
	}
}

// ---- WHERE in the application ----

// matchesWhere evaluates conditions with the precedence of SQL (AND binds
// tighter than OR, orm.SplitOr); groups nest.
func matchesWhere(record map[string]any, wheres []orm.Condition) bool {
	if len(wheres) == 0 {
		return true
	}
	for _, and := range orm.SplitOr(wheres) {
		ok := true
		for _, c := range and {
			if !evalCondition(record, c) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func checkOps(wheres []orm.Condition) error {
	for _, c := range wheres {
		if c.Group != nil {
			if err := checkOps(c.Group); err != nil {
				return err
			}
			continue
		}
		if _, null := orm.IsNullOp(c.Op); !null && !supportedOps[normOp(c.Op)] {
			return fmt.Errorf("redis: operator %q is not supported", c.Op)
		}
	}
	return nil
}

// Count is orm.Counter: the matching records of the scan.
func (c *Conn) Count(ctx context.Context, q *orm.Query) (int64, error) {
	records, err := c.scanMatching(ctx, q.Table, q.Wheres)
	return int64(len(records)), err
}

var _ orm.Counter = (*Conn)(nil)

// supportedOps lists the operators evalCondition understands (after normOp).
var supportedOps = map[string]bool{
	"=": true, "!=": true, "<>": true, ">": true, "<": true, ">=": true, "<=": true,
	"LIKE": true, "IN": true, "NOT IN": true,
}

func normOp(op string) string { return strings.ToUpper(strings.TrimSpace(op)) }

func evalCondition(record map[string]any, c orm.Condition) bool {
	if c.Group != nil {
		return matchesWhere(record, c.Group)
	}
	val, ok := record[c.Column]
	if isNull, nullOp := orm.IsNullOp(c.Op); nullOp {
		return (!ok || val == nil) == isNull
	}
	if !ok {
		return false
	}
	switch normOp(c.Op) {
	case "=":
		return compareEqual(val, c.Value)
	case "!=", "<>":
		return !compareEqual(val, c.Value)
	case ">":
		return compareNumeric(val, c.Value) > 0
	case "<":
		return compareNumeric(val, c.Value) < 0
	case ">=":
		return compareNumeric(val, c.Value) >= 0
	case "<=":
		return compareNumeric(val, c.Value) <= 0
	case "LIKE":
		return matchLike(fmt.Sprint(val), fmt.Sprint(c.Value))
	case "IN":
		return matchIn(val, c.Value)
	case "NOT IN":
		return !matchIn(val, c.Value)
	default:
		return false
	}
}

func compareEqual(a, b any) bool {
	af, aok := toFloatSafe(a)
	bf, bok := toFloatSafe(b)
	if aok && bok {
		return af == bf
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func compareNumeric(a, b any) int {
	af, _ := toFloatSafe(a)
	bf, _ := toFloatSafe(b)
	switch {
	case af < bf:
		return -1
	case af > bf:
		return 1
	default:
		return 0
	}
}

func toFloatSafe(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// matchLike converts an SQL LIKE pattern (% and _) to a regex.
func matchLike(value, pattern string) bool {
	var b strings.Builder
	b.WriteString("^")
	for _, r := range pattern {
		switch r {
		case '%':
			b.WriteString(".*")
		case '_':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile("(?i)" + b.String())
	if err != nil {
		return false
	}
	return re.MatchString(value)
}

func matchIn(val, list any) bool {
	lv := reflect.ValueOf(list)
	if lv.Kind() != reflect.Slice && lv.Kind() != reflect.Array {
		return compareEqual(val, list) // IN with a scalar works as in SQL: col IN (v)
	}
	for i := 0; i < lv.Len(); i++ {
		if compareEqual(val, lv.Index(i).Interface()) {
			return true
		}
	}
	return false
}

// ---- sorting and LIMIT/OFFSET in the application ----

func sortRecords(records []map[string]any, orderBy []string) {
	if len(orderBy) == 0 {
		return
	}
	type sortKey struct {
		column string
		desc   bool
	}
	var keys []sortKey
	for _, o := range orderBy {
		parts := strings.Fields(o)
		if len(parts) == 0 {
			continue
		}
		keys = append(keys, sortKey{column: parts[0], desc: len(parts) > 1 && strings.EqualFold(parts[1], "DESC")})
	}

	sort.SliceStable(records, func(i, j int) bool {
		for _, k := range keys {
			cmp := compareNumeric(records[i][k.column], records[j][k.column])
			if cmp == 0 {
				// numeric comparison did not distinguish the values; compare as strings
				si, sj := fmt.Sprint(records[i][k.column]), fmt.Sprint(records[j][k.column])
				if si == sj {
					continue
				}
				if k.desc {
					return si > sj
				}
				return si < sj
			}
			if k.desc {
				return cmp > 0
			}
			return cmp < 0
		}
		return false
	})
}

func applyLimitOffset(records []map[string]any, limit, offset int) []map[string]any {
	if offset > 0 {
		if offset >= len(records) {
			return nil
		}
		records = records[offset:]
	}
	if limit > 0 && limit < len(records) {
		records = records[:limit]
	}
	return records
}

var _ orm.QueryExecutor = (*Conn)(nil)

// raiseSeq sets the counter to at least ARGV[1].
var raiseSeq = goredis.NewScript(`
local cur = tonumber(redis.call('GET', KEYS[1]) or '0')
if tonumber(ARGV[1]) > cur then redis.call('SET', KEYS[1], ARGV[1]) end
return 0`)

func intKey(v any) (int64, bool) {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32:
		return int64(rv.Uint()), true
	}
	return 0, false
}

// timeLayout is fixed-width UTC, so stored times sort as strings
// (OrderBy, Latest) the way they sort as times.
const timeLayout = "2006-01-02T15:04:05.000000000Z"

// normalizeTimes stores time.Time and *time.Time values in timeLayout.
func normalizeTimes(record map[string]any) {
	for k, v := range record {
		switch t := v.(type) {
		case time.Time:
			record[k] = t.UTC().Format(timeLayout)
		case *time.Time:
			if t != nil {
				record[k] = t.UTC().Format(timeLayout)
			}
		}
	}
}
