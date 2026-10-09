//go:build integration

package main

import (
	"context"
	"os"
	"reflect"
	"testing"

	"orm"
	postgres "orm-postgres"
)

// ORM_TEST_POSTGRES_DSN=postgres://... go test -tags integration .
func TestSeedIsReproducible(t *testing.T) {
	dsn := os.Getenv("ORM_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("ORM_TEST_POSTGRES_DSN is not set")
	}
	ctx := context.Background()
	conn, err := orm.New(ctx, postgres.Driver(dsn))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	exec := conn.(orm.QueryExecutor)

	run := func() []User {
		if err := CreateTable(ctx, conn.(orm.SQLExecutor)); err != nil {
			t.Fatal(err)
		}
		users, err := SeedUsers(ctx, exec, 7, 50)
		if err != nil {
			t.Fatal(err)
		}
		return users
	}
	first, second := run(), run()
	if !reflect.DeepEqual(first, second) {
		for i := range first {
			if !reflect.DeepEqual(first[i], second[i]) {
				t.Fatalf("same seed must give the same data:\n%+v\n%+v", first[i], second[i])
			}
		}
	}

	stored, err := orm.NewRepository[User](exec).All(ctx, orm.NewQuery("seed_users").OrderByAsc("id"))
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 50 {
		t.Fatalf("stored %d users", len(stored))
	}
	emails := map[string]bool{}
	for i, u := range stored {
		if u.Name == "" || u.City == "" || u.Bio == "" || u.Age < 18 || u.Age > 65 || emails[u.Email] {
			t.Fatalf("bad user %+v", u)
		}
		emails[u.Email] = true
		if !u.CreatedAt.Equal(second[i].CreatedAt) {
			t.Fatalf("created_at round trip: %v != %v", u.CreatedAt, second[i].CreatedAt)
		}
	}
	if _, err := conn.(orm.SQLExecutor).ExecContext(ctx, "DROP TABLE seed_users"); err != nil {
		t.Log(err)
	}
}
