// Package schema is a database schema builder in the spirit of Laravel Schema Builder:
// a Blueprint with the full set of column types (boolean, string, the integer family,
// date/time, json, uuid/ulid, morphs, foreign key, etc.) and modifiers
// (nullable, default, unique, unsigned...), which are translated into real DDL
// through the dialect of a specific database (mysql/postgres).
//
// It applies to relational databases only. Mongo/Redis have no schema in this
// sense; for them a "migration" is arbitrary Go code (creating indexes,
// transforming data), not a Blueprint. See the README.
package schema

import "context"

// ColumnType is one of the column types listed in the reference (boolean,
// string, the integer family, date/time, json, uuid/ulid, spatial, enum/set...).
type ColumnType string

const (
	TypeBoolean ColumnType = "boolean"

	TypeChar       ColumnType = "char"
	TypeString     ColumnType = "string"
	TypeText       ColumnType = "text"
	TypeMediumText ColumnType = "mediumText"
	TypeLongText   ColumnType = "longText"
	TypeTinyText   ColumnType = "tinyText"

	TypeTinyInteger   ColumnType = "tinyInteger"
	TypeSmallInteger  ColumnType = "smallInteger"
	TypeMediumInteger ColumnType = "mediumInteger"
	TypeInteger       ColumnType = "integer"
	TypeBigInteger    ColumnType = "bigInteger"
	TypeDecimal       ColumnType = "decimal"
	TypeDouble        ColumnType = "double"
	TypeFloat         ColumnType = "float"

	TypeDate      ColumnType = "date"
	TypeDateTime  ColumnType = "dateTime"
	TypeTime      ColumnType = "time"
	TypeTimestamp ColumnType = "timestamp"
	TypeYear      ColumnType = "year"

	TypeBinary ColumnType = "binary"
	TypeJSON   ColumnType = "json"
	TypeJSONB  ColumnType = "jsonb"

	TypeUUID ColumnType = "uuid"
	TypeULID ColumnType = "ulid"

	TypeGeometry  ColumnType = "geometry"
	TypeGeography ColumnType = "geography"

	TypeEnum       ColumnType = "enum"
	TypeSet        ColumnType = "set"
	TypeMacAddress ColumnType = "macAddress"
	TypeIPAddress  ColumnType = "ipAddress"
	TypeVector     ColumnType = "vector"
)

// Column is the definition of a single column (the counterpart of Laravel's ColumnDefinition).
type Column struct {
	Name string
	Type ColumnType

	Length     int      // char/string/ulid
	Precision  int      // decimal/double/float/dateTime/time/timestamp
	Scale      int      // decimal/double/float
	Values     []string // enum/set
	Dimensions int      // vector

	WithTZ bool // dateTimeTz/timeTz/timestampTz

	IsNullable bool
	IsUnsigned bool
	IsUnique   bool
	IsPrimary  bool
	IsAutoIncr bool
	IsChange   bool // ALTER: modify an existing column rather than add a new one

	HasDefault   bool
	DefaultValue any
	DefaultIsRaw bool // DefaultValue is a raw SQL expression (e.g. "CURRENT_TIMESTAMP"), not a literal

	CommentText string
	AfterColumn string // MySQL-specific column order hint (ignored on Postgres)
}

func (c *Column) Nullable(value ...bool) *Column {
	c.IsNullable = true
	if len(value) > 0 {
		c.IsNullable = value[0]
	}
	return c
}

func (c *Column) Default(value any) *Column {
	c.HasDefault, c.DefaultValue, c.DefaultIsRaw = true, value, false
	return c
}

// DefaultRaw sets the default value as a raw SQL expression
// (e.g. "CURRENT_TIMESTAMP"); it is not quoted as a string.
func (c *Column) DefaultRaw(expr string) *Column {
	c.HasDefault, c.DefaultValue, c.DefaultIsRaw = true, expr, true
	return c
}

func (c *Column) Unsigned() *Column           { c.IsUnsigned = true; return c }
func (c *Column) Unique() *Column             { c.IsUnique = true; return c }
func (c *Column) Primary() *Column            { c.IsPrimary = true; return c }
func (c *Column) AutoIncrement() *Column      { c.IsAutoIncr = true; return c }
func (c *Column) Comment(text string) *Column { c.CommentText = text; return c }
func (c *Column) After(column string) *Column { c.AfterColumn = column; return c }

// Change marks the column as "modify existing" when used
// inside Table(...) (ALTER); the counterpart of Laravel's ->change().
func (c *Column) Change() *Column { c.IsChange = true; return c }

// IndexType is the kind of index.
type IndexType int

const (
	IdxIndex IndexType = iota
	IdxUnique
	IdxPrimary
)

