package core

import "context"

// Driver — то, что предоставляет каждый драйвер-пакет как фабрику
// подключения: mysql.Driver(dsn), postgres.Driver(dsn), mongodb.Driver(dsn),
// redis.Driver(dsn). Никакого глобального реестра строковых имён и
// скрытых init()-side-effects (как в database/sql, где драйвер
// регистрирует себя по имени через анонимный импорт) — какой драйвер
// используется, видно прямо в месте вызова New(...), и это обычное
// Go-значение, а не строка.
type Driver interface {
	Open(ctx context.Context) (Connection, error)
}

// New открывает соединение через явно переданный Driver.
//
//	conn, err := core.New(ctx, mysql.Driver("user:pass@tcp(127.0.0.1:3306)/db"))
//	conn, err := core.New(ctx, postgres.Driver("postgres://user:pass@localhost:5432/db"))
//	conn, err := core.New(ctx, mongodb.Driver("mongodb://localhost:27017", "mydb"))
//	conn, err := core.New(ctx, redis.Driver("redis://localhost:6379/0"))
//
// Драйвер можно и не оборачивать в New — mysql.Driver(dsn).Open(ctx) работает
// точно так же; New существует просто как единая точка входа для читаемости.
func New(ctx context.Context, d Driver) (Connection, error) {
	return d.Open(ctx)
}
