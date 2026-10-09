package orm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
)

// sqlQueryExecutor implements QueryExecutor on top of any SQLExecutor,
// using its Dialect to translate a Query into SQL text.
type sqlQueryExecutor struct {
	exec SQLExecutor
}

// AsQueryExecutor wraps an SQLExecutor in a QueryExecutor.
// mysql.Conn and postgres.Conn already implement QueryExecutor directly (their
// Select/Insert/Update/Delete methods do exactly this internally); calling
// AsQueryExecutor by hand is only needed for your own SQLExecutor driver.
func AsQueryExecutor(exec SQLExecutor) QueryExecutor {
	return &sqlQueryExecutor{exec: exec}
}

func (a *sqlQueryExecutor) Driver() string                 { return a.exec.Driver() }
func (a *sqlQueryExecutor) Ping(ctx context.Context) error { return a.exec.Ping(ctx) }
func (a *sqlQueryExecutor) Close() error                   { return a.exec.Close() }

func (a *sqlQueryExecutor) Select(ctx context.Context, q *Query, dest any) error {
	v := reflect.ValueOf(dest)
	if v.Kind() != reflect.Ptr {
		return fmt.Errorf("orm: Select expects a pointer, got %T", dest)
	}

	// SELECT * returns columns in table order, while scanRowInto scans
	// in struct field order. So "*" is replaced with an explicit column list
	// from the db tags: the order matches, and extra table columns don't interfere.
	elem := v.Elem().Type()
	if elem.Kind() == reflect.Slice {
		elem = elem.Elem()
	}
	if isSelectAll(q.Columns) && elem.Kind() == reflect.Struct {
		qc := *q
		qc.Columns = dbColumns(elem)
		if len(q.Joins) > 0 { // with a JOIN, unqualified names are ambiguous (id exists in both)
			for i, c := range qc.Columns {
				qc.Columns[i] = q.Table + "." + c
			}
		}
		q = &qc
	}
	sqlText, args := a.exec.Dialect().BuildSelect(q)

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
	if r, ok := a.exec.Dialect().(InsertReturningID); ok && r.InsertReturnsID() {
		var id int64
		if err := a.exec.QueryRowContext(ctx, sqlText, args...).Scan(&id); err != nil {
			return Result{}, err
		}
		return Result{LastInsertID: id, RowsAffected: 1}, nil
	}
	return a.exec.ExecContext(ctx, sqlText, args...)
}

// InsertReturningID is an optional Dialect extension: BuildInsert returns
// the new record's id via RETURNING (Postgres) rather than via LastInsertId.
type InsertReturningID interface {
	InsertReturnsID() bool
}

func isSelectAll(cols []string) bool {
	return len(cols) == 0 || len(cols) == 1 && cols[0] == "*"
}

// dbColumns returns the columns from the db tags in the same order in which
// scanRowInto scans them (embedded structs recursively).
func dbColumns(t reflect.Type) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			out = append(out, dbColumns(f.Type)...)
			continue
		}
		if tag := f.Tag.Get("db"); tag != "" && tag != "-" {
			out = append(out, tag)
		}
	}
	return out
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
