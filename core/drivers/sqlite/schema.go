package sqlite

import (
	"fmt"
	"strings"

	"orm"
	"orm/schema"
)

// ---- schema.Dialect: DDL for SQLite ----

func (d Dialect) ColumnSQL(col *schema.Column) (string, error) {
	typeSQL, err := sqliteColumnType(col)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString(d.Quote(col.Name))
	b.WriteString(" ")
	b.WriteString(typeSQL)

	// SQLite has no UNSIGNED as a separate type attribute (its type system is
	// type affinity, not fixed width), so we silently drop it. This does not
	// change actual behavior (values are still stored correctly); only the
	// declarative "always non-negative" guarantee at the schema level is
	// lost. See the README.

	if col.IsNullable {
		b.WriteString(" NULL")
	} else {
		b.WriteString(" NOT NULL")
	}

	if col.HasDefault {
		b.WriteString(" DEFAULT ")
		b.WriteString(sqliteLiteral(col))
	}

	// SQLite has no ENUM; constrain the values with CHECK, as on Postgres.
	if col.Type == schema.TypeEnum && len(col.Values) > 0 {
		vals := make([]string, len(col.Values))
		for i, v := range col.Values {
			vals[i] = "'" + strings.ReplaceAll(v, "'", "''") + "'"
		}
		fmt.Fprintf(&b, " CHECK (%s IN (%s))", d.Quote(col.Name), strings.Join(vals, ", "))
	}

	return b.String(), nil
}

// AlterColumnSQL: SQLite does not support ALTER TABLE ... ALTER COLUMN /
// MODIFY COLUMN at all (the only way to change the type of an existing
// column is to recreate the table: CREATE new -> INSERT SELECT -> DROP
// old -> RENAME). We don't do that automatically: it involves too many implicit
// decisions (what about indexes/foreign keys pointing at this table, what to do
// with incompatible data), so Column.Change() on SQLite returns an
// honest error instead of silently generating invalid SQL.
func (d Dialect) AlterColumnSQL(table string, col *schema.Column) (string, error) {
	return "", fmt.Errorf("sqlite: ALTER COLUMN is not supported by SQLite directly (column %q, table %q); "+
		"recreate the table manually (CREATE new -> INSERT SELECT -> DROP old -> RENAME)", col.Name, table)
}

func (d Dialect) TableSuffix() string { return "" }

func (d Dialect) CreateIndexSQL(table string, idx *schema.Index) string {
	cols := make([]string, len(idx.Columns))
	for i, c := range idx.Columns {
		cols[i] = d.Quote(c)
	}
	return fmt.Sprintf("CREATE INDEX %s ON %s (%s)", d.Quote(idx.Name), d.Quote(table), strings.Join(cols, ", "))
}

func (d Dialect) DropIndexSQL(table string, indexName string) string {
	return fmt.Sprintf("DROP INDEX %s", d.Quote(indexName))
}

// DropForeignKeySQL: SQLite does not support ALTER TABLE ... DROP CONSTRAINT.
// Foreign keys there are not named constraints that can be dropped
// separately but part of the table definition, removable only by recreating
// the table. We return an honest error instead of invalid or no-op SQL.
func (d Dialect) DropForeignKeySQL(table string, fkName string) (string, error) {
	return "", fmt.Errorf("sqlite: DROP CONSTRAINT is not supported by SQLite (foreign key %q, table %q); "+
		"recreate the table without this foreign key", fkName, table)
}

func (d Dialect) ForeignKeyClause(fk *schema.ForeignKey) string {
	name := fk.Name
	if name == "" {
		name = fk.Column + "_foreign"
	}
	clause := fmt.Sprintf("CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s)",
		d.Quote(name), d.Quote(fk.Column), d.Quote(fk.RefTable), d.Quote(fk.RefColumn))
	if fk.OnDeleteAction != "" {
		clause += " ON DELETE " + strings.ToUpper(fk.OnDeleteAction)
	}
	if fk.OnUpdateAction != "" {
		clause += " ON UPDATE " + strings.ToUpper(fk.OnUpdateAction)
	}
	return clause
}

// ---- type mapping ----

