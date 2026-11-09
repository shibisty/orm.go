package orm_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"orm"
)

// sqlConn is a driver Conn: an SQLExecutor that wraps the adapter, like
// mysql.Conn (no Count method of its own).
type sqlConn struct {
	*recExec
	qe orm.QueryExecutor
}

func newSQLConn(r *recExec) *sqlConn { return &sqlConn{recExec: r, qe: orm.AsQueryExecutor(r)} }
func (c *sqlConn) Select(ctx context.Context, q *orm.Query, dest any) error {
	return c.qe.Select(ctx, q, dest)
}
func (c *sqlConn) Insert(ctx context.Context, q *orm.Query) (orm.Result, error) {
	return c.qe.Insert(ctx, q)
}
func (c *sqlConn) Update(ctx context.Context, q *orm.Query) (orm.Result, error) {
	return c.qe.Update(ctx, q)
}
func (c *sqlConn) Delete(ctx context.Context, q *orm.Query) (orm.Result, error) {
	return c.qe.Delete(ctx, q)
}

func TestBuilderClauses(t *testing.T) {
	rec := &recExec{dialect: pgDialect{}, row: []any{int64(5)}}
	ex := newSQLConn(rec)
	b := orm.Q[Comment](ex).
		Select("id", "body").
		WhereNotIn("id", []int{1, 2}).
		WhereNotNull("body").
		OrWhereGroup(func(g *orm.Query) { g.Where("post_id", "=", 3) }).
		Scope(func(q *orm.Query) { q.Where("body", "!=", "") }).
		Latest("id").
		Limit(10).
		Offset(20)
	if b.Query().Table != "comments" {
		t.Fatal("Query()")
	}
	b.Get(ctx)
	want := `SELECT "id", "body" FROM "comments" WHERE "id" NOT IN ($1, $2) AND "body" IS NOT NULL OR ("post_id" = $3) AND "body" != $4 ORDER BY "id" DESC LIMIT 10 OFFSET 20`
	if rec.sql[0] != want {
		t.Fatalf("\n got %s\nwant %s", rec.sql[0], want)
	}
	// A Conn type that is an SQLExecutor counts with SELECT COUNT(*).
	n, err := orm.Q[Comment](ex).Where("post_id", "=", 1).Count(ctx)
	if err != nil || n != 5 || !strings.HasPrefix(rec.sql[len(rec.sql)-1], "SELECT COUNT(*) FROM (") {
		t.Fatalf("Count = %d %v %s", n, err, rec.sql[len(rec.sql)-1])
	}
	if _, err := orm.Q[Comment](ex).Paginate(ctx, 1, 0); err == nil {
		t.Fatal("Paginate with perPage 0")
	}
	if err := orm.Q[Comment](ex).Chunk(ctx, 0, nil); err == nil {
		t.Fatal("Chunk with size 0")
	}
	rec.execErr = errFake
	if _, err := orm.Q[Comment](ex).Where("id", "=", 1).Update(ctx, map[string]any{"body": "x"}); err == nil {
		t.Fatal("Update error lost")
	}
	rec.qErr = errFake
	if _, err := orm.Q[Comment](ex).Get(ctx); err == nil {
		t.Fatal("Get error lost")
	}
	if _, err := orm.Q[Comment](ex).Paginate(ctx, 0, 5); err == nil {
		t.Fatal("Paginate error lost")
	}
	if err := orm.Q[Comment](ex).Chunk(ctx, 5, func([]Comment) error { return nil }); err == nil {
		t.Fatal("Chunk error lost")
	}
}

var errFake = jsonErr("fake")

type jsonErr string

func (e jsonErr) Error() string { return string(e) }

func TestChunkWithOrder(t *testing.T) {
	m := newMem()
	repo := orm.NewRepository[Comment](m)
	for i := 0; i < 5; i++ {
		repo.Create(ctx, &Comment{PostID: int64(i), Body: "b"})
	}
	var ids []int64
	err := repo.Query().OrderByDesc("id").Chunk(ctx, 2, func(batch []Comment) error {
		for _, c := range batch {
			ids = append(ids, c.ID)
		}
		return nil
	})
	if err != nil || len(ids) != 5 || ids[0] != 5 {
		t.Fatalf("Chunk by offset: %v %v", ids, err)
	}
	stop := jsonErr("stop")
	if err := repo.Query().Chunk(ctx, 2, func([]Comment) error { return stop }); err != stop {
		t.Fatalf("Chunk callback error: %v", err)
	}
}

func TestKeysAndTimes(t *testing.T) {
	type U struct {
		ID   uint32     `db:"id"`
		At   time.Time  `db:"at"`
		Seen *time.Time `db:"seen"`
		Nick *string    `db:"nick"`
	}
	var u U
	now := time.Date(2026, 11, 9, 1, 2, 3, 0, time.UTC)
	err := orm.FillStruct(&u, map[string]any{
		"id": json.Number("7"), "at": now.Format(time.RFC3339Nano), "seen": bsonLike(now), "nick": "n",
	})
	if err != nil || u.ID != 7 || !u.At.Equal(now) || u.Seen == nil || !u.Seen.Equal(now) || *u.Nick != "n" {
		t.Fatalf("FillStruct: %+v %v", u, err)
	}
	if err := orm.FillStruct(&u, map[string]any{"at": 12}); err == nil {
		t.Fatal("a number as a time")
	}
	if err := orm.FillStruct(&u, map[string]any{"at": "yesterday"}); err == nil {
		t.Fatal("a bad time string")
	}
	if names := orm.RelationNames[Author](); strings.Join(names, ",") != "posts,profile,tags,team" {
		t.Fatalf("RelationNames = %v", names)
	}
	if orm.RelationNames[Comment]() != nil {
		t.Fatal("RelationNames of a model without relations")
	}
	q := orm.NewQuery("t").Where("a", "=", 1)
	q.Values = map[string]any{"a": 1}
	c := q.Clone()
	c.Values["a"] = 2
	c.Where("b", "=", 2)
	if q.Values["a"] != 1 || len(q.Wheres) != 1 {
		t.Fatal("Clone shares state")
	}
}

