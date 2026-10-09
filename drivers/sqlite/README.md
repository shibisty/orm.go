# orm-sqlite

SQLite driver for [`orm`](https://github.com/shibisty/orm.go). Go package: `sqlite`.

Dependency: you choose the low-level driver: `import _ "modernc.org/sqlite"` (pure Go) or mattn/go-sqlite3 (cgo, name "sqlite3").

## Installation

```bash
gtr add github:shibisty/orm.go github:shibisty/orm.go-sqlite-driver modernc.org/sqlite
```

`orm` is a peer dependency (`^0.1`). The low-level SQLite implementation is yours to pick
(here modernc.org/sqlite, pure Go); import it with `_` in your main.

## Usage

```go
import (
    "orm"
    sqlite "orm-sqlite"
)

conn, err := orm.New(ctx, sqlite.Driver("file:app.db?_pragma=foreign_keys(1)"))
```

## Limitations

`Change()` and adding or dropping a foreign key on an existing table are possible only by recreating the table (the driver returns a clear error). A unique index in ALTER is created via `CREATE UNIQUE INDEX`.

## Tests

The integration test lives in a separate gtr project, `integration/`: it needs
modernc.org/sqlite, while the driver itself does not pull a low-level implementation into
its dependencies.

In a checkout, run `gtr install` once, then:

```bash
gtr run test -- -race -cover          # no database needed (DDL golden test, etc.)
cd integration && gtr install && ORM_TEST_SQLITE_DSN="file:test.db" gtr run test:integration
```

After an intentional DDL change (with the `go` shim, `gtr self shims`): `go test -run DDLGolden -update .`

## License

MIT
