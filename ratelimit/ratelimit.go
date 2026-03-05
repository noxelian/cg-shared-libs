package ratelimit

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"gitlab.com/xakpro/cg-shared-libs/logger"
	"go.uber.org/zap"
)

// Config holds rate limiter configuration
type Config struct {
	Limit  int           `yaml:"limit" env:"RATE_LIMIT" env-default:"100"`
	Window time.Duration `yaml:"window" env:"RATE_LIMIT_WINDOW" env-default:"60s"`
}

// Result represents the result of a rate limit check
type Result struct {
	Allowed    bool
	Remaining  int64
	ResetAfter time.Duration
	Limit      int
}

// Limiter implements sliding window rate limiting using Redis
type Limiter struct {
	client *redis.Client
	config Config
	prefix string
}

// New creates a new rate limiter
func New(client *redis.Client, cfg Config, prefix string) *Limiter {
	if prefix == "" {
		prefix = "ratelimit"
	}
	return &Limiter{
		client: client,
		config: cfg,
		prefix: prefix,
	}
}

// Allow checks if a request is allowed under the rate limit
// Uses sliding window algorithm with Redis sorted sets
func (l *Limiter) Allow(ctx context.Context, key string) (Result, error) {
	return l.AllowN(ctx, key, 1)
}

// isTransactionRecoverableError returns true if the error is EXECABORT or WRONGTYPE.
// Such errors can occur when the key exists with a different type (e.g. string from
// another service or old code). Deleting the key and retrying once fixes the issue.
func isTransactionRecoverableError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "EXECABORT") || strings.Contains(s, "WRONGTYPE")
}

// isReadOnlyError returns true if the error is Redis READONLY (e.g. write against a replica).
// Rate limiting requires writes; on a replica we fail open (allow request) to avoid blocking traffic.
func isReadOnlyError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "READONLY")
}

// AllowN checks if N requests are allowed under the rate limit
func (l *Limiter) AllowN(ctx context.Context, key string, n int) (Result, error) {
	result, err := l.allowN(ctx, key, n)
	if err == nil {
		return result, nil
	}
	// Fail open when connected to a read replica so traffic is not blocked
	if isReadOnlyError(err) {
		logger.Debug("rate limit skipped (Redis read-only replica)", zap.String("key", key))
		return Result{Allowed: true, Limit: l.config.Limit, Remaining: int64(l.config.Limit), ResetAfter: l.config.Window}, nil
	}
	if !isTransactionRecoverableError(err) {
		return Result{}, err
	}
	// Key may exist with wrong type (e.g. string). Delete and retry once.
	// Skip recovery on read-only (replica): Del would fail and retry would hit same error.
	fullKey := fmt.Sprintf("%s:%s", l.prefix, key)
	if delErr := l.client.Del(ctx, fullKey).Err(); delErr != nil {
		if isReadOnlyError(delErr) {
			logger.Debug("rate limit recovery skipped (Redis read-only replica)", zap.String("key", key))
			return Result{Allowed: true, Limit: l.config.Limit, Remaining: int64(l.config.Limit), ResetAfter: l.config.Window}, nil
		}
		return Result{}, fmt.Errorf("rate limit check: %w", err)
	}
	logger.Debug("rate limit key had wrong type, deleted and retrying",
		zap.String("key", key),
		zap.String("full_key", fullKey),
	)
	result, err = l.allowN(ctx, key, n)
	return result, err
}

