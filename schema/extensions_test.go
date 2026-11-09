package schema

import (
	"strings"
	"testing"
)

// hookDialect uses the optional CreateTableDialect and RenameTableDialect.
type hookDialect struct{ testDialect }

func (hookDialect) CreateTableSQL(bp *Blueprint, ifNotExists bool) ([]string, error) {
	return []string{"CREATE " + bp.Table + " engine=" + bp.Options["engine"]}, nil
}
func (hookDialect) RenameTableSQL(from, to string) string { return "RENAME " + from + " " + to }

func TestDialectExtensions(t *testing.T) {
	b := New(nil, hookDialect{})
	stmts, err := b.SQLForCreate("t", func(t *Blueprint) {
		t.ID()
		t.Option("engine", "Log")
		t.Double("d", 10, 2)
		t.SoftDeletes()
		t.IndexCols("d").Using("minmax")
	})
	if err != nil || stmts[0] != "CREATE t engine=Log" {
		t.Fatalf("CreateTableSQL hook: %v %v", stmts, err)
	}
	ex := &recExec{}
	if err := New(ex, hookDialect{}).Rename(ctx, "a", "b"); err != nil || ex.stmts[0] != "RENAME a b" {
		t.Fatalf("RenameTableSQL hook: %v %v", ex.stmts, err)
	}

	// Without the hooks: the generic compiler, Using kept on the index.
	bp := &Blueprint{Table: "t"}
	bp.Double("d", 10, 2)
	bp.SoftDeletes()
	idx := bp.IndexCols("d").Using("minmax")
	if idx.Algorithm != "minmax" || bp.Columns[0].Scale != 2 || !bp.Columns[1].IsNullable {
		t.Fatalf("blueprint: %+v %+v", idx, bp.Columns)
	}
	if got := sqlQuoteLiteral("it's"); got != "'it''s'" && !strings.Contains(got, "''") {
		t.Fatalf("sqlQuoteLiteral = %s", got)
	}
}
