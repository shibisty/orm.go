//go:build integration

package postgres_test

import (
	"os"
	"testing"

	postgres "orm-postgres"
	"orm/drivertest"
)

// go test -tags integration ./...  with ORM_TEST_POSTGRES_DSN, e.g.
// postgres://postgres@127.0.0.1:5432/ormtest?sslmode=disable
func TestDriver(t *testing.T) {
	dsn := os.Getenv("ORM_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("ORM_TEST_POSTGRES_DSN is not set")
	}
	conn, err := postgres.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	drivertest.SQL(t, conn)
}
