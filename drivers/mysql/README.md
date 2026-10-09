# orm-mysql

MySQL and MariaDB driver for [`orm`](https://github.com/shibisty/orm.go). Go package: `mysql`.

Dependency: github.com/go-sql-driver/mysql (wired in by the driver itself).

[![Patreon](https://c5.patreon.com/external/logo/become_a_patron_button.png)](https://www.patreon.com/cw/shibisty)

## Installation

```bash
gtr add github:shibisty/orm.go github:shibisty/orm.go-mysql-driver
```

`orm` is a peer dependency (`^0.1`): the application adds it, so all its drivers share one core.

## Usage

```go
import (
    "orm"
    mysql "orm-mysql"
)

conn, err := orm.New(ctx, mysql.Driver("user:pass@tcp(127.0.0.1:3306)/app?parseTime=true"))
```

## Limitations

DDL runs with an implicit COMMIT, so a transaction does not roll back CREATE/ALTER. `Vector` is not supported.

## Tests

In a checkout, run `gtr install` once (it generates `go.mod` and fills `gtr_modules/`), then:

```bash
gtr run test -- -race -cover          # no database needed (DDL golden test, etc.)
ORM_TEST_MYSQL_DSN="root:@tcp(127.0.0.1:3306)/ormtest" gtr run test:integration   # shared orm/drivertest suite
```

After an intentional DDL change (with the `go` shim, `gtr self shims`): `go test -run DDLGolden -update .`

## License

MIT

[![Patreon](https://c5.patreon.com/external/logo/become_a_patron_button.png)](https://www.patreon.com/cw/shibisty)

If this project helps you, consider supporting its development on Patreon ❤️
