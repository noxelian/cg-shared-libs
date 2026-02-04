package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"gitlab.com/xakpro/cg-shared-libs/ratelimit"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func setupTestRedisClient(t *testing.T) (*redis.Client, func()) {
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

func TestRateLimitMiddleware(t *testing.T) {
	client, cleanup := setupTestRedisClient(t)
	defer cleanup()

	configs := map[string]ratelimit.Config{
		"api": {Limit: 3, Window: time.Minute},
	}
	limiter := ratelimit.NewMultiLimiter(client, configs)

	router := gin.New()
	router.Use(RateLimitMiddleware(limiter, "api"))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// First 3 requests should succeed
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = "1.1.1.1:12345"
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i+1, w.Code)
		}

		// Check rate limit headers
		if w.Header().Get("X-RateLimit-Limit") != "3" {
			t.Errorf("expected X-RateLimit-Limit=3, got %s", w.Header().Get("X-RateLimit-Limit"))
		}
	}

	// 4th request should be rate limited
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "1.1.1.1:12345"
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w.Code)
	}

	// Check Retry-After header
	if w.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header")
	}
}

func TestRateLimitMiddleware_DifferentUsers(t *testing.T) {
	client, cleanup := setupTestRedisClient(t)
	defer cleanup()

	configs := map[string]ratelimit.Config{
		"api": {Limit: 2, Window: time.Minute},
	}
	limiter := ratelimit.NewMultiLimiter(client, configs)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		// Simulate authenticated user
		if userID := c.GetHeader("X-User-ID"); userID != "" {
			c.Set("user_id", userID)
		}
		c.Next()
	})
	router.Use(RateLimitMiddleware(limiter, "api"))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// User 1 makes 2 requests
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("X-User-ID", "user1")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("user1 request %d failed: %d", i+1, w.Code)
		}
	}

	// User 2 should still be able to make requests
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-User-ID", "user2")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("user2 request failed: %d", w.Code)
	}
}

func TestIPRateLimitMiddleware(t *testing.T) {
	client, cleanup := setupTestRedisClient(t)
	defer cleanup()

	cfg := ratelimit.Config{
		Limit:  2,
		Window: time.Minute,
	}
	limiter := ratelimit.New(client, cfg, "test")

	router := gin.New()
	router.Use(IPRateLimitMiddleware(limiter))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// 2 requests should succeed
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = "1.1.1.1:12345"
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("request %d failed: %d", i+1, w.Code)
		}
	}

	// 3rd request should be blocked
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "1.1.1.1:12345"
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w.Code)
	}
}

func TestAuthTierUsesIPKey(t *testing.T) {
	client, cleanup := setupTestRedisClient(t)
	defer cleanup()

	configs := map[string]ratelimit.Config{
		"auth": {Limit: 2, Window: time.Minute},
	}
	limiter := ratelimit.NewMultiLimiter(client, configs)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		// Even with user_id set, auth tier should use IP
		c.Set("user_id", int64(123))
		c.Next()
	})
	router.Use(RateLimitMiddleware(limiter, "auth"))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// Same IP should be rate limited regardless of user_id
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = "1.1.1.1:12345"
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("request %d failed: %d", i+1, w.Code)
		}
	}

	// 3rd request from same IP should be blocked
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "1.1.1.1:12345"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w.Code)
	}
}

func TestWebSocketRateLimitMiddleware(t *testing.T) {
	client, cleanup := setupTestRedisClient(t)
	defer cleanup()

	configs := map[string]ratelimit.Config{
		"websocket": {Limit: 2, Window: time.Minute},
	}
	limiter := ratelimit.NewMultiLimiter(client, configs)

	extractUserID := func(r *http.Request) (int64, error) {
		return 123, nil
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	wrappedHandler := WebSocketRateLimitMiddleware(limiter, extractUserID)(handler)

	// First 2 requests should succeed
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/ws", nil)
		w := httptest.NewRecorder()
		wrappedHandler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i+1, w.Code)
		}
	}

	// 3rd request should be rate limited
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	w := httptest.NewRecorder()
	wrappedHandler.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w.Code)
	}

	// Check headers
	if w.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header")
	}
}

