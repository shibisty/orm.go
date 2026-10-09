package schema

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"orm"
)

// testDialect is a simple, predictable DDL: a column's "type" is its ColumnType.
type testDialect struct{ failColumn string }

func (testDialect) Name() string             { return "test" }
func (testDialect) Quote(s string) string    { return `"` + s + `"` }
func (testDialect) Placeholder(n int) string { return fmt.Sprintf("$%d", n) }
func (d testDialect) ColumnSQL(c *Column) (string, error) {
	if c.Name == d.failColumn {
		return "", errors.New("unsupported")
	}
	s := fmt.Sprintf(`"%s" %s`, c.Name, c.Type)
	if c.IsNullable {
		s += " NULL"
	}
	return s, nil
}
func (d testDialect) AlterColumnSQL(table string, c *Column) (string, error) {
	if c.Name == d.failColumn {
		return "", errors.New("unsupported")
	}
	return fmt.Sprintf(`ALTER "%s" CHANGE "%s" %s`, table, c.Name, c.Type), nil
}
func (testDialect) TableSuffix() string { return " SUFFIX" }
func (testDialect) CreateIndexSQL(table string, idx *Index) string {
	return fmt.Sprintf(`CREATE INDEX "%s" ON "%s" (%s)`, idx.Name, table, strings.Join(idx.Columns, ","))
}
func (testDialect) DropIndexSQL(table, name string) string {
	return fmt.Sprintf(`DROP INDEX "%s"`, name)
}
func (d testDialect) DropForeignKeySQL(table, name string) (string, error) {
	if name == d.failColumn {
		return "", errors.New("unsupported")
	}
	return fmt.Sprintf(`ALTER "%s" DROP FK "%s"`, table, name), nil
}
func (testDialect) ForeignKeyClause(fk *ForeignKey) string {
	s := fmt.Sprintf(`FK "%s" (%s) -> %s(%s)`, fk.Name, fk.Column, fk.RefTable, fk.RefColumn)
	if fk.OnDeleteAction != "" {
		s += " DEL " + fk.OnDeleteAction
	}
	if fk.OnUpdateAction != "" {
		s += " UPD " + fk.OnUpdateAction
	}
	return s
}

// recExec records the executed SQL.
type recExec struct {
	stmts   []string
	failOn  string
	dialect orm.Dialect
}

func (r *recExec) Driver() string                          { return "rec" }
func (r *recExec) Ping(context.Context) error              { return nil }
func (r *recExec) Close() error                            { return nil }
func (r *recExec) Dialect() orm.Dialect                    { return r.dialect }
func (r *recExec) BeginTx(context.Context) (orm.Tx, error) { return nil, nil }
func (r *recExec) QueryContext(context.Context, string, ...any) (orm.Rows, error) {
	return nil, nil
}
func (r *recExec) QueryRowContext(context.Context, string, ...any) orm.Row { return nil }
func (r *recExec) ExecContext(_ context.Context, q string, _ ...any) (orm.Result, error) {
	if r.failOn != "" && strings.Contains(q, r.failOn) {
		return orm.Result{}, errors.New("exec failed")
	}
	r.stmts = append(r.stmts, q)
	return orm.Result{}, nil
}

// ormDialect is a testDialect that also implements orm.Dialect (like the real drivers).
type ormDialect struct{ testDialect }

func (ormDialect) BuildSelect(*orm.Query) (string, []any) { return "", nil }
func (ormDialect) BuildInsert(*orm.Query) (string, []any) { return "", nil }
func (ormDialect) BuildUpdate(*orm.Query) (string, []any) { return "", nil }
func (ormDialect) BuildDelete(*orm.Query) (string, []any) { return "", nil }

type plainDialect struct{}

func (plainDialect) Name() string                           { return "plain" }
func (plainDialect) Quote(s string) string                  { return s }
func (plainDialect) Placeholder(int) string                 { return "?" }
func (plainDialect) BuildSelect(*orm.Query) (string, []any) { return "", nil }
func (plainDialect) BuildInsert(*orm.Query) (string, []any) { return "", nil }
func (plainDialect) BuildUpdate(*orm.Query) (string, []any) { return "", nil }
func (plainDialect) BuildDelete(*orm.Query) (string, []any) { return "", nil }

var ctx = context.Background()

