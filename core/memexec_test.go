package orm_test

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"orm"
)

// memExec is an in-memory QueryExecutor for tests of the Builder,
// relations, timestamps and soft deletes: tables of rows (column → value),
// conditions with groups and SQL precedence, ORDER BY, LIMIT, OFFSET.
type memExec struct {
	mu      sync.Mutex
	tables  map[string][]map[string]any
	next    map[string]int64
	selects []*orm.Query // every Select, to count queries
}

func newMem() *memExec {
	return &memExec{tables: map[string][]map[string]any{}, next: map[string]int64{}}
}

func (m *memExec) Driver() string             { return "mem" }
func (m *memExec) Ping(context.Context) error { return nil }
func (m *memExec) Close() error               { return nil }
func (m *memExec) queries(table string) (n int) {
	for _, q := range m.selects {
		if q.Table == table {
			n++
		}
	}
	return n
}

func (m *memExec) Insert(_ context.Context, q *orm.Query) (orm.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row := map[string]any{}
	for k, v := range q.Values {
		row[k] = v
	}
	var id int64
	key := q.KeyColumn()
	if v, ok := row[key]; !ok || v == nil || reflect.ValueOf(v).IsZero() {
		m.next[q.Table]++
		id = m.next[q.Table]
		row[key] = id
	} else if n, ok := num(v); ok && int64(n) > m.next[q.Table] {
		m.next[q.Table] = int64(n) // like the drivers' counters
	}
	m.tables[q.Table] = append(m.tables[q.Table], row)
	return orm.Result{LastInsertID: id, RowsAffected: 1}, nil
}

func (m *memExec) match(q *orm.Query) []map[string]any {
	var out []map[string]any
	for _, r := range m.tables[q.Table] {
		if evalAll(r, q.Wheres) {
			out = append(out, r)
		}
	}
	return out
}

func (m *memExec) Select(_ context.Context, q *orm.Query, dest any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.selects = append(m.selects, q)
	rows := m.match(q)
	for i := len(q.OrderBy) - 1; i >= 0; i-- {
		f := strings.Fields(q.OrderBy[i])
		desc := len(f) > 1 && strings.EqualFold(f[1], "DESC")
		sort.SliceStable(rows, func(a, b int) bool {
			c := compare(rows[a][f[0]], rows[b][f[0]])
			if desc {
				return c > 0
			}
			return c < 0
		})
	}
	if q.Offset > 0 {
		if q.Offset >= len(rows) {
			rows = nil
		} else {
			rows = rows[q.Offset:]
		}
	}
	if q.Limit > 0 && len(rows) > q.Limit {
		rows = rows[:q.Limit]
	}
	if reflect.ValueOf(dest).Elem().Kind() == reflect.Slice {
		return orm.FillSlice(dest, rows)
	}
	if len(rows) == 0 {
		return orm.ErrNotFound
	}
	return orm.FillStruct(dest, rows[0])
}

func (m *memExec) Update(_ context.Context, q *orm.Query) (orm.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := m.match(q)
	for _, r := range rows {
		for k, v := range q.Values {
			r[k] = v
		}
	}
	return orm.Result{RowsAffected: int64(len(rows))}, nil
}

func (m *memExec) Delete(_ context.Context, q *orm.Query) (orm.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var keep []map[string]any
	n := 0
	for _, r := range m.tables[q.Table] {
		if evalAll(r, q.Wheres) {
			n++
		} else {
			keep = append(keep, r)
		}
	}
	m.tables[q.Table] = keep
	return orm.Result{RowsAffected: int64(n)}, nil
}

func evalAll(r map[string]any, conds []orm.Condition) bool {
	if len(conds) == 0 {
		return true
	}
	for _, and := range orm.SplitOr(conds) {
		ok := true
		for _, c := range and {
			if !eval(r, c) {
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

func eval(r map[string]any, c orm.Condition) bool {
	if c.Group != nil {
		return evalAll(r, c.Group)
	}
	v := deref(r[c.Column])
	if isNull, ok := orm.IsNullOp(c.Op); ok {
		return (v == nil) == isNull
	}
	switch strings.ToUpper(c.Op) {
	case "=":
		return compare(v, c.Value) == 0
	case "!=", "<>":
		return compare(v, c.Value) != 0
	case ">":
		return compare(v, c.Value) > 0
	case "<":
		return compare(v, c.Value) < 0
	case "IN", "NOT IN":
		in := false
		rv := reflect.ValueOf(c.Value)
		for i := 0; i < rv.Len(); i++ {
			if compare(v, rv.Index(i).Interface()) == 0 {
				in = true
			}
		}
		return in == (strings.ToUpper(c.Op) == "IN")
	}
	panic("memExec: operator " + c.Op)
}

func deref(v any) any {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return nil
		}
		return rv.Elem().Interface()
	}
	return v
}

func compare(a, b any) int {
	a, b = deref(a), deref(b)
	af, aok := num(a)
	bf, bok := num(b)
	if aok && bok {
		switch {
		case af < bf:
			return -1
		case af > bf:
			return 1
		}
		return 0
	}
	return strings.Compare(fmt.Sprint(a), fmt.Sprint(b))
}

func num(v any) (float64, bool) {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint()), true
	case reflect.Float32, reflect.Float64:
		return rv.Float(), true
	}
	return 0, false
}
