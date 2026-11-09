// Package postgres implements orm.SQLExecutor for PostgreSQL on top of database/sql.
//
// Requires a low-level driver, for example github.com/jackc/pgx/v5/stdlib
// (pgx is a dependency of this package, so gtr installs it),
// and a blank import of it (registers "pgx" with database/sql).
package postgres

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
	"orm"
)

// postgresDriver is the orm.Driver implementation for Postgres.
type postgresDriver struct{ dsn string }

// Driver returns an orm.Driver for explicit injection:
//
//	conn, err := orm.New(ctx, postgres.Driver("postgres://user:pass@localhost:5432/db"))
func Driver(dsn string) orm.Driver { return postgresDriver{dsn: dsn} }

func (d postgresDriver) Open(ctx context.Context) (orm.Connection, error) { return Open(d.dsn) }

// Dialect describes Postgres syntax specifics: double-quote quoting and $1 $2 placeholders.
type Dialect struct{}

func (Dialect) Name() string { return "postgres" }

func (Dialect) Quote(identifier string) string { return `"` + identifier + `"` }

func (Dialect) Placeholder(n int) string { return fmt.Sprintf("$%d", n) }

func (d Dialect) BuildSelect(q *orm.Query) (string, []any) { return orm.BuildSelectGeneric(d, q) }
func (d Dialect) BuildInsert(q *orm.Query) (string, []any) {
	sqlText, args := orm.BuildInsertGeneric(d, q)
	return sqlText + " RETURNING id", args
}

// InsertReturnsID reports that BuildInsert appends RETURNING id (orm.InsertReturningID).
func (Dialect) InsertReturnsID() bool { return true }

func (d Dialect) BuildUpdate(q *orm.Query) (string, []any) { return orm.BuildUpdateGeneric(d, q) }
func (d Dialect) BuildDelete(q *orm.Query) (string, []any) { return orm.BuildDeleteGeneric(d, q) }

// Conn is a Postgres connection that implements both orm.SQLExecutor and orm.QueryExecutor.
type Conn struct {
	db      *sql.DB
	dialect Dialect
	qe      orm.QueryExecutor
}

// Open opens a connection pool. sqlDriverName is registered by the low-level
// driver (for example "pgx" when using jackc/pgx/v5/stdlib).
func Open(dsn string) (*Conn, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	c := &Conn{db: db}
	c.qe = orm.AsQueryExecutor(c)
	return c, nil
}

func (c *Conn) Driver() string       { return "postgres" }
func (c *Conn) Dialect() orm.Dialect { return c.dialect }

func (c *Conn) Ping(ctx context.Context) error { return c.db.PingContext(ctx) }
func (c *Conn) Close() error                   { return c.db.Close() }

func (c *Conn) ExecContext(ctx context.Context, query string, args ...any) (orm.Result, error) {
	res, err := c.db.ExecContext(ctx, query, args...)
	if err != nil {
		return orm.Result{}, err
	}
	affected, _ := res.RowsAffected()
	// Postgres does not support LastInsertId via database/sql;
	// the usual way to get the ID is to append "RETURNING id" and read it with QueryRowContext.
	return orm.Result{RowsAffected: affected}, nil
}

func (c *Conn) QueryContext(ctx context.Context, query string, args ...any) (orm.Rows, error) {
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return sqlRows{rows}, nil
}

func (c *Conn) QueryRowContext(ctx context.Context, query string, args ...any) orm.Row {
	return c.db.QueryRowContext(ctx, query, args...)
}

func (c *Conn) BeginTx(ctx context.Context) (orm.Tx, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &txWrapper{tx: tx, dialect: c.dialect}, nil
}

// ---- orm.QueryExecutor: makes postgres.Conn uniform with mongodb.Conn/redis.Conn ----

func (c *Conn) Select(ctx context.Context, q *orm.Query, dest any) error {
	return c.qe.Select(ctx, q, dest)
}

// Insert delegates to the shared adapter: it reads the id via RETURNING itself
// because Dialect implements orm.InsertReturningID (in transactions too).
func (c *Conn) Insert(ctx context.Context, q *orm.Query) (orm.Result, error) {
	return c.qe.Insert(ctx, q)
}

func (c *Conn) Update(ctx context.Context, q *orm.Query) (orm.Result, error) {
	return c.qe.Update(ctx, q)
}

func (c *Conn) Delete(ctx context.Context, q *orm.Query) (orm.Result, error) {
	return c.qe.Delete(ctx, q)
}

var _ orm.QueryExecutor = (*Conn)(nil)

type sqlRows struct{ *sql.Rows }

type txWrapper struct {
	tx      *sql.Tx
	dialect Dialect
}

func (t *txWrapper) Driver() string             { return "postgres" }
func (t *txWrapper) Dialect() orm.Dialect       { return t.dialect }
func (t *txWrapper) Ping(context.Context) error { return nil }
func (t *txWrapper) Close() error               { return nil }
func (t *txWrapper) Commit() error              { return t.tx.Commit() }
func (t *txWrapper) Rollback() error            { return t.tx.Rollback() }

func (t *txWrapper) ExecContext(ctx context.Context, query string, args ...any) (orm.Result, error) {
	res, err := t.tx.ExecContext(ctx, query, args...)
	if err != nil {
		return orm.Result{}, err
	}
	affected, _ := res.RowsAffected()
	return orm.Result{RowsAffected: affected}, nil
}

func (t *txWrapper) QueryContext(ctx context.Context, query string, args ...any) (orm.Rows, error) {
	rows, err := t.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return sqlRows{rows}, nil
}

func (t *txWrapper) QueryRowContext(ctx context.Context, query string, args ...any) orm.Row {
	return t.tx.QueryRowContext(ctx, query, args...)
}

func (t *txWrapper) BeginTx(ctx context.Context) (orm.Tx, error) {
	return nil, sql.ErrTxDone
}
