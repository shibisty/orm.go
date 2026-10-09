package schema

import "strings"

type action int

const (
	actionCreate action = iota
	actionAlter
)

// Blueprint describes a table (being created or altered); it is
// populated inside the schema.Builder.Create/Table callback and then
// compiled into the DDL of a specific dialect.
type Blueprint struct {
	Table string
	act   action

	Columns     []*Column
	DroppedCols []string
	RenamedCols map[string]string // old -> new

	Indexes    []*Index
	DroppedIdx []string

	ForeignKeys []*ForeignKey
	DroppedFKs  []string

	DropTable         bool
	DropTableIfExists bool
	RenameTo          string
}

func newBlueprint(table string, act action) *Blueprint {
	return &Blueprint{Table: table, act: act}
}

func (bp *Blueprint) addColumn(name string, t ColumnType) *Column {
	col := &Column{Name: name, Type: t}
	bp.Columns = append(bp.Columns, col)
	return col
}

// ---- Boolean ----

func (bp *Blueprint) Boolean(name string) *Column { return bp.addColumn(name, TypeBoolean) }

// ---- String & Text ----

func (bp *Blueprint) Char(name string, length ...int) *Column {
	c := bp.addColumn(name, TypeChar)
	c.Length = firstOr(length, 255)
	return c
}
func (bp *Blueprint) String(name string, length ...int) *Column {
	c := bp.addColumn(name, TypeString)
	c.Length = firstOr(length, 255)
	return c
}
func (bp *Blueprint) Text(name string) *Column       { return bp.addColumn(name, TypeText) }
func (bp *Blueprint) MediumText(name string) *Column { return bp.addColumn(name, TypeMediumText) }
func (bp *Blueprint) LongText(name string) *Column   { return bp.addColumn(name, TypeLongText) }
func (bp *Blueprint) TinyText(name string) *Column   { return bp.addColumn(name, TypeTinyText) }

// ---- Numeric ----

func (bp *Blueprint) TinyInteger(name string) *Column   { return bp.addColumn(name, TypeTinyInteger) }
func (bp *Blueprint) SmallInteger(name string) *Column  { return bp.addColumn(name, TypeSmallInteger) }
func (bp *Blueprint) MediumInteger(name string) *Column { return bp.addColumn(name, TypeMediumInteger) }
func (bp *Blueprint) Integer(name string) *Column       { return bp.addColumn(name, TypeInteger) }
func (bp *Blueprint) BigInteger(name string) *Column    { return bp.addColumn(name, TypeBigInteger) }

func (bp *Blueprint) UnsignedTinyInteger(name string) *Column { return bp.TinyInteger(name).Unsigned() }
func (bp *Blueprint) UnsignedSmallInteger(name string) *Column {
	return bp.SmallInteger(name).Unsigned()
}
func (bp *Blueprint) UnsignedMediumInteger(name string) *Column {
	return bp.MediumInteger(name).Unsigned()
}
func (bp *Blueprint) UnsignedInteger(name string) *Column    { return bp.Integer(name).Unsigned() }
func (bp *Blueprint) UnsignedBigInteger(name string) *Column { return bp.BigInteger(name).Unsigned() }

func (bp *Blueprint) TinyIncrements(name string) *Column {
	return bp.UnsignedTinyInteger(name).AutoIncrement().Primary()
}
func (bp *Blueprint) SmallIncrements(name string) *Column {
	return bp.UnsignedSmallInteger(name).AutoIncrement().Primary()
}
func (bp *Blueprint) MediumIncrements(name string) *Column {
	return bp.UnsignedMediumInteger(name).AutoIncrement().Primary()
}
func (bp *Blueprint) Increments(name string) *Column {
	return bp.UnsignedInteger(name).AutoIncrement().Primary()
}
func (bp *Blueprint) BigIncrements(name string) *Column {
	return bp.UnsignedBigInteger(name).AutoIncrement().Primary()
}

// ID is an alias for BigIncrements("id"), or BigIncrements(name), as in Laravel.
func (bp *Blueprint) ID(name ...string) *Column {
	return bp.BigIncrements(firstOrStr(name, "id"))
}

func (bp *Blueprint) Decimal(name string, precisionScale ...int) *Column {
	c := bp.addColumn(name, TypeDecimal)
	c.Precision, c.Scale = 8, 2
	if len(precisionScale) > 0 {
		c.Precision = precisionScale[0]
	}
	if len(precisionScale) > 1 {
		c.Scale = precisionScale[1]
	}
	return c
}
func (bp *Blueprint) Double(name string, precisionScale ...int) *Column {
	c := bp.addColumn(name, TypeDouble)
	if len(precisionScale) > 0 {
		c.Precision = precisionScale[0]
	}
	if len(precisionScale) > 1 {
		c.Scale = precisionScale[1]
	}
	return c
}
func (bp *Blueprint) Float(name string, precisionScale ...int) *Column {
	c := bp.addColumn(name, TypeFloat)
	c.Precision = 53
	if len(precisionScale) > 0 {
		c.Precision = precisionScale[0]
	}
	if len(precisionScale) > 1 {
		c.Scale = precisionScale[1]
	}
	return c
}

