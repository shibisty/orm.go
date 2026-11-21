// Package sqlite implements orm.SQLExecutor and orm.QueryExecutor for SQLite
// on top of database/sql.
//
// Requires a low-level driver registered with database/sql under the name
// "sqlite". Pure Go without cgo is recommended:
//
//	gtr add modernc.org/sqlite
//
// and a blank import in your main:
//
//	import _ "modernc.org/sqlite"
//
// (modernc.org/sqlite registers itself as "sqlite"; if you use
// mattn/go-sqlite3, it registers as "sqlite3", so adjust
// driverName in Open below accordingly).
package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"orm"
)

// Dialect describes SQLite syntax specifics: double quotes for identifiers
// (as in standard SQL, which SQLite fully supports) and "?" placeholders.
type Dialect struct{}

func (Dialect) Name() string { return "sqlite" }

func (Dialect) Quote(identifier string) string { return `"` + identifier + `"` }

func (Dialect) Placeholder(_ int) string { return "?" }

func (d Dialect) BuildSelect(q *orm.Query) (string, []any) { return orm.BuildSelectGeneric(d, q) }
func (d Dialect) BuildInsert(q *orm.Query) (string, []any) { return orm.BuildInsertGeneric(d, q) }
func (d Dialect) BuildUpdate(q *orm.Query) (string, []any) { return orm.BuildUpdateGeneric(d, q) }
func (d Dialect) BuildDelete(q *orm.Query) (string, []any) { return orm.BuildDeleteGeneric(d, q) }

// Conn is a SQLite connection that implements orm.SQLExecutor and orm.QueryExecutor.
type Conn struct {
	db      *sql.DB
	dialect Dialect
	qe      orm.QueryExecutor
}

// sqliteDriver is the orm.Driver implementation for explicit injection:
//
//	conn, err := orm.New(ctx, sqlite.Driver("file:app.db?cache=shared"))
type sqliteDriver struct{ dsn string }

func Driver(dsn string) orm.Driver { return sqliteDriver{dsn: dsn} }

func (d sqliteDriver) Open(ctx context.Context) (orm.Connection, error) { return Open(d.dsn) }

// Open opens a SQLite connection. dsn is a file path (for example
// "app.db" or "file:app.db?cache=shared") or ":memory:" for an in-memory database.
//
// Unlike mysql/postgres, for SQLite this is deliberately NOT just
// "sql.Open + return *Conn": SQLite has two behaviors that break the
// standard database/sql connection pool unless they are accounted for:
//
//  1. SQLite does not support concurrent writes from multiple connections
//     (the whole file is locked for writing). The standard database/sql pool
//     freely opens several connections and will try to write
//     concurrently, and you get "database is locked". So the pool
//     is forcibly narrowed to a single connection (SetMaxOpenConns(1)):
//     SQLite itself serializes operations within one connection perfectly well
//     instead of relying on the pool for that.
//  2. PRAGMA foreign_keys = ON applies only to the connection it was
//     executed on, not to the whole pool. If you run it once
//     after Open(), the next random connection from the pool will silently
//     have foreign keys disabled. With a single connection (see item 1)
//     the problem goes away by itself: the PRAGMA runs once and is
//     guaranteed to apply to all subsequent queries.
func Open(dsn string) (*Conn, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0) // don't close the only connection on a timeout

	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite: failed to enable PRAGMA foreign_keys: %w", err)
	}

	c := &Conn{db: db}
	c.qe = orm.AsQueryExecutor(c)
	return c, nil
}

func (c *Conn) Driver() string       { return "sqlite" }
func (c *Conn) Dialect() orm.Dialect { return c.dialect }

func (c *Conn) Ping(ctx context.Context) error { return c.db.PingContext(ctx) }
func (c *Conn) Close() error                   { return c.db.Close() }

func (c *Conn) ExecContext(ctx context.Context, query string, args ...any) (orm.Result, error) {
	res, err := c.db.ExecContext(ctx, query, args...)
	if err != nil {
		return orm.Result{}, err
	}
	id, _ := res.LastInsertId() // SQLite returns LastInsertId natively (rowid); unlike Postgres, no RETURNING is needed
	affected, _ := res.RowsAffected()
	return orm.Result{LastInsertID: id, RowsAffected: affected}, nil
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

// ---- orm.QueryExecutor: makes sqlite.Conn uniform with the other drivers ----

func (c *Conn) Select(ctx context.Context, q *orm.Query, dest any) error {
	return c.qe.Select(ctx, q, dest)
}
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

// txWrapper implements orm.Tx on top of *sql.Tx.
//
// Note: BeginTx on a *Conn with a single pooled connection (see Open) means
// that while the transaction is open, any other ExecContext/QueryContext on the same
// *Conn from another goroutine blocks waiting for a free
// connection rather than failing with an error, which is the same behavior you
// would get without this driver when working with SQLite directly.
type txWrapper struct {
	tx      *sql.Tx
	dialect Dialect
}

func (t *txWrapper) Driver() string             { return "sqlite" }
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
	id, _ := res.LastInsertId()
	affected, _ := res.RowsAffected()
	return orm.Result{LastInsertID: id, RowsAffected: affected}, nil
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
	return nil, sql.ErrTxDone // nested transactions are not supported directly
}
