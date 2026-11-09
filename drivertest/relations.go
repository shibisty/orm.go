package drivertest

import (
	"context"
	"errors"
	"testing"
	"time"

	"orm"
	"orm/schema"
)

// Tables are the tables (collections) the suites use; a NoSQL reset
// function must clear all of them.
var Tables = []string{table, postsTable, tagsTable, pivotTable}

const (
	postsTable = "drivertest_posts"
	tagsTable  = "drivertest_tags"
	pivotTable = "drivertest_post_tag"
)

// Post has timestamps, soft deletes and relations to User and Tag.
type Post struct {
	orm.BaseModel
	orm.Timestamps
	orm.SoftDeletes
	UserID int64  `db:"user_id"`
	Title  string `db:"title"`
	User   *User  `rel:"user"`
	Tags   []Tag  `rel:"tags"`
}

func (Post) TableName() string { return postsTable }
func (Post) Relations() orm.Relations {
	return orm.Relations{
		"user": orm.BelongsTo[User]("user_id"),
		"tags": orm.BelongsToMany[Tag](pivotTable, "post_id", "tag_id"),
	}
}

type Tag struct {
	orm.BaseModel
	Name string `db:"name"`
}

func (Tag) TableName() string { return tagsTable }

// author is User with its posts.
type author struct {
	User
	Posts []Post `rel:"posts"`
}

func (author) Relations() orm.Relations {
	return orm.Relations{"posts": orm.HasMany[Post]("user_id")}
}

// createRelationTables creates the tables of the relations check.
func createRelationTables(ctx context.Context, t *testing.T, sb *schema.Builder) {
	t.Helper()
	for _, tbl := range []string{pivotTable, postsTable, tagsTable} {
		if err := sb.DropIfExists(ctx, tbl); err != nil {
			t.Fatalf("drop %s: %v", tbl, err)
		}
	}
	steps := []struct {
		name string
		fn   func(*schema.Blueprint)
	}{
		{postsTable, func(b *schema.Blueprint) {
			b.ID()
			b.BigInteger("user_id")
			b.String("title")
			b.Timestamps()
			b.SoftDeletes()
		}},
		{tagsTable, func(b *schema.Blueprint) {
			b.ID()
			b.String("name")
		}},
		{pivotTable, func(b *schema.Blueprint) {
			b.ID()
			b.BigInteger("post_id")
			b.BigInteger("tag_id")
		}},
	}
	for _, s := range steps {
		if err := sb.Create(ctx, s.name, s.fn); err != nil {
			t.Fatalf("create %s: %v", s.name, err)
		}
	}
}

