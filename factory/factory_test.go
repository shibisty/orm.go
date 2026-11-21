package factory_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"faker"
	"orm"
	factory "orm/factory"
)

type User struct {
	orm.BaseModel
	orm.Timestamps
	Name   string `db:"name"   fake:"full_name"`
	Email  string `db:"email"  fake:"email"`
	Role   string `db:"role"`
	Active bool   `db:"active"`
}

func (User) TableName() string { return "users" }

type Post struct {
	orm.BaseModel
	UserID int64  `db:"user_id"`
	Title  string `db:"title" fake:"sentence"`
}

func (Post) TableName() string { return "posts" }

// mem stores inserted rows per table and assigns ids; it fails on demand.
type mem struct {
	rows   map[string][]map[string]any
	failOn string
}

func newMem() *mem { return &mem{rows: map[string][]map[string]any{}} }

func (m *mem) Driver() string             { return "mem" }
func (m *mem) Ping(context.Context) error { return nil }
func (m *mem) Close() error               { return nil }
func (m *mem) Insert(_ context.Context, q *orm.Query) (orm.Result, error) {
	if q.Table == m.failOn {
		return orm.Result{}, errors.New("insert failed")
	}
	row := map[string]any{}
	for k, v := range q.Values {
		row[k] = v
	}
	id := int64(len(m.rows[q.Table]) + 1)
	row["id"] = id
	m.rows[q.Table] = append(m.rows[q.Table], row)
	return orm.Result{LastInsertID: id, RowsAffected: 1}, nil
}
func (m *mem) Select(context.Context, *orm.Query, any) error { return orm.ErrNotFound }
func (m *mem) Update(context.Context, *orm.Query) (orm.Result, error) {
	return orm.Result{}, nil
}
func (m *mem) Delete(context.Context, *orm.Query) (orm.Result, error) {
	return orm.Result{}, nil
}

var ctx = context.Background()

var Users = factory.New(func(f *faker.Faker, i int) User {
	return User{Name: f.Person.FullName(), Email: f.Unique().Email(), Role: "user", Active: true}
})

var Posts = factory.Fill[Post]()

func TestMake(t *testing.T) {
	u := Users.Make()
	if u.Name == "" || !strings.Contains(u.Email, "@") || u.ID != 0 {
		t.Fatalf("Make: %+v", u)
	}
	many := Users.Sequence(func(u *User, i int) { u.Role = []string{"admin", "user"}[i%2] }).MakeMany(4)
	if len(many) != 4 || many[0].Role != "admin" || many[1].Role != "user" || many[2].Role != "admin" {
		t.Fatalf("Sequence: %+v", many)
	}
	emails := map[string]bool{}
	for _, u := range Users.MakeMany(50) {
		if emails[u.Email] {
			t.Fatalf("duplicate email %s", u.Email)
		}
		emails[u.Email] = true
	}
	inactive := Users.State(func(u *User) { u.Active = false })
	if inactive.Make().Active || !Users.Make().Active {
		t.Fatal("State changed the original factory")
	}
	p := Posts.Make()
	if p.Title == "" {
		t.Fatalf("Fill: %+v", p)
	}
}

func TestSeed(t *testing.T) {
	a := Users.Seed(7).MakeMany(3)
	b := Users.Seed(7).MakeMany(3)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("same seed, different records:\n%v\n%v", a, b)
	}
	if reflect.DeepEqual(a, Users.Seed(8).MakeMany(3)) {
		t.Fatal("different seeds, same records")
	}
	if Users.Seed(7).Faker() == nil {
		t.Fatal("Faker()")
	}
}

func TestCreate(t *testing.T) {
	m := newMem()
	u, err := Users.Create(ctx, m)
	if err != nil || u.ID != 1 || u.CreatedAt.IsZero() {
		t.Fatalf("Create: %+v %v", u, err)
	}
	if got := m.rows["users"][0]["email"]; got != u.Email {
		t.Fatalf("stored %v", m.rows["users"][0])
	}
	list, err := Users.CreateMany(ctx, m, 3)
	if err != nil || len(list) != 3 || list[2].ID != 4 {
		t.Fatalf("CreateMany: %+v %v", list, err)
	}
}

func TestRelated(t *testing.T) {
	m := newMem()
	authors, err := factory.Has(Users, Posts, 2, func(u *User, p *Post) { p.UserID = u.ID }).CreateMany(ctx, m, 3)
	if err != nil || len(authors) != 3 || len(m.rows["posts"]) != 6 {
		t.Fatalf("Has: %d authors, %d posts, %v", len(authors), len(m.rows["posts"]), err)
	}
	for i, p := range m.rows["posts"] {
		if want := int64(i/2 + 1); p["user_id"] != want {
			t.Fatalf("post %d belongs to %v, want %d", i, p["user_id"], want)
		}
	}

	m = newMem()
	posts, err := factory.For(Posts, Users, func(p *Post, u *User) { p.UserID = u.ID }).CreateMany(ctx, m, 4)
	if err != nil || len(posts) != 4 || len(m.rows["users"]) != 1 || posts[3].UserID != 1 {
		t.Fatalf("For: %+v %v", posts, err)
	}
	one, err := factory.For(Posts, Users, func(p *Post, u *User) { p.UserID = u.ID }).Create(ctx, m)
	if err != nil || one.UserID != 2 {
		t.Fatalf("For.Create: %+v %v", one, err)
	}
}

func TestErrors(t *testing.T) {
	m := newMem()
	m.failOn = "posts"
	_, err := factory.Has(Users, Posts, 1, func(u *User, p *Post) { p.UserID = u.ID }).CreateMany(ctx, m, 2)
	if err == nil || !strings.Contains(err.Error(), "after creating record 1") {
		t.Fatalf("child error: %v", err)
	}
	if _, err := factory.For(Users, Posts, func(u *User, p *Post) {}).Create(ctx, m); err == nil {
		t.Fatal("parent error lost")
	}
	m.failOn = "users"
	if _, err := Users.Create(ctx, m); err == nil || !strings.Contains(err.Error(), "record 1 of 1") {
		t.Fatalf("insert error: %v", err)
	}
	after := Users.AfterCreate(func(context.Context, orm.QueryExecutor, *User) error { return fmt.Errorf("boom") })
	m.failOn = ""
	if _, err := after.CreateMany(ctx, m, 1); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("AfterCreate error: %v", err)
	}
}

type Bad struct {
	orm.BaseModel
	N int `db:"n" fake:"no_such_generator"`
}

func (Bad) TableName() string { return "bad" }

func TestFillPanicsOnBadTag(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("no panic on an unknown fake tag")
		}
	}()
	factory.Fill[Bad]().Make()
}