// allowN runs the rate limit transaction once (ZRemRangeByScore, ZCard, ZAdd, Expire).
func (l *Limiter) allowN(ctx context.Context, key string, n int) (Result, error) {
	now := time.Now()
	windowStart := now.Add(-l.config.Window)
	fullKey := fmt.Sprintf("%s:%s", l.prefix, key)

	// Use Redis transaction to ensure atomicity
	pipe := l.client.TxPipeline()

	// Remove old entries outside the window
	pipe.ZRemRangeByScore(ctx, fullKey, "0", fmt.Sprintf("%d", windowStart.UnixNano()))

	// Count current requests in window
	countCmd := pipe.ZCard(ctx, fullKey)

	// Add new request(s)
	members := make([]redis.Z, n)
	for i := 0; i < n; i++ {
		members[i] = redis.Z{
			Score:  float64(now.UnixNano() + int64(i)),
			Member: fmt.Sprintf("%d-%d", now.UnixNano(), i),
		}
	}
	pipe.ZAdd(ctx, fullKey, members...)

	// Set expiration on the key
	pipe.Expire(ctx, fullKey, l.config.Window)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("rate limit check: %w", err)
	}

	currentCount := countCmd.Val()
	allowed := currentCount+int64(n) <= int64(l.config.Limit)
	remaining := int64(l.config.Limit) - currentCount - int64(n)
	if remaining < 0 {
		remaining = 0
	}

	result := Result{
		Allowed:    allowed,
		Remaining:  remaining,
		ResetAfter: l.config.Window,
		Limit:      l.config.Limit,
	}

	if !allowed {
		// Remove the requests we just added since they're not allowed
		rollbackPipe := l.client.TxPipeline()
		for i := 0; i < n; i++ {
			rollbackPipe.ZRem(ctx, fullKey, fmt.Sprintf("%d-%d", now.UnixNano(), i))
		}
		_, _ = rollbackPipe.Exec(ctx)

		logger.Debug("rate limit exceeded",
			zap.String("key", key),
			zap.Int64("current_count", currentCount),
			zap.Int("limit", l.config.Limit),
		)
	}

	return result, nil
}

// Reset resets the rate limit for a key
func (l *Limiter) Reset(ctx context.Context, key string) error {
	fullKey := fmt.Sprintf("%s:%s", l.prefix, key)
	return l.client.Del(ctx, fullKey).Err()
}

// GetCount returns the current request count for a key
func (l *Limiter) GetCount(ctx context.Context, key string) (int64, error) {
	fullKey := fmt.Sprintf("%s:%s", l.prefix, key)
	windowStart := time.Now().Add(-l.config.Window)

	// Remove old entries and get count
	pipe := l.client.TxPipeline()
	pipe.ZRemRangeByScore(ctx, fullKey, "0", fmt.Sprintf("%d", windowStart.UnixNano()))
	countCmd := pipe.ZCard(ctx, fullKey)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("get count: %w", err)
	}

	return countCmd.Val(), nil
}

// MultiLimiter provides rate limiting with multiple tiers
type MultiLimiter struct {
	limiters map[string]*Limiter
}

// NewMultiLimiter creates a limiter with multiple rate limit tiers
func NewMultiLimiter(client *redis.Client, configs map[string]Config) *MultiLimiter {
	limiters := make(map[string]*Limiter, len(configs))
	for name, cfg := range configs {
		limiters[name] = New(client, cfg, fmt.Sprintf("ratelimit:%s", name))
	}
	return &MultiLimiter{limiters: limiters}
}

// Allow checks rate limit for a specific tier
func (m *MultiLimiter) Allow(ctx context.Context, tier, key string) (Result, error) {
	limiter, ok := m.limiters[tier]
	if !ok {
		return Result{Allowed: true, Limit: -1}, nil // No limit for unknown tiers
	}
	return limiter.Allow(ctx, key)
}

// DefaultTiers returns default rate limit configurations
func DefaultTiers() map[string]Config {
	return map[string]Config{
		"auth": {
			Limit:  10,
			Window: time.Minute,
		},
		"payment": {
			Limit:  10,
			Window: time.Minute,
		},
		"api": {
			Limit:  100,
			Window: time.Minute,
		},
		"search": {
			Limit:  30,
			Window: time.Minute,
		},
		"websocket": {
			Limit:  5,
			Window: time.Minute,
		},
	}
}
