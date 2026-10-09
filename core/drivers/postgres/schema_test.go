package postgres_test

import (
	"strings"
	"testing"

	postgres "orm-postgres"
	"orm/schema"
)

func TestPostgres_CreateTable_SerialAndUUID(t *testing.T) {
	b := schema.New(nil, postgres.Dialect{})

	stmts, err := b.SQLForCreate("users", func(t *schema.Blueprint) {
		t.ID()
		t.UUID("public_id")
		t.String("email").Unique()
		t.JSONB("meta").Nullable()
	})
	if err != nil {
		t.Fatalf("SQLForCreate returned an error: %v", err)
	}

	create := stmts[0]
	for _, want := range []string{
		`CREATE TABLE "users"`,
		`"id" BIGSERIAL NOT NULL`,
		`"public_id" UUID NOT NULL`,
		`"meta" JSONB NULL`,
		`PRIMARY KEY ("id")`,
	} {
		if !strings.Contains(create, want) {
			t.Errorf("CREATE TABLE (postgres) does not contain %q\nfull SQL:\n%s", want, create)
		}
	}
	if strings.Contains(create, "ENGINE") {
		t.Errorf("Postgres CREATE TABLE must not contain ENGINE (that is MySQL-specific): %s", create)
	}
}

func TestAlterTable_AddDropRenameColumn(t *testing.T) {
	b := schema.New(nil, postgres.Dialect{})

	stmts, err := b.SQLForAlter("users", func(t *schema.Blueprint) {
		t.String("nickname").Nullable()
		t.DropColumn("legacy_field")
		t.RenameColumn("old_name", "new_name")
	})
	if err != nil {
		t.Fatalf("SQLForAlter returned an error: %v", err)
	}

	joined := strings.Join(stmts, " ;; ")
	for _, want := range []string{
		`ALTER TABLE "users" ADD COLUMN "nickname" VARCHAR(255) NULL`,
		`ALTER TABLE "users" DROP COLUMN "legacy_field"`,
		`ALTER TABLE "users" RENAME COLUMN "old_name" TO "new_name"`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("ALTER TABLE does not contain %q\nall statements:\n%s", want, joined)
		}
	}
}

func TestEnumOnPostgres_UsesCheckConstraint(t *testing.T) {
	b := schema.New(nil, postgres.Dialect{})

	stmts, err := b.SQLForCreate("orders", func(t *schema.Blueprint) {
		t.Enum("status", []string{"pending", "paid", "shipped"})
	})
	if err != nil {
		t.Fatalf("SQLForCreate returned an error: %v", err)
	}

	create := stmts[0]
	if !strings.Contains(create, `CHECK ("status" IN ('pending', 'paid', 'shipped'))`) {
		t.Errorf("enum on Postgres should be emulated via CHECK: %s", create)
	}
}

func TestSetOnPostgres_ReturnsError(t *testing.T) {
	b := schema.New(nil, postgres.Dialect{})

	_, err := b.SQLForCreate("tags", func(t *schema.Blueprint) {
		t.Set("flags", []string{"a", "b"})
	})
	if err == nil {
		t.Fatal("expected an error: set is not natively supported on Postgres")
	}
}

func TestUUIDMorphs(t *testing.T) {
	b := schema.New(nil, postgres.Dialect{})

	stmts, err := b.SQLForCreate("comments", func(t *schema.Blueprint) {
		t.UUID("id")
		t.NullableUUIDMorphs("commentable")
	})
	if err != nil {
		t.Fatalf("SQLForCreate returned an error: %v", err)
	}
	create := stmts[0]
	if !strings.Contains(create, `"commentable_id" UUID NULL`) {
		t.Errorf("NullableUUIDMorphs should create a nullable UUID commentable_id: %s", create)
	}
	if !strings.Contains(create, `"commentable_type" VARCHAR(255) NULL`) {
		t.Errorf("NullableUUIDMorphs should create a nullable commentable_type: %s", create)
	}
}
