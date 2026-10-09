package orm

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Condition is a single WHERE condition.
type Condition struct {
	Bool   string // "AND" / "OR"
	Column string
	Op     string // "=", ">", "<", "IN", "LIKE" ...
	Value  any
}

// Join is a single JOIN clause.
type Join struct {
	Type  string // "INNER", "LEFT", "RIGHT"
	Table string
	On    string // "orders.user_id = users.id"
}

// Query is a driver-independent AST representation of SELECT/INSERT/UPDATE/DELETE.
// It is built by the Query methods (Where, OrderByAsc…), and a concrete Dialect
// (mysql.Dialect{}, postgres.Dialect{}) turns it into real SQL plus an argument list.
type Query struct {
	Table   string
	Columns []string
	Wheres  []Condition
	Joins   []Join
	OrderBy []string
	GroupBy []string
	Limit   int
	Offset  int

	// For INSERT/UPDATE
	Values map[string]any
}

func NewQuery(table string) *Query {
	return &Query{Table: table, Columns: []string{"*"}}
}

func (q *Query) Select(cols ...string) *Query {
	q.Columns = cols
	return q
}

func (q *Query) Where(column, op string, value any) *Query {
	q.Wheres = append(q.Wheres, Condition{Bool: "AND", Column: column, Op: op, Value: value})
	return q
}

func (q *Query) OrWhere(column, op string, value any) *Query {
	q.Wheres = append(q.Wheres, Condition{Bool: "OR", Column: column, Op: op, Value: value})
	return q
}

func (q *Query) Join(joinType, table, on string) *Query {
	q.Joins = append(q.Joins, Join{Type: joinType, Table: table, On: on})
	return q
}

func (q *Query) OrderByAsc(column string) *Query {
	q.OrderBy = append(q.OrderBy, column+" ASC")
	return q
}

func (q *Query) OrderByDesc(column string) *Query {
	q.OrderBy = append(q.OrderBy, column+" DESC")
	return q
}

func (q *Query) LimitOffset(limit, offset int) *Query {
	q.Limit = limit
	q.Offset = offset
	return q
}

// Dialect encapsulates everything that actually differs between SQL dialects:
// placeholders ($1 vs ?), identifier quoting, LIMIT/OFFSET syntax, etc.
// Each relational driver (mysql, postgres) provides its own implementation.
type Dialect interface {
	Name() string
	Quote(identifier string) string
	// Placeholder returns the placeholder for position n (1-based).
	// Postgres: $1, $2...; MySQL/SQLite: ? ? ?
	Placeholder(n int) string

	BuildSelect(q *Query) (sql string, args []any)
	BuildInsert(q *Query) (sql string, args []any)
	BuildUpdate(q *Query) (sql string, args []any)
	BuildDelete(q *Query) (sql string, args []any)
}

// baseBuildSelect is the shared SELECT building logic that dialects can
// reuse by plugging in their own Quote/Placeholder.
func BuildSelectGeneric(d Dialect, q *Query) (string, []any) {
	sql := "SELECT "
	for i, c := range q.Columns {
		if i > 0 {
			sql += ", "
		}
		sql += QuoteIdent(d, c)
	}
	sql += " FROM " + d.Quote(q.Table)

	for _, j := range q.Joins {
		sql += fmt.Sprintf(" %s JOIN %s ON %s", j.Type, d.Quote(j.Table), j.On)
	}

	var args []any
	whereSQL, whereArgs := buildWhere(d, q.Wheres, len(args)+1)
	sql += whereSQL
	args = append(args, whereArgs...)

	if len(q.GroupBy) > 0 {
		sql += " GROUP BY "
		for i, g := range q.GroupBy {
			if i > 0 {
				sql += ", "
			}
			sql += QuoteIdent(d, g)
		}
	}

	if len(q.OrderBy) > 0 {
		sql += " ORDER BY "
		for i, o := range q.OrderBy {
			if i > 0 {
				sql += ", "
			}
			sql += orderClause(d, o)
		}
	}

	if q.Limit > 0 {
		sql += fmt.Sprintf(" LIMIT %d", q.Limit)
	}
	if q.Offset > 0 {
		sql += fmt.Sprintf(" OFFSET %d", q.Offset)
	}

	return sql, args
}

// HomogeneousConditions reports whether all conditions, starting from the
// second, use the same logical operator (all AND or all
// OR). Relational drivers rely on the database's own precedence (AND
// binds tighter than OR), while NoSQL drivers (mongodb, redis) build the filter
// by hand and cannot reproduce that precedence, so they
// support only homogeneous conditions and return an error for mixed ones.
func HomogeneousConditions(conds []Condition) bool {
	if len(conds) <= 1 {
		return true
	}
	b := conds[1].Bool
	for _, c := range conds[1:] {
		if c.Bool != b {
			return false
		}
	}
	return true
}

