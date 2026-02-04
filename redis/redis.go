package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"gitlab.com/xakpro/cg-shared-libs/logger"
	"go.uber.org/zap"
)

// Config holds Redis connection configuration
type Config struct {
	Host         string        `yaml:"host" env:"REDIS_HOST" env-default:"localhost"`
	Port         int           `yaml:"port" env:"REDIS_PORT" env-default:"6379"`
	Password     string        `yaml:"password" env:"REDIS_PASSWORD"`
	DB           int           `yaml:"db" env:"REDIS_DB" env-default:"0"`
	PoolSize     int           `yaml:"pool_size" env:"REDIS_POOL_SIZE" env-default:"100"`
	MinIdleConns int           `yaml:"min_idle_conns" env:"REDIS_MIN_IDLE_CONNS" env-default:"10"`
	DialTimeout  time.Duration `yaml:"dial_timeout" env:"REDIS_DIAL_TIMEOUT" env-default:"5s"`
	ReadTimeout  time.Duration `yaml:"read_timeout" env:"REDIS_READ_TIMEOUT" env-default:"3s"`
	WriteTimeout time.Duration `yaml:"write_timeout" env:"REDIS_WRITE_TIMEOUT" env-default:"3s"`
}

// Addr returns Redis address
func (c *Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// Client wraps redis.Client with additional functionality
type Client struct {
	*redis.Client
}

// New creates a new Redis client
func New(ctx context.Context, cfg Config) (*Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:         cfg.Addr(),
		Password:     cfg.Password,
		DB:           cfg.DB,
		PoolSize:     cfg.PoolSize,
		MinIdleConns: cfg.MinIdleConns,
		DialTimeout:  cfg.DialTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	})

	// Test connection
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	logger.Info("Redis connected",
		zap.String("addr", cfg.Addr()),
		zap.Int("db", cfg.DB),
	)

	return &Client{Client: client}, nil
}

// Close closes the Redis connection
func (c *Client) Close() error {
	if c.Client != nil {
		logger.Info("Redis connection closed")
		return c.Client.Close()
	}
	return nil
}

// SetJSON sets a value as JSON with expiration
func (c *Client) SetJSON(ctx context.Context, key string, value any, expiration time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal value: %w", err)
	}
	return c.Set(ctx, key, data, expiration).Err()
}

// GetJSON gets a value and unmarshals from JSON
func (c *Client) GetJSON(ctx context.Context, key string, dest any) error {
	data, err := c.Get(ctx, key).Bytes()
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dest)
}

// SetJSONNX sets a value as JSON only if key doesn't exist
func (c *Client) SetJSONNX(ctx context.Context, key string, value any, expiration time.Duration) (bool, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return false, fmt.Errorf("marshal value: %w", err)
	}
	return c.SetNX(ctx, key, data, expiration).Result()
}

// Exists checks if key exists
func (c *Client) KeyExists(ctx context.Context, key string) (bool, error) {
	n, err := c.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// DeletePattern deletes all keys matching pattern
func (c *Client) DeletePattern(ctx context.Context, pattern string) error {
	iter := c.Scan(ctx, 0, pattern, 100).Iterator()
	for iter.Next(ctx) {
		if err := c.Del(ctx, iter.Val()).Err(); err != nil {
			return err
		}
	}
	return iter.Err()
}

// Counter operations for counter-service

// IncrCounter increments a hash field atomically
func (c *Client) IncrCounter(ctx context.Context, key, field string, delta int64) (int64, error) {
	return c.HIncrBy(ctx, key, field, delta).Result()
}

// GetCounters gets all counters for a key
func (c *Client) GetCounters(ctx context.Context, key string) (map[string]int64, error) {
	result, err := c.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}

	counters := make(map[string]int64, len(result))
	for k, v := range result {
		var val int64
		fmt.Sscanf(v, "%d", &val)
		counters[k] = val
	}
	return counters, nil
}

// SetCounter sets a specific counter value
func (c *Client) SetCounter(ctx context.Context, key, field string, value int64) error {
	return c.HSet(ctx, key, field, value).Err()
}

// GetCounter gets a specific counter value
func (c *Client) GetCounter(ctx context.Context, key, field string) (int64, error) {
	return c.HGet(ctx, key, field).Int64()
}

// DeleteCounter deletes a hash field
func (c *Client) DeleteCounter(ctx context.Context, key, field string) error {
	return c.HDel(ctx, key, field).Err()
}

// SetCounterExpire sets expiration on a counter key
func (c *Client) SetCounterExpire(ctx context.Context, key string, expiration time.Duration) error {
	return c.Expire(ctx, key, expiration).Err()
}

// Lock operations for distributed locking

// Lock acquires a distributed lock
func (c *Client) Lock(ctx context.Context, key string, value string, expiration time.Duration) (bool, error) {
	return c.SetNX(ctx, key, value, expiration).Result()
}

// Unlock releases a distributed lock
func (c *Client) Unlock(ctx context.Context, key string, value string) error {
	script := redis.NewScript(`
		if redis.call("get", KEYS[1]) == ARGV[1] then
			return redis.call("del", KEYS[1])
		else
			return 0
		end
	`)
	return script.Run(ctx, c.Client, []string{key}, value).Err()
}

// IsNil checks if error is redis.Nil
func IsNil(err error) bool {
	return errors.Is(err, redis.Nil)
}

