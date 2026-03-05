package featureflags

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func setupTest(t *testing.T) (*redis.Client, *miniredis.Miniredis, func()) {
	t.Helper()

	mr, err := miniredis.Run()
	require.NoError(t, err)

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	cleanup := func() {
		client.Close()
		mr.Close()
	}

	return client, mr, cleanup
}

func defaultConfig() Config {
	return Config{
		Enabled:   true,
		CacheTTL:  time.Second,
		KeyPrefix: "ff",
	}
}

func TestIsEnabled_GlobalFlag(t *testing.T) {
	client, _, cleanup := setupTest(t)
	defer cleanup()

	mgr := New(client, defaultConfig(), zap.NewNop())
	ctx := context.Background()

	// Flag does not exist yet - should return false
	assert.False(t, mgr.IsEnabled(ctx, "new-dashboard"))

	// Set flag to enabled
	err := mgr.SetFlag(ctx, "new-dashboard", true)
	require.NoError(t, err)

	assert.True(t, mgr.IsEnabled(ctx, "new-dashboard"))

	// Set flag to disabled
	err = mgr.SetFlag(ctx, "new-dashboard", false)
	require.NoError(t, err)

	// Evict cache so the new value is read from Redis
	mgr.cache.Delete(mgr.globalKey("new-dashboard"))
	assert.False(t, mgr.IsEnabled(ctx, "new-dashboard"))

	// Delete flag
	err = mgr.DeleteFlag(ctx, "new-dashboard")
	require.NoError(t, err)

	assert.False(t, mgr.IsEnabled(ctx, "new-dashboard"))
}

func TestIsEnabledForUser_Override(t *testing.T) {
	client, _, cleanup := setupTest(t)
	defer cleanup()

	mgr := New(client, defaultConfig(), zap.NewNop())
	ctx := context.Background()

	// Set global flag to disabled
	err := mgr.SetFlag(ctx, "beta-feature", false)
	require.NoError(t, err)

	// Global check should return false
	assert.False(t, mgr.IsEnabled(ctx, "beta-feature"))

	// Enable for specific user
	err = mgr.SetFlagForUser(ctx, "beta-feature", 42, true)
	require.NoError(t, err)

	// User 42 should see the feature enabled
	assert.True(t, mgr.IsEnabledForUser(ctx, "beta-feature", 42))

	// User 99 should fall back to global (disabled)
	assert.False(t, mgr.IsEnabledForUser(ctx, "beta-feature", 99))

	// Now enable global and disable for user 42
	err = mgr.SetFlag(ctx, "beta-feature", true)
	require.NoError(t, err)

	err = mgr.SetFlagForUser(ctx, "beta-feature", 42, false)
	require.NoError(t, err)

	// User 42 has explicit override to false
	assert.False(t, mgr.IsEnabledForUser(ctx, "beta-feature", 42))

	// User 99 falls back to global (now enabled)
	// Evict global cache entry so the updated value is fetched from Redis
	mgr.cache.Delete(mgr.globalKey("beta-feature"))
	assert.True(t, mgr.IsEnabledForUser(ctx, "beta-feature", 99))
}

func TestCacheTTL(t *testing.T) {
	client, _, cleanup := setupTest(t)
	defer cleanup()

	cfg := defaultConfig()
	cfg.CacheTTL = 50 * time.Millisecond

	mgr := New(client, cfg, zap.NewNop())

	// Use a controllable clock
	now := time.Now()
	mgr.nowFunc = func() time.Time { return now }

	ctx := context.Background()

	// Set flag and read to populate cache
	err := mgr.SetFlag(ctx, "cached-flag", true)
	require.NoError(t, err)
	assert.True(t, mgr.IsEnabled(ctx, "cached-flag"))

	// Change value directly in Redis (simulates external update)
	client.Set(ctx, "ff:cached-flag", "0", 0)

	// Cache should still return true (not expired)
	assert.True(t, mgr.IsEnabled(ctx, "cached-flag"))

	// Advance time past cache TTL
	now = now.Add(100 * time.Millisecond)

	// Now it should read the updated value from Redis
	assert.False(t, mgr.IsEnabled(ctx, "cached-flag"))
}

func TestRedisUnavailable_ReturnsFalse(t *testing.T) {
	client, mr, cleanup := setupTest(t)
	defer cleanup()

	mgr := New(client, defaultConfig(), zap.NewNop())
	ctx := context.Background()

	// Set a flag while Redis is up
	err := mgr.SetFlag(ctx, "some-flag", true)
	require.NoError(t, err)

	// Evict cache
	mgr.cache.Delete(mgr.globalKey("some-flag"))

	// Shut down Redis
	mr.Close()

	// Should return false (safe default) rather than panicking
	assert.False(t, mgr.IsEnabled(ctx, "some-flag"))
	assert.False(t, mgr.IsEnabledForUser(ctx, "some-flag", 1))
}

func TestDisabledConfig(t *testing.T) {
	client, _, cleanup := setupTest(t)
	defer cleanup()

	cfg := defaultConfig()
	cfg.Enabled = false

	mgr := New(client, cfg, zap.NewNop())
	ctx := context.Background()

	// Set a flag in Redis directly
	client.Set(ctx, "ff:active-flag", "1", 0)

	// All checks should return false when manager is disabled
	assert.False(t, mgr.IsEnabled(ctx, "active-flag"))
	assert.False(t, mgr.IsEnabledForUser(ctx, "active-flag", 1))
}

func TestGetAllFlags(t *testing.T) {
	client, _, cleanup := setupTest(t)
	defer cleanup()

	mgr := New(client, defaultConfig(), zap.NewNop())
	ctx := context.Background()

	// Set several flags
	require.NoError(t, mgr.SetFlag(ctx, "flag-a", true))
	require.NoError(t, mgr.SetFlag(ctx, "flag-b", false))
	require.NoError(t, mgr.SetFlag(ctx, "flag-c", true))

	// Set a user-level override (should not appear in GetAllFlags)
	require.NoError(t, mgr.SetFlagForUser(ctx, "flag-a", 1, false))

	flags, err := mgr.GetAllFlags(ctx)
	require.NoError(t, err)

	assert.Len(t, flags, 3)
	assert.True(t, flags["flag-a"])
	assert.False(t, flags["flag-b"])
	assert.True(t, flags["flag-c"])
}

func TestInterceptor_FromContext(t *testing.T) {
	client, _, cleanup := setupTest(t)
	defer cleanup()

	mgr := New(client, defaultConfig(), zap.NewNop())

	// Without interceptor, FromContext returns nil
	ctx := context.Background()
	assert.Nil(t, FromContext(ctx))

	// With value in context
	ctx = context.WithValue(ctx, managerCtxKey{}, mgr)
	retrieved := FromContext(ctx)
	assert.NotNil(t, retrieved)
	assert.Equal(t, mgr, retrieved)
}

func TestDefaultConfigValues(t *testing.T) {
	client, _, cleanup := setupTest(t)
	defer cleanup()

	// Zero-value config should get defaults
	mgr := New(client, Config{Enabled: true}, nil)

	assert.Equal(t, defaultCacheTTL, mgr.cfg.CacheTTL)
	assert.Equal(t, defaultKeyPrefix, mgr.cfg.KeyPrefix)
	assert.NotNil(t, mgr.logger)
}
