package orm

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"time"
)

// Timestamps can be embedded in a model: Create fills CreatedAt and
// UpdatedAt, Update refreshes UpdatedAt (schema: Blueprint.Timestamps()).
type Timestamps struct {
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// SoftDeletes can be embedded in a model: Delete sets DeletedAt instead of
// removing the record, and queries skip deleted records unless WithTrashed
// or OnlyTrashed (schema: Blueprint.SoftDeletes()).
type SoftDeletes struct {
	DeletedAt *time.Time `db:"deleted_at"`
}

// Trashed reports whether the record is soft-deleted.
func (s SoftDeletes) Trashed() bool { return s.DeletedAt != nil }

// KeyNamer is implemented by models whose primary key column is not "id".
type KeyNamer interface {
	PrimaryKey() string
}

// Now is the clock of timestamps and soft deletes (UTC, microseconds: what
// every supported database stores without rounding). Tests may replace it.
var Now = func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

// Hooks: a model implements any of them on its pointer type, and Repository
// and Builder call them. An error from a Before hook cancels the operation.
type (
	BeforeCreator interface {
		BeforeCreate(ctx context.Context) error
	}
	AfterCreator interface {
		AfterCreate(ctx context.Context) error
	}
	BeforeUpdater interface {
		BeforeUpdate(ctx context.Context) error
	}
	AfterUpdater interface {
		AfterUpdate(ctx context.Context) error
	}
	BeforeDeleter interface {
		BeforeDelete(ctx context.Context) error
	}
	AfterDeleter interface {
		AfterDelete(ctx context.Context) error
	}
	AfterFinder interface {
		AfterFind(ctx context.Context) error
	}
)

// modelInfo is what the orm knows about a model type, computed once.
type modelInfo struct {
	table      string
	key        string // primary key column
	keyPath    []int  // field index path of the key; nil if the model has none
	timestamps bool
	softDelete bool
	columns    map[string][]int // db tag → field index path
	relations  map[string]relField
}

// relField is a struct field filled by eager loading: `rel:"posts"`.
type relField struct {
	path []int
	typ  reflect.Type
}

var (
	timestampsType  = reflect.TypeOf(Timestamps{})
	softDeletesType = reflect.TypeOf(SoftDeletes{})
	infoCache       sync.Map // reflect.Type → *modelInfo
)

func infoOf(t reflect.Type) *modelInfo {
	if mi, ok := infoCache.Load(t); ok {
		return mi.(*modelInfo)
	}
	mi := &modelInfo{key: "id", columns: map[string][]int{}, relations: map[string]relField{}}
	if m, ok := asIface[Model](t); ok {
		mi.table = m.TableName()
	}
	if k, ok := asIface[KeyNamer](t); ok && k.PrimaryKey() != "" {
		mi.key = k.PrimaryKey()
	}
	var walk func(rt reflect.Type, prefix []int)
	walk = func(rt reflect.Type, prefix []int) {
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			path := append(append([]int(nil), prefix...), i)
			if f.Anonymous && f.Type.Kind() == reflect.Ptr && f.Type.Elem().Kind() == reflect.Struct {
				panic(fmt.Sprintf("orm: %s embeds *%s; embed model structs by value (%s)", t, f.Type.Elem().Name(), f.Type.Elem().Name()))
			}
			if f.Anonymous && f.Type.Kind() == reflect.Struct {
				switch f.Type {
				case timestampsType:
					mi.timestamps = true
				case softDeletesType:
					mi.softDelete = true
				}
				walk(f.Type, path)
				continue
			}
			if !f.IsExported() {
				continue
			}
			if name := f.Tag.Get("rel"); name != "" {
				mi.relations[name] = relField{path: path, typ: f.Type}
				continue
			}
			tag := f.Tag.Get("db")
			if tag == "" || tag == "-" {
				continue
			}
			if _, dup := mi.columns[tag]; !dup {
				mi.columns[tag] = path
			}
		}
	}
	walk(t, nil)
	mi.keyPath = mi.columns[mi.key]
	actual, _ := infoCache.LoadOrStore(t, mi)
	return actual.(*modelInfo)
}

// asIface finds an interface implemented by T or *T (value or pointer
// receiver methods).
func asIface[I any](t reflect.Type) (I, bool) {
	if i, ok := reflect.New(t).Elem().Interface().(I); ok {
		return i, true
	}
	i, ok := reflect.New(t).Interface().(I)
	return i, ok
}

func infoFor[T any]() *modelInfo {
	var zero T
	return infoOf(reflect.TypeOf(zero))
}

// keyOf returns the primary key value of an addressable struct value.
func (mi *modelInfo) keyOf(v reflect.Value) (any, bool) {
	if mi.keyPath == nil {
		return nil, false
	}
	return v.FieldByIndex(mi.keyPath).Interface(), true
}

func (mi *modelInfo) column(v reflect.Value, col string) (reflect.Value, bool) {
	path, ok := mi.columns[col]
	if !ok {
		return reflect.Value{}, false
	}
	return v.FieldByIndex(path), true
}

// setKey stores a database-assigned id (int64) in the key field, converting
// to its integer kind; other kinds are left alone.
func (mi *modelInfo) setKey(v reflect.Value, id int64) {
	if mi.keyPath == nil {
		return
	}
	f := v.FieldByIndex(mi.keyPath)
	switch f.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		f.SetInt(id)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		f.SetUint(uint64(id))
	}
}

func isZeroValue(v any) bool {
	if v == nil {
		return true
	}
	return reflect.ValueOf(v).IsZero()
}

// keyString normalizes a key for matching related records: 7, int32(7),
// float64(7) (JSON) and "7" are the same key.
func keyString(v any) string {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return ""
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		f := rv.Float()
		if f == float64(int64(f)) {
			return strconv.FormatInt(int64(f), 10)
		}
		return strconv.FormatFloat(f, 'g', -1, 64)
	case reflect.Invalid:
		return ""
	}
	if s, ok := rv.Interface().(fmt.Stringer); ok {
		return s.String()
	}
	return fmt.Sprint(rv.Interface())
}

// call runs a hook if *T implements it.
func callHook[H any](entity any, ctx context.Context, run func(H, context.Context) error) error {
	if h, ok := entity.(H); ok {
		return run(h, ctx)
	}
	return nil
}
