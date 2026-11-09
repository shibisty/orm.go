package orm

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Relations are a model's relations by name, returned by its Relations
// method; each fills the struct field tagged `rel:"<name>"` when loaded with
// Builder.With:
//
//	type User struct {
//	    orm.BaseModel
//	    TeamID  int64   `db:"team_id"`
//	    Posts   []Post  `rel:"posts"`
//	    Team    *Team   `rel:"team"`
//	    Roles   []Role  `rel:"roles"`
//	}
//
//	func (User) Relations() orm.Relations {
//	    return orm.Relations{
//	        "posts": orm.HasMany[Post]("user_id"),
//	        "team":  orm.BelongsTo[Team]("team_id"),
//	        "roles": orm.BelongsToMany[Role]("role_user", "user_id", "role_id"),
//	    }
//	}
//
// A field holds []C or []*C for many records, *C or C for one. Related
// records are loaded with one query per relation (WHERE key IN (...)), on
// every driver.
type Relations map[string]Relation

// Relater is implemented by models with relations.
type Relater interface {
	Relations() Relations
}

// Relation loads related records into a batch of parent records.
type Relation interface {
	load(ctx context.Context, exec QueryExecutor, parent *modelInfo, parents reflect.Value, field relField, node *withNode) error
}

// HasMany: the related model C has a column (foreignKey) holding the
// parent's primary key: User HasMany Post via posts.user_id.
func HasMany[C Model](foreignKey string) Relation { return hasRel[C]{fk: foreignKey, many: true} }

// HasOne is HasMany with at most one related record: User HasOne Profile.
func HasOne[C Model](foreignKey string) Relation { return hasRel[C]{fk: foreignKey} }

// BelongsTo: this model has a column (foreignKey) holding the primary key of
// the related model C: Post BelongsTo User via posts.user_id.
func BelongsTo[C Model](foreignKey string) Relation { return belongsTo[C]{fk: foreignKey} }

// BelongsToMany: records are linked through a pivot table with the parent's
// key (foreignPivotKey) and the related key (relatedPivotKey):
// User BelongsToMany Role through role_user(user_id, role_id).
func BelongsToMany[C Model](pivot, foreignPivotKey, relatedPivotKey string) Relation {
	return belongsToMany[C]{pivot: pivot, fpk: foreignPivotKey, rpk: relatedPivotKey}
}

// withNode is the tree of relations to load: "posts.comments" and
// "posts.author" are posts → {comments, author}; fn constrains a level.
type withNode struct {
	children map[string]*withNode
	order    []string
	fn       func(*Query)
}

func (n *withNode) add(path string, fn func(*Query)) {
	cur := n
	for _, name := range strings.Split(path, ".") {
		name = strings.TrimSpace(name)
		if cur.children == nil {
			cur.children = map[string]*withNode{}
		}
		next, ok := cur.children[name]
		if !ok {
			next = &withNode{}
			cur.children[name] = next
			cur.order = append(cur.order, name)
		}
		cur = next
	}
	if fn != nil {
		cur.fn = fn
	}
}

func (n *withNode) clone() *withNode {
	if n == nil {
		return &withNode{}
	}
	c := &withNode{fn: n.fn, order: append([]string(nil), n.order...)}
	if n.children != nil {
		c.children = make(map[string]*withNode, len(n.children))
		for k, v := range n.children {
			c.children[k] = v.clone()
		}
	}
	return c
}

// eagerLoad loads node's relations into every element of parents (a slice
// of model structs).
func eagerLoad(ctx context.Context, exec QueryExecutor, mi *modelInfo, parents reflect.Value, node *withNode) error {
	if node == nil || len(node.order) == 0 || parents.Len() == 0 {
		return nil
	}
	var rels Relations
	if rel, ok := asIface[Relater](parents.Type().Elem()); ok {
		rels = rel.Relations()
	}
	for _, name := range node.order {
		r, ok := rels[name]
		if !ok {
			return fmt.Errorf("orm: %s has no relation %q (add it to its Relations method)", parents.Type().Elem(), name)
		}
		field, ok := mi.relations[name]
		if !ok {
			return fmt.Errorf("orm: %s has no field tagged rel:%q for relation %q", parents.Type().Elem(), name, name)
		}
		if err := r.load(ctx, exec, mi, parents, field, node.children[name]); err != nil {
			return fmt.Errorf("orm: loading %s.%s: %w", parents.Type().Elem(), name, err)
		}
	}
	return nil
}