func TestCreateCompilesConstraintsAndIndexes(t *testing.T) {
	b := New(nil, testDialect{})
	stmts, err := b.SQLForCreate("posts", func(t *Blueprint) {
		t.ID()
		t.String("slug").Unique()
		t.ForeignID("user_id").Constrained().OnDelete("cascade").OnUpdate("restrict")
		t.ForeignIDFor("categories")
		t.Morphs("taggable")
		t.UniqueCols("user_id", "slug")
		t.IndexCols("created_at").As("posts_created")
		t.Timestamps()
	})
	if err != nil {
		t.Fatal(err)
	}
	create := stmts[0]
	for _, want := range []string{
		`CREATE TABLE "posts" (`,
		`"id" bigInteger`,
		`PRIMARY KEY ("id")`,
		`CONSTRAINT "posts_slug_unique" UNIQUE ("slug")`,
		`CONSTRAINT "posts_user_id_slug_unique" UNIQUE ("user_id", "slug")`,
		`FK "posts_user_id_foreign" (user_id) -> users(id) DEL cascade UPD restrict`,
		`FK "posts_category_id_foreign" (category_id) -> categories(id)`,
		`"created_at" timestamp NULL`,
		`) SUFFIX`,
	} {
		if !strings.Contains(create, want) {
			t.Errorf("CREATE has no %q:\n%s", want, create)
		}
	}
	wantIdx := []string{
		`CREATE INDEX "posts_taggable_type_taggable_id_index" ON "posts" (taggable_type,taggable_id)`,
		`CREATE INDEX "posts_created" ON "posts" (created_at)`,
	}
	if !reflect.DeepEqual(stmts[1:], wantIdx) {
		t.Fatalf("index statements = %q", stmts[1:])
	}
}

func TestCreateErrorsAndIfNotExists(t *testing.T) {
	b := New(nil, testDialect{failColumn: "bad"})
	if _, err := b.SQLForCreate("t", func(t *Blueprint) { t.String("bad") }); err == nil {
		t.Fatal("column error should be returned")
	}

	ex := &recExec{}
	b = New(ex, testDialect{})
	if err := b.CreateIfNotExists(ctx, "t", func(t *Blueprint) { t.ID() }); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ex.stmts[0], `CREATE TABLE IF NOT EXISTS "t"`) {
		t.Fatalf("got %s", ex.stmts[0])
	}
	b = New(ex, testDialect{failColumn: "bad"})
	if err := b.CreateIfNotExists(ctx, "t", func(t *Blueprint) { t.String("bad") }); err == nil {
		t.Fatal("CreateIfNotExists should return compile errors")
	}
}

