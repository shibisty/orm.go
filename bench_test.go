package orm_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"orm"
)

// benchUser is mapped by reflection; see genUser in bench_gen_test.go for
// the same model with orm-gen mappers.
type benchUser struct {
	orm.BaseModel
	Name   string  `db:"name"`
	Email  string  `db:"email"`
	Age    int64   `db:"age"`
	Active bool    `db:"active"`
	Score  float64 `db:"score"`
	Bio    *string `db:"bio"`
	orm.Timestamps
	orm.SoftDeletes
}

func (benchUser) TableName() string { return "users" }

// genUser is benchUser with the mappers orm-gen generates (orm_gen_test.go,
// regenerated with `go run orm-gen -tests -type genUser` in this directory).
type genUser struct {
	orm.BaseModel
	Name   string  `db:"name"`
	Email  string  `db:"email"`
	Age    int64   `db:"age"`
	Active bool    `db:"active"`
	Score  float64 `db:"score"`
	Bio    *string `db:"bio"`
	orm.Timestamps
	orm.SoftDeletes
}

func (genUser) TableName() string { return "users" }

var benchTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func benchRow(i int) []any {
	bio := "bio"
	return []any{int64(i + 1), fmt.Sprint("user", i), fmt.Sprint("u", i, "@example.com"),
		int64(20 + i%50), i%2 == 0, float64(i) / 3, &bio, benchTime, benchTime, (*time.Time)(nil)}
}

// benchExec returns fixed rows without reflection, so that the benchmarks
// measure the orm's mapping, not the fake driver.
type benchExec struct{ rows [][]any }

func (b *benchExec) Driver() string                          { return "bench" }
func (b *benchExec) Ping(context.Context) error              { return nil }
func (b *benchExec) Close() error                            { return nil }
func (b *benchExec) Dialect() orm.Dialect                    { return pgDialect{} }
func (b *benchExec) BeginTx(context.Context) (orm.Tx, error) { return nil, errors.New("no tx") }
func (b *benchExec) ExecContext(context.Context, string, ...any) (orm.Result, error) {
	return orm.Result{LastInsertID: 1, RowsAffected: 1}, nil
}
func (b *benchExec) QueryContext(context.Context, string, ...any) (orm.Rows, error) {
	return &benchRows{data: b.rows}, nil
}
func (b *benchExec) QueryRowContext(context.Context, string, ...any) orm.Row {
	return &benchRows{data: b.rows, pos: 1}
}

type benchRows struct {
	data [][]any
	pos  int
}

func (r *benchRows) Next() bool   { r.pos++; return r.pos <= len(r.data) }
func (r *benchRows) Err() error   { return nil }
func (r *benchRows) Close() error { return nil }
func (r *benchRows) Scan(dest ...any) error {
	row := r.data[r.pos-1]
	if len(dest) != len(row) {
		return fmt.Errorf("scan: %d targets for %d columns", len(dest), len(row))
	}
	for i, d := range dest {
		switch p := d.(type) {
		case *int64:
			*p = row[i].(int64)
		case *string:
			*p = row[i].(string)
		case *bool:
			*p = row[i].(bool)
		case *float64:
			*p = row[i].(float64)
		case **string:
			*p = row[i].(*string)
		case *time.Time:
			*p = row[i].(time.Time)
		case **time.Time:
			*p = row[i].(*time.Time)
		default:
			return fmt.Errorf("scan: unexpected target %T", d)
		}
	}
	return nil
}

func benchRows100() [][]any {
	rows := make([][]any, 100)
	for i := range rows {
		rows[i] = benchRow(i)
	}
	return rows
}

func benchMaps100() []map[string]any {
	cols := []string{"id", "name", "email", "age", "active", "score", "bio", "created_at", "updated_at", "deleted_at"}
	out := make([]map[string]any, 100)
	for i := range out {
		m := map[string]any{}
		for j, v := range benchRow(i) {
			if j == 3 {
				v = float64(v.(int64)) // JSON numbers, as Redis returns them
			}
			if j == 7 || j == 8 {
				v = benchTime.Format(time.RFC3339Nano)
			}
			m[cols[j]] = v
		}
		out[i] = m
	}
	return out
}

func benchSelect[T orm.Model](b *testing.B) {
	exec := orm.AsQueryExecutor(&benchExec{rows: benchRows100()})
	repo := orm.NewRepository[T](exec)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		users, err := repo.All(ctx, nil)
		if err != nil || len(users) != 100 {
			b.Fatal(err, len(users))
		}
	}
}

func benchFind[T orm.Model](b *testing.B) {
	exec := orm.AsQueryExecutor(&benchExec{rows: benchRows100()[:1]})
	repo := orm.NewRepository[T](exec)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := repo.Find(ctx, 1); err != nil {
			b.Fatal(err)
		}
	}
}

func benchFillSlice[T any](b *testing.B) {
	maps := benchMaps100()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var users []T
		if err := orm.FillSlice(&users, maps); err != nil || len(users) != 100 {
			b.Fatal(err)
		}
	}
}

func benchCreate[T orm.Model](b *testing.B, mk func() *T) {
	repo := orm.NewRepository[T](orm.AsQueryExecutor(&benchExec{}))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := repo.Create(ctx, mk()); err != nil {
			b.Fatal(err)
		}
	}
}

func newBenchUser() *benchUser {
	return &benchUser{Name: "Ann", Email: "ann@example.com", Age: 30, Active: true, Score: 1.5}
}

func newGenUser() *genUser {
	return &genUser{Name: "Ann", Email: "ann@example.com", Age: 30, Active: true, Score: 1.5}
}

// go test -run '^$' -bench . -benchmem: reflection (Reflect) against the
// orm-gen mappers (Generated).
func BenchmarkSelect100(b *testing.B) {
	b.Run("Reflect", benchSelect[benchUser])
	b.Run("Generated", benchSelect[genUser])
}

func BenchmarkFind(b *testing.B) {
	b.Run("Reflect", benchFind[benchUser])
	b.Run("Generated", benchFind[genUser])
}

func BenchmarkFillSlice100(b *testing.B) {
	b.Run("Reflect", benchFillSlice[benchUser])
	b.Run("Generated", benchFillSlice[genUser])
}

func BenchmarkCreate(b *testing.B) {
	b.Run("Reflect", func(b *testing.B) { benchCreate(b, newBenchUser) })
	b.Run("Generated", func(b *testing.B) { benchCreate(b, newGenUser) })
}
