package orm

import "context"

// QueryExecutor is the single entry point for orm.Repository[T]: it executes
// a driver-independent Query directly (without intermediate SQL text).
//
// Implementations:
//   - mysql.Conn, postgres.Conn: via the ready-made AsQueryExecutor adapter
//     (see sql_adapter.go): Query -> Dialect.BuildXxx -> SQL text -> database/sql.
//   - mongodb.Conn: natively: Query -> bson filter / aggregation pipeline
//     (JOIN is emulated via $lookup + $unwind).
//   - redis.Conn: natively: each record is stored as a JSON document under
//     the key "<table>:<id>"; WHERE is emulated by a full scan of all ids
//     in the table with filtering in the application (O(n), a deliberate trade-off for
//     dev/small datasets, see the README).
//
// Thanks to this, the same model with `db:"..."` tags and the same
// orm.Repository[T] work the same way on mysql/postgres/mongodb/redis.
type QueryExecutor interface {
	Connection

	// Select executes a SELECT-like Query and fills dest:
	//   *T   — a single record; ErrNotFound is returned if nothing is found
	//   *[]T — a slice of records (an empty slice if nothing is found)
	Select(ctx context.Context, q *Query, dest any) error

	Insert(ctx context.Context, q *Query) (Result, error)
	Update(ctx context.Context, q *Query) (Result, error)
	Delete(ctx context.Context, q *Query) (Result, error)
}
