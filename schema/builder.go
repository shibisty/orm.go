package schema

import (
	"context"
	"fmt"

	"orm"
)

// Builder is the counterpart of Laravel's Schema:: facade, bound to a specific
// SQL connection (mysql/postgres both implement orm.SQLExecutor + Dialect).
type Builder struct {
	exec    orm.SQLExecutor
	dialect Dialect
}

// New creates a Builder directly, if you have your own Dialect.
func New(exec orm.SQLExecutor, dialect Dialect) *Builder {
	return &Builder{exec: exec, dialect: dialect}
}

// NewFor creates a Builder, obtaining the schema.Dialect from exec.Dialect() via
// a type assertion. This is simpler for the built-in drivers (mysql.Dialect{} and
// postgres.Dialect{} implement both interfaces: orm.Dialect for DML and
// schema.Dialect for DDL).
func NewFor(exec orm.SQLExecutor) (*Builder, error) {
	d, ok := exec.Dialect().(Dialect)
	if !ok {
		return nil, fmt.Errorf("schema: dialect %q does not implement schema.Dialect (this driver does not support DDL)", exec.Driver())
	}
	return New(exec, d), nil
}

func (b *Builder) execAll(ctx context.Context, stmts []string) error {
	for _, s := range stmts {
		if _, err := b.exec.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("schema: %s: %w", s, err)
		}
	}
	return nil
}

// SQLForCreate returns the SQL that Create would execute, without executing it;
// useful for dry runs, logging migrations and tests.
func (b *Builder) SQLForCreate(table string, fn func(*Blueprint)) ([]string, error) {
	bp := newBlueprint(table, actionCreate)
	fn(bp)
	return compileCreate(b.dialect, bp, false)
}

// SQLForAlter is the same for Table (ALTER).
func (b *Builder) SQLForAlter(table string, fn func(*Blueprint)) ([]string, error) {
	bp := newBlueprint(table, actionAlter)
	fn(bp)
	return compileAlter(b.dialect, bp)
}

// Create creates a new table.
func (b *Builder) Create(ctx context.Context, table string, fn func(*Blueprint)) error {
	stmts, err := b.SQLForCreate(table, fn)
	if err != nil {
		return err
	}
	return b.execAll(ctx, stmts)
}

// CreateIfNotExists is the same but with "IF NOT EXISTS" (used
// internally by the migrate package for the migrations table).
func (b *Builder) CreateIfNotExists(ctx context.Context, table string, fn func(*Blueprint)) error {
	bp := newBlueprint(table, actionCreate)
	fn(bp)
	stmts, err := compileCreate(b.dialect, bp, true)
	if err != nil {
		return err
	}
	return b.execAll(ctx, stmts)
}

// Table alters an existing table (ALTER TABLE).
func (b *Builder) Table(ctx context.Context, table string, fn func(*Blueprint)) error {
	stmts, err := b.SQLForAlter(table, fn)
	if err != nil {
		return err
	}
	return b.execAll(ctx, stmts)
}

func (b *Builder) Drop(ctx context.Context, table string) error {
	return b.execAll(ctx, compileDrop(b.dialect, table))
}

func (b *Builder) DropIfExists(ctx context.Context, table string) error {
	return b.execAll(ctx, compileDropIfExists(b.dialect, table))
}

func (b *Builder) Rename(ctx context.Context, from, to string) error {
	return b.execAll(ctx, compileRename(b.dialect, from, to))
}