func sqliteColumnType(col *schema.Column) (string, error) {
	// In SQLite, auto-increment is a rowid alias, not a separate type attribute:
	// it works ONLY if the column type is declared literally as "INTEGER"
	// (not "BIGINT" or anything else); otherwise the rowid aliasing does not
	// kick in and no value is generated. So for auto-increment
	// integer columns of any source size (tiny/small/medium/big) we
	// force "INTEGER" regardless of Type.
	if col.IsAutoIncr && isSQLiteIntegerLike(col.Type) {
		return "INTEGER", nil
	}

	switch col.Type {
	case schema.TypeBoolean:
		return "INTEGER", nil // SQLite has no BOOLEAN; stored as 0/1 (NUMERIC type affinity)
	case schema.TypeChar:
		return fmt.Sprintf("CHAR(%d)", orDefault(col.Length, 255)), nil
	case schema.TypeString:
		return fmt.Sprintf("VARCHAR(%d)", orDefault(col.Length, 255)), nil
	case schema.TypeText, schema.TypeMediumText, schema.TypeLongText, schema.TypeTinyText:
		return "TEXT", nil // like Postgres, SQLite has a single TEXT without MySQL's tiers

	case schema.TypeTinyInteger, schema.TypeSmallInteger, schema.TypeMediumInteger,
		schema.TypeInteger, schema.TypeBigInteger:
		return "INTEGER", nil // SQLite integer types have no fixed width (type affinity)
	case schema.TypeDecimal:
		return fmt.Sprintf("NUMERIC(%d,%d)", orDefault(col.Precision, 8), col.Scale), nil
	case schema.TypeDouble, schema.TypeFloat:
		return "REAL", nil

	case schema.TypeDate, schema.TypeDateTime, schema.TypeTime, schema.TypeTimestamp:
		// SQLite has no native date/time types; the common convention is
		// to store them as TEXT in ISO8601 format (SQLite can work with it
		// via the date()/time()/datetime() functions). WithTZ/Precision are
		// ignored at the type level; formatting the value is the application's
		// responsibility.
		return "TEXT", nil
	case schema.TypeYear:
		return "INTEGER", nil

	case schema.TypeBinary:
		return "BLOB", nil
	case schema.TypeJSON, schema.TypeJSONB:
		return "TEXT", nil // SQLite stores JSON as TEXT (the json_* functions work on TEXT/BLOB)

	case schema.TypeUUID:
		return fmt.Sprintf("CHAR(%d)", 36), nil
	case schema.TypeULID:
		return fmt.Sprintf("CHAR(%d)", orDefault(col.Length, 26)), nil

	case schema.TypeGeometry, schema.TypeGeography:
		return "", fmt.Errorf("sqlite: type %s is not supported without the SpatiaLite extension", col.Type)

	case schema.TypeEnum:
		// SQLite has no native ENUM: VARCHAR + CHECK (added in ColumnSQL).
		return fmt.Sprintf("VARCHAR(%d)", orDefault(col.Length, 255)), nil
	case schema.TypeSet:
		return "", fmt.Errorf("sqlite: set type is not natively supported; use a JSON array (TEXT) and application-level validation")
	case schema.TypeMacAddress:
		return "VARCHAR(17)", nil
	case schema.TypeIPAddress:
		return "VARCHAR(45)", nil

	case schema.TypeVector:
		return "", fmt.Errorf("sqlite: vector type is not supported without an extension (e.g. sqlite-vec)")

	default:
		return "", fmt.Errorf("sqlite: unknown column type %q", col.Type)
	}
}

func isSQLiteIntegerLike(t schema.ColumnType) bool {
	switch t {
	case schema.TypeTinyInteger, schema.TypeSmallInteger, schema.TypeMediumInteger,
		schema.TypeInteger, schema.TypeBigInteger:
		return true
	default:
		return false
	}
}

func orDefault(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func sqliteLiteral(col *schema.Column) string {
	if col.DefaultIsRaw {
		return fmt.Sprint(col.DefaultValue)
	}
	switch v := col.DefaultValue.(type) {
	case string:
		return quoteLiteral(v)
	case bool:
		if v {
			return "1"
		}
		return "0"
	case int, int64, float64:
		return fmt.Sprint(v)
	default:
		return quoteLiteral(fmt.Sprint(v))
	}
}

// AddUniqueSQL: SQLite cannot do ALTER TABLE ... ADD CONSTRAINT, but a unique
// index gives the same guarantee.
func (d Dialect) AddUniqueSQL(table string, idx *schema.Index) (string, error) {
	cols := make([]string, len(idx.Columns))
	for i, c := range idx.Columns {
		cols[i] = d.Quote(c)
	}
	return fmt.Sprintf("CREATE UNIQUE INDEX %s ON %s (%s)", d.Quote(idx.Name), d.Quote(table), strings.Join(cols, ", ")), nil
}

// AddForeignKeySQL: a foreign key can be added to an existing SQLite table
// only by recreating the table, so we return a clear error.
func (d Dialect) AddForeignKeySQL(table string, fk *schema.ForeignKey) (string, error) {
	return "", fmt.Errorf("sqlite: cannot add a foreign key to existing table %q; "+
		"declare it in Create or recreate the table", table)
}

// Compile-time check: Dialect implements both interfaces.
var (
	_ schema.Dialect                = Dialect{}
	_ schema.AlterConstraintDialect = Dialect{}
	_ orm.Dialect                   = Dialect{}
)
