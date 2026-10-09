package postgres

import (
	"fmt"
	"strings"

	"orm"
	"orm/schema"
)

// ---- schema.Dialect: DDL for Postgres ----

func (d Dialect) ColumnSQL(col *schema.Column) (string, error) {
	typeSQL, err := postgresColumnType(col)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString(d.Quote(col.Name))
	b.WriteString(" ")
	b.WriteString(typeSQL)

	if col.IsNullable {
		b.WriteString(" NULL")
	} else {
		b.WriteString(" NOT NULL")
	}

	// SERIAL/BIGSERIAL/SMALLSERIAL already include auto-increment via a
	// sequence, so no separate DEFAULT/AUTO_INCREMENT is needed.
	if col.HasDefault && !(col.IsAutoIncr && isPostgresIntegerLike(col.Type)) {
		b.WriteString(" DEFAULT ")
		b.WriteString(postgresLiteral(col))
	}

	if col.Type == schema.TypeEnum {
		b.WriteString(fmt.Sprintf(" CHECK (%s IN (%s))", d.Quote(col.Name), quotedList(col.Values)))
	}

	return b.String(), nil
}

func (d Dialect) AlterColumnSQL(table string, col *schema.Column) (string, error) {
	typeSQL, err := postgresColumnType(col)
	if err != nil {
		return "", err
	}
	// Postgres requires separate ALTER COLUMN clauses for type/NULL/DEFAULT. In
	// this version Change() only alters the type and nullability in one command
	// (the common case: widening a VARCHAR, int -> bigint, etc.); to change the
	// DEFAULT, use a separate ALTER TABLE ... ALTER COLUMN ... SET DEFAULT.
	nullClause := "SET NOT NULL"
	if col.IsNullable {
		nullClause = "DROP NOT NULL"
	}
	return fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s TYPE %s, ALTER COLUMN %s %s",
		d.Quote(table), d.Quote(col.Name), typeSQL, d.Quote(col.Name), nullClause), nil
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

func (d Dialect) DropForeignKeySQL(table string, fkName string) (string, error) {
	return fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s", d.Quote(table), d.Quote(fkName)), nil
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

func isPostgresIntegerLike(t schema.ColumnType) bool {
	switch t {
	case schema.TypeSmallInteger, schema.TypeInteger, schema.TypeBigInteger,
		schema.TypeTinyInteger, schema.TypeMediumInteger:
		return true
	default:
		return false
	}
}

func postgresColumnType(col *schema.Column) (string, error) {
	// In Postgres, auto-increment is a distinct column type (the SERIAL family),
	// not a suffix on a plain INTEGER like AUTO_INCREMENT in MySQL.
	if col.IsAutoIncr {
		switch col.Type {
		case schema.TypeTinyInteger, schema.TypeSmallInteger:
			return "SMALLSERIAL", nil
		case schema.TypeMediumInteger, schema.TypeInteger:
			return "SERIAL", nil
		case schema.TypeBigInteger:
			return "BIGSERIAL", nil
		}
	}

	switch col.Type {
	case schema.TypeBoolean:
		return "BOOLEAN", nil
	case schema.TypeChar:
		return fmt.Sprintf("CHAR(%d)", orDefault(col.Length, 255)), nil
	case schema.TypeString:
		return fmt.Sprintf("VARCHAR(%d)", orDefault(col.Length, 255)), nil
	case schema.TypeText, schema.TypeMediumText, schema.TypeLongText, schema.TypeTinyText:
		// Postgres has a single TEXT with no size limit, so MySQL's text tiers
		// (tiny/medium/long) all collapse into the same type here.
		return "TEXT", nil

	case schema.TypeTinyInteger, schema.TypeSmallInteger:
		return "SMALLINT", nil // Postgres has no separate TINYINT
	case schema.TypeMediumInteger, schema.TypeInteger:
		return "INTEGER", nil // Postgres has no separate MEDIUMINT
	case schema.TypeBigInteger:
		return "BIGINT", nil
	case schema.TypeDecimal:
		return fmt.Sprintf("NUMERIC(%d,%d)", orDefault(col.Precision, 8), col.Scale), nil
	case schema.TypeDouble:
		return "DOUBLE PRECISION", nil
	case schema.TypeFloat:
		if col.Precision > 24 {
			return "DOUBLE PRECISION", nil
		}
		return "REAL", nil

	case schema.TypeDate:
		return "DATE", nil
	case schema.TypeDateTime, schema.TypeTimestamp:
		// Postgres has no separate DATETIME; MySQL's DATETIME maps to TIMESTAMP
		return withPrecisionTz("TIMESTAMP", col.Precision, col.WithTZ), nil
	case schema.TypeTime:
		return withPrecisionTz("TIME", col.Precision, col.WithTZ), nil
	case schema.TypeYear:
		return "SMALLINT", nil // Postgres has no YEAR type

	case schema.TypeBinary:
		return "BYTEA", nil
	case schema.TypeJSON:
		return "JSON", nil
	case schema.TypeJSONB:
		return "JSONB", nil

	case schema.TypeUUID:
		return "UUID", nil // native Postgres type
	case schema.TypeULID:
		return fmt.Sprintf("CHAR(%d)", orDefault(col.Length, 26)), nil // Postgres has no native ULID

	case schema.TypeGeometry:
		return "geometry", nil // requires the PostGIS extension
	case schema.TypeGeography:
		return "geography", nil // requires the PostGIS extension

	case schema.TypeEnum:
		// A proper named ENUM in Postgres requires a separate
		// CREATE TYPE ... AS ENUM(...) outside the column's transaction; this is
		// deliberately simplified to VARCHAR + CHECK (col IN (...)) to avoid
		// managing the lifecycle of named types across migrations.
		return fmt.Sprintf("VARCHAR(%d)", orDefault(col.Length, 255)), nil
	case schema.TypeSet:
		return "", fmt.Errorf("postgres: set type is not natively supported (no equivalent of MySQL SET); use []string/JSONB and application-level validation")
	case schema.TypeMacAddress:
		return "MACADDR", nil // native Postgres type
	case schema.TypeIPAddress:
		return "INET", nil // native Postgres type

	case schema.TypeVector:
		return fmt.Sprintf("vector(%d)", col.Dimensions), nil // requires the pgvector extension

	default:
		return "", fmt.Errorf("postgres: unknown column type %q", col.Type)
	}
}

func withPrecisionTz(base string, precision int, tz bool) string {
	s := base
	if precision > 0 {
		s += fmt.Sprintf("(%d)", precision)
	}
	if tz {
		s += " WITH TIME ZONE"
	}
	return s
}

func orDefault(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func quotedList(values []string) string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = quoteLiteral(v)
	}
	return strings.Join(out, ", ")
}

func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func postgresLiteral(col *schema.Column) string {
	if col.DefaultIsRaw {
		return fmt.Sprint(col.DefaultValue)
	}
	switch v := col.DefaultValue.(type) {
	case string:
		return quoteLiteral(v)
	case bool:
		if v {
			return "TRUE"
		}
		return "FALSE"
	case int, int64, float64:
		return fmt.Sprint(v)
	default:
		return quoteLiteral(fmt.Sprint(v))
	}
}

// Compile-time check: Dialect implements both interfaces.
var (
	_ schema.Dialect = Dialect{}
	_ orm.Dialect    = Dialect{}
)
