// Package drivertest is a shared test suite for orm SQL drivers.
//
// Each driver (mysql, postgres, sqlite, third-party) runs it in its own
// integration test against a real database:
//
//	//go:build integration
//
//	func TestDriver(t *testing.T) {
//		dsn := os.Getenv("ORM_TEST_POSTGRES_DSN")
//		if dsn == "" {
//			t.Skip("ORM_TEST_POSTGRES_DSN is not set")
//		}
//		conn, err := postgres.Open(dsn)
//		...
//		drivertest.SQL(t, conn)
//	}
//
// The suite checks what the rest of the code relies on: DDL via schema,
// Repository (CRUD), Query (WHERE/ORDER/LIMIT), transactions and ErrNotFound.
package drivertest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"orm"
	"orm/schema"
)

// User is the model under test. Its field order deliberately does NOT match
// the table's column order, which catches positional scanning of SELECT *.
type User struct {
	orm.BaseModel
	Name   string `db:"name"`
	Active bool   `db:"active"`
	Email  string `db:"email"`
	Age    int64  `db:"age"`
}

func (User) TableName() string { return table }

const table = "drivertest_users"

// SQL runs all checks for a driver that implements orm.SQLExecutor and
// orm.QueryExecutor (like mysql.Conn, postgres.Conn, sqlite.Conn).
func SQL(t *testing.T, conn interface {
	orm.SQLExecutor
	orm.QueryExecutor
}) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if err := conn.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	sb, err := schema.NewFor(conn)
	if err != nil {
		t.Fatalf("schema.NewFor: %v", err)
	}

	reset := func(t *testing.T) {
		t.Helper()
		if err := sb.DropIfExists(ctx, table); err != nil {
			t.Fatalf("drop: %v", err)
		}
		// Columns in a different order than the User fields, plus an extra note column.
		err := sb.Create(ctx, table, func(b *schema.Blueprint) {
			b.ID()
			b.String("email").Unique()
			b.String("note").Nullable()
			b.String("name")
			b.BigInteger("age").Default(0)
			b.Boolean("active").Default(false)
		})
		if err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
	t.Cleanup(func() { _ = sb.DropIfExists(context.Background(), table) })

	t.Run("Repository CRUD", func(t *testing.T) {
		reset(t)
		crud(ctx, t, conn)
	})

	t.Run("Query where order limit", func(t *testing.T) {
		reset(t)
		queries(ctx, t, conn)
	})

	t.Run("Unique violation is an error", func(t *testing.T) {
		reset(t)
		repo := orm.NewRepository[User](conn)
		a := User{Name: "a", Email: "same@example.com"}
		b := User{Name: "b", Email: "same@example.com"}
		if err := repo.Create(ctx, &a); err != nil {
			t.Fatal(err)
		}
		if err := repo.Create(ctx, &b); err == nil {
			t.Fatal("second insert with the same unique email should fail")
		}
	})

	t.Run("Transactions", func(t *testing.T) {
		reset(t)

		tx, err := conn.BeginTx(ctx)
		if err != nil {
			t.Fatalf("BeginTx: %v", err)
		}
		defer func() { _ = tx.Rollback() }() // don't hold locks if the check failed
		txRepo := orm.NewRepository[User](orm.AsQueryExecutor(tx))
		u := User{Name: "rolled back", Email: "rb@example.com"}
		if err := txRepo.Create(ctx, &u); err != nil {
			t.Fatalf("Create in tx: %v", err)
		}
		if u.ID <= 0 {
			t.Fatalf("Create in tx should set ID, got %d", u.ID)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatalf("Rollback: %v", err)
		}
		if n := count(ctx, t, conn); n != 0 {
			t.Fatalf("after Rollback: %d rows, want 0", n)
		}

		tx, err = conn.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		txRepo = orm.NewRepository[User](orm.AsQueryExecutor(tx))
		u = User{Name: "committed", Email: "c@example.com"}
		if err := txRepo.Create(ctx, &u); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit: %v", err)
		}
		got, err := orm.NewRepository[User](conn).Find(ctx, u.ID)
		if err != nil || got.Name != "committed" {
			t.Fatalf("after Commit Find = %+v, %v", got, err)
		}
	})

	t.Run("Alter table", func(t *testing.T) {
		reset(t)
		if err := sb.Table(ctx, table, func(b *schema.Blueprint) {
			b.String("nickname").Nullable()
		}); err != nil {
			t.Fatalf("add column: %v", err)
		}
		d := conn.Dialect()
		q := fmt.Sprintf("INSERT INTO %s (%s, %s, %s) VALUES (%s, %s, %s)",
			d.Quote(table), d.Quote("email"), d.Quote("name"), d.Quote("nickname"),
			d.Placeholder(1), d.Placeholder(2), d.Placeholder(3))
		if _, err := conn.ExecContext(ctx, q, "n@example.com", "n", "nick"); err != nil {
			t.Fatalf("insert into new column: %v", err)
		}
		if err := sb.Table(ctx, table, func(b *schema.Blueprint) { b.DropColumn("nickname") }); err != nil {
			t.Fatalf("drop column: %v", err)
		}
	})
}

