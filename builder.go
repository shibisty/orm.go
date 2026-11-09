package orm

import (
	"context"
	"fmt"
	"reflect"
	"strings"
)

// Builder is a typed query of model T: conditions, order, limits, scopes,
// eager-loaded relations and soft deletes, ending in Get, First, Find,
// Count, Paginate, Update or Delete. A Builder is used once; Clone copies it.
//
//	posts, err := orm.NewRepository[Post](conn).
//	    Where("published", "=", true).
//	    Scope(Recent).
//	    With("author", "comments.user").
//	    OrderByDesc("id").
//	    Paginate(ctx, 2, 20)
type Builder[T Model] struct {
	exec    QueryExecutor
	info    *modelInfo
	q       *Query
	with    *withNode
	trashed trashMode
}

type trashMode int

const (
	withoutTrashed trashMode = iota
	withTrashed
	onlyTrashed
)

func newBuilder[T Model](exec QueryExecutor) *Builder[T] {
	mi := infoFor[T]()
	return &Builder[T]{exec: exec, info: mi, q: NewQuery(mi.table), with: &withNode{}}
}

// Q starts a typed query of T on exec: orm.Q[User](conn).Where(...).Get(ctx).
func Q[T Model](exec QueryExecutor) *Builder[T] { return newBuilder[T](exec) }

// Query returns the underlying query (without the soft-delete condition).
func (b *Builder[T]) Query() *Query { return b.q }

// Clone copies the builder: the copy can be changed without affecting b.
func (b *Builder[T]) Clone() *Builder[T] {
	c := *b
	c.q = b.q.Clone()
	c.with = b.with.clone()
	return &c
}

func (b *Builder[T]) Where(column, op string, value any) *Builder[T] {
	b.q.Where(column, op, value)
	return b
}

func (b *Builder[T]) OrWhere(column, op string, value any) *Builder[T] {
	b.q.OrWhere(column, op, value)
	return b
}

func (b *Builder[T]) WhereIn(column string, values any) *Builder[T] {
	b.q.Where(column, "IN", values)
	return b
}

func (b *Builder[T]) WhereNotIn(column string, values any) *Builder[T] {
	b.q.Where(column, "NOT IN", values)
	return b
}

func (b *Builder[T]) WhereNull(column string) *Builder[T]    { b.q.WhereNull(column); return b }
func (b *Builder[T]) WhereNotNull(column string) *Builder[T] { b.q.WhereNotNull(column); return b }

// WhereGroup adds conditions in parentheses (see Query.WhereGroup).
func (b *Builder[T]) WhereGroup(fn func(g *Query)) *Builder[T]   { b.q.WhereGroup(fn); return b }
func (b *Builder[T]) OrWhereGroup(fn func(g *Query)) *Builder[T] { b.q.OrWhereGroup(fn); return b }

func (b *Builder[T]) Select(columns ...string) *Builder[T] { b.q.Select(columns...); return b }

func (b *Builder[T]) Join(joinType, table, on string) *Builder[T] {
	b.q.Join(joinType, table, on)
	return b
}

func (b *Builder[T]) OrderBy(column string) *Builder[T]     { b.q.OrderByAsc(column); return b }
func (b *Builder[T]) OrderByDesc(column string) *Builder[T] { b.q.OrderByDesc(column); return b }

// Latest orders by created_at (or the given column), newest first.
func (b *Builder[T]) Latest(column ...string) *Builder[T] {
	col := "created_at"
	if len(column) > 0 {
		col = column[0]
	}
	return b.OrderByDesc(col)
}

func (b *Builder[T]) Limit(n int) *Builder[T]  { b.q.Limit = n; return b }
func (b *Builder[T]) Offset(n int) *Builder[T] { b.q.Offset = n; return b }

// Scope applies reusable query parts, written once for any model:
//
//	func Published(q *orm.Query) { q.Where("published", "=", true) }
//	posts.Query().Scope(Published).Get(ctx)
func (b *Builder[T]) Scope(scopes ...func(q *Query)) *Builder[T] {
	for _, s := range scopes {
		s(b.q)
	}
	return b
}

// With eager-loads relations declared by the model's Relations method into
// its fields tagged `rel:"name"`, with one query per relation (no N+1).
// "posts.comments" loads posts and their comments.
func (b *Builder[T]) With(relations ...string) *Builder[T] {
	for _, r := range relations {
		b.with.add(r, nil)
	}
	return b
}

