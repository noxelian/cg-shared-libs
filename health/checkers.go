package health

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// DatabaseChecker checks database connectivity
type DatabaseChecker struct {
	db     *sql.DB
	name   string
	timeout time.Duration
}

// NewDatabaseChecker creates a new database checker
func NewDatabaseChecker(db *sql.DB, name string) *DatabaseChecker {
	return &DatabaseChecker{
		db:      db,
		name:    name,
		timeout: 2 * time.Second,
	}
}

// Name returns checker name
func (c *DatabaseChecker) Name() string {
	return c.name
}

// Check performs database health check
func (c *DatabaseChecker) Check(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	if err := c.db.PingContext(ctx); err != nil {
		return fmt.Errorf("database ping failed: %w", err)
	}
	return nil
}

// RedisChecker checks Redis connectivity
type RedisChecker struct {
	client  redis.Cmdable
	name    string
	timeout time.Duration
}

// NewRedisChecker creates a new Redis checker
func NewRedisChecker(client redis.Cmdable, name string) *RedisChecker {
	return &RedisChecker{
		client:  client,
		name:    name,
		timeout: 2 * time.Second,
	}
}

// Name returns checker name
func (c *RedisChecker) Name() string {
	return c.name
}

// Check performs Redis health check
func (c *RedisChecker) Check(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	if err := c.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis ping failed: %w", err)
	}
	return nil
}

// CustomChecker allows custom health checks
type CustomChecker struct {
	name    string
	checkFn func(ctx context.Context) error
}

// NewCustomChecker creates a new custom checker
func NewCustomChecker(name string, checkFn func(ctx context.Context) error) *CustomChecker {
	return &CustomChecker{
		name:    name,
		checkFn: checkFn,
	}
}

// Name returns checker name
func (c *CustomChecker) Name() string {
	return c.name
}

// Check performs custom health check
func (c *CustomChecker) Check(ctx context.Context) error {
	return c.checkFn(ctx)
}
