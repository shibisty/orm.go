// Package core содержит контракты, общие для всех драйверов библиотеки.
//
// Идея: НЕ пытаться натянуть один интерфейс "как в SQL" на Mongo/Redis —
// это ломает абстракцию и заставляет NoSQL-драйверы эмулировать то,
// чего у них нет (JOIN, транзакции в смысле SQL и т.д.).
//
// Вместо этого используется подход стандартной библиотеки Go (io.Reader/io.Writer):
//   - Connection    — минимальный контракт, который есть у ЛЮБОГО драйвера
//   - SQLExecutor   — реализуют mysql/postgres/sqlite и т.п.
//   - DocumentStore — реализует mongodb
//   - KeyValueStore — реализует redis
//
// Верхнеуровневый код работает с Connection и через type assertion / type switch
// достаёт нужную capability, либо драйвер-пакеты сразу возвращают
// конкретный сконкретизированный тип (*mysql.Conn, *mongodb.Conn и т.д.),
// который просто "тоже является" Connection.
package core

import "context"

// Connection — общий контракт для любого драйвера (реляционного и нет).
type Connection interface {
	// Driver возвращает имя драйвера, например "mysql", "postgres", "mongodb", "redis".
	Driver() string

	// Ping проверяет живость соединения.
	Ping(ctx context.Context) error

	// Close закрывает соединение и освобождает ресурсы.
	Close() error
}

// Row — абстракция одной строки/документа результата.
// Совместима по духу с *sql.Row, но не привязана к database/sql.
type Row interface {
	Scan(dest ...any) error
}

// Rows — абстракция набора строк/документов.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close() error
}

// Result — результат операции изменения данных (INSERT/UPDATE/DELETE).
type Result struct {
	LastInsertID int64
	RowsAffected int64
}

// SQLExecutor реализуют реляционные драйверы (mysql, postgres, sqlite...).
// Он работает с уже готовым SQL-запросом и позиционными аргументами —
// перевод Query (см. query.go) в конкретный SQL берёт на себя Dialect.
type SQLExecutor interface {
	Connection
	Dialect() Dialect

	ExecContext(ctx context.Context, query string, args ...any) (Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) Row

	// BeginTx открывает транзакцию. Возвращает Tx, реализующий тот же SQLExecutor,
	// плюс Commit/Rollback.
	BeginTx(ctx context.Context) (Tx, error)
}

// Tx — транзакция для реляционных БД.
type Tx interface {
	SQLExecutor
	Commit() error
	Rollback() error
}

// Filter — драйверо-независимое описание условия для NoSQL-хранилищ.
// Для Mongo естественно мапится в bson.M, для Redis используется частично
// (например для сканирования по паттерну ключа).
type Filter map[string]any

// DocumentStore реализует mongodb (и в перспективе любая документная БД).
type DocumentStore interface {
	Connection

	InsertOne(ctx context.Context, collection string, document any) (insertedID any, err error)
	FindOne(ctx context.Context, collection string, filter Filter, dest any) error
	Find(ctx context.Context, collection string, filter Filter, dest any) error
	UpdateOne(ctx context.Context, collection string, filter Filter, update any) (matched, modified int64, err error)
	DeleteOne(ctx context.Context, collection string, filter Filter) (deleted int64, err error)
}

// KeyValueStore реализует redis (и в перспективе memcached и т.п.).
type KeyValueStore interface {
	Connection

	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value any, ttlSeconds int) error
	Del(ctx context.Context, keys ...string) (int64, error)
	Exists(ctx context.Context, keys ...string) (int64, error)
}
