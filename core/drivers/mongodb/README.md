# orm-mongodb

MongoDB driver for [`orm`](https://github.com/shibisty/orm.go). Go package: `mongodb`.

Dependency: go.mongodb.org/mongo-driver/v2.

## Installation

```bash
gtr add github:shibisty/orm.go github:shibisty/orm.go-mongodb-driver
```

`orm` is a peer dependency (`^0.1`): the application adds it, so all its drivers share one core.

## Usage

```go
import (
    "orm"
    mongodb "orm-mongodb"
)

conn, err := orm.New(ctx, mongodb.Driver("mongodb://localhost:27017", "app"))
```

## Limitations

JOIN uses `$lookup`, and WHERE applies only to the base collection; AND and OR cannot be mixed; the auto-increment id uses a `counters` collection. Native operations are available via `orm.DocumentStore`.

## Tests

In a checkout, run `gtr install` once (it generates `go.mod` and fills `gtr_modules/`), then:

```bash
gtr run test -- -race -cover          # no database needed (DDL golden test, etc.)
ORM_TEST_MONGODB_DSN="mongodb://127.0.0.1:27017" gtr run test:integration   # shared orm/drivertest suite
```

## License

MIT
