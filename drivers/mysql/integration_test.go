//go:build integration

package mysql_test

import (
	"os"
	"testing"

	mysql "orm-mysql"
	"orm/drivertest"
)

// go test -tags integration ./...  with ORM_TEST_MYSQL_DSN, e.g.
// root:@tcp(127.0.0.1:3306)/ormtest
func TestDriver(t *testing.T) {
	dsn := os.Getenv("ORM_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("ORM_TEST_MYSQL_DSN is not set")
	}
	conn, err := mysql.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	drivertest.SQL(t, conn)
}
