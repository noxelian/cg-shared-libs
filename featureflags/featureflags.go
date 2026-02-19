package featureflags

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"go.uber.org/zap"
)

const (
	defaultCacheTTL  = 30 * time.Second
	defaultKeyPrefix = "ff"
)

// Config holds feature flag manager configuration.
type Config struct {
	Enabled   bool          `yaml:"enabled" env:"FEATURE_FLAGS_ENABLED"`
	CacheTTL  time.Duration `yaml:"cache_ttl" env:"FEATURE_FLAGS_CACHE_TTL"`
	KeyPrefix string        `yaml:"key_prefix" env:"FEATURE_FLAGS_KEY_PREFIX"`
}

// cacheEntry holds a cached flag value with its expiration time.
type cacheEntry struct {
	value     bool
	expiresAt time.Time
}

// Manager provides Redis-backed feature flags with in-memory caching.
type Manager struct {
	client    *redis.Client
	cfg       Config
	logger    *zap.Logger
	cache     sync.Map
	nowFunc   func() time.Time // for testing
}

// New creates a new feature flag Manager.
func New(client *redis.Client, cfg Config, logger *zap.Logger) *Manager {
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = defaultCacheTTL
	}
	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = defaultKeyPrefix
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Manager{
		client:  client,
		cfg:     cfg,
		logger:  logger,
		nowFunc: time.Now,
	}
}

// IsEnabled checks whether a global feature flag is enabled.
// Returns false if the manager is disabled or on Redis errors (safe default).
func (m *Manager) IsEnabled(ctx context.Context, flag string) bool {
	if !m.cfg.Enabled {
		return false
	}

	key := m.globalKey(flag)
	return m.resolve(ctx, key)
}

// SetFlag sets a global feature flag value.
func (m *Manager) SetFlag(ctx context.Context, flag string, enabled bool) error {
	key := m.globalKey(flag)
	val := boolToStr(enabled)

	if err := m.client.Set(ctx, key, val, 0).Err(); err != nil {
		return fmt.Errorf("featureflags: set %q: %w", flag, err)
	}

	m.cacheSet(key, enabled)
	return nil
}

// DeleteFlag removes a global feature flag.
func (m *Manager) DeleteFlag(ctx context.Context, flag string) error {
	key := m.globalKey(flag)

	if err := m.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("featureflags: delete %q: %w", flag, err)
	}

	m.cache.Delete(key)
	return nil
}

// GetAllFlags returns all global feature flags and their values.
// It scans Redis keys matching the prefix and excludes per-user overrides.
func (m *Manager) GetAllFlags(ctx context.Context) (map[string]bool, error) {
	pattern := fmt.Sprintf("%s:*", m.cfg.KeyPrefix)
	flags := make(map[string]bool)

	iter := m.client.Scan(ctx, 0, pattern, 0).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()

		// Skip per-user override keys (contain ":u:")
		if isUserKey(key) {
			continue
		}

		val, err := m.client.Get(ctx, key).Result()
		if err != nil {
			m.logger.Warn("featureflags: failed to get flag",
				zap.String("key", key),
				zap.Error(err),
			)
			continue
		}

		flagName := extractFlagName(key, m.cfg.KeyPrefix)
		flags[flagName] = val == "1"
	}

	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("featureflags: scan: %w", err)
	}

	return flags, nil
}

// IsEnabledForUser checks whether a feature flag is enabled for a specific user.
// Per-user overrides take precedence over the global flag value.
// Returns false if the manager is disabled or on Redis errors (safe default).
func (m *Manager) IsEnabledForUser(ctx context.Context, flag string, userID int64) bool {
	if !m.cfg.Enabled {
		return false
	}

	// Check per-user override first
	userKey := m.userKey(flag, userID)
	if val, ok := m.cacheGet(userKey); ok {
		return val
	}

	userVal, err := m.client.Get(ctx, userKey).Result()
	if err == nil {
		enabled := userVal == "1"
		m.cacheSet(userKey, enabled)
		return enabled
	}

	if err != redis.Nil {
		m.logger.Warn("featureflags: redis error reading user flag, falling back to global",
			zap.String("flag", flag),
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
	}

	// Fall back to global flag
	return m.IsEnabled(ctx, flag)
}

// SetFlagForUser sets a per-user feature flag override.
func (m *Manager) SetFlagForUser(ctx context.Context, flag string, userID int64, enabled bool) error {
	key := m.userKey(flag, userID)
	val := boolToStr(enabled)

	if err := m.client.Set(ctx, key, val, 0).Err(); err != nil {
		return fmt.Errorf("featureflags: set %q for user %d: %w", flag, userID, err)
	}

	m.cacheSet(key, enabled)
	return nil
}

// resolve looks up a flag value from cache or Redis.
// Returns false on any error (safe default).
func (m *Manager) resolve(ctx context.Context, key string) bool {
	if val, ok := m.cacheGet(key); ok {
		return val
	}

	val, err := m.client.Get(ctx, key).Result()
	if err != nil {
		if err != redis.Nil {
			m.logger.Warn("featureflags: redis error, returning false",
				zap.String("key", key),
				zap.Error(err),
			)
		}
		return false
	}

	enabled := val == "1"
	m.cacheSet(key, enabled)
	return enabled
}

// cacheGet returns the cached value if it exists and has not expired.
func (m *Manager) cacheGet(key string) (bool, bool) {
	raw, ok := m.cache.Load(key)
	if !ok {
		return false, false
	}

	entry := raw.(cacheEntry)
	if m.nowFunc().After(entry.expiresAt) {
		m.cache.Delete(key)
		return false, false
	}

	return entry.value, true
}

// cacheSet stores a value in the in-memory cache with TTL.
func (m *Manager) cacheSet(key string, value bool) {
	m.cache.Store(key, cacheEntry{
		value:     value,
		expiresAt: m.nowFunc().Add(m.cfg.CacheTTL),
	})
}

// globalKey returns the Redis key for a global flag.
func (m *Manager) globalKey(flag string) string {
	return fmt.Sprintf("%s:%s", m.cfg.KeyPrefix, flag)
}

// userKey returns the Redis key for a per-user flag override.
func (m *Manager) userKey(flag string, userID int64) string {
	return fmt.Sprintf("%s:%s:u:%d", m.cfg.KeyPrefix, flag, userID)
}

// boolToStr converts a bool to "1" or "0" for Redis storage.
func boolToStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// isUserKey checks whether a Redis key is a per-user override key.
func isUserKey(key string) bool {
	// Per-user keys contain ":u:" segment
	for i := 0; i < len(key)-2; i++ {
		if key[i] == ':' && key[i+1] == 'u' && key[i+2] == ':' {
			return true
		}
	}
	return false
}

// extractFlagName strips the prefix from a Redis key to get the flag name.
func extractFlagName(key, prefix string) string {
	// key format: "{prefix}:{flag}"
	if len(key) > len(prefix)+1 {
		return key[len(prefix)+1:]
	}
	return key
}
