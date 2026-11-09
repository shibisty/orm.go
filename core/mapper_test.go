package orm_test

import (
	"strings"
	"testing"
	"time"

	"orm"
)

// The orm-gen mappers of genUser behave exactly like reflection on the
// identical benchUser.
func TestMapperMatchesReflection(t *testing.T) {
	rows := benchRows100()[:3]
	ref, err := orm.NewRepository[benchUser](orm.AsQueryExecutor(&benchExec{rows: rows})).All(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	gen, err := orm.NewRepository[genUser](orm.AsQueryExecutor(&benchExec{rows: rows})).All(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(gen) != 3 || ref[2].Name != "user2" || *ref[2].Bio != "bio" {
		t.Fatalf("%+v", ref)
	}
	for i := range ref {
		if g := benchUser(gen[i]); !sameUser(g, ref[i]) {
			t.Fatalf("row %d: %+v != %+v", i, g, ref[i])
		}
	}

	one, err := orm.NewRepository[genUser](orm.AsQueryExecutor(&benchExec{rows: rows[:1]})).Find(ctx, 1)
	if err != nil || one.ID != 1 || one.CreatedAt != benchTime {
		t.Fatalf("%+v %v", one, err)
	}

	maps := benchMaps100()[:3]
	var rf []benchUser
	var gf []genUser
	if err := orm.FillSlice(&rf, maps); err != nil {
		t.Fatal(err)
	}
	if err := orm.FillSlice(&gf, maps); err != nil {
		t.Fatal(err)
	}
	for i := range rf {
		if g := benchUser(gf[i]); !sameUser(g, rf[i]) || rf[i].Age == 0 || rf[i].CreatedAt.IsZero() {
			t.Fatalf("fill %d: %+v != %+v", i, g, rf[i])
		}
	}

	// Insert values: the same columns and values, without the zero key.
	rr := &recExec{dialect: pgDialect{}, lastID: 9}
	rg := &recExec{dialect: pgDialect{}, lastID: 9}
	at := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	old := orm.Now
	orm.Now = func() time.Time { return at }
	defer func() { orm.Now = old }()
	u := newBenchUser()
	g := newGenUser()
	if err := orm.NewRepository[benchUser](orm.AsQueryExecutor(rr)).Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := orm.NewRepository[genUser](orm.AsQueryExecutor(rg)).Create(ctx, g); err != nil {
		t.Fatal(err)
	}
	if rr.sql[0] != rg.sql[0] || len(rr.args[0]) != len(rg.args[0]) || g.ID != 9 || g.CreatedAt != at {
		t.Fatalf("insert:\n%s %v\n%s %v", rr.sql[0], rr.args[0], rg.sql[0], rg.args[0])
	}
	g.Name = "Bob"
	if err := orm.NewRepository[genUser](orm.AsQueryExecutor(rg)).Update(ctx, g); err != nil || !strings.Contains(rg.sql[1], "UPDATE") {
		t.Fatal(err, rg.sql)
	}
}

func sameUser(a, b benchUser) bool {
	ab, bb := a.Bio, b.Bio
	a.Bio, b.Bio = nil, nil
	if a != b {
		return false
	}
	return (ab == nil) == (bb == nil) && (ab == nil || *ab == *bb)
}

// staleUser has mappers whose columns no longer match the struct (a field
// was added without go generate).
type staleUser struct {
	orm.BaseModel
	Name  string `db:"name"`
	Email string `db:"email"`
}

func (staleUser) TableName() string                   { return "users" }
func (m *staleUser) OrmColumns() []string             { return []string{"id", "name"} }
func (m *staleUser) OrmScanTargets() []any            { return []any{&m.ID, &m.Name} }
func (m *staleUser) OrmValues() map[string]any        { return map[string]any{"id": m.ID, "name": m.Name} }
func (m *staleUser) OrmFill(row map[string]any) error { return nil }

func TestMapperOutOfDate(t *testing.T) {
	exec := orm.AsQueryExecutor(&benchExec{rows: [][]any{{int64(1), "a", "b"}}})
	repo := orm.NewRepository[staleUser](exec)
	check := func(what string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "out of date") || !strings.Contains(err.Error(), "go generate") {
			t.Errorf("%s: %v", what, err)
		}
	}
	_, err := repo.All(ctx, nil)
	check("All", err)
	_, err = repo.Find(ctx, 1)
	check("Find", err)
	check("Create", repo.Create(ctx, &staleUser{Name: "x"}))
	check("Update", repo.Update(ctx, &staleUser{BaseModel: orm.BaseModel{ID: 1}}))
	var s staleUser
	check("FillStruct", orm.FillStruct(&s, map[string]any{"id": 1}))
	var list []staleUser
	check("FillSlice", orm.FillSlice(&list, []map[string]any{{"id": 1}}))
}

func TestAssign(t *testing.T) {
	var n int64 = 5
	if err := orm.Assign(&n, nil); err != nil || n != 5 {
		t.Fatal("nil changed the value", n, err)
	}
	if err := orm.Assign(&n, int64(7)); err != nil || n != 7 {
		t.Fatal(n, err)
	}
	if err := orm.Assign(&n, float64(9)); err != nil || n != 9 { // JSON number
		t.Fatal(n, err)
	}
	var ts time.Time
	if err := orm.Assign(&ts, "2026-01-02T03:04:05Z"); err != nil || ts.Year() != 2026 {
		t.Fatal(ts, err)
	}
	var p *string
	if err := orm.Assign(&p, "x"); err != nil || p == nil || *p != "x" {
		t.Fatal(p, err)
	}
	var b bool
	if err := orm.Assign(&b, "yes"); err == nil {
		t.Fatal("string assigned to bool")
	}
}

type hiddenBase struct {
	Secret string `db:"secret"`
}

// withHidden embeds an unexported struct: its fields cannot be set, so the
// orm skips them instead of panicking.
type withHidden struct {
	hiddenBase
	ID   int64  `db:"id"`
	Name string `db:"name"`
}

func (withHidden) TableName() string { return "hidden" }

func TestUnexportedEmbeddingSkipped(t *testing.T) {
	rec := &recExec{dialect: pgDialect{}, rows: [][]any{{int64(1), "a"}}}
	got, err := orm.NewRepository[withHidden](orm.AsQueryExecutor(rec)).All(ctx, nil)
	if err != nil || len(got) != 1 || got[0].Name != "a" || got[0].Secret != "" {
		t.Fatalf("%+v %v", got, err)
	}
	if !strings.Contains(rec.sql[0], `"id", "name"`) || strings.Contains(rec.sql[0], "secret") {
		t.Fatal(rec.sql[0])
	}
	var h withHidden
	if err := orm.FillStruct(&h, map[string]any{"secret": "s", "name": "n"}); err != nil || h.Name != "n" || h.Secret != "" {
		t.Fatalf("%+v %v", h, err)
	}
}

func TestFillSliceNeedsStructs(t *testing.T) {
	var ptrs []*benchUser
	if err := orm.FillSlice(&ptrs, []map[string]any{{"id": 1}}); err == nil {
		t.Fatal("slice of pointers accepted")
	}
	var empty []benchUser
	if err := orm.FillSlice(&empty, nil); err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("%#v %v", empty, err)
	}
	if err := orm.FillSlice(&empty, []map[string]any{{"age": "x"}}); err == nil || !strings.Contains(err.Error(), `field "Age"`) {
		t.Fatal(err)
	}
}

func TestSelectNoRowsIsEmptySlice(t *testing.T) {
	got, err := orm.NewRepository[genUser](orm.AsQueryExecutor(&benchExec{})).All(ctx, nil)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("%#v %v", got, err)
	}
}