// related queries C with the nested relations and constraints of node.
func related[C Model](exec QueryExecutor, node *withNode) *Builder[C] {
	b := newBuilder[C](exec)
	if node != nil {
		if node.fn != nil {
			// The constraint's conditions go in parentheses, so its OR does
			// not escape the key condition added after it.
			g := NewQuery(b.q.Table)
			node.fn(g)
			if len(g.Wheres) > 0 {
				b.q.Wheres = append(b.q.Wheres, Condition{Bool: "AND", Group: g.Wheres})
			}
			b.q.Columns, b.q.Joins, b.q.OrderBy, b.q.GroupBy = g.Columns, g.Joins, g.OrderBy, g.GroupBy
			b.q.Limit, b.q.Offset = g.Limit, g.Offset
		}
		b.with = node.clone()
		b.with.fn = nil
	}
	return b
}

// columnValues collects the distinct values of a column of the parents.
func columnValues(mi *modelInfo, parents reflect.Value, col string) ([]any, error) {
	seen := map[string]bool{}
	var out []any
	for i := 0; i < parents.Len(); i++ {
		f, ok := mi.column(parents.Index(i), col)
		if !ok {
			return nil, fmt.Errorf("%s has no field tagged db:%q", parents.Type().Elem(), col)
		}
		v := f.Interface()
		if f.Kind() == reflect.Ptr {
			if f.IsNil() {
				continue
			}
			v = f.Elem().Interface()
		}
		k := keyString(v)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, v)
	}
	return out, nil
}

// assign stores related records (a slice of C, addressable) in a parent's
// field of type []C, []*C, *C or C.
func assign(field reflect.Value, items []reflect.Value) error {
	ft := field.Type()
	switch {
	case ft.Kind() == reflect.Slice:
		out := reflect.MakeSlice(ft, 0, len(items))
		for _, it := range items {
			if ft.Elem().Kind() == reflect.Ptr {
				out = reflect.Append(out, it.Addr())
			} else {
				out = reflect.Append(out, it)
			}
		}
		field.Set(out)
	case ft.Kind() == reflect.Ptr:
		if len(items) == 0 {
			field.Set(reflect.Zero(ft))
		} else {
			field.Set(items[0].Addr())
		}
	case ft.Kind() == reflect.Struct:
		if len(items) == 0 {
			field.Set(reflect.Zero(ft))
		} else {
			field.Set(items[0])
		}
	default:
		return fmt.Errorf("a relation field must be a slice, pointer or struct, not %s", ft)
	}
	return nil
}

func checkField[C any](field relField) error {
	ct := reflect.TypeOf((*C)(nil)).Elem()
	t := field.typ
	if t.Kind() == reflect.Slice || t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t != ct {
		return fmt.Errorf("the field is %s, but the relation loads %s", field.typ, ct)
	}
	return nil
}

// ---- HasMany / HasOne ----

type hasRel[C Model] struct {
	fk   string
	many bool
}

func (r hasRel[C]) load(ctx context.Context, exec QueryExecutor, mi *modelInfo, parents reflect.Value, field relField, node *withNode) error {
	if err := checkField[C](field); err != nil {
		return err
	}
	keys, err := columnValues(mi, parents, mi.key)
	if err != nil {
		return err
	}
	var children []C
	if len(keys) > 0 {
		if children, err = related[C](exec, node).WhereIn(r.fk, keys).Get(ctx); err != nil {
			return err
		}
	}
	ci := infoFor[C]()
	cv := reflect.ValueOf(children)
	groups := map[string][]reflect.Value{}
	for i := 0; i < cv.Len(); i++ {
		f, ok := ci.column(cv.Index(i), r.fk)
		if !ok {
			return fmt.Errorf("%T has no field tagged db:%q", children, r.fk)
		}
		k := keyString(f.Interface())
		groups[k] = append(groups[k], cv.Index(i))
	}
	for i := 0; i < parents.Len(); i++ {
		p := parents.Index(i)
		key, _ := mi.keyOf(p)
		items := groups[keyString(key)]
		if !r.many && len(items) > 1 {
			items = items[:1]
		}
		if err := assign(p.FieldByIndex(field.path), items); err != nil {
			return err
		}
	}
	return nil
}

// ---- BelongsTo ----

type belongsTo[C Model] struct{ fk string }

