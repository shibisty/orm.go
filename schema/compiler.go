package schema

import (
	"fmt"
	"sort"
	"strings"
)

func compileCreate(d Dialect, bp *Blueprint, ifNotExists bool) ([]string, error) {
	var defs []string
	var primaryCols []string
	var uniqueSingleCols []string

	for _, col := range bp.Columns {
		colSQL, err := d.ColumnSQL(col)
		if err != nil {
			return nil, fmt.Errorf("schema: column %q: %w", col.Name, err)
		}
		defs = append(defs, colSQL)
		if col.IsPrimary {
			primaryCols = append(primaryCols, col.Name)
		}
		if col.IsUnique {
			uniqueSingleCols = append(uniqueSingleCols, col.Name)
		}
	}

	for _, idx := range bp.Indexes {
		if idx.Type == IdxPrimary {
			primaryCols = append(primaryCols, idx.Columns...)
		}
	}
	if len(primaryCols) > 0 {
		defs = append(defs, "PRIMARY KEY ("+strings.Join(quoteAll(d, primaryCols), ", ")+")")
	}

	for _, col := range uniqueSingleCols {
		defs = append(defs, fmt.Sprintf("CONSTRAINT %s UNIQUE (%s)", d.Quote(bp.IndexName("unique", col)), d.Quote(col)))
	}

	for _, idx := range bp.Indexes {
		if idx.Type == IdxUnique {
			defs = append(defs, fmt.Sprintf("CONSTRAINT %s UNIQUE (%s)", d.Quote(idx.Name), strings.Join(quoteAll(d, idx.Columns), ", ")))
		}
	}

	for _, fk := range bp.ForeignKeys {
		defs = append(defs, d.ForeignKeyClause(fk))
	}

	ifNotExistsClause := ""
	if ifNotExists {
		ifNotExistsClause = "IF NOT EXISTS "
	}

	createSQL := fmt.Sprintf("CREATE TABLE %s%s (\n  %s\n)%s",
		ifNotExistsClause, d.Quote(bp.Table), strings.Join(defs, ",\n  "), d.TableSuffix())

	stmts := []string{createSQL}

	// Regular (non-unique, non-primary) indexes are separate statements: this is
	// portable between MySQL/Postgres, unlike MySQL's inline KEY(...).
	for _, idx := range bp.Indexes {
		if idx.Type == IdxIndex {
			stmts = append(stmts, d.CreateIndexSQL(bp.Table, idx))
		}
	}

	return stmts, nil
}

func compileAlter(d Dialect, bp *Blueprint) ([]string, error) {
	var stmts []string

	for _, col := range bp.Columns {
		if col.IsChange {
			s, err := d.AlterColumnSQL(bp.Table, col)
			if err != nil {
				return nil, fmt.Errorf("schema: changing column %q: %w", col.Name, err)
			}
			stmts = append(stmts, s)
			continue
		}
		colSQL, err := d.ColumnSQL(col)
		if err != nil {
			return nil, fmt.Errorf("schema: column %q: %w", col.Name, err)
		}
		stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", d.Quote(bp.Table), colSQL))
		if col.IsUnique {
			s, err := addUnique(d, bp.Table, &Index{Name: bp.IndexName("unique", col.Name), Columns: []string{col.Name}, Type: IdxUnique})
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, s)
		}
	}

	for _, name := range bp.DroppedCols {
		stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", d.Quote(bp.Table), d.Quote(name)))
	}

	// Map order is random; sort so the SQL is identical on every run.
	froms := make([]string, 0, len(bp.RenamedCols))
	for from := range bp.RenamedCols {
		froms = append(froms, from)
	}
	sort.Strings(froms)
	for _, from := range froms {
		stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s", d.Quote(bp.Table), d.Quote(from), d.Quote(bp.RenamedCols[from])))
	}

	for _, idx := range bp.Indexes {
		switch idx.Type {
		case IdxUnique:
			s, err := addUnique(d, bp.Table, idx)
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, s)
		case IdxIndex:
			stmts = append(stmts, d.CreateIndexSQL(bp.Table, idx))
		case IdxPrimary:
			stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s ADD PRIMARY KEY (%s)",
				d.Quote(bp.Table), strings.Join(quoteAll(d, idx.Columns), ", ")))
		}
	}

	for _, name := range bp.DroppedIdx {
		stmts = append(stmts, d.DropIndexSQL(bp.Table, name))
	}

	for _, fk := range bp.ForeignKeys {
		if ac, ok := d.(AlterConstraintDialect); ok {
			s, err := ac.AddForeignKeySQL(bp.Table, fk)
			if err != nil {
				return nil, fmt.Errorf("schema: foreign key %q: %w", fk.Name, err)
			}
			stmts = append(stmts, s)
			continue
		}
		stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s ADD %s", d.Quote(bp.Table), d.ForeignKeyClause(fk)))
	}

	for _, name := range bp.DroppedFKs {
		s, err := d.DropForeignKeySQL(bp.Table, name)
		if err != nil {
			return nil, fmt.Errorf("schema: dropping foreign key %q: %w", name, err)
		}
		stmts = append(stmts, s)
	}

	return stmts, nil
}

func addUnique(d Dialect, table string, idx *Index) (string, error) {
	if ac, ok := d.(AlterConstraintDialect); ok {
		s, err := ac.AddUniqueSQL(table, idx)
		if err != nil {
			return "", fmt.Errorf("schema: unique index %q: %w", idx.Name, err)
		}
		return s, nil
	}
	return fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s UNIQUE (%s)",
		d.Quote(table), d.Quote(idx.Name), strings.Join(quoteAll(d, idx.Columns), ", ")), nil
}

func compileDrop(d Dialect, table string) []string {
	return []string{fmt.Sprintf("DROP TABLE %s", d.Quote(table))}
}

func compileDropIfExists(d Dialect, table string) []string {
	return []string{fmt.Sprintf("DROP TABLE IF EXISTS %s", d.Quote(table))}
}

func compileRename(d Dialect, from, to string) []string {
	return []string{fmt.Sprintf("ALTER TABLE %s RENAME TO %s", d.Quote(from), d.Quote(to))}
}
