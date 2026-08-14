package core

import "fmt"

// Condition — одно условие WHERE.
type Condition struct {
	Bool   string // "AND" / "OR"
	Column string
	Op     string // "=", ">", "<", "IN", "LIKE" ...
	Value  any
}

// Join — одно JOIN-условие.
type Join struct {
	Type  string // "INNER", "LEFT", "RIGHT"
	Table string
	On    string // "orders.user_id = users.id"
}

// Query — драйвер-независимое AST-представление SELECT/INSERT/UPDATE/DELETE.
// Строит его QueryBuilder (см. querybuilder), а конкретный Dialect
// (mysql.Dialect{}, postgres.Dialect{}) превращает в реальный SQL + список аргументов.
type Query struct {
	Table   string
	Columns []string
	Wheres  []Condition
	Joins   []Join
	OrderBy []string
	GroupBy []string
	Limit   int
	Offset  int

	// Для INSERT/UPDATE
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

// Dialect инкапсулирует всё, чем реально отличаются диалекты SQL:
// плейсхолдеры ($1 vs ?), кавычки идентификаторов, LIMIT/OFFSET синтаксис и т.п.
// Каждый реляционный драйвер (mysql, postgres) предоставляет свою реализацию.
type Dialect interface {
	Name() string
	Quote(identifier string) string
	// Placeholder возвращает плейсхолдер для позиции n (1-based).
	// Postgres: $1, $2...; MySQL/SQLite: ? ? ?
	Placeholder(n int) string

	BuildSelect(q *Query) (sql string, args []any)
	BuildInsert(q *Query) (sql string, args []any)
	BuildUpdate(q *Query) (sql string, args []any)
	BuildDelete(q *Query) (sql string, args []any)
}

// baseBuildSelect — общая логика построения SELECT, которую диалекты могут
// переиспользовать, подставляя свой Quote/Placeholder.
func BuildSelectGeneric(d Dialect, q *Query) (string, []any) {
	sql := "SELECT "
	for i, c := range q.Columns {
		if i > 0 {
			sql += ", "
		}
		if c == "*" {
			sql += "*"
		} else {
			sql += d.Quote(c)
		}
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
			sql += d.Quote(g)
		}
	}

	if len(q.OrderBy) > 0 {
		sql += " ORDER BY "
		for i, o := range q.OrderBy {
			if i > 0 {
				sql += ", "
			}
			sql += o
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

// HomogeneousConditions возвращает true, если все условия, начиная со
// второго, используют один и тот же логический оператор (все AND либо все
// OR). Реляционные драйверы полагаются на precedence самой СУБД (AND
// связывает крепче OR), а NoSQL-драйверы (mongodb, redis) строят фильтр
// вручную и не умеют это precedence воспроизвести — поэтому они
// поддерживают только однородные условия и возвращают ошибку для смешанных.
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
		sql += fmt.Sprintf("%s %s %s", d.Quote(c.Column), c.Op, d.Placeholder(pos))
		args = append(args, c.Value)
		pos++
	}
	return sql, args
}

func BuildInsertGeneric(d Dialect, q *Query) (string, []any) {
	cols := make([]string, 0, len(q.Values))
	args := make([]any, 0, len(q.Values))
	for k, v := range q.Values {
		cols = append(cols, k)
		args = append(args, v)
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
	cols := make([]string, 0, len(q.Values))
	args := make([]any, 0, len(q.Values)+len(q.Wheres))
	for k, v := range q.Values {
		cols = append(cols, k)
		args = append(args, v)
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
