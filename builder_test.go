package orm_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"orm"
	"orm/drivertest"
)

type Author struct {
	orm.BaseModel
	orm.Timestamps
	Name    string   `db:"name"`
	TeamID  int64    `db:"team_id"`
	Posts   []Post   `rel:"posts"`
	Profile *Profile `rel:"profile"`
	Team    *Team    `rel:"team"`
	Tags    []*Tag   `rel:"tags"`
}

func (Author) TableName() string { return "authors" }
func (Author) Relations() orm.Relations {
	return orm.Relations{
		"posts":   orm.HasMany[Post]("author_id"),
		"profile": orm.HasOne[Profile]("author_id"),
		"team":    orm.BelongsTo[Team]("team_id"),
		"tags":    orm.BelongsToMany[Tag]("author_tag", "author_id", "tag_id"),
	}
}

type Post struct {
	orm.BaseModel
	orm.SoftDeletes
	AuthorID  int64     `db:"author_id"`
	Title     string    `db:"title"`
	Published bool      `db:"published"`
	Author    Author    `rel:"author"`
	Comments  []Comment `rel:"comments"`
}

func (Post) TableName() string { return "posts" }
func (Post) Relations() orm.Relations {
	return orm.Relations{
		"author":   orm.BelongsTo[Author]("author_id"),
		"comments": orm.HasMany[Comment]("post_id"),
	}
}

type Comment struct {
	orm.BaseModel
	PostID int64  `db:"post_id"`
	Body   string `db:"body"`
}

func (Comment) TableName() string { return "comments" }

type Profile struct {
	orm.BaseModel
	AuthorID int64  `db:"author_id"`
	Bio      string `db:"bio"`
}

func (Profile) TableName() string { return "profiles" }

type Team struct {
	orm.BaseModel
	Name string `db:"name"`
}

func (Team) TableName() string { return "teams" }

type Tag struct {
	orm.BaseModel
	Name string `db:"name"`
}

func (Tag) TableName() string { return "tags" }

// Session has a string primary key named token.
type Session struct {
	Token  string `db:"token"`
	UserID int64  `db:"user_id"`
}

func (Session) TableName() string  { return "sessions" }
func (Session) PrimaryKey() string { return "token" }

// Hooked records its hooks; BeforeCreate refuses an empty name.
type Hooked struct {
	orm.BaseModel
	Name  string `db:"name"`
	calls []string
}

func (Hooked) TableName() string { return "hooked" }
func (h *Hooked) BeforeCreate(context.Context) error {
	if h.Name == "" {
		return errors.New("name is required")
	}
	h.calls = append(h.calls, "before-create")
	return nil
}
func (h *Hooked) AfterCreate(context.Context) error {
	h.calls = append(h.calls, "after-create")
	return nil
}
func (h *Hooked) AfterFind(context.Context) error { h.Name = strings.ToUpper(h.Name); return nil }
func (h *Hooked) BeforeDelete(context.Context) error {
	if h.Name == "KEEP" {
		return errors.New("kept")
	}
	return nil
}

func fixedClock(t *testing.T) time.Time {
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	old := orm.Now
	orm.Now = func() time.Time { return now }
	t.Cleanup(func() { orm.Now = old })
	return now
}