// ---- Date & Time ----

func (bp *Blueprint) Date(name string) *Column { return bp.addColumn(name, TypeDate) }

func (bp *Blueprint) DateTime(name string, precision ...int) *Column {
	c := bp.addColumn(name, TypeDateTime)
	c.Precision = firstOr(precision, 0)
	return c
}
func (bp *Blueprint) DateTimeTz(name string, precision ...int) *Column {
	c := bp.DateTime(name, precision...)
	c.WithTZ = true
	return c
}
func (bp *Blueprint) Time(name string, precision ...int) *Column {
	c := bp.addColumn(name, TypeTime)
	c.Precision = firstOr(precision, 0)
	return c
}
func (bp *Blueprint) TimeTz(name string, precision ...int) *Column {
	c := bp.Time(name, precision...)
	c.WithTZ = true
	return c
}
func (bp *Blueprint) Timestamp(name string, precision ...int) *Column {
	c := bp.addColumn(name, TypeTimestamp)
	c.Precision = firstOr(precision, 0)
	return c
}
func (bp *Blueprint) TimestampTz(name string, precision ...int) *Column {
	c := bp.Timestamp(name, precision...)
	c.WithTZ = true
	return c
}

// Timestamps adds created_at and updated_at (nullable timestamps).
func (bp *Blueprint) Timestamps(precision ...int) {
	bp.Timestamp("created_at", precision...).Nullable()
	bp.Timestamp("updated_at", precision...).Nullable()
}
func (bp *Blueprint) TimestampsTz(precision ...int) {
	bp.TimestampTz("created_at", precision...).Nullable()
	bp.TimestampTz("updated_at", precision...).Nullable()
}

// SoftDeletes adds a nullable deleted_at, used together with a
// "deleted_at IS NULL" check in application-level queries
// (the Blueprint itself does not build in that logic, just like Laravel).
func (bp *Blueprint) SoftDeletes(precision ...int) *Column {
	return bp.Timestamp("deleted_at", precision...).Nullable()
}
func (bp *Blueprint) SoftDeletesTz(precision ...int) *Column {
	return bp.TimestampTz("deleted_at", precision...).Nullable()
}

func (bp *Blueprint) Year(name string) *Column { return bp.addColumn(name, TypeYear) }

// ---- Binary ----

func (bp *Blueprint) Binary(name string) *Column { return bp.addColumn(name, TypeBinary) }

// ---- Object & JSON ----

func (bp *Blueprint) JSON(name string) *Column  { return bp.addColumn(name, TypeJSON) }
func (bp *Blueprint) JSONB(name string) *Column { return bp.addColumn(name, TypeJSONB) }

// ---- UUID & ULID ----

func (bp *Blueprint) UUID(name ...string) *Column {
	return bp.addColumn(firstOrStr(name, "uuid"), TypeUUID)
}
func (bp *Blueprint) ULID(name ...string) *Column {
	c := bp.addColumn(firstOrStr(name, "ulid"), TypeULID)
	c.Length = 26
	return c
}

func (bp *Blueprint) addMorphIndex(name string, indexName []string) {
	idxName := bp.IndexName("index", name+"_type", name+"_id")
	if len(indexName) > 0 {
		idxName = indexName[0]
	}
	bp.Indexes = append(bp.Indexes, &Index{Name: idxName, Columns: []string{name + "_type", name + "_id"}, Type: IdxIndex})
}

func (bp *Blueprint) Morphs(name string, indexName ...string) {
	bp.String(name + "_type")
	bp.UnsignedBigInteger(name + "_id")
	bp.addMorphIndex(name, indexName)
}
func (bp *Blueprint) NullableMorphs(name string, indexName ...string) {
	bp.String(name + "_type").Nullable()
	bp.UnsignedBigInteger(name + "_id").Nullable()
	bp.addMorphIndex(name, indexName)
}
func (bp *Blueprint) UUIDMorphs(name string, indexName ...string) {
	bp.String(name + "_type")
	bp.UUID(name + "_id")
	bp.addMorphIndex(name, indexName)
}
func (bp *Blueprint) NullableUUIDMorphs(name string, indexName ...string) {
	bp.String(name + "_type").Nullable()
	bp.UUID(name + "_id").Nullable()
	bp.addMorphIndex(name, indexName)
}
func (bp *Blueprint) ULIDMorphs(name string, indexName ...string) {
	bp.String(name + "_type")
	bp.ULID(name + "_id")
	bp.addMorphIndex(name, indexName)
}
func (bp *Blueprint) NullableULIDMorphs(name string, indexName ...string) {
	bp.String(name + "_type").Nullable()
	bp.ULID(name + "_id").Nullable()
	bp.addMorphIndex(name, indexName)
}

// ---- Spatial ----

func (bp *Blueprint) Geometry(name string) *Column  { return bp.addColumn(name, TypeGeometry) }
func (bp *Blueprint) Geography(name string) *Column { return bp.addColumn(name, TypeGeography) }

// ---- Relationship ----

