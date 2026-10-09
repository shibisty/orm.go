// Package redis implements orm.KeyValueStore (not the SQL or document interface:
// Redis has its own operation semantics).
//
// It depends on github.com/redis/go-redis/v9, which gtr installs with it.
package redis

import (
	"context"
	"errors"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"orm"
)

// redisDriver is the orm.Driver implementation for Redis.
type redisDriver struct{ dsn string }

// Driver returns an orm.Driver for explicit injection:
//
//	conn, err := orm.New(ctx, redis.Driver("redis://localhost:6379/0"))
func Driver(dsn string) orm.Driver { return redisDriver{dsn: dsn} }

func (d redisDriver) Open(ctx context.Context) (orm.Connection, error) { return Open(d.dsn) }

// Conn implements orm.KeyValueStore.
type Conn struct {
	client *goredis.Client
}

// Open connects to Redis. dsn is, for example, "redis://localhost:6379/0".
func Open(dsn string) (*Conn, error) {
	opt, err := goredis.ParseURL(dsn)
	if err != nil {
		return nil, err
	}
	return &Conn{client: goredis.NewClient(opt)}, nil
}

func (c *Conn) Driver() string { return "redis" }

func (c *Conn) Ping(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

func (c *Conn) Close() error {
	return c.client.Close()
}

// Get returns orm.ErrNotFound if the key does not exist, so calling code does
// not depend on go-redis (redis.Nil).
func (c *Conn) Get(ctx context.Context, key string) (string, error) {
	v, err := c.client.Get(ctx, key).Result()
	if errors.Is(err, goredis.Nil) {
		return "", orm.ErrNotFound
	}
	return v, err
}

func (c *Conn) Set(ctx context.Context, key string, value any, ttlSeconds int) error {
	var ttl time.Duration
	if ttlSeconds > 0 {
		ttl = time.Duration(ttlSeconds) * time.Second
	}
	return c.client.Set(ctx, key, value, ttl).Err()
}

func (c *Conn) Del(ctx context.Context, keys ...string) (int64, error) {
	return c.client.Del(ctx, keys...).Result()
}

func (c *Conn) Exists(ctx context.Context, keys ...string) (int64, error) {
	return c.client.Exists(ctx, keys...).Result()
}

// Compile-time check that Conn implements orm.KeyValueStore.
var _ orm.KeyValueStore = (*Conn)(nil)
