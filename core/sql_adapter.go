package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
)

// sqlQueryExecutor реализует QueryExecutor поверх любого SQLExecutor,
// используя его Dialect для перевода Query в текст SQL.
type sqlQueryExecutor struct {
	exec SQLExecutor
}

// AsQueryExecutor оборачивает SQLExecutor в QueryExecutor.
// mysql.Conn и postgres.Conn уже реализуют QueryExecutor напрямую (их
// методы Select/Insert/Update/Delete внутри делают ровно это) — вызывать
// AsQueryExecutor вручную нужно только для собственного SQLExecutor-драйвера.
func AsQueryExecutor(exec SQLExecutor) QueryExecutor {
	return &sqlQueryExecutor{exec: exec}
}

func (a *sqlQueryExecutor) Driver() string                 { return a.exec.Driver() }
func (a *sqlQueryExecutor) Ping(ctx context.Context) error  { return a.exec.Ping(ctx) }
func (a *sqlQueryExecutor) Close() error                    { return a.exec.Close() }

func (a *sqlQueryExecutor) Select(ctx context.Context, q *Query, dest any) error {
	sqlText, args := a.exec.Dialect().BuildSelect(q)

	v := reflect.ValueOf(dest)
	if v.Kind() != reflect.Ptr {
		return fmt.Errorf("core: Select ожидает указатель, получено %T", dest)
	}

	if v.Elem().Kind() == reflect.Slice {
		rows, err := a.exec.QueryContext(ctx, sqlText, args...)
		if err != nil {
			return err
		}
		defer rows.Close()

		elemType := v.Elem().Type().Elem()
		out := reflect.MakeSlice(v.Elem().Type(), 0, 0)
		for rows.Next() {
			itemPtr := reflect.New(elemType)
			if err := scanRowInto(rows, itemPtr.Interface()); err != nil {
				return err
			}
			out = reflect.Append(out, itemPtr.Elem())
		}
		if err := rows.Err(); err != nil {
			return err
		}
		v.Elem().Set(out)
		return nil
	}

	row := a.exec.QueryRowContext(ctx, sqlText, args...)
	if err := scanRowInto(row, dest); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func (a *sqlQueryExecutor) Insert(ctx context.Context, q *Query) (Result, error) {
	sqlText, args := a.exec.Dialect().BuildInsert(q)
	return a.exec.ExecContext(ctx, sqlText, args...)
}

func (a *sqlQueryExecutor) Update(ctx context.Context, q *Query) (Result, error) {
	sqlText, args := a.exec.Dialect().BuildUpdate(q)
	return a.exec.ExecContext(ctx, sqlText, args...)
}

func (a *sqlQueryExecutor) Delete(ctx context.Context, q *Query) (Result, error) {
	sqlText, args := a.exec.Dialect().BuildDelete(q)
	return a.exec.ExecContext(ctx, sqlText, args...)
}

var _ QueryExecutor = (*sqlQueryExecutor)(nil)
