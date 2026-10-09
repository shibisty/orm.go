package orm_test

import (
	"fmt"
	"reflect"
	"testing"

	"orm"
)

// pgDialect is a Postgres-style dialect ("..." and $N); myDialect is MySQL-style (`...` and ?).
type pgDialect struct{}

func (pgDialect) Name() string             { return "pg" }
func (pgDialect) Quote(s string) string    { return `"` + s + `"` }
func (pgDialect) Placeholder(n int) string { return fmt.Sprintf("$%d", n) }
func (d pgDialect) BuildSelect(q *orm.Query) (string, []any) {
	return orm.BuildSelectGeneric(d, q)
}
func (d pgDialect) BuildInsert(q *orm.Query) (string, []any) {
	return orm.BuildInsertGeneric(d, q)
}
func (d pgDialect) BuildUpdate(q *orm.Query) (string, []any) {
	return orm.BuildUpdateGeneric(d, q)
}
func (d pgDialect) BuildDelete(q *orm.Query) (string, []any) {
	return orm.BuildDeleteGeneric(d, q)
}

type myDialect struct{ pgDialect }

func (myDialect) Quote(s string) string  { return "`" + s + "`" }
func (myDialect) Placeholder(int) string { return "?" }
func (d myDialect) BuildSelect(q *orm.Query) (string, []any) {
	return orm.BuildSelectGeneric(d, q)
}

func TestBuildSelect(t *testing.T) {
	cases := []struct {
		name     string
		q        *orm.Query
		wantSQL  string
		wantArgs []any
	}{
		{
			"all",
			orm.NewQuery("users"),
			`SELECT * FROM "users"`, nil,
		},
		{
			"columns, where, order, limit",
			orm.NewQuery("users").Select("id", "name").Where("age", ">", 18).Where("active", "=", true).
				OrderByDesc("created_at").OrderByAsc("id").LimitOffset(10, 20),
			`SELECT "id", "name" FROM "users" WHERE "age" > $1 AND "active" = $2 ORDER BY "created_at" DESC, "id" ASC LIMIT 10 OFFSET 20`,
			[]any{18, true},
		},
		{
			"or",
			orm.NewQuery("users").Where("role", "=", "admin").OrWhere("role", "=", "owner"),
			`SELECT * FROM "users" WHERE "role" = $1 OR "role" = $2`,
			[]any{"admin", "owner"},
		},
		{
			"join with table-qualified columns",
			orm.NewQuery("users").Select("users.*", "orders.total").Join("LEFT", "orders", "orders.user_id = users.id").
				Where("orders.total", ">=", 100),
			`SELECT "users".*, "orders"."total" FROM "users" LEFT JOIN "orders" ON orders.user_id = users.id WHERE "orders"."total" >= $1`,
			[]any{100},
		},
		{
			"in and not in",
			orm.NewQuery("users").Where("id", "in", []int{1, 2, 3}).Where("status", "NOT IN", []string{"banned"}).Where("age", ">", 1),
			`SELECT * FROM "users" WHERE "id" IN ($1, $2, $3) AND "status" NOT IN ($4) AND "age" > $5`,
			[]any{1, 2, 3, "banned", 1},
		},
		{
			"empty in matches nothing, empty not in matches all",
			orm.NewQuery("users").Where("id", "IN", []int{}).OrWhere("id", "NOT IN", []int64{}),
			`SELECT * FROM "users" WHERE 1 = 0 OR 1 = 1`, nil,
		},
		{
			"bytes are one value, not a list",
			orm.NewQuery("files").Where("hash", "IN", []byte{1, 2}),
			`SELECT * FROM "files" WHERE "hash" IN ($1)`,
			[]any{[]byte{1, 2}},
		},
		{
			"in with a scalar gets parentheses",
			orm.NewQuery("users").Where("id", "IN", 5),
			`SELECT * FROM "users" WHERE "id" IN ($1)`,
			[]any{5},
		},
		{
			"group by",
			func() *orm.Query {
				q := orm.NewQuery("orders").Select("user_id")
				q.GroupBy = []string{"user_id"}
				return q
			}(),
			`SELECT "user_id" FROM "orders" GROUP BY "user_id"`, nil,
		},
		{
			"order by is quoted: no injection through column name",
			orm.NewQuery("users").OrderByAsc("name; DROP TABLE users"),
			`SELECT * FROM "users" ORDER BY "name; DROP TABLE users" ASC`, nil,
		},
		{
			"raw order without direction gets ASC",
			&orm.Query{Table: "users", Columns: []string{"*"}, OrderBy: []string{"name"}},
			`SELECT * FROM "users" ORDER BY "name" ASC`, nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sql, args := pgDialect{}.BuildSelect(tc.q)
			if sql != tc.wantSQL {
				t.Errorf("SQL\n got: %s\nwant: %s", sql, tc.wantSQL)
			}
			if !reflect.DeepEqual(args, tc.wantArgs) {
				t.Errorf("args = %#v, want %#v", args, tc.wantArgs)
			}
		})
	}
}

