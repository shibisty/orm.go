package orm_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"orm"
)

var ctx = context.Background()

type User struct {
	orm.BaseModel
	Name  string `db:"name"`
	Email string `db:"email"`
	Skip  string `db:"-"`
	Plain string
}

func (User) TableName() string { return "users" }

// recExec is an SQLExecutor that records SQL and returns predefined rows.
type recExec struct {
	dialect orm.Dialect
	sql     []string
	args    [][]any
	rows    [][]any // for QueryContext
	row     []any   // for QueryRowContext; nil → sql.ErrNoRows
	execErr error
	qErr    error
	lastID  int64
}

func (r *recExec) Driver() string                          { return "rec" }
func (r *recExec) Ping(context.Context) error              { return nil }
func (r *recExec) Close() error                            { return nil }
func (r *recExec) Dialect() orm.Dialect                    { return r.dialect }
func (r *recExec) BeginTx(context.Context) (orm.Tx, error) { return nil, errors.New("no tx") }

func (r *recExec) record(q string, args []any) {
	r.sql = append(r.sql, q)
	r.args = append(r.args, args)
}

func (r *recExec) ExecContext(_ context.Context, q string, args ...any) (orm.Result, error) {
	r.record(q, args)
	if r.execErr != nil {
		return orm.Result{}, r.execErr
	}
	return orm.Result{LastInsertID: r.lastID, RowsAffected: 1}, nil
}

func (r *recExec) QueryContext(_ context.Context, q string, args ...any) (orm.Rows, error) {
	r.record(q, args)
	if r.qErr != nil {
		return nil, r.qErr
	}
	return &memRows{data: r.rows}, nil
}

func (r *recExec) QueryRowContext(_ context.Context, q string, args ...any) orm.Row {
	r.record(q, args)
	if r.row == nil {
		return memRow{err: sql.ErrNoRows}
	}
	return memRow{vals: r.row}
}

type memRows struct {
	data [][]any
	pos  int
	err  error
}

func (m *memRows) Next() bool { m.pos++; return m.pos <= len(m.data) }
func (m *memRows) Scan(dest ...any) error {
	if m.err != nil {
		return m.err
	}
	return assign(dest, m.data[m.pos-1])
}
func (m *memRows) Err() error   { return nil }
func (m *memRows) Close() error { return nil }

type memRow struct {
	vals []any
	err  error
}

func (m memRow) Scan(dest ...any) error {
	if m.err != nil {
		return m.err
	}
	return assign(dest, m.vals)
}

func assign(dest, vals []any) error {
	if len(dest) != len(vals) {
		return errors.New("scan: column count mismatch")
	}
	for i, v := range vals {
		reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(v))
	}
	return nil
}

