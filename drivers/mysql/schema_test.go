package mysql_test

import (
	"strings"
	"testing"

	mysql "orm-mysql"
	"orm/schema"
)

func TestMySQL_CreateTable_UsersLikeExample(t *testing.T) {
	b := schema.New(nil, mysql.Dialect{})

	stmts, err := b.SQLForCreate("users", func(t *schema.Blueprint) {
		t.ID()
		t.String("name")
		t.String("email").Unique()
		t.Integer("age").Nullable()
		t.Boolean("active").Default(true)
		t.Timestamps()
	})
	if err != nil {
		t.Fatalf("SQLForCreate returned an error: %v", err)
	}

	create := stmts[0]
	for _, want := range []string{
		"CREATE TABLE `users`",
		"`id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT",
		"`name` VARCHAR(255) NOT NULL",
		"`email` VARCHAR(255) NOT NULL",
		"`age` INT NULL",
		"`active` TINYINT(1) NOT NULL DEFAULT 1",
		"PRIMARY KEY (`id`)",
		"CONSTRAINT `users_email_unique` UNIQUE (`email`)",
		"ENGINE=InnoDB",
	} {
		if !strings.Contains(create, want) {
			t.Errorf("CREATE TABLE does not contain %q\nfull SQL:\n%s", want, create)
		}
	}
}

func TestMorphs_GeneratesTwoColumnsAndIndex(t *testing.T) {
	b := schema.New(nil, mysql.Dialect{})

	stmts, err := b.SQLForCreate("comments", func(t *schema.Blueprint) {
		t.ID()
		t.Morphs("commentable")
	})
	if err != nil {
		t.Fatalf("SQLForCreate returned an error: %v", err)
	}

	create := stmts[0]
	if !strings.Contains(create, "`commentable_type` VARCHAR(255) NOT NULL") {
		t.Errorf("morphs should create commentable_type: %s", create)
	}
	if !strings.Contains(create, "`commentable_id` BIGINT UNSIGNED NOT NULL") {
		t.Errorf("morphs should create commentable_id: %s", create)
	}

	found := false
	for _, s := range stmts[1:] {
		if strings.Contains(s, "CREATE INDEX") && strings.Contains(s, "commentable_type") && strings.Contains(s, "commentable_id") {
			found = true
		}
	}
	if !found {
		t.Errorf("morphs should create a composite index on (type, id), statements: %+v", stmts)
	}
}

func TestForeignID_Constrained(t *testing.T) {
	b := schema.New(nil, mysql.Dialect{})

	stmts, err := b.SQLForCreate("posts", func(t *schema.Blueprint) {
		t.ID()
		t.ForeignID("company_id").Constrained()
	})
	if err != nil {
		t.Fatalf("SQLForCreate returned an error: %v", err)
	}

	create := stmts[0]
	if !strings.Contains(create, "FOREIGN KEY (`company_id`) REFERENCES `companies` (`id`)") {
		t.Errorf("Constrained() should infer company_id -> companies(id): %s", create)
	}
}

func TestVectorOnMySQL_ReturnsError(t *testing.T) {
	b := schema.New(nil, mysql.Dialect{})

	_, err := b.SQLForCreate("embeddings", func(t *schema.Blueprint) {
		t.Vector("embedding", 1536)
	})
	if err == nil {
		t.Fatal("expected an error: vector is not supported by standard MySQL")
	}
}

func TestForeignIDFor(t *testing.T) {
	b := schema.New(nil, mysql.Dialect{})

	stmts, err := b.SQLForCreate("posts", func(t *schema.Blueprint) {
		t.ID()
		t.ForeignIDFor("companies")
	})
	if err != nil {
		t.Fatalf("SQLForCreate returned an error: %v", err)
	}
	create := stmts[0]
	if !strings.Contains(create, "`company_id` BIGINT UNSIGNED NOT NULL") {
		t.Errorf("ForeignIDFor(\"companies\") should create a company_id column: %s", create)
	}
	if !strings.Contains(create, "FOREIGN KEY (`company_id`) REFERENCES `companies` (`id`)") {
		t.Errorf("ForeignIDFor(\"companies\") should reference companies(id): %s", create)
	}
}
