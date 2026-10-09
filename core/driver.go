package orm

import "context"

// Driver is what every driver package provides as a connection
// factory: mysql.Driver(dsn), postgres.Driver(dsn), mongodb.Driver(dsn),
// redis.Driver(dsn). There is no global registry of string names and no
// hidden init() side effects (as in database/sql, where a driver
// registers itself by name via a blank import): which driver
// is used is visible right at the New(...) call site, and it is a plain
// Go value, not a string.
type Driver interface {
	Open(ctx context.Context) (Connection, error)
}

// New opens a connection via an explicitly passed Driver.
//
//	conn, err := orm.New(ctx, mysql.Driver("user:pass@tcp(127.0.0.1:3306)/db"))
//	conn, err := orm.New(ctx, postgres.Driver("postgres://user:pass@localhost:5432/db"))
//	conn, err := orm.New(ctx, mongodb.Driver("mongodb://localhost:27017", "mydb"))
//	conn, err := orm.New(ctx, redis.Driver("redis://localhost:6379/0"))
//
// The driver doesn't have to be wrapped in New: mysql.Driver(dsn).Open(ctx) works
// exactly the same; New exists simply as a single, readable entry point.
func New(ctx context.Context, d Driver) (Connection, error) {
	return d.Open(ctx)
}
