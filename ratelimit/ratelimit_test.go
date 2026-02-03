package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
)

func setupTestRedis(t *testing.T) (*redis.Client, func()) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	cleanup := func() {
		client.Close()
		mr.Close()
	}

	return client, cleanup
}

func TestLimiter_Allow(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	cfg := Config{
		Limit:  3,
		Window: time.Minute,
	}

	limiter := New(client, cfg, "test")
	ctx := context.Background()

	// First 3 requests should be allowed
	for i := 0; i < 3; i++ {
		result, err := limiter.Allow(ctx, "user:1")
		if err != nil {
			t.Fatalf("Allow failed: %v", err)
		}
		if !result.Allowed {
			t.Errorf("request %d should be allowed", i+1)
		}
	}

	// 4th request should be denied
	result, err := limiter.Allow(ctx, "user:1")
	if err != nil {
		t.Fatalf("Allow failed: %v", err)
	}
	if result.Allowed {
		t.Error("4th request should be denied")
	}
	if result.Remaining != 0 {
		t.Errorf("remaining should be 0, got %d", result.Remaining)
	}
}

func TestLimiter_DifferentKeys(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	cfg := Config{
		Limit:  2,
		Window: time.Minute,
	}

	limiter := New(client, cfg, "test")
	ctx := context.Background()

	// User 1 makes 2 requests
	for i := 0; i < 2; i++ {
		result, err := limiter.Allow(ctx, "user:1")
		if err != nil {
			t.Fatalf("Allow failed: %v", err)
		}
		if !result.Allowed {
			t.Errorf("user1 request %d should be allowed", i+1)
		}
	}

	// User 2 should still be able to make requests
	result, err := limiter.Allow(ctx, "user:2")
	if err != nil {
		t.Fatalf("Allow failed: %v", err)
	}
	if !result.Allowed {
		t.Error("user2 first request should be allowed")
	}
}

func TestLimiter_Reset(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	cfg := Config{
		Limit:  1,
		Window: time.Minute,
	}

	limiter := New(client, cfg, "test")
	ctx := context.Background()

	// Make one request
	result, _ := limiter.Allow(ctx, "user:1")
	if !result.Allowed {
		t.Error("first request should be allowed")
	}

	// Second should be denied
	result, _ = limiter.Allow(ctx, "user:1")
	if result.Allowed {
		t.Error("second request should be denied")
	}

	// Reset the limit
	err := limiter.Reset(ctx, "user:1")
	if err != nil {
		t.Fatalf("Reset failed: %v", err)
	}

	// Now should be allowed again
	result, _ = limiter.Allow(ctx, "user:1")
	if !result.Allowed {
		t.Error("request after reset should be allowed")
	}
}

func TestLimiter_GetCount(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	cfg := Config{
		Limit:  10,
		Window: time.Minute,
	}

	limiter := New(client, cfg, "test")
	ctx := context.Background()

	// Initial count should be 0
	count, err := limiter.GetCount(ctx, "user:1")
	if err != nil {
		t.Fatalf("GetCount failed: %v", err)
	}
	if count != 0 {
		t.Errorf("initial count should be 0, got %d", count)
	}

	// Make 5 requests
	for i := 0; i < 5; i++ {
		limiter.Allow(ctx, "user:1")
	}

	count, err = limiter.GetCount(ctx, "user:1")
	if err != nil {
		t.Fatalf("GetCount failed: %v", err)
	}
	if count != 5 {
		t.Errorf("count should be 5, got %d", count)
	}
}

func TestMultiLimiter_DifferentTiers(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	configs := map[string]Config{
		"auth": {Limit: 2, Window: time.Minute},
		"api":  {Limit: 10, Window: time.Minute},
	}

	limiter := NewMultiLimiter(client, configs)
	ctx := context.Background()

	// Auth tier - 2 requests allowed
	for i := 0; i < 2; i++ {
		result, err := limiter.Allow(ctx, "auth", "ip:1.1.1.1")
		if err != nil {
			t.Fatalf("Allow failed: %v", err)
		}
		if !result.Allowed {
			t.Errorf("auth request %d should be allowed", i+1)
		}
	}

	// 3rd auth request should be denied
	result, _ := limiter.Allow(ctx, "auth", "ip:1.1.1.1")
	if result.Allowed {
		t.Error("3rd auth request should be denied")
	}

	// API tier should still allow requests
	result, err := limiter.Allow(ctx, "api", "ip:1.1.1.1")
	if err != nil {
		t.Fatalf("Allow failed: %v", err)
	}
	if !result.Allowed {
		t.Error("api request should be allowed")
	}
}

func TestMultiLimiter_UnknownTier(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	configs := map[string]Config{
		"auth": {Limit: 2, Window: time.Minute},
	}

	limiter := NewMultiLimiter(client, configs)
	ctx := context.Background()

	// Unknown tier should allow all requests
	result, err := limiter.Allow(ctx, "unknown", "key")
	if err != nil {
		t.Fatalf("Allow failed: %v", err)
	}
	if !result.Allowed {
		t.Error("unknown tier should allow requests")
	}
	if result.Limit != -1 {
		t.Errorf("unknown tier limit should be -1, got %d", result.Limit)
	}
}

func TestDefaultTiers(t *testing.T) {
	tiers := DefaultTiers()

	expectedTiers := []string{"auth", "payment", "api", "search"}
	for _, tier := range expectedTiers {
		if _, ok := tiers[tier]; !ok {
			t.Errorf("expected tier %q not found", tier)
		}
	}

	// Check auth tier limits
	if tiers["auth"].Limit != 5 {
		t.Errorf("auth limit should be 5, got %d", tiers["auth"].Limit)
	}

	// Check api tier limits
	if tiers["api"].Limit != 100 {
		t.Errorf("api limit should be 100, got %d", tiers["api"].Limit)
	}
}
