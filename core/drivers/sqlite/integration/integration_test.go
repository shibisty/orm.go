//go:build integration

package integration_test

import (
	"os"
	"testing"

	sqlite "orm-sqlite"
	"orm/drivertest"

	// The driver deliberately does not pick a low-level implementation itself (see the package doc);
	// the test uses pure Go without cgo.
	_ "modernc.org/sqlite"
)

// cd integration && gtr install && gtr run test:integration  with ORM_TEST_SQLITE_DSN, e.g.
// file:test.db?_pragma=foreign_keys(1)  (or :memory:)
func TestDriver(t *testing.T) {
	dsn := os.Getenv("ORM_TEST_SQLITE_DSN")
	if dsn == "" {
		t.Skip("ORM_TEST_SQLITE_DSN is not set")
	}
	conn, err := sqlite.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	drivertest.SQL(t, conn)
}