func TestBuildSelectMySQLPlaceholders(t *testing.T) {
	sql, args := myDialect{}.BuildSelect(orm.NewQuery("users").Where("id", "IN", []int{1, 2}).Where("name", "LIKE", "a%"))
	want := "SELECT * FROM `users` WHERE `id` IN (?, ?) AND `name` LIKE ?"
	if sql != want || len(args) != 3 {
		t.Fatalf("got %s %v", sql, args)
	}
}

// INSERT and UPDATE: columns in a stable (alphabetical) order; placeholder
// numbering continues into WHERE.
func TestBuildInsertUpdateDelete(t *testing.T) {
	d := pgDialect{}
	values := map[string]any{"name": "Ann", "email": "a@x", "age": 30}

	for i := 0; i < 20; i++ { // map order is random; check stability
		sql, args := d.BuildInsert(&orm.Query{Table: "users", Values: values})
		if sql != `INSERT INTO "users" ("age", "email", "name") VALUES ($1, $2, $3)` {
			t.Fatalf("insert: %s", sql)
		}
		if !reflect.DeepEqual(args, []any{30, "a@x", "Ann"}) {
			t.Fatalf("insert args: %v", args)
		}
	}

	q := &orm.Query{Table: "users", Values: map[string]any{"name": "Bob", "age": 31}}
	q.Where("id", "=", 7).Where("tenant", "IN", []int{1, 2})
	sql, args := d.BuildUpdate(q)
	if sql != `UPDATE "users" SET "age" = $1, "name" = $2 WHERE "id" = $3 AND "tenant" IN ($4, $5)` {
		t.Fatalf("update: %s", sql)
	}
	if !reflect.DeepEqual(args, []any{31, "Bob", 7, 1, 2}) {
		t.Fatalf("update args: %v", args)
	}

	sql, args = d.BuildDelete(orm.NewQuery("users").Where("id", "=", 7))
	if sql != `DELETE FROM "users" WHERE "id" = $1` || !reflect.DeepEqual(args, []any{7}) {
		t.Fatalf("delete: %s %v", sql, args)
	}
	sql, _ = d.BuildDelete(orm.NewQuery("users"))
	if sql != `DELETE FROM "users"` {
		t.Fatalf("delete all: %s", sql)
	}
}

func TestQuoteIdent(t *testing.T) {
	d := pgDialect{}
	for in, want := range map[string]string{
		"*":           "*",
		"id":          `"id"`,
		"users.id":    `"users"."id"`,
		"users.*":     `"users".*`,
		"s.users.id":  `"s"."users"."id"`,
		"created_at":  `"created_at"`,
		"weird name":  `"weird name"`,
		"orders.user": `"orders"."user"`,
	} {
		if got := orm.QuoteIdent(d, in); got != want {
			t.Errorf("QuoteIdent(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestHomogeneousConditions(t *testing.T) {
	and := orm.Condition{Bool: "AND"}
	or := orm.Condition{Bool: "OR"}
	cases := []struct {
		conds []orm.Condition
		want  bool
	}{
		{nil, true},
		{[]orm.Condition{or}, true},
		{[]orm.Condition{and, and, and}, true},
		{[]orm.Condition{and, or, or}, true}, // the Bool of the first condition is ignored
		{[]orm.Condition{and, and, or}, false},
	}
	for i, tc := range cases {
		if got := orm.HomogeneousConditions(tc.conds); got != tc.want {
			t.Errorf("case %d: got %v", i, got)
		}
	}
}
