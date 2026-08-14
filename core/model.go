package core

import (
	"context"
	"fmt"
	"reflect"
)

// Model — минимальный контракт "модели" в духе Eloquent.
// Конкретная структура пользователя встраивает core.BaseModel или просто
// реализует TableName().
type Model interface {
	TableName() string
}

// BaseModel можно встроить в структуру, чтобы не объявлять ID вручную.
type BaseModel struct {
	ID int64 `db:"id"`
}

// Repository — generic-репозиторий для одной модели T поверх любого
// QueryExecutor (то есть поверх ЛЮБОГО драйвера — mysql/postgres/mongodb/redis).
// Аналог Eloquent-модели: Find, All, Create, Update, Delete.
type Repository[T Model] struct {
	exec  QueryExecutor
	table string
}

func NewRepository[T Model](exec QueryExecutor) *Repository[T] {
	var zero T
	return &Repository[T]{exec: exec, table: zero.TableName()}
}

// Find ищет одну запись по id.
func (r *Repository[T]) Find(ctx context.Context, id int64) (*T, error) {
	q := NewQuery(r.table).Where("id", "=", id).LimitOffset(1, 0)
	var out T
	if err := r.exec.Select(ctx, q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// All возвращает все записи, попавшие в условия Query.
// Если q == nil — возвращает все записи таблицы.
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

// Create вставляет новую запись, поля берутся из struct-тегов `db`.
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

// Update обновляет запись по её ID.
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

// Delete удаляет запись по ID.
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
		return nil, fmt.Errorf("goorm: entity должен быть структурой, получено %s", v.Kind())
	}

	values := map[string]any{}
	collectFields(v, skipID, values)
	return values, nil
}

func collectFields(v reflect.Value, skipID bool, out map[string]any) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		// Встроенный BaseModel/анонимная структура — рекурсивно разбираем.
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
		return 0, fmt.Errorf("goorm: у модели нет поля с тегом db:\"id\"")
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
