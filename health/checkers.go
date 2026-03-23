package health

import (
	"context"
	"database/sql"
	"fmt"
	"net"
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

// KafkaChecker checks Kafka broker reachability via TCP dial to brokers[0].
// Uses net.Dialer directly — no kafka-go dependency needed for a connectivity probe.
type KafkaChecker struct {
	brokers []string
	name    string
	timeout time.Duration
}

// NewKafkaChecker creates a KafkaChecker that dials brokers[0] with a 3s timeout.
// Register on /readyz only for services where Kafka is a hard dependency
// (cg-ai, cg-orders, cg-workshop). Do NOT register on emit-only services.
func NewKafkaChecker(brokers []string, name string) *KafkaChecker {
	return &KafkaChecker{
		brokers: brokers,
		name:    name,
		timeout: 3 * time.Second,
	}
}

// Name returns checker name
func (c *KafkaChecker) Name() string { return c.name }

// Check dials brokers[0] to verify Kafka is reachable.
// Returns nil when brokers list is empty (Kafka not configured).
func (c *KafkaChecker) Check(ctx context.Context) error {
	if len(c.brokers) == 0 {
		return nil // Kafka not configured — skip check
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", c.brokers[0])
	if err != nil {
		return fmt.Errorf("kafka broker unreachable: %w", err)
	}
	_ = conn.Close()
	return nil
}
