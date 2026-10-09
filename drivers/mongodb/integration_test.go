//go:build integration

package mongodb

import (
	"context"
	"os"
	"testing"

	"orm/drivertest"
)

// go test -tags integration ./...  with ORM_TEST_MONGODB_DSN, e.g.
// mongodb://127.0.0.1:27017 (database ORM_TEST_MONGODB_DB, ormtest by default).
func TestDriver(t *testing.T) {
	dsn := os.Getenv("ORM_TEST_MONGODB_DSN")
	if dsn == "" {
		t.Skip("ORM_TEST_MONGODB_DSN is not set")
	}
	db := os.Getenv("ORM_TEST_MONGODB_DB")
	if db == "" {
		db = "ormtest"
	}
	ctx := context.Background()
	conn, err := Open(ctx, dsn, db)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	drivertest.NoSQL(t, conn, func(t *testing.T) {
		for _, coll := range []string{"drivertest_users", "counters"} {
			if err := conn.db.Collection(coll).Drop(ctx); err != nil {
				t.Fatalf("drop %s: %v", coll, err)
			}
		}
	})
}
