//go:build integration

package redis_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"orm"
	redis "orm-redis"
	"orm/drivertest"
)

// go test -tags integration ./...  with ORM_TEST_REDIS_DSN, e.g.
// redis://127.0.0.1:6379/15 (a dedicated database: the test clears it with FLUSHDB).
func TestDriver(t *testing.T) {
	dsn := os.Getenv("ORM_TEST_REDIS_DSN")
	if dsn == "" {
		t.Skip("ORM_TEST_REDIS_DSN is not set")
	}
	conn, err := redis.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	opts, err := goredis.ParseURL(dsn)
	if err != nil {
		t.Fatal(err)
	}
	raw := goredis.NewClient(opts)
	defer raw.Close()

	drivertest.NoSQL(t, conn, func(t *testing.T) {
		if err := raw.FlushDB(context.Background()).Err(); err != nil {
			t.Fatalf("flushdb: %v", err)
		}
	})
}

// KeyValueStore: a missing key returns orm.ErrNotFound, and TTL is honored.
func TestKeyValue(t *testing.T) {
	dsn := os.Getenv("ORM_TEST_REDIS_DSN")
	if dsn == "" {
		t.Skip("ORM_TEST_REDIS_DSN is not set")
	}
	conn, err := redis.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx := context.Background()

	if _, err := conn.Get(ctx, "drivertest:missing"); !errors.Is(err, orm.ErrNotFound) {
		t.Fatalf("missing key: want orm.ErrNotFound, got %v", err)
	}
	if err := conn.Set(ctx, "drivertest:k", "v", 1); err != nil {
		t.Fatal(err)
	}
	if v, err := conn.Get(ctx, "drivertest:k"); err != nil || v != "v" {
		t.Fatalf("Get = %q, %v", v, err)
	}
	if n, err := conn.Exists(ctx, "drivertest:k"); err != nil || n != 1 {
		t.Fatalf("Exists = %d, %v", n, err)
	}
	time.Sleep(1300 * time.Millisecond)
	if _, err := conn.Get(ctx, "drivertest:k"); !errors.Is(err, orm.ErrNotFound) {
		t.Fatalf("expired key: %v", err)
	}
	_ = conn.Set(ctx, "drivertest:d", "v", 0)
	if n, err := conn.Del(ctx, "drivertest:d"); err != nil || n != 1 {
		t.Fatalf("Del = %d, %v", n, err)
	}
}
