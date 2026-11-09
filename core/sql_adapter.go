package orm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
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

		// Rows are scanned in place into a growing slice: no per-row copy.
		out := reflect.New(v.Elem().Type()).Elem()
		for rows.Next() {
			n := out.Len()
			out.Grow(1)
			out.SetLen(n + 1)
			if err := scanRowInto(rows, out.Index(n).Addr().Interface()); err != nil {
				return err
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if out.IsNil() {
			out = reflect.MakeSlice(out.Type(), 0, 0) // no rows: empty, not nil
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
	d := a.exec.Dialect()
	sqlText, args := d.BuildInsert(q)
	if r, ok := d.(InsertReturningID); ok && r.InsertReturnsID() {
		if key := q.KeyColumn(); key != "id" { // dialects append RETURNING id
			sqlText = strings.TrimSuffix(sqlText, " RETURNING id") + " RETURNING " + QuoteIdent(d, key)
		}
		var id any
		if err := a.exec.QueryRowContext(ctx, sqlText, args...).Scan(&id); err != nil {
			return Result{}, err
		}
		res := Result{RowsAffected: 1}
		if n, err := toInt64(id); err == nil { // a string or UUID key is the application's
			res.LastInsertID = n
		}
		return res, nil
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
	return append([]string(nil), planOf(t).cols...)
}

// Count runs SELECT COUNT(*) FROM (<the query>) AS orm_count (Counter):
// joins, groups and conditions count as they select.
func (a *sqlQueryExecutor) Count(ctx context.Context, q *Query) (int64, error) {
	qc := q.Clone()
	qc.OrderBy, qc.Limit, qc.Offset = nil, 0, 0
	if isSelectAll(qc.Columns) {
		qc.Columns = []string{"*"}
		if len(qc.Joins) > 0 {
			qc.Columns = []string{qc.Table + ".*"}
		}
	}
	inner, args := a.exec.Dialect().BuildSelect(qc)
	var n int64
	err := a.exec.QueryRowContext(ctx, "SELECT COUNT(*) FROM ("+inner+") AS orm_count", args...).Scan(&n)
	return n, err
}

func (a *sqlQueryExecutor) Update(ctx context.Context, q *Query) (Result, error) {
	sqlText, args := a.exec.Dialect().BuildUpdate(q)
	return a.exec.ExecContext(ctx, sqlText, args...)
}

func (a *sqlQueryExecutor) Delete(ctx context.Context, q *Query) (Result, error) {
	sqlText, args := a.exec.Dialect().BuildDelete(q)
	return a.exec.ExecContext(ctx, sqlText, args...)
}

var (
	_ QueryExecutor = (*sqlQueryExecutor)(nil)
	_ Counter       = (*sqlQueryExecutor)(nil)
)