// relations checks the Builder on a real database: eager loading
// (HasMany, BelongsTo, BelongsToMany, nested), timestamps, soft deletes,
// conditions with OR and groups, Count and Paginate.
func relations(ctx context.Context, t *testing.T, qe orm.QueryExecutor) {
	t.Helper()
	// Second precision: what every database here stores.
	clock := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	old := orm.Now
	orm.Now = func() time.Time { return clock }
	defer func() { orm.Now = old }()

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	users := orm.NewRepository[User](qe)
	posts := orm.NewRepository[Post](qe)
	tags := orm.NewRepository[Tag](qe)
	ann, bob := &User{Name: "ann", Email: "ann@x"}, &User{Name: "bob", Email: "bob@x"}
	must(users.Create(ctx, ann))
	must(users.Create(ctx, bob))
	goTag, sqlTag := &Tag{Name: "go"}, &Tag{Name: "sql"}
	must(tags.Create(ctx, goTag))
	must(tags.Create(ctx, sqlTag))
	p1, p2, p3 := &Post{UserID: ann.ID, Title: "p1"}, &Post{UserID: ann.ID, Title: "p2"}, &Post{UserID: bob.ID, Title: "p3"}
	for _, p := range []*Post{p1, p2, p3} {
		must(posts.Create(ctx, p))
	}
	for _, tg := range []*Tag{goTag, sqlTag} {
		_, err := qe.Insert(ctx, &orm.Query{Table: pivotTable, Values: map[string]any{"post_id": p1.ID, "tag_id": tg.ID}})
		must(err)
	}

	if !p1.CreatedAt.Equal(clock) || !p1.UpdatedAt.Equal(clock) {
		t.Fatalf("timestamps on Create: %v %v", p1.CreatedAt, p1.UpdatedAt)
	}
	clock = clock.Add(time.Hour)
	p1.Title = "p1!"
	must(posts.Update(ctx, p1))
	got, err := posts.Find(ctx, p1.ID)
	must(err)
	if got.Title != "p1!" || !got.CreatedAt.Equal(clock.Add(-time.Hour)) || !got.UpdatedAt.Equal(clock) {
		t.Fatalf("timestamps after Update: %+v", got)
	}

	// Soft deletes.
	must(posts.Delete(ctx, p2.ID))
	if _, err := posts.Find(ctx, p2.ID); !errors.Is(err, orm.ErrNotFound) {
		t.Fatalf("Find of a soft-deleted post: %v", err)
	}
	if tr, err := posts.Query().WithTrashed().Find(ctx, p2.ID); err != nil || !tr.Trashed() {
		t.Fatalf("WithTrashed: %+v %v", tr, err)
	}
	if n, err := posts.Query().Count(ctx); err != nil || n != 2 {
		t.Fatalf("Count = %d, %v", n, err)
	}
	if only, err := posts.Query().OnlyTrashed().Get(ctx); err != nil || len(only) != 1 || only[0].ID != p2.ID {
		t.Fatalf("OnlyTrashed: %+v %v", only, err)
	}
	// OR stays inside parentheses before the soft-delete condition.
	if or, err := posts.Where("title", "=", "p2").OrWhere("title", "=", "p3").Get(ctx); err != nil || len(or) != 1 || or[0].Title != "p3" {
		t.Fatalf("OR with soft deletes: %+v %v", or, err)
	}
	// a AND (b OR c), and IS NULL.
	grp, err := posts.Where("user_id", "=", ann.ID).
		WhereGroup(func(g *orm.Query) { g.Where("title", "=", "p1!").OrWhere("title", "=", "p3") }).
		WhereNull("deleted_at").Get(ctx)
	if err != nil || len(grp) != 1 || grp[0].ID != p1.ID {
		t.Fatalf("WhereGroup: %+v %v", grp, err)
	}

	// Eager loading.
	authors, err := orm.NewRepository[author](qe).With("posts.tags").OrderBy("name").Get(ctx)
	must(err)
	if len(authors) != 2 || len(authors[0].Posts) != 1 || len(authors[1].Posts) != 1 {
		t.Fatalf("HasMany (soft-deleted p2 excluded): %+v", authors)
	}
	if len(authors[0].Posts[0].Tags) != 2 || len(authors[1].Posts[0].Tags) != 0 {
		t.Fatalf("nested BelongsToMany: %+v", authors)
	}
	withUser, err := posts.With("user").OrderBy("title").Get(ctx)
	must(err)
	if len(withUser) != 2 || withUser[0].User == nil || withUser[0].User.Name != "ann" || withUser[1].User.Name != "bob" {
		t.Fatalf("BelongsTo: %+v", withUser)
	}

	// Paginate.
	page, err := posts.Query().WithTrashed().OrderBy("title").Paginate(ctx, 2, 2)
	must(err)
	if page.Total != 3 || page.LastPage != 2 || len(page.Items) != 1 || page.Items[0].Title != "p3" {
		t.Fatalf("Paginate: %+v", page)
	}

	// Restore and force delete.
	must(posts.Restore(ctx, p2.ID))
	if n, _ := posts.Query().Count(ctx); n != 3 {
		t.Fatalf("Count after Restore = %d", n)
	}
	must(posts.ForceDelete(ctx, p2.ID))
	if n, _ := posts.Query().WithTrashed().Count(ctx); n != 2 {
		t.Fatalf("Count after ForceDelete = %d", n)
	}
}
