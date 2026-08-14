// Package mysql реализует core.SQLExecutor для MySQL/MariaDB поверх database/sql.
//
// Реальное подключение требует драйвер database/sql, например:
//
//	go get github.com/go-sql-driver/mysql
//
// и анонимный импорт этого пакета в main (или прямо здесь, если хотите
// зашить конкретный low-level драйвер жёстко):
//
//	import _ "github.com/go-sql-driver/mysql"
package mysql

import (
	"context"
	"database/sql"

	"github.com/youruser/goorm/core"
	_ "github.com/go-sql-driver/mysql"
)

// mysqlDriver — реализация core.Driver для MySQL. Создаётся через Driver(dsn),
// подключение реально открывается только в Open(ctx) (ленивая инициализация).
type mysqlDriver struct{ dsn string }

// Driver возвращает core.Driver для явной инъекции:
//
//	conn, err := core.New(ctx, mysql.Driver("user:pass@tcp(127.0.0.1:3306)/db"))
//
// Либо используйте Open(dsn) напрямую, если core.Driver вам не нужен.
func Driver(dsn string) core.Driver { return mysqlDriver{dsn: dsn} }

func (d mysqlDriver) Open(ctx context.Context) (core.Connection, error) { return Open(d.dsn) }
// Dialect — особенности синтаксиса MySQL: обратные кавычки, "?" плейсхолдеры.
type Dialect struct{}

func (Dialect) Name() string { return "mysql" }

func (Dialect) Quote(identifier string) string { return "`" + identifier + "`" }

func (Dialect) Placeholder(_ int) string { return "?" }

func (d Dialect) BuildSelect(q *core.Query) (string, []any) { return core.BuildSelectGeneric(d, q) }
func (d Dialect) BuildInsert(q *core.Query) (string, []any) { return core.BuildInsertGeneric(d, q) }
func (d Dialect) BuildUpdate(q *core.Query) (string, []any) { return core.BuildUpdateGeneric(d, q) }
func (d Dialect) BuildDelete(q *core.Query) (string, []any) { return core.BuildDeleteGeneric(d, q) }

// Conn — соединение с MySQL, реализующее core.SQLExecutor И core.QueryExecutor
// (второе — через core.AsQueryExecutor, чтобы Repository[T] мог работать
// с этим же Conn одинаково с mongodb/redis).
type Conn struct {
	db      *sql.DB
	dialect Dialect
	qe      core.QueryExecutor
}

// Open открывает пул соединений. dsn — в формате go-sql-driver/mysql,
// например "user:pass@tcp(127.0.0.1:3306)/dbname?parseTime=true".
func Open(dsn string) (*Conn, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	c := &Conn{db: db}
	c.qe = core.AsQueryExecutor(c)
	return c, nil
}

func (c *Conn) Driver() string  { return "mysql" }
func (c *Conn) Dialect() core.Dialect { return c.dialect }

func (c *Conn) Ping(ctx context.Context) error { return c.db.PingContext(ctx) }
func (c *Conn) Close() error                   { return c.db.Close() }

func (c *Conn) ExecContext(ctx context.Context, query string, args ...any) (core.Result, error) {
	res, err := c.db.ExecContext(ctx, query, args...)
	if err != nil {
		return core.Result{}, err
	}
	id, _ := res.LastInsertId()
	affected, _ := res.RowsAffected()
	return core.Result{LastInsertID: id, RowsAffected: affected}, nil
}

func (c *Conn) QueryContext(ctx context.Context, query string, args ...any) (core.Rows, error) {
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return sqlRows{rows}, nil
}

func (c *Conn) QueryRowContext(ctx context.Context, query string, args ...any) core.Row {
	return c.db.QueryRowContext(ctx, query, args...)
}

func (c *Conn) BeginTx(ctx context.Context) (core.Tx, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &txWrapper{tx: tx, dialect: c.dialect}, nil
}

// ---- core.QueryExecutor: делает mysql.Conn единообразным с mongodb.Conn/redis.Conn ----

func (c *Conn) Select(ctx context.Context, q *core.Query, dest any) error {
	return c.qe.Select(ctx, q, dest)
}

func (c *Conn) Insert(ctx context.Context, q *core.Query) (core.Result, error) {
	return c.qe.Insert(ctx, q)
}

func (c *Conn) Update(ctx context.Context, q *core.Query) (core.Result, error) {
	return c.qe.Update(ctx, q)
}

func (c *Conn) Delete(ctx context.Context, q *core.Query) (core.Result, error) {
	return c.qe.Delete(ctx, q)
}

var _ core.QueryExecutor = (*Conn)(nil)

// sqlRows адаптирует *sql.Rows под core.Rows (интерфейсы почти идентичны).
type sqlRows struct{ *sql.Rows }

// txWrapper реализует core.Tx поверх *sql.Tx.
type txWrapper struct {
	tx      *sql.Tx
	dialect Dialect
}

func (t *txWrapper) Driver() string        { return "mysql" }
func (t *txWrapper) Dialect() core.Dialect { return t.dialect }
func (t *txWrapper) Ping(context.Context) error { return nil }
func (t *txWrapper) Close() error               { return nil }
func (t *txWrapper) Commit() error              { return t.tx.Commit() }
func (t *txWrapper) Rollback() error            { return t.tx.Rollback() }

func (t *txWrapper) ExecContext(ctx context.Context, query string, args ...any) (core.Result, error) {
	res, err := t.tx.ExecContext(ctx, query, args...)
	if err != nil {
		return core.Result{}, err
	}
	id, _ := res.LastInsertId()
	affected, _ := res.RowsAffected()
	return core.Result{LastInsertID: id, RowsAffected: affected}, nil
}

func (t *txWrapper) QueryContext(ctx context.Context, query string, args ...any) (core.Rows, error) {
	rows, err := t.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return sqlRows{rows}, nil
}

func (t *txWrapper) QueryRowContext(ctx context.Context, query string, args ...any) core.Row {
	return t.tx.QueryRowContext(ctx, query, args...)
}

func (t *txWrapper) BeginTx(ctx context.Context) (core.Tx, error) {
	return nil, sql.ErrTxDone // вложенные транзакции не поддерживаются напрямую
}
