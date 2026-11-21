// Package factory builds model records for tests and seeders with faker,
// in the spirit of Laravel model factories:
//
//	var Users = factory.New(func(f *faker.Faker, i int) User {
//	    return User{Name: f.Person.FullName(), Email: f.Unique().Email(), Active: true}
//	})
//
//	u := Users.Make()                                   // not saved
//	admins, err := Users.State(func(u *User) { u.Role = "admin" }).
//	    CreateMany(ctx, conn, 3)                         // saved with orm.Repository
//	authors, err := factory.Has(Users, Posts, 5,
//	    func(u *User, p *Post) { p.UserID = u.ID }).CreateMany(ctx, conn, 2)
//
// A factory is immutable: State, Sequence, Seed and AfterCreate return a
// new factory. Fill[T] builds records from `fake:"..."` struct tags.
package factory

import (
	"context"
	"fmt"

	"faker"
	"orm"
)

// Factory makes records of model T.
type Factory[T orm.Model] struct {
	def    func(f *faker.Faker, i int) T
	states []func(r *T, i int)
	after  []func(ctx context.Context, exec orm.QueryExecutor, r *T) error
	fk     *faker.Faker
}

// New is a factory whose records come from def; i is the record's index in
// one Make/MakeMany/CreateMany call. For values unique across calls use
// f.Unique() (faker keeps what it gave out).
func New[T orm.Model](def func(f *faker.Faker, i int) T) *Factory[T] {
	return &Factory[T]{def: def, fk: faker.New()}
}

// Fill is a factory filling records from their `fake:"..."` tags
// (faker.FillStruct); fields without a tag keep their zero value.
func Fill[T orm.Model]() *Factory[T] {
	return New(func(f *faker.Faker, _ int) T {
		var r T
		if err := f.FillStruct(&r); err != nil {
			panic(fmt.Sprintf("factory.Fill[%T]: %v", r, err))
		}
		return r
	})
}

func (fa *Factory[T]) clone() *Factory[T] {
	c := *fa
	c.states = append([](func(*T, int))(nil), fa.states...)
	c.after = append([](func(context.Context, orm.QueryExecutor, *T) error)(nil), fa.after...)
	return &c
}

// Seed makes the factory deterministic: the same seed gives the same
// records. Unique values (f.Unique()) are unique per seeded factory.
func (fa *Factory[T]) Seed(seed int64) *Factory[T] {
	c := fa.clone()
	c.fk = faker.New(seed)
	return c
}

// Faker is the generator the factory uses (to set Locale or RefDate).
func (fa *Factory[T]) Faker() *faker.Faker { return fa.fk }

// State changes every record after the definition:
// Users.State(func(u *User) { u.Active = false }).
func (fa *Factory[T]) State(fn func(r *T)) *Factory[T] {
	c := fa.clone()
	c.states = append(c.states, func(r *T, _ int) { fn(r) })
	return c
}

// Sequence is State with the record's index among those made in one call:
// Users.Sequence(func(u *User, i int) { u.Role = []string{"admin", "user"}[i%2] }).
func (fa *Factory[T]) Sequence(fn func(r *T, i int)) *Factory[T] {
	c := fa.clone()
	c.states = append(c.states, fn)
	return c
}

// AfterCreate runs after each record is saved (Create, CreateMany), with
// the connection it was saved on: for related records, counters, files.
func (fa *Factory[T]) AfterCreate(fn func(ctx context.Context, exec orm.QueryExecutor, r *T) error) *Factory[T] {
	c := fa.clone()
	c.after = append(c.after, fn)
	return c
}

func (fa *Factory[T]) make(i int) T {
	r := fa.def(fa.fk, i)
	for _, s := range fa.states {
		s(&r, i)
	}
	return r
}

// Make returns one record, not saved.
func (fa *Factory[T]) Make() T { return fa.make(0) }

// MakeMany returns n records, not saved; Sequence sees i = 0..n-1.
func (fa *Factory[T]) MakeMany(n int) []T {
	out := make([]T, n)
	for i := range out {
		out[i] = fa.make(i)
	}
	return out
}

// Create makes one record and saves it with orm.Repository[T] on exec
// (hooks, timestamps and ids as for any Create), then runs AfterCreate.
func (fa *Factory[T]) Create(ctx context.Context, exec orm.QueryExecutor) (*T, error) {
	list, err := fa.CreateMany(ctx, exec, 1)
	if err != nil {
		return nil, err
	}
	return &list[0], nil
}

// CreateMany makes and saves n records. On an error the records saved so
// far stay saved (run it in a transaction to undo them).
func (fa *Factory[T]) CreateMany(ctx context.Context, exec orm.QueryExecutor, n int) ([]T, error) {
	repo := orm.NewRepository[T](exec)
	out := make([]T, 0, n)
	for i := 0; i < n; i++ {
		r := fa.make(i)
		if err := repo.Create(ctx, &r); err != nil {
			return out, fmt.Errorf("factory: record %d of %d: %w", i+1, n, err)
		}
		for _, fn := range fa.after {
			if err := fn(ctx, exec, &r); err != nil {
				return out, fmt.Errorf("factory: after creating record %d: %w", i+1, err)
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// Has saves n child records for every parent the factory creates; link
// sets the child's foreign key from the saved parent:
//
//	factory.Has(Users, Posts, 3, func(u *User, p *Post) { p.UserID = u.ID })
func Has[P, C orm.Model](parent *Factory[P], child *Factory[C], n int, link func(p *P, c *C)) *Factory[P] {
	return parent.AfterCreate(func(ctx context.Context, exec orm.QueryExecutor, p *P) error {
		_, err := child.Sequence(func(c *C, _ int) { link(p, c) }).CreateMany(ctx, exec, n)
		return err
	})
}

// For sets a child's foreign key to a parent created first, once per call:
//
//	posts, err := factory.For(Posts, Users, func(p *Post, u *User) { p.UserID = u.ID }).
//	    CreateMany(ctx, conn, 5)   // five posts of one new user
//
// The parent is created when the first child is made by Create or CreateMany.
func For[C, P orm.Model](child *Factory[C], parent *Factory[P], link func(c *C, p *P)) *forFactory[C, P] {
	return &forFactory[C, P]{child: child, parent: parent, link: link}
}

type forFactory[C, P orm.Model] struct {
	child  *Factory[C]
	parent *Factory[P]
	link   func(c *C, p *P)
}

// CreateMany creates the parent, then n children linked to it.
func (f *forFactory[C, P]) CreateMany(ctx context.Context, exec orm.QueryExecutor, n int) ([]C, error) {
	p, err := f.parent.Create(ctx, exec)
	if err != nil {
		return nil, err
	}
	return f.child.Sequence(func(c *C, _ int) { f.link(c, p) }).CreateMany(ctx, exec, n)
}

// Create creates the parent and one child.
func (f *forFactory[C, P]) Create(ctx context.Context, exec orm.QueryExecutor) (*C, error) {
	list, err := f.CreateMany(ctx, exec, 1)
	if err != nil {
		return nil, err
	}
	return &list[0], nil
}