// WithQuery eager-loads a relation with extra conditions on the related
// query: WithQuery("posts", func(q *orm.Query) { q.Where("published", "=", true) }).
func (b *Builder[T]) WithQuery(relation string, fn func(q *Query)) *Builder[T] {
	b.with.add(relation, fn)
	return b
}

// WithTrashed includes soft-deleted records; OnlyTrashed returns only them.
func (b *Builder[T]) WithTrashed() *Builder[T] { b.trashed = withTrashed; return b }
func (b *Builder[T]) OnlyTrashed() *Builder[T] { b.trashed = onlyTrashed; return b }

// isSQL reports whether the driver runs SQL (where joined tables make
// unqualified columns ambiguous); NoSQL drivers filter the base collection
// by plain names.
func (b *Builder[T]) isSQL() bool {
	switch b.exec.(type) {
	case SQLExecutor, *sqlQueryExecutor:
		return true
	}
	return false
}

// col qualifies a column of the model's table when a SQL query has joins.
func (b *Builder[T]) col(name string) string {
	if len(b.q.Joins) > 0 && b.isSQL() && !strings.Contains(name, ".") {
		return b.info.table + "." + name
	}
	return name
}

// final is the query sent to the driver: the user's conditions (in
// parentheses when they contain OR) and the soft-delete condition.
func (b *Builder[T]) final() *Query {
	q := b.q.Clone()
	if !b.info.softDelete || b.trashed == withTrashed {
		return q
	}
	col := b.col("deleted_at")
	cond := Condition{Bool: "AND", Column: col, Op: "IS NULL"}
	if b.trashed == onlyTrashed {
		cond.Op = "IS NOT NULL"
	}
	for _, c := range q.Wheres {
		if strings.EqualFold(c.Bool, "OR") {
			q.Wheres = []Condition{{Bool: "AND", Group: q.Wheres}}
			break
		}
	}
	q.Wheres = append(q.Wheres, cond)
	return q
}

// Get returns the matching records with their eager-loaded relations.
func (b *Builder[T]) Get(ctx context.Context) ([]T, error) {
	var out []T
	if err := b.exec.Select(ctx, b.final(), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []T{}
	}
	if err := b.loaded(ctx, reflect.ValueOf(out)); err != nil {
		return nil, err
	}
	return out, nil
}

// loaded runs AfterFind hooks and eager loading on a slice of T.
func (b *Builder[T]) loaded(ctx context.Context, slice reflect.Value) error {
	if slice.Len() == 0 {
		return nil
	}
	if _, ok := reflect.New(slice.Type().Elem()).Interface().(AfterFinder); ok {
		for i := 0; i < slice.Len(); i++ {
			if err := slice.Index(i).Addr().Interface().(AfterFinder).AfterFind(ctx); err != nil {
				return err
			}
		}
	}
	return eagerLoad(ctx, b.exec, b.info, slice, b.with)
}

// First returns the first matching record; ErrNotFound if there is none.
func (b *Builder[T]) First(ctx context.Context) (*T, error) {
	c := b.Clone()
	c.q.Limit = 1
	list := make([]T, 1)
	if err := c.exec.Select(ctx, c.final(), &list[0]); err != nil {
		return nil, err // ErrNotFound if there is none
	}
	if err := c.loaded(ctx, reflect.ValueOf(list)); err != nil {
		return nil, err
	}
	return &list[0], nil
}

// Find returns the record with this primary key; ErrNotFound if there is none.
func (b *Builder[T]) Find(ctx context.Context, id any) (*T, error) {
	return b.Clone().Where(b.col(b.info.key), "=", id).First(ctx)
}

// Count returns the number of matching records (Limit and Offset ignored).
func (b *Builder[T]) Count(ctx context.Context) (int64, error) {
	q := b.final()
	q.Limit, q.Offset, q.OrderBy = 0, 0, nil
	if c, ok := b.exec.(Counter); ok {
		return c.Count(ctx, q)
	}
	if s, ok := b.exec.(SQLExecutor); ok { // drivers' Conn types wrap the adapter
		return AsQueryExecutor(s).(Counter).Count(ctx, q)
	}
	var all []T
	if err := b.exec.Select(ctx, q, &all); err != nil {
		return 0, err
	}
	return int64(len(all)), nil
}