func seed(t *testing.T) *memExec {
	t.Helper()
	m := newMem()
	authors := orm.NewRepository[Author](m)
	posts := orm.NewRepository[Post](m)
	comments := orm.NewRepository[Comment](m)
	teams := orm.NewRepository[Team](m)
	tags := orm.NewRepository[Tag](m)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"core", "web"} {
		must(teams.Create(ctx, &Team{Name: name}))
	}
	for i, name := range []string{"ann", "bob", "cid"} {
		must(authors.Create(ctx, &Author{Name: name, TeamID: int64(i%2 + 1)}))
	}
	for _, p := range []Post{{AuthorID: 1, Title: "a1", Published: true}, {AuthorID: 1, Title: "a2"}, {AuthorID: 2, Title: "b1", Published: true}} {
		p := p
		must(posts.Create(ctx, &p))
	}
	for _, c := range []Comment{{PostID: 1, Body: "x"}, {PostID: 1, Body: "y"}, {PostID: 3, Body: "z"}} {
		c := c
		must(comments.Create(ctx, &c))
	}
	must(orm.NewRepository[Profile](m).Create(ctx, &Profile{AuthorID: 2, Bio: "bob's"}))
	for _, name := range []string{"go", "sql"} {
		must(tags.Create(ctx, &Tag{Name: name}))
	}
	for _, link := range [][2]int64{{1, 1}, {1, 2}, {3, 2}} {
		m.Insert(ctx, &orm.Query{Table: "author_tag", Values: map[string]any{"author_id": link[0], "tag_id": link[1], "id": int64(100 + link[0]*10 + link[1])}})
	}
	m.selects = nil
	return m
}

func TestEagerLoading(t *testing.T) {
	m := seed(t)
	authors, err := orm.NewRepository[Author](m).With("posts.comments", "profile", "team", "tags").OrderBy("id").Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(authors) != 3 {
		t.Fatalf("%d authors", len(authors))
	}
	ann, bob, cid := authors[0], authors[1], authors[2]
	if len(ann.Posts) != 2 || len(bob.Posts) != 1 || len(cid.Posts) != 0 || cid.Posts == nil {
		t.Fatalf("posts: %d %d %v", len(ann.Posts), len(bob.Posts), cid.Posts)
	}
	if len(ann.Posts[0].Comments) != 2 || len(bob.Posts[0].Comments) != 1 {
		t.Fatalf("nested comments: %+v", ann.Posts)
	}
	if ann.Profile != nil || bob.Profile == nil || bob.Profile.Bio != "bob's" {
		t.Fatalf("profile: %v %v", ann.Profile, bob.Profile)
	}
	if ann.Team == nil || ann.Team.Name != "core" || bob.Team.Name != "web" {
		t.Fatalf("team: %+v %+v", ann.Team, bob.Team)
	}
	if len(ann.Tags) != 2 || ann.Tags[1].Name != "sql" || len(bob.Tags) != 0 || len(cid.Tags) != 1 {
		t.Fatalf("tags: %v %v %v", ann.Tags, bob.Tags, cid.Tags)
	}
	// One query per relation: no N+1.
	for table, want := range map[string]int{"authors": 1, "posts": 1, "comments": 1, "profiles": 1, "teams": 1, "author_tag": 1, "tags": 1} {
		if got := m.queries(table); got != want {
			t.Errorf("%s: %d queries, want %d", table, got, want)
		}
	}

	// BelongsTo into a struct field, constraints on a relation.
	posts, err := orm.NewRepository[Post](m).Query().
		WithQuery("comments", func(q *orm.Query) { q.Where("body", "!=", "y") }).
		With("author").OrderBy("id").Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if posts[0].Author.Name != "ann" || posts[2].Author.Name != "bob" || len(posts[0].Comments) != 1 {
		t.Fatalf("posts: %+v", posts)
	}

	if _, err := orm.NewRepository[Author](m).With("nope").Get(ctx); err == nil || !strings.Contains(err.Error(), `no relation "nope"`) {
		t.Fatalf("unknown relation: %v", err)
	}
	if _, err := orm.NewRepository[Comment](m).With("post").Get(ctx); err == nil {
		t.Fatal("relation of a model without Relations")
	}
	one, err := orm.NewRepository[Author](m).With("posts").Find(ctx, 1)
	if err != nil || len(one.Posts) != 2 {
		t.Fatalf("Find with relations: %+v %v", one, err)
	}
}