// bsonLike has a Time method, like bson.DateTime.
type bsonLike time.Time

func (b bsonLike) Time() time.Time { return time.Time(b) }

// Keys of different types match related records: an unsigned key, a
// string key, a nil pointer foreign key.
type Owner struct {
	Code  uint16 `db:"code"`
	Name  string `db:"name"`
	Items []Item `rel:"items"`
}

func (Owner) TableName() string  { return "owners" }
func (Owner) PrimaryKey() string { return "code" }
func (Owner) Relations() orm.Relations {
	return orm.Relations{"items": orm.HasMany[Item]("owner_code")}
}

type Item struct {
	orm.BaseModel
	OwnerCode *uint16 `db:"owner_code"`
	Ref       string  `db:"ref"`
	Owner     *Owner  `rel:"owner"`
	Tagged    *Tag    `rel:"tagged"`
}

func (Item) TableName() string { return "items" }
func (Item) Relations() orm.Relations {
	return orm.Relations{"owner": orm.BelongsTo[Owner]("owner_code"), "tagged": orm.BelongsTo[Tag]("ref")}
}

func TestMixedKeyTypes(t *testing.T) {
	m := newMem()
	owners, items := orm.NewRepository[Owner](m), orm.NewRepository[Item](m)
	owners.Create(ctx, &Owner{Code: 7, Name: "seven"})
	code := uint16(7)
	items.Create(ctx, &Item{OwnerCode: &code})
	items.Create(ctx, &Item{})                      // no owner
	m.tables["items"][0]["owner_code"] = float64(7) // as JSON gives it back
	got, err := items.With("owner").OrderBy("id").Get(ctx)
	if err != nil || got[0].Owner == nil || got[0].Owner.Name != "seven" || got[1].Owner != nil {
		t.Fatalf("BelongsTo with a float key: %+v %v", got, err)
	}
	o, err := owners.With("items").Find(ctx, 7)
	if err != nil || len(o.Items) != 1 {
		t.Fatalf("HasMany with a uint16 key: %+v %v", o, err)
	}
	if _, err := items.With("tagged").Get(ctx); err != nil {
		t.Fatalf("BelongsTo with a string key: %v", err)
	}
	if err := owners.Restore(ctx, 7); err == nil {
		t.Fatal("Restore without SoftDeletes")
	}
}

// A relation field of the wrong type is an error, not a panic.
type BadRel struct {
	orm.BaseModel
	Posts []Comment `rel:"posts"`
}

func (BadRel) TableName() string { return "badrel" }
func (BadRel) Relations() orm.Relations {
	return orm.Relations{"posts": orm.HasMany[Post]("author_id"), "missing": orm.HasOne[Post]("author_id")}
}

func TestRelationErrors(t *testing.T) {
	m := newMem()
	repo := orm.NewRepository[BadRel](m)
	repo.Create(ctx, &BadRel{})
	if _, err := repo.With("posts").Get(ctx); err == nil || !strings.Contains(err.Error(), "the field is") {
		t.Fatalf("wrong field type: %v", err)
	}
	if _, err := repo.With("missing").Get(ctx); err == nil || !strings.Contains(err.Error(), "no field tagged") {
		t.Fatalf("relation without a field: %v", err)
	}
}

// Hooks that fail after the write, on update and delete.
type Audited struct {
	orm.BaseModel
	orm.SoftDeletes
	Name string `db:"name"`
	log  []string
}

func (Audited) TableName() string { return "audited" }
func (a *Audited) BeforeUpdate(context.Context) error {
	if a.Name == "" {
		return jsonErr("empty")
	}
	return nil
}
func (a *Audited) AfterUpdate(context.Context) error { a.log = append(a.log, "updated"); return nil }
func (a *Audited) AfterDelete(context.Context) error { return nil }

func TestUpdateAndDeleteHooks(t *testing.T) {
	m := newMem()
	repo := orm.NewRepository[Audited](m)
	a := &Audited{Name: "x"}
	repo.Create(ctx, a)
	a.Name = ""
	if err := repo.Update(ctx, a); err == nil {
		t.Fatal("BeforeUpdate error ignored")
	}
	a.Name = "y"
	if err := repo.Update(ctx, a); err != nil || len(a.log) != 1 {
		t.Fatalf("AfterUpdate: %v %v", a.log, err)
	}
	if err := repo.Delete(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.Query().WithTrashed().Find(ctx, a.ID); !got.Trashed() {
		t.Fatal("soft delete with an AfterDelete hook")
	}
	if err := repo.ForceDelete(ctx, a.ID); err != nil || len(m.tables["audited"]) != 0 {
		t.Fatalf("ForceDelete with hooks: %v", err)
	}
	if err := orm.NewRepository[Session](m).Update(ctx, &Session{}); err != nil {
		t.Fatal(err)
	}
}
