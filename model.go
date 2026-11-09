package orm

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"
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
// It is the counterpart of an Eloquent model: Find, All, Create, Update,
// Delete, and Query/Where/With for a typed query (Builder).
type Repository[T Model] struct {
	exec  QueryExecutor
	table string
	info  *modelInfo
}

func NewRepository[T Model](exec QueryExecutor) *Repository[T] {
	var zero T
	return &Repository[T]{exec: exec, table: zero.TableName(), info: infoFor[T]()}
}

// Query starts a typed query of the model (soft-deleted records excluded).
func (r *Repository[T]) Query() *Builder[T] { return newBuilder[T](r.exec) }

// Where starts a typed query with a condition: users.Where("active", "=", true).Get(ctx).
func (r *Repository[T]) Where(column, op string, value any) *Builder[T] {
	return r.Query().Where(column, op, value)
}

// With starts a typed query that eager-loads relations ("posts", "posts.comments").
func (r *Repository[T]) With(relations ...string) *Builder[T] { return r.Query().With(relations...) }

// Find looks up a single record by its primary key; ErrNotFound if there is
// none (or it is soft-deleted).
func (r *Repository[T]) Find(ctx context.Context, id any) (*T, error) {
	return r.Query().Find(ctx, id)
}

// All returns all records matching the Query conditions.
// If q == nil, it returns all records in the table. Soft-deleted records are
// excluded; Query().WithTrashed() includes them.
func (r *Repository[T]) All(ctx context.Context, q *Query) ([]T, error) {
	b := r.Query()
	if q != nil {
		b.q = q.Clone()
		b.q.Table = r.table
	}
	return b.Get(ctx)
}

// IDGenerator is implemented by drivers whose database does not assign ids
// (ClickHouse has no auto-increment): Repository.Create asks it for the id of
// a new record whose id is zero.
type IDGenerator interface {
	NewID() int64
}

// Create inserts a new record; fields are taken from the `db` struct tags.
// The key is left to the database (auto-increment) when the entity's key is
// zero, unless the driver generates ids (IDGenerator); a key set by the
// application is inserted as is. With Timestamps, CreatedAt and UpdatedAt
// are set. BeforeCreate and AfterCreate hooks run around the insert.
func (r *Repository[T]) Create(ctx context.Context, entity *T) error {
	if err := callHook(entity, ctx, BeforeCreator.BeforeCreate); err != nil {
		return err
	}
	v := reflect.ValueOf(entity).Elem()
	mi := r.info
	if mi.timestamps {
		now := Now()
		ts := v.FieldByIndex(mi.columns["created_at"])
		if ts.Interface().(time.Time).IsZero() {
			ts.Set(reflect.ValueOf(now))
		}
		v.FieldByIndex(mi.columns["updated_at"]).Set(reflect.ValueOf(now))
	}
	values, err := mi.values(v, true)
	if err != nil {
		return err
	}
	if id, ok := mi.keyOf(v); ok {
		if gen, ok := r.exec.(IDGenerator); ok && isZeroValue(id) {
			mi.setKey(v, gen.NewID())
			id, _ = mi.keyOf(v)
		}
		if !isZeroValue(id) {
			values[mi.key] = id
		}
	}
	res, err := r.exec.Insert(ctx, &Query{Table: r.table, Values: values, Key: mi.key})
	if err != nil {
		return err
	}
	if res.LastInsertID != 0 {
		mi.setKey(v, res.LastInsertID)
	}
	return callHook(entity, ctx, AfterCreator.AfterCreate)
}

// Update writes every column of the record by its primary key (created_at
// is kept). With Timestamps, UpdatedAt is refreshed. BeforeUpdate and
// AfterUpdate hooks run around it.
func (r *Repository[T]) Update(ctx context.Context, entity *T) error {
	if err := callHook(entity, ctx, BeforeUpdater.BeforeUpdate); err != nil {
		return err
	}
	v := reflect.ValueOf(entity).Elem()
	mi := r.info
	id, ok := mi.keyOf(v)
	if !ok {
		return fmt.Errorf("orm: model %s has no field tagged db:%q", v.Type(), mi.key)
	}
	if mi.timestamps {
		v.FieldByIndex(mi.columns["updated_at"]).Set(reflect.ValueOf(Now()))
	}
	values, err := mi.values(v, true)
	if err != nil {
		return err
	}
	if mi.timestamps {
		delete(values, "created_at")
	}
	if mi.softDelete { // Delete and Restore change it; a stale entity must not
		delete(values, "deleted_at")
	}
	q := &Query{Table: r.table, Values: values}
	q.Where(mi.key, "=", id)
	if _, err := r.exec.Update(ctx, q); err != nil {
		return err
	}
	return callHook(entity, ctx, AfterUpdater.AfterUpdate)
}

// Delete deletes a record by primary key; with SoftDeletes it sets
// deleted_at instead. If the model has BeforeDelete or AfterDelete hooks,
// the record is loaded first (deleting a missing record is not an error).
func (r *Repository[T]) Delete(ctx context.Context, id any) error {
	return r.delete(ctx, id, false)
}

// ForceDelete removes a record even if the model uses SoftDeletes.
func (r *Repository[T]) ForceDelete(ctx context.Context, id any) error {
	return r.delete(ctx, id, true)
}

func (r *Repository[T]) delete(ctx context.Context, id any, force bool) error {
	var entity *T
	var probe T
	_, before := any(&probe).(BeforeDeleter)
	_, after := any(&probe).(AfterDeleter)
	if before || after {
		e, err := r.Query().WithTrashed().Find(ctx, id)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		entity = e
		if err := callHook(entity, ctx, BeforeDeleter.BeforeDelete); err != nil {
			return err
		}
	}
	b := r.Query().Where(r.info.key, "=", id)
	var err error
	if force {
		_, err = b.WithTrashed().ForceDelete(ctx)
	} else {
		_, err = b.Delete(ctx) // an already deleted record keeps its deleted_at
	}
	if err != nil || entity == nil {
		return err
	}
	if r.info.softDelete && !force {
		now := Now()
		reflect.ValueOf(entity).Elem().FieldByIndex(r.info.columns["deleted_at"]).Set(reflect.ValueOf(&now))
	}
	return callHook(entity, ctx, AfterDeleter.AfterDelete)
}

// Restore clears deleted_at of a soft-deleted record.
func (r *Repository[T]) Restore(ctx context.Context, id any) error {
	_, err := r.Query().OnlyTrashed().Where(r.info.key, "=", id).Restore(ctx)
	return err
}

// values are the columns of a struct value; skipKey leaves out the key.
func (mi *modelInfo) values(v reflect.Value, skipKey bool) (map[string]any, error) {
	if p := planOf(v.Type()); p.err != nil {
		return nil, p.err
	} else if p.mapper {
		out := v.Addr().Interface().(Mapper).OrmValues()
		if skipKey {
			delete(out, mi.key)
		}
		return out, nil
	}
	out := make(map[string]any, len(mi.columns))
	for col, path := range mi.columns {
		if skipKey && col == mi.key {
			continue
		}
		out[col] = v.FieldByIndex(path).Interface()
	}
	return out, nil
}

// ---- reflection helpers ----

func scanRowInto(row Row, dest any) error {
	targets, err := scanTargets(dest)
	if err != nil {
		return err
	}
	return row.Scan(targets...)
}
