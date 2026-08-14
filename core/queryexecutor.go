package core

import "context"

// QueryExecutor — единая точка входа для core.Repository[T]: выполняет
// драйверо-независимый Query напрямую (без промежуточного текста SQL).
//
// Реализации:
//   - mysql.Conn, postgres.Conn — через готовый адаптер AsQueryExecutor
//     (см. sql_adapter.go): Query -> Dialect.BuildXxx -> текст SQL -> database/sql.
//   - mongodb.Conn — нативно: Query -> bson-фильтр / aggregation pipeline
//     (JOIN эмулируется через $lookup + $unwind).
//   - redis.Conn — нативно: каждая запись хранится как JSON-документ под
//     ключом "<table>:<id>", WHERE эмулируется полным перебором всех id
//     таблицы с фильтрацией в приложении (O(n) — осознанный компромисс для
//     dev/небольших наборов данных, см. README).
//
// Благодаря этому одна и та же модель с тегами `db:"..."` и один и тот же
// core.Repository[T] работают одинаково на mysql/postgres/mongodb/redis.
type QueryExecutor interface {
	Connection

	// Select выполняет SELECT-подобный Query и заполняет dest:
	//   *T   — одна запись; если не найдено — возвращается ErrNotFound
	//   *[]T — срез записей (пустой срез, если ничего не найдено)
	Select(ctx context.Context, q *Query, dest any) error

	Insert(ctx context.Context, q *Query) (Result, error)
	Update(ctx context.Context, q *Query) (Result, error)
	Delete(ctx context.Context, q *Query) (Result, error)
}