func TestGetClientIP(t *testing.T) {
	tests := []struct {
		name       string
		xff        string
		xri        string
		remoteAddr string
		expected   string
	}{
		{
			name:       "X-Forwarded-For single IP",
			xff:        "1.1.1.1",
			remoteAddr: "127.0.0.1:12345",
			expected:   "1.1.1.1",
		},
		{
			name:       "X-Forwarded-For multiple IPs",
			xff:        "1.1.1.1, 2.2.2.2, 3.3.3.3",
			remoteAddr: "127.0.0.1:12345",
			expected:   "1.1.1.1",
		},
		{
			name:       "X-Real-IP",
			xri:        "1.1.1.1",
			remoteAddr: "127.0.0.1:12345",
			expected:   "1.1.1.1",
		},
		{
			name:       "fallback to remote addr",
			remoteAddr: "1.1.1.1:12345",
			expected:   "1.1.1.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/test", nil)
			c.Request.RemoteAddr = tt.remoteAddr

			if tt.xff != "" {
				c.Request.Header.Set("X-Forwarded-For", tt.xff)
			}
			if tt.xri != "" {
				c.Request.Header.Set("X-Real-IP", tt.xri)
			}

			result := GetClientIP(c)
			if result != tt.expected {
				t.Errorf("GetClientIP() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestGetClientIPFromRequest(t *testing.T) {
	tests := []struct {
		name       string
		xff        string
		xri        string
		remoteAddr string
		expected   string
	}{
		{
			name:       "X-Forwarded-For",
			xff:        "1.1.1.1",
			remoteAddr: "127.0.0.1:12345",
			expected:   "1.1.1.1",
		},
		{
			name:       "X-Real-IP",
			xri:        "2.2.2.2",
			remoteAddr: "127.0.0.1:12345",
			expected:   "2.2.2.2",
		},
		{
			name:       "RemoteAddr with port",
			remoteAddr: "3.3.3.3:54321",
			expected:   "3.3.3.3",
		},
		{
			name:       "RemoteAddr without port",
			remoteAddr: "4.4.4.4",
			expected:   "4.4.4.4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.RemoteAddr = tt.remoteAddr

			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			if tt.xri != "" {
				req.Header.Set("X-Real-IP", tt.xri)
			}

			result := getClientIPFromRequest(req)
			if result != tt.expected {
				t.Errorf("getClientIPFromRequest() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestExtractFirstIP(t *testing.T) {
	tests := []struct {
		xff      string
		expected string
	}{
		{"1.1.1.1", "1.1.1.1"},
		{"1.1.1.1, 2.2.2.2", "1.1.1.1"},
		{"1.1.1.1, 2.2.2.2, 3.3.3.3", "1.1.1.1"},
		{"", ""},
	}

	for _, tt := range tests {
		result := extractFirstIP(tt.xff)
		if result != tt.expected {
			t.Errorf("extractFirstIP(%q) = %q, want %q", tt.xff, result, tt.expected)
		}
	}
}

func TestRateLimitHeaders(t *testing.T) {
	client, cleanup := setupTestRedisClient(t)
	defer cleanup()

	configs := map[string]ratelimit.Config{
		"api": {Limit: 10, Window: time.Minute},
	}
	limiter := ratelimit.NewMultiLimiter(client, configs)

	router := gin.New()
	router.Use(RateLimitMiddleware(limiter, "api"))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "1.1.1.1:12345"
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	requiredHeaders := []string{
		"X-RateLimit-Limit",
		"X-RateLimit-Remaining",
		"X-RateLimit-Reset",
	}

	for _, header := range requiredHeaders {
		if w.Header().Get(header) == "" {
			t.Errorf("missing header: %s", header)
		}
	}
}

func TestHTTPRateLimitMiddleware(t *testing.T) {
	client, cleanup := setupTestRedisClient(t)
	defer cleanup()

	configs := map[string]ratelimit.Config{
		"api": {Limit: 2, Window: time.Minute},
	}
	limiter := ratelimit.NewMultiLimiter(client, configs)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	extractUserID := func(r *http.Request) (int64, error) {
		return 456, nil
	}

	wrappedHandler := HTTPRateLimitMiddleware(limiter, "api", extractUserID)(handler)

	// First 2 requests should succeed
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		w := httptest.NewRecorder()
		wrappedHandler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i+1, w.Code)
		}
	}

	// 3rd request should be rate limited
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	w := httptest.NewRecorder()
	wrappedHandler.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w.Code)
	}
}

// BenchmarkRateLimitMiddleware measures rate limiting performance
func BenchmarkRateLimitMiddleware(b *testing.B) {
	mr, _ := miniredis.Run()
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	configs := map[string]ratelimit.Config{
		"api": {Limit: 1000000, Window: time.Minute}, // High limit to avoid blocking
	}
	limiter := ratelimit.NewMultiLimiter(client, configs)

	router := gin.New()
	router.Use(RateLimitMiddleware(limiter, "api"))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = "1.1.1.1:12345"
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
	}
}
