# orm-postgres

PostgreSQL driver for [`orm`](https://github.com/shibisty/orm.go). Go package: `postgres`.

Dependency: github.com/jackc/pgx/v5 (wired in by the driver itself).

## Installation

```bash
gtr add github:shibisty/orm.go github:shibisty/orm.go-postgres-driver
```

`orm` is a peer dependency (`^0.1`): the application adds it, so all its drivers share one core.

## Usage

```go
import (
    "orm"
    postgres "orm-postgres"
)

conn, err := orm.New(ctx, postgres.Driver("postgres://user:pass@localhost:5432/app?sslmode=disable"))
```

## Limitations

The id of a new record is read via `RETURNING id` (in transactions too). `Set` is not supported, `Enum` uses CHECK, `Vector`/`Geometry` require pgvector/PostGIS.

## Tests

In a checkout, run `gtr install` once (it generates `go.mod` and fills `gtr_modules/`), then:

```bash
gtr run test -- -race -cover          # no database needed (DDL golden test, etc.)
ORM_TEST_POSTGRES_DSN="postgres://postgres@127.0.0.1:5432/ormtest?sslmode=disable" gtr run test:integration   # shared orm/drivertest suite
```

After an intentional DDL change (with the `go` shim, `gtr self shims`): `go test -run DDLGolden -update .`

## License

MIT
