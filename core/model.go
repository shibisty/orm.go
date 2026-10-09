package orm

import (
	"context"
	"fmt"
	"reflect"
)

// Model is the minimal Eloquent-style "model" contract.
// A concrete user struct embeds orm.BaseModel or simply
// implements TableName().
type Model interface {
	TableName() string
}

// BaseModel can be embedded in a struct to avoid declaring ID by hand.
type BaseModel struct {
	ID int64 `db:"id"`
}

// Repository is a generic repository for a single model T on top of any
// QueryExecutor (that is, on top of ANY driver: mysql/postgres/mongodb/redis).
// It is the counterpart of an Eloquent model: Find, All, Create, Update, Delete.
type Repository[T Model] struct {
	exec  QueryExecutor
	table string
}

func NewRepository[T Model](exec QueryExecutor) *Repository[T] {
	var zero T
	return &Repository[T]{exec: exec, table: zero.TableName()}
}

// Find looks up a single record by id.
func (r *Repository[T]) Find(ctx context.Context, id int64) (*T, error) {
	q := NewQuery(r.table).Where("id", "=", id).LimitOffset(1, 0)
	var out T
	if err := r.exec.Select(ctx, q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// All returns all records matching the Query conditions.
// If q == nil, it returns all records in the table.
func (r *Repository[T]) All(ctx context.Context, q *Query) ([]T, error) {
	if q == nil {
		q = NewQuery(r.table)
	} else {
		q.Table = r.table
	}
	var out []T
	if err := r.exec.Select(ctx, q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Create inserts a new record; fields are taken from the `db` struct tags.
func (r *Repository[T]) Create(ctx context.Context, entity *T) error {
	values, err := structToValues(entity, true /*skipID*/)
	if err != nil {
		return err
	}
	q := &Query{Table: r.table, Values: values}
	res, err := r.exec.Insert(ctx, q)
	if err != nil {
		return err
	}
	setID(entity, res.LastInsertID)
	return nil
}

// Update updates a record by its ID.
func (r *Repository[T]) Update(ctx context.Context, entity *T) error {
	id, err := getID(entity)
	if err != nil {
		return err
	}
	values, err := structToValues(entity, true)
	if err != nil {
		return err
	}
	q := &Query{Table: r.table, Values: values}
	q.Where("id", "=", id)
	_, err = r.exec.Update(ctx, q)
	return err
}

// Delete deletes a record by ID.
func (r *Repository[T]) Delete(ctx context.Context, id int64) error {
	q := NewQuery(r.table).Where("id", "=", id)
	_, err := r.exec.Delete(ctx, q)
	return err
}

// ---- reflection helpers ----

func structToValues(entity any, skipID bool) (map[string]any, error) {
	v := reflect.ValueOf(entity)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil, fmt.Errorf("orm: entity must be a struct, got %s", v.Kind())
	}

	values := map[string]any{}
	collectFields(v, skipID, values)
	return values, nil
}

func collectFields(v reflect.Value, skipID bool, out map[string]any) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		// Embedded BaseModel/anonymous struct: walk it recursively.
		if field.Anonymous {
			collectFields(v.Field(i), skipID, out)
			continue
		}

		tag := field.Tag.Get("db")
		if tag == "" || tag == "-" {
			continue
		}
		if skipID && tag == "id" {
			continue
		}
		out[tag] = v.Field(i).Interface()
	}
}

func scanRowInto(row Row, dest any) error {
	v := reflect.ValueOf(dest).Elem()
	t := v.Type()

	var scanTargets []any
	var walk func(rv reflect.Value, rt reflect.Type)
	walk = func(rv reflect.Value, rt reflect.Type) {
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if f.Anonymous {
				walk(rv.Field(i), f.Type)
				continue
			}
			if f.Tag.Get("db") == "" || f.Tag.Get("db") == "-" {
				continue
			}
			scanTargets = append(scanTargets, rv.Field(i).Addr().Interface())
		}
	}
	walk(v, t)
	return row.Scan(scanTargets...)
}

func setID(entity any, id int64) {
	v := reflect.ValueOf(entity).Elem()
	f := findFieldByTag(v, "id")
	if f.IsValid() && f.CanSet() && f.Kind() == reflect.Int64 {
		f.SetInt(id)
	}
}

func getID(entity any) (int64, error) {
	v := reflect.ValueOf(entity).Elem()
	f := findFieldByTag(v, "id")
	if !f.IsValid() {
		return 0, fmt.Errorf("orm: model has no field tagged db:\"id\"")
	}
	return f.Int(), nil
}

func findFieldByTag(v reflect.Value, tag string) reflect.Value {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.Anonymous {
			if f := findFieldByTag(v.Field(i), tag); f.IsValid() {
				return f
			}
			continue
		}
		if field.Tag.Get("db") == tag {
			return v.Field(i)
		}
	}
	return reflect.Value{}
}
