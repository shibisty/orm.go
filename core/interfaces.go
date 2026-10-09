// Package orm contains the contracts shared by all of the library's drivers.
//
// The idea: do NOT try to force a single "SQL-like" interface onto Mongo/Redis;
// that breaks the abstraction and forces NoSQL drivers to emulate things
// they don't have (JOINs, transactions in the SQL sense, etc.).
//
// Instead, the approach of the Go standard library (io.Reader/io.Writer) is used:
//   - Connection    — the minimal contract that EVERY driver has
//   - SQLExecutor   — implemented by mysql/postgres/sqlite, etc.
//   - DocumentStore — implemented by mongodb
//   - KeyValueStore — implemented by redis
//
// Higher-level code works with Connection and obtains the needed capability via a
// type assertion / type switch, or the driver packages return
// a concrete type directly (*mysql.Conn, *mongodb.Conn, etc.)
// that simply "is also" a Connection.
package orm

import "context"

// Connection is the common contract for any driver (relational or not).
type Connection interface {
	// Driver returns the driver name, e.g. "mysql", "postgres", "mongodb", "redis".
	Driver() string

	// Ping checks that the connection is alive.
	Ping(ctx context.Context) error

	// Close closes the connection and releases resources.
	Close() error
}

// Row abstracts a single result row/document.
// It is similar in spirit to *sql.Row but not tied to database/sql.
type Row interface {
	Scan(dest ...any) error
}

// Rows abstracts a set of rows/documents.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close() error
}

// Result is the result of a data modification (INSERT/UPDATE/DELETE).
type Result struct {
	LastInsertID int64
	RowsAffected int64
}

// SQLExecutor is implemented by relational drivers (mysql, postgres, sqlite...).
// It works with a ready SQL query and positional arguments;
// translating a Query (see query.go) into concrete SQL is the Dialect's job.
type SQLExecutor interface {
	Connection
	Dialect() Dialect

	ExecContext(ctx context.Context, query string, args ...any) (Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) Row

	// BeginTx starts a transaction. It returns a Tx that implements the same SQLExecutor
	// plus Commit/Rollback.
	BeginTx(ctx context.Context) (Tx, error)
}

// Tx is a transaction for relational databases.
type Tx interface {
	SQLExecutor
	Commit() error
	Rollback() error
}

// Filter is a driver-independent condition description for NoSQL stores.
// For Mongo it maps naturally to bson.M; Redis uses it partially
// (for example, for scanning by key pattern).
type Filter map[string]any

// DocumentStore is implemented by mongodb (and potentially any document database).
type DocumentStore interface {
	Connection

	InsertOne(ctx context.Context, collection string, document any) (insertedID any, err error)
	FindOne(ctx context.Context, collection string, filter Filter, dest any) error
	Find(ctx context.Context, collection string, filter Filter, dest any) error
	UpdateOne(ctx context.Context, collection string, filter Filter, update any) (matched, modified int64, err error)
	DeleteOne(ctx context.Context, collection string, filter Filter) (deleted int64, err error)
}

// KeyValueStore is implemented by redis (and potentially memcached, etc.).
type KeyValueStore interface {
	Connection

	// Get returns ErrNotFound if the key does not exist (or has expired).
	Get(ctx context.Context, key string) (string, error)
	// Set with ttlSeconds <= 0 stores the key without an expiration.
	Set(ctx context.Context, key string, value any, ttlSeconds int) error
	Del(ctx context.Context, keys ...string) (int64, error)
	Exists(ctx context.Context, keys ...string) (int64, error)
}
