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
	id, err := c.client.Incr(ctx, seqKey(q.Table)).Result()
	if err != nil {
		return orm.Result{}, err
	}

	record := map[string]any{"id": id}
	for k, v := range q.Values {
		record[k] = v
	}

	data, err := json.Marshal(record)
	if err != nil {
		return orm.Result{}, err
	}

	idStr := strconv.FormatInt(id, 10)
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
	if !orm.HomogeneousConditions(wheres) {
		return nil, fmt.Errorf("redis: mixing AND/OR in one Query is not supported; split it into several queries")
	}
	for _, c := range wheres {
		if !supportedOps[normOp(c.Op)] {
			return nil, fmt.Errorf("redis: operator %q is not supported", c.Op)
		}
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

func matchesWhere(record map[string]any, wheres []orm.Condition) bool {
	if len(wheres) == 0 {
		return true
	}
	result := evalCondition(record, wheres[0])
	for _, c := range wheres[1:] {
		v := evalCondition(record, c)
		if strings.EqualFold(c.Bool, "OR") {
			result = result || v
		} else {
			result = result && v
		}
	}
	return result
}

// supportedOps lists the operators evalCondition understands (after normOp).
var supportedOps = map[string]bool{
	"=": true, "!=": true, "<>": true, ">": true, "<": true, ">=": true, "<=": true,
	"LIKE": true, "IN": true, "NOT IN": true,
}

func normOp(op string) string { return strings.ToUpper(strings.TrimSpace(op)) }

func evalCondition(record map[string]any, c orm.Condition) bool {
	val, ok := record[c.Column]
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