func TestSoftDeletes(t *testing.T) {
	now := fixedClock(t)
	m := seed(t)
	posts := orm.NewRepository[Post](m)
	if err := posts.Delete(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := posts.Find(ctx, 2); !errors.Is(err, orm.ErrNotFound) {
		t.Fatalf("Find of a deleted post: %v", err)
	}
	trashed, err := posts.Query().WithTrashed().Find(ctx, 2)
	if err != nil || !trashed.Trashed() || !trashed.DeletedAt.Equal(now) {
		t.Fatalf("WithTrashed: %+v %v", trashed, err)
	}
	if n, _ := posts.Query().Count(ctx); n != 2 {
		t.Fatalf("Count = %d", n)
	}
	if only, _ := posts.Query().OnlyTrashed().Get(ctx); len(only) != 1 || only[0].ID != 2 {
		t.Fatalf("OnlyTrashed: %+v", only)
	}
	// OR inside the user's conditions stays inside parentheses.
	got, err := posts.Where("title", "=", "a2").OrWhere("title", "=", "b1").Get(ctx)
	if err != nil || len(got) != 1 || got[0].Title != "b1" {
		t.Fatalf("OR with soft deletes: %+v %v", got, err)
	}
	// Eager loading skips deleted records.
	a, _ := orm.NewRepository[Author](m).With("posts").Find(ctx, 1)
	if len(a.Posts) != 1 {
		t.Fatalf("eager-loaded deleted post: %d", len(a.Posts))
	}
	if err := posts.Restore(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if p, err := posts.Find(ctx, 2); err != nil || p.Trashed() {
		t.Fatalf("Restore: %+v %v", p, err)
	}
	if err := posts.ForceDelete(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := posts.Query().WithTrashed().Find(ctx, 2); !errors.Is(err, orm.ErrNotFound) {
		t.Fatalf("ForceDelete: %v", err)
	}
	if _, err := orm.NewRepository[Comment](m).Query().Restore(ctx); err == nil {
		t.Fatal("Restore of a model without SoftDeletes")
	}
}

func TestSoftDeleteSQL(t *testing.T) {
	ex := &recExec{dialect: pgDialect{}}
	posts := orm.NewRepository[Post](orm.AsQueryExecutor(ex))
	posts.Where("a", "=", 1).OrWhere("b", "=", 2).Get(ctx)
	want := `SELECT "id", "deleted_at", "author_id", "title", "published" FROM "posts" WHERE ("a" = $1 OR "b" = $2) AND "deleted_at" IS NULL`
	if ex.sql[0] != want {
		t.Fatalf("\n got %s\nwant %s", ex.sql[0], want)
	}
	ex.sql = nil
	posts.Query().WhereGroup(func(g *orm.Query) { g.Where("a", "=", 1).OrWhere("b", "IN", []int{2, 3}) }).OnlyTrashed().Count(ctx)
	want = `SELECT COUNT(*) FROM (SELECT * FROM "posts" WHERE ("a" = $1 OR "b" IN ($2, $3)) AND "deleted_at" IS NOT NULL) AS orm_count`
	if ex.sql[0] != want {
		t.Fatalf("\n got %s\nwant %s", ex.sql[0], want)
	}
}

func TestTimestamps(t *testing.T) {
	now := fixedClock(t)
	m := newMem()
	authors := orm.NewRepository[Author](m)
	a := &Author{Name: "ann"}
	if err := authors.Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	if !a.CreatedAt.Equal(now) || !a.UpdatedAt.Equal(now) {
		t.Fatalf("Create: %v %v", a.CreatedAt, a.UpdatedAt)
	}
	later := now.Add(time.Hour)
	orm.Now = func() time.Time { return later }
	a.Name = "anna"
	a.CreatedAt = time.Time{} // not written by Update
	if err := authors.Update(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, _ := authors.Find(ctx, a.ID)
	if got.Name != "anna" || !got.CreatedAt.Equal(now) || !got.UpdatedAt.Equal(later) {
		t.Fatalf("Update: %+v", got)
	}
	if _, err := authors.Where("id", "=", a.ID).Update(ctx, map[string]any{"name": "x"}); err != nil {
		t.Fatal(err)
	}
	if m.tables["authors"][0]["updated_at"] != later {
		t.Fatalf("mass update did not touch updated_at: %v", m.tables["authors"][0])
	}
}

func TestHooks(t *testing.T) {
	m := newMem()
	repo := orm.NewRepository[Hooked](m)
	if err := repo.Create(ctx, &Hooked{}); err == nil || len(m.tables["hooked"]) != 0 {
		t.Fatalf("BeforeCreate error did not cancel: %v", err)
	}
	h := &Hooked{Name: "keep"}
	if err := repo.Create(ctx, h); err != nil || strings.Join(h.calls, ",") != "before-create,after-create" {
		t.Fatalf("hooks: %v %v", h.calls, err)
	}
	got, _ := repo.Find(ctx, h.ID)
	if got.Name != "KEEP" {
		t.Fatalf("AfterFind: %q", got.Name)
	}
	if err := repo.Delete(ctx, h.ID); err == nil || len(m.tables["hooked"]) != 1 {
		t.Fatalf("BeforeDelete error did not cancel: %v", err)
	}
	if err := repo.Delete(ctx, 999); err != nil {
		t.Fatalf("deleting a missing record: %v", err)
	}
}

func TestPaginateCountChunk(t *testing.T) {
	m := newMem()
	repo := orm.NewRepository[Comment](m)
	for i := 0; i < 23; i++ {
		repo.Create(ctx, &Comment{PostID: int64(i % 3), Body: "c"})
	}
	p, err := repo.Query().OrderBy("id").Paginate(ctx, 3, 10)
	if err != nil || p.Total != 23 || p.LastPage != 3 || len(p.Items) != 3 || p.Items[0].ID != 21 || p.HasMore() {
		t.Fatalf("page 3: %+v %v", p, err)
	}
	if p, _ := repo.Where("post_id", "=", 99).Paginate(ctx, 1, 10); p.LastPage != 1 || len(p.Items) != 0 || p.Items == nil {
		t.Fatalf("empty page: %+v", p)
	}
	if ok, _ := repo.Where("post_id", "=", 2).Exists(ctx); !ok {
		t.Fatal("Exists")
	}
	var seen int
	err = repo.Query().Chunk(ctx, 10, func(batch []Comment) error { seen += len(batch); return nil })
	if err != nil || seen != 23 {
		t.Fatalf("Chunk: %d %v", seen, err)
	}
	first, err := repo.Query().OrderByDesc("id").First(ctx)
	if err != nil || first.ID != 23 {
		t.Fatalf("First: %+v %v", first, err)
	}
	if _, err := repo.Where("id", "=", 0).First(ctx); !errors.Is(err, orm.ErrNotFound) {
		t.Fatalf("First of nothing: %v", err)
	}
}

func TestStringKey(t *testing.T) {
	m := newMem()
	repo := orm.NewRepository[Session](m)
	if err := repo.Create(ctx, &Session{Token: "abc", UserID: 7}); err != nil {
		t.Fatal(err)
	}
	if m.tables["sessions"][0]["token"] != "abc" {
		t.Fatalf("token not inserted: %v", m.tables["sessions"][0])
	}
	s, err := repo.Find(ctx, "abc")
	if err != nil || s.UserID != 7 {
		t.Fatalf("Find: %+v %v", s, err)
	}
	s.UserID = 8
	if err := repo.Update(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(ctx, "abc"); err != nil {
		t.Fatal(err)
	}
	if n, _ := repo.Query().Count(ctx); n != 0 {
		t.Fatalf("Count after Delete = %d", n)
	}
}

// The shared driver suite runs on the in-memory executor too.
func TestDrivertestOnMemory(t *testing.T) {
	m := newMem()
	drivertest.NoSQL(t, m, func(*testing.T) {
		m.mu.Lock()
		m.tables, m.next = map[string][]map[string]any{}, map[string]int64{}
		m.mu.Unlock()
	})
}

// PtrRel declares Relations and PrimaryKey on the pointer receiver and has
// an unexported tagged field, which is ignored.
type PtrRel struct {
	Code   int64     `db:"code"`
	secret string    `db:"secret"`
	Notes  []Comment `rel:"notes"`
}

func (PtrRel) TableName() string   { return "ptrrel" }
func (*PtrRel) PrimaryKey() string { return "code" }
func (*PtrRel) Relations() orm.Relations {
	return orm.Relations{"notes": orm.HasMany[Comment]("post_id")}
}

func TestReviewFixes(t *testing.T) {
	now := fixedClock(t)
	m := seed(t)

	pr := orm.NewRepository[PtrRel](m)
	if err := pr.Create(ctx, &PtrRel{Code: 1, secret: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, leaked := m.tables["ptrrel"][0]["secret"]; leaked {
		t.Fatal("unexported field inserted")
	}
	got, err := pr.With("notes").Find(ctx, 1)
	if err != nil || len(got.Notes) != 2 {
		t.Fatalf("pointer-receiver Relations/PrimaryKey: %+v %v", got, err)
	}

	// A constraint with OR stays in parentheses.
	m.selects = nil
	authors, err := orm.NewRepository[Author](m).Query().
		WithQuery("posts", func(q *orm.Query) { q.Where("title", "=", "a1").OrWhere("title", "=", "b1") }).
		Find(ctx, 1)
	if err != nil || len(authors.Posts) != 1 {
		t.Fatalf("WithQuery OR: %+v %v", authors, err)
	}
	for _, q := range m.selects {
		if q.Table == "posts" && len(q.Wheres) > 0 && q.Wheres[0].Group == nil {
			t.Fatalf("constraint not grouped: %+v", q.Wheres)
		}
	}

	// Deleting twice keeps the first deleted_at; Update does not restore.
	posts := orm.NewRepository[Post](m)
	p, _ := posts.Find(ctx, 1)
	posts.Delete(ctx, 1)
	orm.Now = func() time.Time { return now.Add(time.Hour) }
	posts.Delete(ctx, 1)
	tr, _ := posts.Query().WithTrashed().Find(ctx, 1)
	if !tr.DeletedAt.Equal(now) {
		t.Fatalf("second Delete moved deleted_at to %v", tr.DeletedAt)
	}
	p.Title = "stale" // p was loaded before the delete
	posts.Update(ctx, p)
	if tr, _ := posts.Query().WithTrashed().Find(ctx, 1); !tr.Trashed() || tr.Title != "stale" {
		t.Fatalf("Update of a stale entity: %+v", tr)
	}

	// Chunk may delete the batch it is given.
	comments := orm.NewRepository[Comment](m)
	seen := 0
	err = comments.Query().Chunk(ctx, 2, func(batch []Comment) error {
		seen += len(batch)
		for _, c := range batch {
			comments.Delete(ctx, c.ID)
		}
		return nil
	})
	if err != nil || seen != 3 {
		t.Fatalf("Chunk with deletes saw %d (%v)", seen, err)
	}

	// Soft deletes with a join on a NoSQL-style executor: plain column names.
	m.selects = nil
	posts.Query().Join("LEFT", "authors", "authors.id = posts.author_id").Get(ctx)
	last := m.selects[0].Wheres[len(m.selects[0].Wheres)-1]
	if last.Column != "deleted_at" {
		t.Fatalf("NoSQL join qualified the column: %q", last.Column)
	}
}

func TestSoftDeleteJoinSQL(t *testing.T) {
	ex := &recExec{dialect: pgDialect{}}
	orm.NewRepository[Post](orm.AsQueryExecutor(ex)).Query().Join("LEFT", "authors", "authors.id = posts.author_id").Find(ctx, 3)
	if !strings.Contains(ex.sql[0], `"posts"."id" = $1 AND "posts"."deleted_at" IS NULL`) {
		t.Fatalf("SQL join: %s", ex.sql[0])
	}
}

type embedsPtr struct {
	*orm.BaseModel
	Name string `db:"name"`
}

func (embedsPtr) TableName() string { return "x" }

func TestEmbeddedPointerIsRejected(t *testing.T) {
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "by value") {
			t.Fatalf("recover = %v", r)
		}
	}()
	orm.NewRepository[embedsPtr](newMem())
}