// Exists reports whether any record matches.
func (b *Builder[T]) Exists(ctx context.Context) (bool, error) {
	n, err := b.Count(ctx)
	return n > 0, err
}

// Page is one page of results (Paginate).
type Page[T any] struct {
	Items    []T   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PerPage  int   `json:"per_page"`
	LastPage int   `json:"last_page"`
}

// HasMore reports whether there is a page after this one.
func (p Page[T]) HasMore() bool { return p.Page < p.LastPage }

// Paginate returns page (1-based) of perPage records and the total count.
func (b *Builder[T]) Paginate(ctx context.Context, page, perPage int) (Page[T], error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		return Page[T]{}, fmt.Errorf("orm: Paginate: perPage must be positive, got %d", perPage)
	}
	total, err := b.Count(ctx)
	if err != nil {
		return Page[T]{}, err
	}
	p := Page[T]{Total: total, Page: page, PerPage: perPage, LastPage: int((total + int64(perPage) - 1) / int64(perPage))}
	if p.LastPage == 0 {
		p.LastPage = 1
	}
	c := b.Clone()
	c.q.Limit, c.q.Offset = perPage, (page-1)*perPage
	if p.Items, err = c.Get(ctx); err != nil {
		return Page[T]{}, err
	}
	return p, nil
}

// Chunk calls fn with consecutive batches of size records, until fn returns
// an error or the records run out. Without an OrderBy it pages by the
// primary key (key > last), so fn may update or delete the batch; with an
// OrderBy it pages by offset.
func (b *Builder[T]) Chunk(ctx context.Context, size int, fn func([]T) error) error {
	if size < 1 {
		return fmt.Errorf("orm: Chunk: size must be positive, got %d", size)
	}
	byKey := len(b.q.OrderBy) == 0 && b.info.keyPath != nil
	var last any
	for offset := 0; ; offset += size {
		c := b.Clone()
		if byKey {
			c.q.OrderByAsc(b.col(b.info.key))
			if last != nil {
				c.Where(b.col(b.info.key), ">", last)
			}
			c.q.Limit = size
		} else {
			c.q.Limit, c.q.Offset = size, offset
		}
		batch, err := c.Get(ctx)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		if byKey {
			last, _ = b.info.keyOf(reflect.ValueOf(batch).Index(len(batch) - 1))
		}
		if err := fn(batch); err != nil {
			return err
		}
		if len(batch) < size {
			return nil
		}
	}
}

// Update sets columns of every matching record (no hooks); with Timestamps
// updated_at is set too. It returns the number of records the driver
// reports (0 on ClickHouse, which does not report it).
func (b *Builder[T]) Update(ctx context.Context, values map[string]any) (int64, error) {
	q := b.final()
	q.Values = make(map[string]any, len(values)+1)
	for k, v := range values {
		q.Values[k] = v
	}
	if b.info.timestamps {
		if _, set := q.Values["updated_at"]; !set {
			q.Values["updated_at"] = Now()
		}
	}
	res, err := b.exec.Update(ctx, q)
	return res.RowsAffected, err
}

// Delete deletes every matching record (no hooks); with SoftDeletes it sets
// deleted_at instead.
func (b *Builder[T]) Delete(ctx context.Context) (int64, error) {
	if b.info.softDelete {
		return b.Update(ctx, map[string]any{"deleted_at": Now()})
	}
	return b.ForceDelete(ctx)
}

// ForceDelete removes every matching record even with SoftDeletes.
func (b *Builder[T]) ForceDelete(ctx context.Context) (int64, error) {
	res, err := b.exec.Delete(ctx, b.final())
	return res.RowsAffected, err
}

// Restore clears deleted_at of the matching soft-deleted records.
func (b *Builder[T]) Restore(ctx context.Context) (int64, error) {
	if !b.info.softDelete {
		return 0, fmt.Errorf("orm: %s does not use SoftDeletes", b.info.table)
	}
	c := b.Clone()
	if c.trashed == withoutTrashed {
		c.trashed = onlyTrashed
	}
	return c.Update(ctx, map[string]any{"deleted_at": nil})
}
