package mysql

import (
	"fmt"
	"strings"

	"github.com/youruser/goorm/schema"
)

// ---- schema.Dialect: DDL для MySQL ----

func (d Dialect) ColumnSQL(col *schema.Column) (string, error) {
	typeSQL, err := mysqlColumnType(col)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString(d.Quote(col.Name))
	b.WriteString(" ")
	b.WriteString(typeSQL)

	if col.IsUnsigned && isMySQLNumeric(col.Type) {
		b.WriteString(" UNSIGNED")
	}

	if col.IsNullable {
		b.WriteString(" NULL")
	} else {
		b.WriteString(" NOT NULL")
	}

	if col.IsAutoIncr {
		b.WriteString(" AUTO_INCREMENT")
	}

	if col.HasDefault {
		b.WriteString(" DEFAULT ")
		b.WriteString(mysqlLiteral(col))
	}

	if col.CommentText != "" {
		b.WriteString(" COMMENT ")
		b.WriteString(quoteLiteral(col.CommentText))
	}

	if col.AfterColumn != "" {
		b.WriteString(" AFTER ")
		b.WriteString(d.Quote(col.AfterColumn))
	}

	return b.String(), nil
}

func (d Dialect) AlterColumnSQL(table string, col *schema.Column) (string, error) {
	colSQL, err := d.ColumnSQL(col)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("ALTER TABLE %s MODIFY COLUMN %s", d.Quote(table), colSQL), nil
}

func (d Dialect) TableSuffix() string {
	return " ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"
}

func (d Dialect) CreateIndexSQL(table string, idx *schema.Index) string {
	cols := make([]string, len(idx.Columns))
	for i, c := range idx.Columns {
		cols[i] = d.Quote(c)
	}
	return fmt.Sprintf("CREATE INDEX %s ON %s (%s)", d.Quote(idx.Name), d.Quote(table), strings.Join(cols, ", "))
}

func (d Dialect) DropIndexSQL(table string, indexName string) string {
	return fmt.Sprintf("ALTER TABLE %s DROP INDEX %s", d.Quote(table), d.Quote(indexName))
}

func (d Dialect) DropForeignKeySQL(table string, fkName string) string {
	return fmt.Sprintf("ALTER TABLE %s DROP FOREIGN KEY %s", d.Quote(table), d.Quote(fkName))
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

// ---- маппинг типов ----

func isMySQLNumeric(t schema.ColumnType) bool {
	switch t {
	case schema.TypeTinyInteger, schema.TypeSmallInteger, schema.TypeMediumInteger,
		schema.TypeInteger, schema.TypeBigInteger, schema.TypeDecimal, schema.TypeDouble, schema.TypeFloat:
		return true
	default:
		return false
	}
}

func mysqlColumnType(col *schema.Column) (string, error) {
	switch col.Type {
	case schema.TypeBoolean:
		return "TINYINT(1)", nil
	case schema.TypeChar:
		return fmt.Sprintf("CHAR(%d)", orDefault(col.Length, 255)), nil
	case schema.TypeString:
		return fmt.Sprintf("VARCHAR(%d)", orDefault(col.Length, 255)), nil
	case schema.TypeText:
		return "TEXT", nil
	case schema.TypeMediumText:
		return "MEDIUMTEXT", nil
	case schema.TypeLongText:
		return "LONGTEXT", nil
	case schema.TypeTinyText:
		return "TINYTEXT", nil

	case schema.TypeTinyInteger:
		return "TINYINT", nil
	case schema.TypeSmallInteger:
		return "SMALLINT", nil
	case schema.TypeMediumInteger:
		return "MEDIUMINT", nil
	case schema.TypeInteger:
		return "INT", nil
	case schema.TypeBigInteger:
		return "BIGINT", nil
	case schema.TypeDecimal:
		return fmt.Sprintf("DECIMAL(%d,%d)", orDefault(col.Precision, 8), col.Scale), nil
	case schema.TypeDouble:
		return "DOUBLE", nil
	case schema.TypeFloat:
		return "FLOAT", nil

	case schema.TypeDate:
		return "DATE", nil
	case schema.TypeDateTime:
		return withPrecision("DATETIME", col.Precision), nil
	case schema.TypeTime:
		return withPrecision("TIME", col.Precision), nil
	case schema.TypeTimestamp:
		return withPrecision("TIMESTAMP", col.Precision), nil
	case schema.TypeYear:
		return "YEAR", nil

	case schema.TypeBinary:
		return "BLOB", nil
	case schema.TypeJSON, schema.TypeJSONB:
		return "JSON", nil // у MySQL нет отдельного JSONB — JSON и есть двоичный, документируем в README

	case schema.TypeUUID:
		return "CHAR(36)", nil
	case schema.TypeULID:
		return "CHAR(26)", nil

	case schema.TypeGeometry, schema.TypeGeography:
		return "GEOMETRY", nil // MySQL не различает geometry/geography — оба маппятся в GEOMETRY

	case schema.TypeEnum:
		return "ENUM(" + quotedList(col.Values) + ")", nil
	case schema.TypeSet:
		return "SET(" + quotedList(col.Values) + ")", nil
	case schema.TypeMacAddress:
		return "VARCHAR(17)", nil
	case schema.TypeIPAddress:
		return "VARCHAR(45)", nil

	case schema.TypeVector:
		return "", fmt.Errorf("mysql: тип vector не поддерживается стандартным MySQL")

	default:
		return "", fmt.Errorf("mysql: неизвестный тип колонки %q", col.Type)
	}
}

func withPrecision(base string, precision int) string {
	if precision > 0 {
		return fmt.Sprintf("%s(%d)", base, precision)
	}
	return base
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

func mysqlLiteral(col *schema.Column) string {
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
