# orm-redis

Redis driver for [`orm`](https://github.com/shibisty/orm.go). Go package: `redis`.

Dependency: github.com/redis/go-redis/v9.

## Installation

```bash
gtr add github:shibisty/orm.go github:shibisty/orm.go-redis-driver
```

`orm` is a peer dependency (`^0.1`): the application adds it, so all its drivers share one core.

## Usage

```go
import (
    "orm"
    redis "orm-redis"
)

conn, err := orm.New(ctx, redis.Driver("redis://localhost:6379/0"))
```

## Limitations

`Select` is a full scan of the table's records (O(n)), fine for dev and small tables; AND and OR cannot be mixed. Native operations are available via `orm.KeyValueStore`.

## Tests

In a checkout, run `gtr install` once (it generates `go.mod` and fills `gtr_modules/`), then:

```bash
gtr run test -- -race -cover          # no database needed (DDL golden test, etc.)
ORM_TEST_REDIS_DSN="redis://127.0.0.1:6379/15" gtr run test:integration   # shared orm/drivertest suite
```

## License

MIT