func (r belongsTo[C]) load(ctx context.Context, exec QueryExecutor, mi *modelInfo, parents reflect.Value, field relField, node *withNode) error {
	if err := checkField[C](field); err != nil {
		return err
	}
	ids, err := columnValues(mi, parents, r.fk)
	if err != nil {
		return err
	}
	ci := infoFor[C]()
	var owners []C
	if len(ids) > 0 {
		if owners, err = related[C](exec, node).WhereIn(ci.key, ids).Get(ctx); err != nil {
			return err
		}
	}
	ov := reflect.ValueOf(owners)
	byKey := map[string]reflect.Value{}
	for i := 0; i < ov.Len(); i++ {
		k, _ := ci.keyOf(ov.Index(i))
		byKey[keyString(k)] = ov.Index(i)
	}
	for i := 0; i < parents.Len(); i++ {
		p := parents.Index(i)
		f, _ := mi.column(p, r.fk)
		var items []reflect.Value
		if o, ok := byKey[keyString(f.Interface())]; ok {
			items = []reflect.Value{o}
		}
		if err := assign(p.FieldByIndex(field.path), items); err != nil {
			return err
		}
	}
	return nil
}

// ---- BelongsToMany ----

type belongsToMany[C Model] struct{ pivot, fpk, rpk string }

func (r belongsToMany[C]) load(ctx context.Context, exec QueryExecutor, mi *modelInfo, parents reflect.Value, field relField, node *withNode) error {
	if err := checkField[C](field); err != nil {
		return err
	}
	keys, err := columnValues(mi, parents, mi.key)
	if err != nil {
		return err
	}
	ci := infoFor[C]()
	if len(keys) == 0 || ci.keyPath == nil || mi.keyPath == nil {
		return r.assignAll(mi, parents, field, nil, nil)
	}
	// Pivot rows are scanned into a struct made for the two key columns,
	// typed like the keys, so every driver can fill it.
	parentKeyType := parents.Type().Elem().FieldByIndex(mi.keyPath).Type
	childKeyType := reflect.TypeOf((*C)(nil)).Elem().FieldByIndex(ci.keyPath).Type
	pivotType := reflect.StructOf([]reflect.StructField{
		{Name: "Parent", Type: parentKeyType, Tag: reflect.StructTag(fmt.Sprintf(`db:%q`, r.fpk))},
		{Name: "Child", Type: childKeyType, Tag: reflect.StructTag(fmt.Sprintf(`db:%q`, r.rpk))},
	})
	rows := reflect.New(reflect.SliceOf(pivotType))
	if err := exec.Select(ctx, NewQuery(r.pivot).Where(r.fpk, "IN", keys), rows.Interface()); err != nil {
		return err
	}
	pivots := rows.Elem()
	var ids []any
	seen := map[string]bool{}
	links := map[string][]string{} // parent key → child keys, in pivot order
	for i := 0; i < pivots.Len(); i++ {
		pk, ck := keyString(pivots.Index(i).Field(0).Interface()), pivots.Index(i).Field(1).Interface()
		links[pk] = append(links[pk], keyString(ck))
		if !seen[keyString(ck)] {
			seen[keyString(ck)] = true
			ids = append(ids, ck)
		}
	}
	var children []C
	if len(ids) > 0 {
		if children, err = related[C](exec, node).WhereIn(ci.key, ids).Get(ctx); err != nil {
			return err
		}
	}
	cv := reflect.ValueOf(children)
	byKey := map[string]reflect.Value{}
	for i := 0; i < cv.Len(); i++ {
		k, _ := ci.keyOf(cv.Index(i))
		byKey[keyString(k)] = cv.Index(i)
	}
	return r.assignAll(mi, parents, field, links, byKey)
}

func (r belongsToMany[C]) assignAll(mi *modelInfo, parents reflect.Value, field relField, links map[string][]string, byKey map[string]reflect.Value) error {
	for i := 0; i < parents.Len(); i++ {
		p := parents.Index(i)
		key, _ := mi.keyOf(p)
		var items []reflect.Value
		for _, ck := range links[keyString(key)] {
			if c, ok := byKey[ck]; ok { // a soft-deleted or missing record is skipped
				items = append(items, c)
			}
		}
		if err := assign(p.FieldByIndex(field.path), items); err != nil {
			return err
		}
	}
	return nil
}

// RelationNames lists the relations a model declares, sorted (for tools
// and error messages).
func RelationNames[T Model]() []string {
	var zero T
	r, ok := asIface[Relater](reflect.TypeOf(zero))
	if !ok {
		return nil
	}
	var names []string
	for n := range r.Relations() {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