func TestSelectOneExpandsStarToStructColumns(t *testing.T) {
	ex := &recExec{dialect: pgDialect{}, row: []any{int64(7), "Ann", "a@x"}}
	repo := orm.NewRepository[User](orm.AsQueryExecutor(ex))

	u, err := repo.Find(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != 7 || u.Name != "Ann" || u.Email != "a@x" {
		t.Fatalf("Find = %+v", u)
	}
	want := `SELECT "id", "name", "email" FROM "users" WHERE "id" = $1 LIMIT 1`
	if ex.sql[0] != want {
		t.Fatalf("SQL\n got: %s\nwant: %s", ex.sql[0], want)
	}
}

// With a JOIN, model columns are qualified with the table name, otherwise "id" is ambiguous.
func TestSelectAllWithJoinQualifiesColumns(t *testing.T) {
	ex := &recExec{dialect: pgDialect{}, rows: [][]any{}}
	repo := orm.NewRepository[User](orm.AsQueryExecutor(ex))
	q := orm.NewQuery("users").Join("INNER", "orders", "orders.user_id = users.id").Where("orders.total", ">", 1)
	if _, err := repo.All(ctx, q); err != nil {
		t.Fatal(err)
	}
	want := `SELECT "users"."id", "users"."name", "users"."email" FROM "users" INNER JOIN "orders" ON orders.user_id = users.id WHERE "orders"."total" > $1`
	if ex.sql[0] != want {
		t.Fatalf("SQL\n got: %s\nwant: %s", ex.sql[0], want)
	}
	if len(q.Columns) != 1 || q.Columns[0] != "*" {
		t.Fatalf("caller's query must not be modified: %v", q.Columns)
	}
}

func TestSelectNotFound(t *testing.T) {
	ex := &recExec{dialect: pgDialect{}}
	_, err := orm.NewRepository[User](orm.AsQueryExecutor(ex)).Find(ctx, 1)
	if !errors.Is(err, orm.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestSelectAllAndExplicitColumns(t *testing.T) {
	ex := &recExec{dialect: pgDialect{}, rows: [][]any{{int64(1), "a", "a@x"}, {int64(2), "b", "b@x"}}}
	repo := orm.NewRepository[User](orm.AsQueryExecutor(ex))

	all, err := repo.All(ctx, nil)
	if err != nil || len(all) != 2 || all[1].Name != "b" {
		t.Fatalf("All = %+v, %v", all, err)
	}
	if !strings.HasPrefix(ex.sql[0], `SELECT "id", "name", "email" FROM "users"`) {
		t.Fatalf("All SQL: %s", ex.sql[0])
	}

	// An explicit column list is left untouched; Table is replaced with the model's table.
	ex.rows = [][]any{}
	q := orm.NewQuery("ignored").Select("id", "name", "email").Where("name", "=", "x")
	got, err := repo.All(ctx, q)
	if err != nil || len(got) != 0 || got == nil {
		t.Fatalf("empty All = %#v, %v (want empty non-nil slice)", got, err)
	}
	if ex.sql[1] != `SELECT "id", "name", "email" FROM "users" WHERE "name" = $1` {
		t.Fatalf("explicit columns SQL: %s", ex.sql[1])
	}
}

func TestSelectErrors(t *testing.T) {
	qe := orm.AsQueryExecutor(&recExec{dialect: pgDialect{}, qErr: errBoom})
	var users []User
	if err := qe.Select(ctx, orm.NewQuery("users"), &users); !errors.Is(err, errBoom) {
		t.Errorf("query error: %v", err)
	}
	var u User
	if err := qe.Select(ctx, orm.NewQuery("users"), u); err == nil {
		t.Error("non-pointer dest should be an error")
	}
	scanErr := orm.AsQueryExecutor(&rowsErrExec{recExec{dialect: pgDialect{}, rows: [][]any{{int64(1), "a", "b"}}}})
	if err := scanErr.Select(ctx, orm.NewQuery("users"), &users); !errors.Is(err, errBoom) {
		t.Errorf("scan error: %v", err)
	}
	rowErr := orm.AsQueryExecutor(&rowErrExec{recExec{dialect: pgDialect{}}})
	if err := rowErr.Select(ctx, orm.NewQuery("users"), &u); !errors.Is(err, errBoom) {
		t.Errorf("row error: %v", err)
	}
}

var errBoom = errors.New("boom")

type rowsErrExec struct{ recExec }

func (r *rowsErrExec) QueryContext(context.Context, string, ...any) (orm.Rows, error) {
	return &memRows{data: r.rows, err: errBoom}, nil
}

type rowErrExec struct{ recExec }

func (r *rowErrExec) QueryRowContext(context.Context, string, ...any) orm.Row {
	return memRow{err: errBoom}
}

func TestCreateUpdateDelete(t *testing.T) {
	ex := &recExec{dialect: pgDialect{}, lastID: 42}
	repo := orm.NewRepository[User](orm.AsQueryExecutor(ex))

	u := User{Name: "Ann", Email: "a@x", Skip: "s", Plain: "p"}
	if err := repo.Create(ctx, &u); err != nil {
		t.Fatal(err)
	}
	if u.ID != 42 {
		t.Fatalf("ID = %d", u.ID)
	}
	if ex.sql[0] != `INSERT INTO "users" ("email", "name") VALUES ($1, $2)` {
		t.Fatalf("insert SQL: %s", ex.sql[0])
	}

	u.Name = "Anna"
	if err := repo.Update(ctx, &u); err != nil {
		t.Fatal(err)
	}
	if ex.sql[1] != `UPDATE "users" SET "email" = $1, "name" = $2 WHERE "id" = $3` ||
		!reflect.DeepEqual(ex.args[1], []any{"a@x", "Anna", int64(42)}) {
		t.Fatalf("update: %s %v", ex.sql[1], ex.args[1])
	}

	if err := repo.Delete(ctx, 42); err != nil {
		t.Fatal(err)
	}
	if ex.sql[2] != `DELETE FROM "users" WHERE "id" = $1` {
		t.Fatalf("delete SQL: %s", ex.sql[2])
	}

	ex.execErr = errBoom
	if err := repo.Create(ctx, &u); !errors.Is(err, errBoom) {
		t.Errorf("Create error: %v", err)
	}
	if err := repo.Update(ctx, &u); !errors.Is(err, errBoom) {
		t.Errorf("Update error: %v", err)
	}
	if err := repo.Delete(ctx, 1); !errors.Is(err, errBoom) {
		t.Errorf("Delete error: %v", err)
	}
}

// returningDialect works like Postgres: the id comes via RETURNING, not LastInsertId.
type returningDialect struct{ pgDialect }

func (returningDialect) InsertReturnsID() bool { return true }
func (d returningDialect) BuildInsert(q *orm.Query) (string, []any) {
	s, a := orm.BuildInsertGeneric(d, q)
	return s + " RETURNING id", a
}

func TestInsertReturningID(t *testing.T) {
	ex := &recExec{dialect: returningDialect{}, row: []any{int64(99)}}
	repo := orm.NewRepository[User](orm.AsQueryExecutor(ex))
	u := User{Name: "Ann"}
	if err := repo.Create(ctx, &u); err != nil {
		t.Fatal(err)
	}
	if u.ID != 99 || !strings.HasSuffix(ex.sql[0], "RETURNING id") {
		t.Fatalf("ID = %d, SQL = %s", u.ID, ex.sql[0])
	}
	ex.row = nil
	if err := repo.Create(ctx, &u); err == nil {
		t.Fatal("scan error should be returned")
	}
}

type NoID struct {
	Name string `db:"name"`
}

func (NoID) TableName() string { return "no_id" }

func TestUpdateWithoutIDField(t *testing.T) {
	repo := orm.NewRepository[NoID](orm.AsQueryExecutor(&recExec{dialect: pgDialect{}}))
	if err := repo.Update(ctx, &NoID{Name: "x"}); err == nil || !strings.Contains(err.Error(), "id") {
		t.Fatalf("want error about missing id field, got %v", err)
	}
	// Create without an id field works (the id is simply not set).
	if err := repo.Create(ctx, &NoID{Name: "x"}); err != nil {
		t.Fatal(err)
	}
}

func TestAdapterPassThrough(t *testing.T) {
	qe := orm.AsQueryExecutor(&recExec{dialect: pgDialect{}})
	if qe.Driver() != "rec" || qe.Ping(ctx) != nil || qe.Close() != nil {
		t.Fatal("adapter should delegate Driver/Ping/Close")
	}
	if _, err := qe.Update(ctx, &orm.Query{Table: "t", Values: map[string]any{"a": 1}}); err != nil {
		t.Fatal(err)
	}
}

type fakeDriver struct {
	conn orm.Connection
	err  error
}

func (d fakeDriver) Open(context.Context) (orm.Connection, error) { return d.conn, d.err }

func TestNew(t *testing.T) {
	ex := &recExec{dialect: pgDialect{}}
	c, err := orm.New(ctx, fakeDriver{conn: ex})
	if err != nil || c != ex {
		t.Fatalf("New = %v, %v", c, err)
	}
	if _, err := orm.New(ctx, fakeDriver{err: errBoom}); !errors.Is(err, errBoom) {
		t.Fatalf("New error: %v", err)
	}
}
