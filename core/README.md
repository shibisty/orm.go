# orm

A minimalist ORM for Go in the spirit of Laravel Eloquent: one model with `db` tags
and one `Repository[T]` work on MySQL, Postgres, SQLite, MongoDB and Redis.
Each database driver is a separate package; the core has no external dependencies.

[![Patreon](https://c5.patreon.com/external/logo/become_a_patron_button.png)](https://www.patreon.com/cw/shibisty)

| Package | What it is |
|---|---|
| `orm` (this repository) | Core: interfaces, `Query`, `Repository[T]`, the `schema` and `drivertest` subpackages |
| [`orm-mysql`](https://github.com/shibisty/orm.go-mysql-driver) | MySQL/MariaDB |
| [`orm-postgres`](https://github.com/shibisty/orm.go-postgres-driver) | PostgreSQL |
| [`orm-sqlite`](https://github.com/shibisty/orm.go-sqlite-driver) | SQLite |
| [`orm-mongodb`](https://github.com/shibisty/orm.go-mongodb-driver) | MongoDB |
| [`orm-redis`](https://github.com/shibisty/orm.go-redis-driver) | Redis |
| [`migration`](https://github.com/shibisty/migration.go) + [`migration-orm`](https://github.com/shibisty/migration.go-orm-driver) | Migrations (a separate, higher-level package; orm is one of its backends) |

Requires Go 1.22+.

## Installation

With [gtr](https://github.com/shibisty/gtr) (until the gtr registry, by repository):

```bash
gtr add github:shibisty/orm.go github:shibisty/orm.go-postgres-driver
```

Import as `import "orm"`; drivers have `orm` as a peer dependency, so the application
adds it too.

## Quick start

```go
import (
    "orm"
    postgres "orm-postgres"
)

type User struct {
    orm.BaseModel        // ID field tagged db:"id"
    Name  string `db:"name"`
    Email string `db:"email"`
}

func (User) TableName() string { return "users" }

conn, err := orm.New(ctx, postgres.Driver("postgres://user:pass@localhost:5432/app"))
users := orm.NewRepository[User](conn.(orm.QueryExecutor))

u := &User{Name: "Ivan", Email: "ivan@example.com"}
err = users.Create(ctx, u)          // u.ID is set
found, err := users.Find(ctx, u.ID) // orm.ErrNotFound if missing
list, err := users.All(ctx, orm.NewQuery("users").
    Where("email", "LIKE", "%@example.com").
    Where("id", "IN", []int64{1, 2, 3}).
    OrderByDesc("id").
    LimitOffset(10, 0))
```

To switch to another database, change only the driver passed to `orm.New`:
`mysql.Driver(dsn)`, `sqlite.Driver(dsn)`, `mongodb.Driver(dsn, "db")`, `redis.Driver(dsn)`.
Full example: [`orm.go/example`](../example/main.go) in the local workspace.

## Architecture

- **`Connection`** is the common minimum of every driver: `Driver()`, `Ping`, `Close`.
- **`QueryExecutor`** provides `Select/Insert/Update/Delete` via the driver-independent
  `Query`. Every driver implements it, and `Repository[T]` is built on it.
- **Database-specific capabilities** are not hidden behind a lowest common denominator:
  `SQLExecutor` (SQL and transactions — mysql, postgres, sqlite),
  `DocumentStore` (MongoDB), `KeyValueStore` (Redis). A driver implements both the common
  interface and its own, so nothing is lost.
- **The driver is passed explicitly**, as a value: `orm.New(ctx, mysql.Driver(dsn))`.
  There is no global registry of string names as in `database/sql`: if the code
  compiles, the right driver is wired in.
- **`Dialect`** translates a `Query` into the SQL of a specific database. Optional
  extensions: `orm.InsertReturningID` (id via `RETURNING`, Postgres),
  `schema.AlterConstraintDialect` (constraints without `ALTER ... ADD CONSTRAINT`, SQLite).

## Queries

| Method | SQL |
|---|---|
| `Where(col, op, v)` / `OrWhere` | `"col" op $1`; `IN` / `NOT IN` with a slice expand to `($1, $2, …)` |
| `Select(cols...)` | explicit list; defaults to `*` (for models, replaced with the columns from `db` tags) |
| `Join(type, table, on)` | `LEFT JOIN "orders" ON …` (`on` is raw SQL; never put user input there) |
| `OrderByAsc` / `OrderByDesc` | the column name is quoted; the direction is ASC/DESC only |
| `LimitOffset(n, m)` | `LIMIT n OFFSET m` |

`table.column` names are quoted part by part: `"users"."id"`.
`INSERT`/`UPDATE` columns are emitted in alphabetical order, so the same data always produces the same SQL.

## Schema: `schema`

A schema builder in the spirit of Laravel Schema Builder: every column type (numbers,
strings, dates, json, uuid/ulid, enum/set, morphs, vector…), modifiers and
foreign keys.

```go
sb, err := schema.NewFor(conn.(orm.SQLExecutor))
err = sb.Create(ctx, "posts", func(t *schema.Blueprint) {
    t.ID()
    t.String("title")
    t.ForeignID("user_id").Constrained().OnDelete("cascade")
    t.Morphs("commentable")
    t.Timestamps()
    t.SoftDeletes()
})
stmts, err := sb.SQLForCreate("posts", fn) // DDL without executing it (dry run)
```

Index and constraint names follow Laravel: `posts_title_unique`,
`posts_user_id_foreign`, `posts_commentable_type_commentable_id_index`.
The table name is required in them: in Postgres and SQLite index names are shared
across the whole schema. `IndexName(table, kind, cols...)`, `DropIndexCols` and `DropForeignCol`
build the same names.

Migrations live in a separate package, [`migration`](https://github.com/shibisty/migration.go):
it does not depend on orm, while [`migration-orm`](https://github.com/shibisty/migration.go-orm-driver)
plugs orm in as a backend and gives each migration a transaction and a `Schema`.

### Schema limitations

- Types the database lacks are an error, not a silent substitution: `Vector` on MySQL,
  `Set` on Postgres, `Change()` and dropping/adding a foreign key on SQLite
  (possible only by recreating the table).
- Similar types are collapsed: Postgres has no `TINYINT`/`MEDIUMINT`/`YEAR`/`DATETIME`
  (the closest type without narrowing the range is used); SQLite uses type affinity.
- `Enum` on Postgres is `VARCHAR + CHECK`, not `CREATE TYPE`.
- MySQL runs DDL with an implicit `COMMIT`: a transaction does not roll back an
  already executed `CREATE`/`ALTER`.
- Pluralization in `Constrained()`/`ForeignIDFor()` is naive: `company` → `companies`,
  but not `person` → `people` — pass the table explicitly.

## NoSQL: what is emulated

- **MongoDB**: `WHERE` → filter, `JOIN` → `$lookup` + `$unwind`, auto-increment
  `id` → a `counters` collection. `WHERE` with a JOIN applies only to the base collection.
- **Redis**: a record is JSON under the key `<table>:<id>`, plus a set of the table's ids.
  `Select` is a full scan (O(n)): fine for dev and small tables.
- Mixing `AND` and `OR` in one query is not allowed on Mongo/Redis — the driver returns
  an error (`HomogeneousConditions`) instead of applying the wrong precedence.
- Only SQL drivers support transactions (`SQLExecutor.BeginTx`).

## Tests

In a checkout, run `gtr install` once (it generates `go.mod`), then:

```bash
gtr run test -- -race -cover        # core and schema, no database needed
```

`drivertest` is a shared test suite for drivers: DDL, CRUD, queries,
unique constraints, transactions. A driver runs it in its integration test:

```go
//go:build integration
func TestDriver(t *testing.T) { drivertest.SQL(t, conn) }               // SQL drivers
func TestDriver(t *testing.T) { drivertest.NoSQL(t, conn, resetFn) }    // MongoDB, Redis
```

```bash
cd orm.go-postgres-driver
ORM_TEST_POSTGRES_DSN="postgres://postgres@127.0.0.1:5432/ormtest?sslmode=disable" \
  gtr run test:integration
```

Variables: `ORM_TEST_MYSQL_DSN`, `ORM_TEST_POSTGRES_DSN`, `ORM_TEST_SQLITE_DSN`,
`ORM_TEST_MONGODB_DSN`, `ORM_TEST_REDIS_DSN`.

## License

MIT

[![Patreon](https://c5.patreon.com/external/logo/become_a_patron_button.png)](https://www.patreon.com/cw/shibisty)

If this project helps you, consider supporting its development on Patreon ❤️
