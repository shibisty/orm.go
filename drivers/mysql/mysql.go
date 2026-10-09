// Package mysql implements orm.SQLExecutor for MySQL/MariaDB on top of database/sql.
//
// A real connection requires a database/sql driver, for example
// github.com/go-sql-driver/mysql (a dependency of this package, so gtr installs it),
// and a blank import of that package in main (or right here, if you want
// to hard-wire a specific low-level driver):
//
//	import _ "github.com/go-sql-driver/mysql"
package mysql

import (
	"context"
	"database/sql"

	_ "github.com/go-sql-driver/mysql"
	"orm"
)

// mysqlDriver is the orm.Driver implementation for MySQL. It is created via Driver(dsn);
// the connection is actually opened only in Open(ctx) (lazy initialization).
type mysqlDriver struct{ dsn string }

// Driver returns an orm.Driver for explicit injection:
//
//	conn, err := orm.New(ctx, mysql.Driver("user:pass@tcp(127.0.0.1:3306)/db"))
//
// Or use Open(dsn) directly if you don't need an orm.Driver.
func Driver(dsn string) orm.Driver { return mysqlDriver{dsn: dsn} }

func (d mysqlDriver) Open(ctx context.Context) (orm.Connection, error) { return Open(d.dsn) }

// Dialect describes MySQL syntax specifics: backtick quoting and "?" placeholders.
type Dialect struct{}

func (Dialect) Name() string { return "mysql" }

func (Dialect) Quote(identifier string) string { return "`" + identifier + "`" }

func (Dialect) Placeholder(_ int) string { return "?" }

func (d Dialect) BuildSelect(q *orm.Query) (string, []any) { return orm.BuildSelectGeneric(d, q) }
func (d Dialect) BuildInsert(q *orm.Query) (string, []any) { return orm.BuildInsertGeneric(d, q) }
func (d Dialect) BuildUpdate(q *orm.Query) (string, []any) { return orm.BuildUpdateGeneric(d, q) }
func (d Dialect) BuildDelete(q *orm.Query) (string, []any) { return orm.BuildDeleteGeneric(d, q) }

// Conn is a MySQL connection that implements both orm.SQLExecutor and orm.QueryExecutor
// (the latter via orm.AsQueryExecutor, so that Repository[T] can work with
// this Conn the same way as with mongodb/redis).
type Conn struct {
	db      *sql.DB
	dialect Dialect
	qe      orm.QueryExecutor
}

// Open opens a connection pool. dsn uses the go-sql-driver/mysql format,
// for example "user:pass@tcp(127.0.0.1:3306)/dbname?parseTime=true".
func Open(dsn string) (*Conn, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	c := &Conn{db: db}
	c.qe = orm.AsQueryExecutor(c)
	return c, nil
}

func (c *Conn) Driver() string       { return "mysql" }
func (c *Conn) Dialect() orm.Dialect { return c.dialect }

func (c *Conn) Ping(ctx context.Context) error { return c.db.PingContext(ctx) }
func (c *Conn) Close() error                   { return c.db.Close() }

func (c *Conn) ExecContext(ctx context.Context, query string, args ...any) (orm.Result, error) {
	res, err := c.db.ExecContext(ctx, query, args...)
	if err != nil {
		return orm.Result{}, err
	}
	id, _ := res.LastInsertId()
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

// ---- orm.QueryExecutor: makes mysql.Conn uniform with mongodb.Conn/redis.Conn ----

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

// sqlRows adapts *sql.Rows to orm.Rows (the interfaces are almost identical).
type sqlRows struct{ *sql.Rows }

// txWrapper implements orm.Tx on top of *sql.Tx.
type txWrapper struct {
	tx      *sql.Tx
	dialect Dialect
}

func (t *txWrapper) Driver() string             { return "mysql" }
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