func TestAlter(t *testing.T) {
	b := New(nil, testDialect{})
	stmts, err := b.SQLForAlter("users", func(t *Blueprint) {
		t.String("nick").Nullable().Unique()
		t.Integer("age").Change()
		t.DropColumn("legacy")
		t.RenameColumn("b_old", "b_new")
		t.RenameColumn("a_old", "a_new")
		t.UniqueCols("email")
		t.IndexCols("nick")
		t.PrimaryCols("id")
		t.DropIndex("old_index")
		t.DropIndexCols("x", "y")
		t.ForeignID("team_id").Constrained("teams")
		t.DropForeignCol("company_id")
		t.DropSoftDeletes()
		t.DropTimestamps()
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`ALTER TABLE "users" ADD COLUMN "nick" string NULL`,
		`ALTER TABLE "users" ADD CONSTRAINT "users_nick_unique" UNIQUE ("nick")`,
		`ALTER "users" CHANGE "age" integer`,
		`ALTER TABLE "users" ADD COLUMN "team_id" bigInteger`,
		`ALTER TABLE "users" DROP COLUMN "legacy"`,
		`ALTER TABLE "users" DROP COLUMN "deleted_at"`,
		`ALTER TABLE "users" DROP COLUMN "created_at"`,
		`ALTER TABLE "users" DROP COLUMN "updated_at"`,
		`ALTER TABLE "users" RENAME COLUMN "a_old" TO "a_new"`,
		`ALTER TABLE "users" RENAME COLUMN "b_old" TO "b_new"`,
		`ALTER TABLE "users" ADD CONSTRAINT "users_email_unique" UNIQUE ("email")`,
		`CREATE INDEX "users_nick_index" ON "users" (nick)`,
		`ALTER TABLE "users" ADD PRIMARY KEY ("id")`,
		`DROP INDEX "old_index"`,
		`DROP INDEX "users_x_y_index"`,
		`ALTER TABLE "users" ADD FK "users_team_id_foreign" (team_id) -> teams(id)`,
		`ALTER "users" DROP FK "users_company_id_foreign"`,
	}
	if !reflect.DeepEqual(stmts, want) {
		t.Fatalf("ALTER statements:\n%s\nwant:\n%s", strings.Join(stmts, "\n"), strings.Join(want, "\n"))
	}

	bad := New(nil, testDialect{failColumn: "bad"})
	if _, err := bad.SQLForAlter("t", func(t *Blueprint) { t.String("bad") }); err == nil {
		t.Error("add column error should be returned")
	}
	if _, err := bad.SQLForAlter("t", func(t *Blueprint) { t.String("bad").Change() }); err == nil {
		t.Error("change column error should be returned")
	}
	if _, err := bad.SQLForAlter("t", func(t *Blueprint) { t.DropForeign("bad") }); err == nil {
		t.Error("drop foreign key error should be returned")
	}
}

func TestBuilderExecutes(t *testing.T) {
	ex := &recExec{}
	b := New(ex, testDialect{})
	steps := []func() error{
		func() error { return b.Create(ctx, "a", func(t *Blueprint) { t.ID(); t.IndexCols("id") }) },
		func() error { return b.Table(ctx, "a", func(t *Blueprint) { t.DropColumn("x") }) },
		func() error { return b.Rename(ctx, "a", "b") },
		func() error { return b.Drop(ctx, "b") },
		func() error { return b.DropIfExists(ctx, "b") },
	}
	for i, s := range steps {
		if err := s(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	want := []string{`CREATE INDEX`, `ALTER TABLE "a" DROP COLUMN "x"`, `ALTER TABLE "a" RENAME TO "b"`, `DROP TABLE "b"`, `DROP TABLE IF EXISTS "b"`}
	got := ex.stmts[1:]
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Fatalf("stmt %d = %s, want prefix %s", i, got[i], want[i])
		}
	}

	failing := New(&recExec{failOn: "DROP"}, testDialect{})
	if err := failing.Drop(ctx, "x"); err == nil || !strings.Contains(err.Error(), "DROP TABLE") {
		t.Fatalf("exec error should name the statement, got %v", err)
	}
	bad := New(&recExec{}, testDialect{failColumn: "bad"})
	if err := bad.Create(ctx, "t", func(t *Blueprint) { t.String("bad") }); err == nil {
		t.Error("Create should return compile errors")
	}
	if err := bad.Table(ctx, "t", func(t *Blueprint) { t.String("bad") }); err == nil {
		t.Error("Table should return compile errors")
	}
}

func TestNewFor(t *testing.T) {
	if _, err := NewFor(&recExec{dialect: ormDialect{}}); err != nil {
		t.Fatalf("dialect with DDL: %v", err)
	}
	if _, err := NewFor(&recExec{dialect: plainDialect{}}); err == nil {
		t.Fatal("dialect without DDL should be rejected")
	}
}

func TestColumnTypesAndModifiers(t *testing.T) {
	bp := newBlueprint("t", actionCreate)
	type check struct {
		col  *Column
		typ  ColumnType
		test func(*Column) bool
	}
	checks := []check{
		{bp.Boolean("b"), TypeBoolean, nil},
		{bp.Char("c"), TypeChar, func(c *Column) bool { return c.Length == 255 }},
		{bp.Char("c2", 10), TypeChar, func(c *Column) bool { return c.Length == 10 }},
		{bp.String("s", 50), TypeString, func(c *Column) bool { return c.Length == 50 }},
		{bp.Text("t"), TypeText, nil},
		{bp.MediumText("mt"), TypeMediumText, nil},
		{bp.LongText("lt"), TypeLongText, nil},
		{bp.TinyText("tt"), TypeTinyText, nil},
		{bp.TinyInteger("ti"), TypeTinyInteger, nil},
		{bp.SmallInteger("si"), TypeSmallInteger, nil},
		{bp.MediumInteger("mi"), TypeMediumInteger, nil},
		{bp.Integer("i"), TypeInteger, nil},
		{bp.BigInteger("bi"), TypeBigInteger, nil},
		{bp.UnsignedTinyInteger("uti"), TypeTinyInteger, func(c *Column) bool { return c.IsUnsigned }},
		{bp.UnsignedSmallInteger("usi"), TypeSmallInteger, func(c *Column) bool { return c.IsUnsigned }},
		{bp.UnsignedMediumInteger("umi"), TypeMediumInteger, func(c *Column) bool { return c.IsUnsigned }},
		{bp.UnsignedInteger("ui"), TypeInteger, func(c *Column) bool { return c.IsUnsigned }},
		{bp.UnsignedBigInteger("ubi"), TypeBigInteger, func(c *Column) bool { return c.IsUnsigned }},
		{bp.TinyIncrements("tinc"), TypeTinyInteger, autoPK},
		{bp.SmallIncrements("sinc"), TypeSmallInteger, autoPK},
		{bp.MediumIncrements("minc"), TypeMediumInteger, autoPK},
		{bp.Increments("inc"), TypeInteger, autoPK},
		{bp.BigIncrements("binc"), TypeBigInteger, autoPK},
		{bp.Decimal("d"), TypeDecimal, func(c *Column) bool { return c.Precision == 8 && c.Scale == 2 }},
		{bp.Decimal("d2", 10, 4), TypeDecimal, func(c *Column) bool { return c.Precision == 10 && c.Scale == 4 }},
		{bp.Double("db"), TypeDouble, nil},
		{bp.Float("f", 5, 1), TypeFloat, func(c *Column) bool { return c.Precision == 5 && c.Scale == 1 }},
		{bp.Date("da"), TypeDate, nil},
		{bp.DateTime("dt", 3), TypeDateTime, func(c *Column) bool { return c.Precision == 3 }},
		{bp.DateTimeTz("dtz"), TypeDateTime, func(c *Column) bool { return c.WithTZ }},
		{bp.Time("tm"), TypeTime, nil},
		{bp.TimeTz("tmz"), TypeTime, func(c *Column) bool { return c.WithTZ }},
		{bp.TimestampTz("tsz"), TypeTimestamp, func(c *Column) bool { return c.WithTZ }},
		{bp.SoftDeletesTz(), TypeTimestamp, func(c *Column) bool { return c.WithTZ && c.IsNullable }},
		{bp.Year("y"), TypeYear, nil},
		{bp.Binary("bin"), TypeBinary, nil},
		{bp.JSON("j"), TypeJSON, nil},
		{bp.JSONB("jb"), TypeJSONB, nil},
		{bp.UUID(), TypeUUID, func(c *Column) bool { return c.Name == "uuid" }},
		{bp.ULID("u"), TypeULID, func(c *Column) bool { return c.Length == 26 }},
		{bp.Geometry("g"), TypeGeometry, nil},
		{bp.Geography("gg"), TypeGeography, nil},
		{bp.Enum("e", []string{"a"}), TypeEnum, func(c *Column) bool { return len(c.Values) == 1 }},
		{bp.Set("st", []string{"a", "b"}), TypeSet, func(c *Column) bool { return len(c.Values) == 2 }},
		{bp.MacAddress("mac"), TypeMacAddress, nil},
		{bp.IPAddress("ip"), TypeIPAddress, nil},
		{bp.Vector("v", 3), TypeVector, func(c *Column) bool { return c.Dimensions == 3 }},
		{bp.RememberToken(), TypeString, func(c *Column) bool { return c.Length == 100 && c.IsNullable }},
	}
	for _, c := range checks {
		if c.col.Type != c.typ {
			t.Errorf("%s: type %s, want %s", c.col.Name, c.col.Type, c.typ)
		}
		if c.test != nil && !c.test(c.col) {
			t.Errorf("%s: unexpected %+v", c.col.Name, *c.col)
		}
	}

	c := bp.String("m").Nullable().Default("x").Unsigned().Primary().AutoIncrement().Comment("hi").After("id")
	if !c.IsNullable || c.DefaultValue != "x" || c.DefaultIsRaw || !c.IsUnsigned || !c.IsPrimary || !c.IsAutoIncr || c.CommentText != "hi" || c.AfterColumn != "id" {
		t.Fatalf("modifiers: %+v", *c)
	}
	if c.Nullable(false).IsNullable {
		t.Fatal("Nullable(false) should unset")
	}
	if r := bp.String("r").DefaultRaw("NOW()"); !r.DefaultIsRaw || r.DefaultValue != "NOW()" {
		t.Fatal("DefaultRaw")
	}

	bp2 := newBlueprint("c", actionCreate)
	bp2.TimestampsTz()
	bp2.NullableMorphs("a")
	bp2.UUIDMorphs("b")
	bp2.NullableUUIDMorphs("c")
	bp2.ULIDMorphs("d", "custom_idx")
	bp2.NullableULIDMorphs("e")
	bp2.ForeignUUID("fu").References("uuid").On("others")
	bp2.ForeignULID("fl")
	bp2.ForeignUUIDFor("teams")
	bp2.ForeignID("owner_id").Nullable().Constrained("people")
	if n := len(bp2.Indexes); n != 5 || bp2.Indexes[3].Name != "custom_idx" {
		t.Fatalf("morph indexes: %d, %+v", n, bp2.Indexes)
	}
	if len(bp2.ForeignKeys) != 3 || bp2.ForeignKeys[0].RefColumn != "uuid" || bp2.ForeignKeys[1].RefTable != "teams" {
		t.Fatalf("foreign keys: %+v %+v", bp2.ForeignKeys[0], bp2.ForeignKeys[1])
	}
	// OnDelete/OnUpdate without On() don't break anything.
	bp2.ForeignID("x_id").OnDelete("cascade").OnUpdate("cascade")
}

func autoPK(c *Column) bool { return c.IsAutoIncr && c.IsPrimary && c.IsUnsigned }

func TestIndexName(t *testing.T) {
	for _, tc := range []struct {
		table, kind string
		cols        []string
		want        string
	}{
		{"users", "unique", []string{"email"}, "users_email_unique"},
		{"Users", "index", []string{"a", "b"}, "users_a_b_index"},
		{"my-schema.users", "foreign", []string{"team_id"}, "my_schema_users_team_id_foreign"},
	} {
		if got := IndexName(tc.table, tc.kind, tc.cols...); got != tc.want {
			t.Errorf("IndexName(%q) = %q, want %q", tc.table, got, tc.want)
		}
	}
}

func TestPluralizeSingularize(t *testing.T) {
	for s, p := range map[string]string{"company": "companies", "day": "days", "box": "boxes", "bus": "buses", "church": "churches", "dish": "dishes", "tag": "tags"} {
		if got := pluralize(s); got != p {
			t.Errorf("pluralize(%q) = %q, want %q", s, got, p)
		}
	}
	for p, s := range map[string]string{"companies": "company", "boxes": "box", "buses": "bus", "churches": "church", "dishes": "dish", "tags": "tag", "fish": "fish"} {
		if got := singularize(p); got != s {
			t.Errorf("singularize(%q) = %q, want %q", p, got, s)
		}
	}
}

// altDialect implements AlterConstraintDialect (like SQLite).
type altDialect struct{ testDialect }

func (altDialect) AddUniqueSQL(table string, idx *Index) (string, error) {
	if idx.Name == "t_bad_unique" {
		return "", errors.New("unsupported")
	}
	return "UNIQUE INDEX " + idx.Name, nil
}
func (altDialect) AddForeignKeySQL(string, *ForeignKey) (string, error) {
	return "", errors.New("no fk in alter")
}

func TestAlterConstraintDialect(t *testing.T) {
	b := New(nil, altDialect{})
	stmts, err := b.SQLForAlter("t", func(t *Blueprint) {
		t.String("nick").Unique()
		t.UniqueCols("email")
	})
	if err != nil {
		t.Fatal(err)
	}
	if stmts[1] != "UNIQUE INDEX t_nick_unique" || stmts[2] != "UNIQUE INDEX t_email_unique" {
		t.Fatalf("got %q", stmts)
	}
	if _, err := b.SQLForAlter("t", func(t *Blueprint) { t.ForeignID("x_id").Constrained() }); err == nil {
		t.Error("AddForeignKeySQL error should be returned")
	}
	if _, err := b.SQLForAlter("t", func(t *Blueprint) { t.String("bad").Unique() }); err == nil {
		t.Error("AddUniqueSQL error should be returned for a column")
	}
	if _, err := b.SQLForAlter("t", func(t *Blueprint) { t.UniqueCols("bad") }); err == nil {
		t.Error("AddUniqueSQL error should be returned for an index")
	}
}