// Index is a standalone index (regular, unique or composite primary key).
type Index struct {
	Name    string
	Columns []string
	Type    IndexType
}

// As sets an explicit index name (by default it is generated from the column names).
func (idx *Index) As(name string) *Index { idx.Name = name; return idx }

// ForeignKey is a foreign key definition.
type ForeignKey struct {
	Name           string // constraint name; generated if empty
	Column         string
	RefColumn      string
	RefTable       string
	OnDeleteAction string // "cascade" | "restrict" | "set null" | "no action" | ""
	OnUpdateAction string
}

// ForeignIDColumnDefinition is the result of ForeignID/ForeignUUID/ForeignULID:
// a column plus a References/On/Constrained/OnDelete/OnUpdate chain that describes
// the foreign key right where the column is declared (like Laravel's foreignId()->constrained()).
type ForeignIDColumnDefinition struct {
	*Column
	bp               *Blueprint
	pendingRefColumn string
	pendingFK        *ForeignKey
}

func (f *ForeignIDColumnDefinition) References(column string) *ForeignIDColumnDefinition {
	f.pendingRefColumn = column
	return f
}

func (f *ForeignIDColumnDefinition) On(table string) *ForeignIDColumnDefinition {
	fk := &ForeignKey{Name: f.bp.IndexName("foreign", f.Name), Column: f.Name, RefColumn: f.pendingRefColumn, RefTable: table}
	if fk.RefColumn == "" {
		fk.RefColumn = "id"
	}
	f.bp.ForeignKeys = append(f.bp.ForeignKeys, fk)
	f.pendingFK = fk
	return f
}

func (f *ForeignIDColumnDefinition) OnDelete(action string) *ForeignIDColumnDefinition {
	if f.pendingFK != nil {
		f.pendingFK.OnDeleteAction = action
	}
	return f
}

func (f *ForeignIDColumnDefinition) OnUpdate(action string) *ForeignIDColumnDefinition {
	if f.pendingFK != nil {
		f.pendingFK.OnUpdateAction = action
	}
	return f
}

// Constrained is a shortcut: it infers the table from the column name
// (company_id -> companies, via naive pluralization, see util.go) and
// references "id". Pass the table name explicitly if auto-pluralization
// gets it wrong (irregular plurals, compound words, etc.).
func (f *ForeignIDColumnDefinition) Constrained(table ...string) *ForeignIDColumnDefinition {
	t := firstOrStr(table, pluralize(trimIDSuffix(f.Name)))
	return f.References("id").On(t)
}

func (f *ForeignIDColumnDefinition) Nullable(v ...bool) *ForeignIDColumnDefinition {
	f.Column.Nullable(v...)
	return f
}

// Dialect is what a specific SQL driver (mysql/postgres) must provide
// so that a Blueprint can be compiled into real DDL. It embeds
// orm.Dialect (Quote/Placeholder are already there) and adds DDL specifics.
type Dialect interface {
	Name() string
	Quote(identifier string) string
	Placeholder(n int) string

	// ColumnSQL is the full column definition for CREATE/ALTER TABLE,
	// including the type, NULL/NOT NULL, DEFAULT and auto-increment.
	ColumnSQL(col *Column) (string, error)
	// AlterColumnSQL is the SQL that modifies an existing column (Column.Change()).
	// The syntax differs between MySQL (MODIFY COLUMN) and Postgres (ALTER COLUMN ... TYPE ...).
	AlterColumnSQL(table string, col *Column) (string, error)
	// TableSuffix is, for example, "ENGINE=InnoDB DEFAULT CHARSET=utf8mb4" for MySQL and "" for Postgres.
	TableSuffix() string

	CreateIndexSQL(table string, idx *Index) string
	DropIndexSQL(table string, indexName string) string
	// DropForeignKeySQL returns an error if the database cannot drop a foreign
	// key on its own (SQLite: only by recreating the table).
	DropForeignKeySQL(table string, fkName string) (string, error)
	ForeignKeyClause(fk *ForeignKey) string
}

// AlterConstraintDialect is an optional Dialect extension for databases where
// constraints cannot be added via ALTER TABLE ... ADD CONSTRAINT (SQLite).
// If the dialect implements it, Table(...) uses these methods.
type AlterConstraintDialect interface {
	AddUniqueSQL(table string, idx *Index) (string, error)
	AddForeignKeySQL(table string, fk *ForeignKey) (string, error)
}

// migrationExecutorCtx is a helper type that lets Blueprint functions be
// plain func(*Blueprint) without depending on context; the Builder itself
// takes a context.Context separately (see builder.go).
type migrationExecutorCtx = context.Context