// crud covers Create/Find/Update/Delete via Repository.
func crud(ctx context.Context, t *testing.T, qe orm.QueryExecutor) {
	t.Helper()
	repo := orm.NewRepository[User](qe)
	u := User{Name: "Ann", Email: "ann@example.com", Age: 31, Active: true}
	if err := repo.Create(ctx, &u); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if u.ID <= 0 {
		t.Fatalf("Create should set ID, got %d", u.ID)
	}

	got, err := repo.Find(ctx, u.ID)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if *got != u {
		t.Fatalf("Find = %+v, want %+v (columns scanned into wrong fields?)", *got, u)
	}

	u.Name, u.Age, u.Active = "Anna", 32, false
	if err := repo.Update(ctx, &u); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err = repo.Find(ctx, u.ID)
	if err != nil || *got != u {
		t.Fatalf("Find after Update = %+v, %v; want %+v", got, err, u)
	}

	if err := repo.Delete(ctx, u.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Find(ctx, u.ID); !errors.Is(err, orm.ErrNotFound) {
		t.Fatalf("Find after Delete: want ErrNotFound, got %v", err)
	}
}

// queries covers WHERE/OR/ORDER/LIMIT/OFFSET via Repository.All.
func queries(ctx context.Context, t *testing.T, qe orm.QueryExecutor) {
	t.Helper()
	repo := orm.NewRepository[User](qe)
	for i := 1; i <= 5; i++ {
		u := User{Name: fmt.Sprintf("u%d", i), Email: fmt.Sprintf("u%d@example.com", i), Age: int64(20 + i), Active: i%2 == 1}
		if err := repo.Create(ctx, &u); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}

	all, err := repo.All(ctx, nil)
	if err != nil || len(all) != 5 {
		t.Fatalf("All(nil) = %d rows, %v", len(all), err)
	}

	q := orm.NewQuery(table).Where("active", "=", true).Where("age", ">", 21).OrderByDesc("age")
	got, err := repo.All(ctx, q)
	if err != nil {
		t.Fatalf("All(where): %v", err)
	}
	if names := namesOf(got); fmt.Sprint(names) != "[u5 u3]" {
		t.Fatalf("active AND age>21 ORDER BY age DESC = %v, want [u5 u3]", names)
	}

	q = orm.NewQuery(table).Where("name", "=", "u1").OrWhere("name", "=", "u4").OrderByAsc("name")
	if got, err = repo.All(ctx, q); err != nil || fmt.Sprint(namesOf(got)) != "[u1 u4]" {
		t.Fatalf("OR = %v, %v", namesOf(got), err)
	}

	q = orm.NewQuery(table).Where("name", "IN", []string{"u2", "u4", "nobody"}).OrderByAsc("name")
	if got, err = repo.All(ctx, q); err != nil || fmt.Sprint(namesOf(got)) != "[u2 u4]" {
		t.Fatalf("IN = %v, %v", namesOf(got), err)
	}

	q = orm.NewQuery(table).Where("name", "not in", []string{"u1", "u2", "u3"}).OrderByAsc("name")
	if got, err = repo.All(ctx, q); err != nil || fmt.Sprint(namesOf(got)) != "[u4 u5]" {
		t.Fatalf("NOT IN (lowercase op) = %v, %v", namesOf(got), err)
	}

	q = orm.NewQuery(table).OrderByAsc("age").LimitOffset(2, 1)
	if got, err = repo.All(ctx, q); err != nil || fmt.Sprint(namesOf(got)) != "[u2 u3]" {
		t.Fatalf("LIMIT 2 OFFSET 1 = %v, %v", namesOf(got), err)
	}

	q = orm.NewQuery(table).Where("name", "=", "nobody")
	if got, err = repo.All(ctx, q); err != nil || len(got) != 0 {
		t.Fatalf("empty result = %v, %v", got, err)
	}
}

// NoSQL runs the Repository and Query checks for drivers without SQL
// (mongodb, redis). reset must clear the drivertest_users collection/keys.
func NoSQL(t *testing.T, qe orm.QueryExecutor, reset func(t *testing.T)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := qe.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	t.Run("Repository CRUD", func(t *testing.T) {
		reset(t)
		crud(ctx, t, qe)
	})
	t.Run("Query where order limit", func(t *testing.T) {
		reset(t)
		queries(ctx, t, qe)
	})
}

func namesOf(us []User) []string {
	out := make([]string, len(us))
	for i, u := range us {
		out[i] = u.Name
	}
	return out
}

func count(ctx context.Context, t *testing.T, exec orm.SQLExecutor) int {
	t.Helper()
	var n int
	q := "SELECT COUNT(*) FROM " + exec.Dialect().Quote(table)
	if err := exec.QueryRowContext(ctx, q).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}