func buildWhere(d Dialect, conds []Condition, startPos int) (string, []any) {
	if len(conds) == 0 {
		return "", nil
	}
	sql := " WHERE "
	var args []any
	pos := startPos
	for i, c := range conds {
		if i > 0 {
			sql += " " + c.Bool + " "
		}
		op := strings.ToUpper(strings.TrimSpace(c.Op))
		if (op == "IN" || op == "NOT IN") && isList(c.Value) {
			// IN with a slice: one placeholder per element. An empty slice is a condition
			// that matches nothing (IN) or everything (NOT IN).
			rv := reflect.ValueOf(c.Value)
			if rv.Len() == 0 {
				if op == "IN" {
					sql += "1 = 0"
				} else {
					sql += "1 = 1"
				}
				continue
			}
			ph := make([]string, rv.Len())
			for j := 0; j < rv.Len(); j++ {
				ph[j] = d.Placeholder(pos)
				args = append(args, rv.Index(j).Interface())
				pos++
			}
			sql += fmt.Sprintf("%s %s (%s)", QuoteIdent(d, c.Column), op, strings.Join(ph, ", "))
			continue
		}
		if op == "IN" || op == "NOT IN" {
			// IN with a scalar (or []byte): a single value in parentheses.
			sql += fmt.Sprintf("%s %s (%s)", QuoteIdent(d, c.Column), op, d.Placeholder(pos))
		} else {
			sql += fmt.Sprintf("%s %s %s", QuoteIdent(d, c.Column), c.Op, d.Placeholder(pos))
		}
		args = append(args, c.Value)
		pos++
	}
	return sql, args
}

// isList reports whether v is a slice or array (except []byte, which is a single value).
func isList(v any) bool {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return false
	}
	k := rv.Kind()
	if k != reflect.Slice && k != reflect.Array {
		return false
	}
	return rv.Type().Elem().Kind() != reflect.Uint8
}

// QuoteIdent quotes an identifier, taking the table into account: users.id → "users"."id".
// "*" and "users.*" leave the asterisk unquoted.
func QuoteIdent(d Dialect, name string) string {
	if name == "*" {
		return name
	}
	parts := strings.Split(name, ".")
	for i, p := range parts {
		if p != "*" {
			parts[i] = d.Quote(p)
		}
	}
	return strings.Join(parts, ".")
}

// orderClause turns "column ASC" (as written by OrderByAsc/OrderByDesc) into
// a quoted expression. The direction is only ASC or DESC, so a column name
// from user input cannot become an SQL injection.
func orderClause(d Dialect, o string) string {
	col, dir := strings.TrimSpace(o), "ASC"
	if i := strings.LastIndexByte(col, ' '); i > 0 {
		switch strings.ToUpper(col[i+1:]) {
		case "ASC", "DESC":
			col, dir = strings.TrimSpace(col[:i]), strings.ToUpper(col[i+1:])
		}
	}
	return QuoteIdent(d, col) + " " + dir
}

// sortedKeys returns INSERT/UPDATE columns in a stable order: the same SQL
// for the same data (logs, prepared statement caches, tests).
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func BuildInsertGeneric(d Dialect, q *Query) (string, []any) {
	cols := sortedKeys(q.Values)
	args := make([]any, 0, len(cols))
	for _, k := range cols {
		args = append(args, q.Values[k])
	}

	sql := "INSERT INTO " + d.Quote(q.Table) + " ("
	for i, c := range cols {
		if i > 0 {
			sql += ", "
		}
		sql += d.Quote(c)
	}
	sql += ") VALUES ("
	for i := range cols {
		if i > 0 {
			sql += ", "
		}
		sql += d.Placeholder(i + 1)
	}
	sql += ")"
	return sql, args
}

func BuildUpdateGeneric(d Dialect, q *Query) (string, []any) {
	cols := sortedKeys(q.Values)
	args := make([]any, 0, len(cols)+len(q.Wheres))
	for _, k := range cols {
		args = append(args, q.Values[k])
	}

	sql := "UPDATE " + d.Quote(q.Table) + " SET "
	for i, c := range cols {
		if i > 0 {
			sql += ", "
		}
		sql += fmt.Sprintf("%s = %s", d.Quote(c), d.Placeholder(i+1))
	}

	whereSQL, whereArgs := buildWhere(d, q.Wheres, len(cols)+1)
	sql += whereSQL
	args = append(args, whereArgs...)
	return sql, args
}

func BuildDeleteGeneric(d Dialect, q *Query) (string, []any) {
	sql := "DELETE FROM " + d.Quote(q.Table)
	whereSQL, args := buildWhere(d, q.Wheres, 1)
	sql += whereSQL
	return sql, args
}