func (bp *Blueprint) ForeignID(name string) *ForeignIDColumnDefinition {
	return &ForeignIDColumnDefinition{Column: bp.UnsignedBigInteger(name), bp: bp}
}
func (bp *Blueprint) ForeignUUID(name string) *ForeignIDColumnDefinition {
	return &ForeignIDColumnDefinition{Column: bp.UUID(name), bp: bp}
}
func (bp *Blueprint) ForeignULID(name string) *ForeignIDColumnDefinition {
	return &ForeignIDColumnDefinition{Column: bp.ULID(name), bp: bp}
}

// ForeignIDFor builds a "<singular>_id" column from a table name, with a
// reference to that table's "id". Example: ForeignIDFor("companies") -> column
// company_id, FOREIGN KEY -> companies(id).
func (bp *Blueprint) ForeignIDFor(table string, column ...string) *ForeignIDColumnDefinition {
	colName := firstOrStr(column, singularize(table)+"_id")
	fk := bp.ForeignID(colName)
	fk.References("id").On(table)
	return fk
}
func (bp *Blueprint) ForeignUUIDFor(table string, column ...string) *ForeignIDColumnDefinition {
	colName := firstOrStr(column, singularize(table)+"_id")
	fk := bp.ForeignUUID(colName)
	fk.References("id").On(table)
	return fk
}

// ---- Specialty ----

func (bp *Blueprint) Enum(name string, values []string) *Column {
	c := bp.addColumn(name, TypeEnum)
	c.Values = values
	return c
}
func (bp *Blueprint) Set(name string, values []string) *Column {
	c := bp.addColumn(name, TypeSet)
	c.Values = values
	return c
}
func (bp *Blueprint) MacAddress(name string) *Column { return bp.addColumn(name, TypeMacAddress) }
func (bp *Blueprint) IPAddress(name string) *Column  { return bp.addColumn(name, TypeIPAddress) }

// RememberToken adds a nullable remember_token VARCHAR(100), as in Laravel.
func (bp *Blueprint) RememberToken() *Column {
	return bp.String("remember_token", 100).Nullable()
}

// Vector is a fixed-dimension vector column (for pgvector on Postgres;
// MySQL has no native support and returns a compile error, see drivers/mysql).
func (bp *Blueprint) Vector(name string, dimensions int) *Column {
	c := bp.addColumn(name, TypeVector)
	c.Dimensions = dimensions
	return c
}

// ---- Indexes ----

func (bp *Blueprint) IndexCols(columns ...string) *Index {
	idx := &Index{Name: bp.IndexName("index", columns...), Columns: columns, Type: IdxIndex}
	bp.Indexes = append(bp.Indexes, idx)
	return idx
}
func (bp *Blueprint) UniqueCols(columns ...string) *Index {
	idx := &Index{Name: bp.IndexName("unique", columns...), Columns: columns, Type: IdxUnique}
	bp.Indexes = append(bp.Indexes, idx)
	return idx
}
func (bp *Blueprint) PrimaryCols(columns ...string) *Index {
	idx := &Index{Columns: columns, Type: IdxPrimary}
	bp.Indexes = append(bp.Indexes, idx)
	return idx
}

// ---- ALTER operations (used inside Table(...)) ----

func (bp *Blueprint) DropColumn(names ...string) { bp.DroppedCols = append(bp.DroppedCols, names...) }

func (bp *Blueprint) RenameColumn(from, to string) {
	if bp.RenamedCols == nil {
		bp.RenamedCols = map[string]string{}
	}
	bp.RenamedCols[from] = to
}

func (bp *Blueprint) DropIndex(name string)   { bp.DroppedIdx = append(bp.DroppedIdx, name) }
func (bp *Blueprint) DropForeign(name string) { bp.DroppedFKs = append(bp.DroppedFKs, name) }

// DropIndexCols drops the index created by IndexCols(columns...), using the conventional name.
func (bp *Blueprint) DropIndexCols(columns ...string) {
	bp.DropIndex(bp.IndexName("index", columns...))
}

// DropForeignCol drops the foreign key created for column, using the conventional name.
func (bp *Blueprint) DropForeignCol(column string) { bp.DropForeign(bp.IndexName("foreign", column)) }

// IndexName builds an index or constraint name following the Laravel convention:
// "<table>_<columns>_<kind>", e.g. users_email_unique, posts_user_id_foreign.
// The table name is required: in Postgres and SQLite index names are shared across
// the whole schema, and identical names in different tables conflict.
func (bp *Blueprint) IndexName(kind string, columns ...string) string {
	return IndexName(bp.Table, kind, columns...)
}

// IndexName is the same as Blueprint.IndexName, for an arbitrary table.
func IndexName(table, kind string, columns ...string) string {
	name := table + "_" + strings.Join(columns, "_") + "_" + kind
	return strings.ToLower(strings.NewReplacer("-", "_", ".", "_").Replace(name))
}
func (bp *Blueprint) DropSoftDeletes() { bp.DropColumn("deleted_at") }
func (bp *Blueprint) DropTimestamps()  { bp.DropColumn("created_at", "updated_at") }
