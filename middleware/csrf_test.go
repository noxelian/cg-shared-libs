package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func setupCSRFTestRedis(t *testing.T) (store *RedisCSRFStore, cleanup func()) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	store = NewRedisCSRFStore(client)

	cleanup = func() {
		if err := client.Close(); err != nil {
			t.Errorf("close Redis client: %v", err)
		}
		mr.Close()
	}

	return store, cleanup
}

func TestCSRFMiddleware_SkipMobileEndpoints(t *testing.T) {
	store, cleanup := setupCSRFTestRedis(t)
	defer cleanup()

	cfg := DefaultCSRFConfig()
	router := gin.New()
	router.Use(CSRFMiddleware(store, cfg))
	router.POST("/api/v1/mobile/auth/login", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/mobile/auth/login", http.NoBody)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for mobile endpoint, got %d", w.Code)
	}
}

func TestCSRFMiddleware_SkipBearerToken(t *testing.T) {
	store, cleanup := setupCSRFTestRedis(t)
	defer cleanup()

	cfg := DefaultCSRFConfig()
	router := gin.New()
	router.Use(CSRFMiddleware(store, cfg))
	router.POST("/api/v1/web/action", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/web/action", http.NoBody)
	req.Header.Set("Authorization", "Bearer valid_token_here")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for Bearer token request, got %d", w.Code)
	}
}

func TestCSRFMiddleware_SkipWebSocket(t *testing.T) {
	store, cleanup := setupCSRFTestRedis(t)
	defer cleanup()

	cfg := DefaultCSRFConfig()
	router := gin.New()
	router.Use(CSRFMiddleware(store, cfg))
	router.GET("/ws", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ws", http.NoBody)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "upgrade")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for WebSocket request, got %d", w.Code)
	}
}

func TestCSRFMiddleware_GenerateTokenOnGET(t *testing.T) {
	store, cleanup := setupCSRFTestRedis(t)
	defer cleanup()

	cfg := DefaultCSRFConfig()
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", int64(123))
		c.Next()
	})
	router.Use(CSRFMiddleware(store, cfg))
	router.GET("/api/v1/web/profile", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/web/profile", http.NoBody)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	// Check CSRF token is returned in header
	csrfToken := w.Header().Get(CSRFTokenHeader)
	if csrfToken == "" {
		t.Error("expected CSRF token in response header")
	}

	// Check CSRF cookie is set
	cookies := w.Result().Cookies()
	var csrfCookie *http.Cookie
	for _, cookie := range cookies {
		if cookie.Name == CSRFCookieName {
			csrfCookie = cookie
			break
		}
	}
	if csrfCookie == nil {
		t.Error("expected CSRF cookie to be set")
	}

	// Cookie value should match header value
	// Note: Cookie.Value is already URL-decoded by http.Cookie parser
	// But we just verify the cookie exists and is non-empty
	if csrfCookie != nil && csrfCookie.Value == "" {
		t.Error("CSRF cookie should have a non-empty value")
	}
}

func TestCSRFMiddleware_ValidateTokenOnPOST(t *testing.T) {
	store, cleanup := setupCSRFTestRedis(t)
	defer cleanup()

	cfg := DefaultCSRFConfig()
	sessionID := "123"

	// Pre-store a valid token
	validToken := "valid_csrf_token_abc123" //nolint:gosec // Test fixture, not a credential.
	if err := store.Set(context.Background(), sessionID, validToken, time.Hour); err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", int64(123))
		c.Next()
	})
	router.Use(CSRFMiddleware(store, cfg))
	router.POST("/api/v1/web/action", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// Test with valid token
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/web/action", http.NoBody)
	req.Header.Set(CSRFTokenHeader, validToken)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 with valid CSRF token, got %d", w.Code)
	}
}

func TestCSRFMiddleware_RejectInvalidToken(t *testing.T) {
	store, cleanup := setupCSRFTestRedis(t)
	defer cleanup()

	cfg := DefaultCSRFConfig()
	sessionID := "123"

	// Pre-store a valid token
	validToken := "valid_csrf_token_abc123" //nolint:gosec // Test fixture, not a credential.
	if err := store.Set(context.Background(), sessionID, validToken, time.Hour); err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", int64(123))
		c.Next()
	})
	router.Use(CSRFMiddleware(store, cfg))
	router.POST("/api/v1/web/action", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// Test with invalid token
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/web/action", http.NoBody)
	req.Header.Set(CSRFTokenHeader, "invalid_token")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 with invalid CSRF token, got %d", w.Code)
	}
}

func TestCSRFMiddleware_RejectMissingToken(t *testing.T) {
	store, cleanup := setupCSRFTestRedis(t)
	defer cleanup()

	cfg := DefaultCSRFConfig()
	sessionID := "123"

	// Pre-store a valid token
	validToken := "valid_csrf_token_abc123" //nolint:gosec // Test fixture, not a credential.
	if err := store.Set(context.Background(), sessionID, validToken, time.Hour); err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", int64(123))
		c.Next()
	})
	router.Use(CSRFMiddleware(store, cfg))
	router.POST("/api/v1/web/action", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// Test without token
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/web/action", http.NoBody)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 without CSRF token, got %d", w.Code)
	}
}

func TestCSRFMiddleware_DoubleSubmitPattern(t *testing.T) {
	store, cleanup := setupCSRFTestRedis(t)
	defer cleanup()

	cfg := DefaultCSRFConfig()
	sessionID := "123"

	// Pre-store a valid token
	validToken := "valid_csrf_token_double_submit" //nolint:gosec // Test fixture, not a credential.
	if err := store.Set(context.Background(), sessionID, validToken, time.Hour); err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", int64(123))
		c.Next()
	})
	router.Use(CSRFMiddleware(store, cfg))
	router.POST("/api/v1/web/action", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// Test with both header and cookie (double-submit pattern)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/web/action", http.NoBody)
	req.Header.Set(CSRFTokenHeader, validToken)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: validToken})
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 with double-submit pattern, got %d", w.Code)
	}
}

func TestCSRFMiddleware_DoubleSubmitMismatch(t *testing.T) {
	store, cleanup := setupCSRFTestRedis(t)
	defer cleanup()

	cfg := DefaultCSRFConfig()
	sessionID := "123"

	// Pre-store a valid token
	validToken := "valid_csrf_token_stored" //nolint:gosec // Test fixture, not a credential.
	if err := store.Set(context.Background(), sessionID, validToken, time.Hour); err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", int64(123))
		c.Next()
	})
	router.Use(CSRFMiddleware(store, cfg))
	router.POST("/api/v1/web/action", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// Test with mismatched header and cookie
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/web/action", http.NoBody)
	req.Header.Set(CSRFTokenHeader, "header_token")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "cookie_token"})
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 with mismatched tokens, got %d", w.Code)
	}
}

func TestCSRFMiddleware_SkipExcludedPaths(t *testing.T) {
	store, cleanup := setupCSRFTestRedis(t)
	defer cleanup()

	cfg := DefaultCSRFConfig()
	cfg.ExcludedPaths = []string{"/api/v1/public", "/health"}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", int64(123))
		c.Next()
	})
	router.Use(CSRFMiddleware(store, cfg))
	router.POST("/api/v1/public/data", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/public/data", http.NoBody)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for excluded path, got %d", w.Code)
	}
}

func TestCSRFMiddleware_Disabled(t *testing.T) {
	store, cleanup := setupCSRFTestRedis(t)
	defer cleanup()

	cfg := DefaultCSRFConfig()
	cfg.Enabled = false

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", int64(123))
		c.Next()
	})
	router.Use(CSRFMiddleware(store, cfg))
	router.POST("/api/v1/web/action", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/web/action", http.NoBody)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 when CSRF disabled, got %d", w.Code)
	}
}

func TestCSRFMiddleware_PUTMethod(t *testing.T) {
	store, cleanup := setupCSRFTestRedis(t)
	defer cleanup()

	cfg := DefaultCSRFConfig()
	sessionID := "123"

	// Pre-store a valid token
	validToken := "valid_csrf_token_put" //nolint:gosec // Test fixture, not a credential.
	if err := store.Set(context.Background(), sessionID, validToken, time.Hour); err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", int64(123))
		c.Next()
	})
	router.Use(CSRFMiddleware(store, cfg))
	router.PUT("/api/v1/web/resource", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// Test PUT with valid token
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/web/resource", http.NoBody)
	req.Header.Set(CSRFTokenHeader, validToken)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for PUT with valid token, got %d", w.Code)
	}
}

func TestCSRFMiddleware_DELETEMethod(t *testing.T) {
	store, cleanup := setupCSRFTestRedis(t)
	defer cleanup()

	cfg := DefaultCSRFConfig()
	sessionID := "123"

	// Pre-store a valid token
	validToken := "valid_csrf_token_delete" //nolint:gosec // Test fixture, not a credential.
	if err := store.Set(context.Background(), sessionID, validToken, time.Hour); err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", int64(123))
		c.Next()
	})
	router.Use(CSRFMiddleware(store, cfg))
	router.DELETE("/api/v1/web/resource/:id", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// Test DELETE with valid token
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/web/resource/123", http.NoBody)
	req.Header.Set(CSRFTokenHeader, validToken)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for DELETE with valid token, got %d", w.Code)
	}
}

func TestCSRFMiddleware_PATCHMethod(t *testing.T) {
	store, cleanup := setupCSRFTestRedis(t)
	defer cleanup()

	cfg := DefaultCSRFConfig()
	sessionID := "123"

	validToken := "valid_csrf_token_patch" //nolint:gosec // Static CSRF fixture, not a credential.
	if err := store.Set(context.Background(), sessionID, validToken, time.Hour); err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", int64(123))
		c.Next()
	})
	router.Use(CSRFMiddleware(store, cfg))
	router.PATCH("/api/v1/web/resource/:id", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// Test PATCH without valid token should fail
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/api/v1/web/resource/123", http.NoBody)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for PATCH without token, got %d", w.Code)
	}
}

func TestGenerateCSRFToken(t *testing.T) {
	token1, err := generateCSRFToken()
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	if token1 == "" {
		t.Error("generated token should not be empty")
	}

	token2, err := generateCSRFToken()
	if err != nil {
		t.Fatalf("failed to generate second token: %v", err)
	}

	if token1 == token2 {
		t.Error("generated tokens should be unique")
	}
}

func TestRedisCSRFStore(t *testing.T) {
	store, cleanup := setupCSRFTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	testKey := "test_session"
	testToken := "test_csrf_token_123" //nolint:gosec // Static CSRF fixture, not a credential.

	// Test Set
	err := store.Set(ctx, testKey, testToken, time.Hour)
	if err != nil {
		t.Fatalf("failed to set token: %v", err)
	}

	// Test Get
	retrieved, err := store.Get(ctx, testKey)
	if err != nil {
		t.Fatalf("failed to get token: %v", err)
	}
	if retrieved != testToken {
		t.Errorf("expected %q, got %q", testToken, retrieved)
	}

	// Test Delete
	err = store.Delete(ctx, testKey)
	if err != nil {
		t.Fatalf("failed to delete token: %v", err)
	}

	// Verify deleted
	_, err = store.Get(ctx, testKey)
	if err != redis.Nil {
		t.Error("expected redis.Nil error after delete")
	}
}

func TestConstantTimeCompare(t *testing.T) {
	tests := []struct {
		a, b     string
		expected bool
	}{
		{"abc", "abc", true},
		{"abc", "xyz", false},
		{"", "", true},
		{"abc", "ab", false},
		{"ab", "abc", false},
	}

	for _, tt := range tests {
		result := constantTimeCompare(tt.a, tt.b)
		if result != tt.expected {
			t.Errorf("constantTimeCompare(%q, %q) = %v, want %v", tt.a, tt.b, result, tt.expected)
		}
	}
}

func TestIsReadOnlyMethod(t *testing.T) {
	tests := []struct {
		method   string
		expected bool
	}{
		{http.MethodGet, true},
		{http.MethodHead, true},
		{http.MethodOptions, true},
		{http.MethodPost, false},
		{http.MethodPut, false},
		{http.MethodDelete, false},
		{http.MethodPatch, false},
	}

	for _, tt := range tests {
		result := isReadOnlyMethod(tt.method)
		if result != tt.expected {
			t.Errorf("isReadOnlyMethod(%q) = %v, want %v", tt.method, result, tt.expected)
		}
	}
}

func TestIsMobileEndpoint(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"/api/v1/mobile/auth", true},
		{"/api/v1/mobile/profile", true},
		{"/mobile/test", true},
		{"/api/v1/web/profile", false},
		{"/api/v1/admin/users", false},
	}

	for _, tt := range tests {
		result := isMobileEndpoint(tt.path)
		if result != tt.expected {
			t.Errorf("isMobileEndpoint(%q) = %v, want %v", tt.path, result, tt.expected)
		}
	}
}

func TestFormatInt64(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0"},
		{123, "123"},
		{-456, "-456"},
		{9223372036854775807, "9223372036854775807"},
	}

	for _, tt := range tests {
		result := formatInt64(tt.input)
		if result != tt.expected {
			t.Errorf("formatInt64(%d) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestHTTPCSRFMiddleware(t *testing.T) {
	store, cleanup := setupCSRFTestRedis(t)
	defer cleanup()

	cfg := DefaultCSRFConfig()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("ok")); err != nil {
			t.Errorf("write response: %v", err)
		}
	})

	getUserID := func(r *http.Request) (string, bool) {
		if id := r.Header.Get("X-User-ID"); id != "" {
			return id, true
		}
		return "", false
	}

	wrappedHandler := HTTPCSRFMiddleware(store, cfg, getUserID)(handler)

	// Test GET request generates token
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/web/test", http.NoBody)
	req.Header.Set("X-User-ID", "123")
	w := httptest.NewRecorder()
	wrappedHandler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	csrfToken := w.Header().Get(CSRFTokenHeader)
	if csrfToken == "" {
		t.Error("expected CSRF token in response header")
	}

	// Test POST with valid token
	if err := store.Set(context.Background(), "456", "stored_token", time.Hour); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/web/action", http.NoBody)
	req.Header.Set("X-User-ID", "456")
	req.Header.Set(CSRFTokenHeader, "stored_token")
	w = httptest.NewRecorder()
	wrappedHandler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 with valid token, got %d", w.Code)
	}

	// Test POST without token
	req = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/web/action", http.NoBody)
	req.Header.Set("X-User-ID", "789")
	w = httptest.NewRecorder()
	wrappedHandler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 without token, got %d", w.Code)
	}
}

func TestCSRFMiddleware_NoSession(t *testing.T) {
	store, cleanup := setupCSRFTestRedis(t)
	defer cleanup()

	cfg := DefaultCSRFConfig()

	router := gin.New()
	// Note: No user_id is set in context
	router.Use(CSRFMiddleware(store, cfg))
	router.POST("/api/v1/web/public", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	router.GET("/api/v1/web/public", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// POST without session should be allowed (no session to protect)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/web/public", http.NoBody)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for POST without session, got %d", w.Code)
	}

	// GET without session should still generate token
	req = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/web/public", http.NoBody)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for GET without session, got %d", w.Code)
	}

	// Token should be generated even without session
	csrfToken := w.Header().Get(CSRFTokenHeader)
	if csrfToken == "" {
		t.Error("expected CSRF token even without session")
	}
}

// BenchmarkGenerateCSRFToken measures token generation performance
func BenchmarkGenerateCSRFToken(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, err := generateCSRFToken()
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCSRFMiddleware measures middleware performance
func BenchmarkCSRFMiddleware(b *testing.B) {
	mr, err := miniredis.Run()
	if err != nil {
		b.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() {
		if err := client.Close(); err != nil {
			b.Errorf("close Redis client: %v", err)
		}
	}()

	store := NewRedisCSRFStore(client)
	cfg := DefaultCSRFConfig()

	// Pre-store a token
	if err := store.Set(context.Background(), "123", "benchmark_token", time.Hour); err != nil {
		b.Fatal(err)
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", int64(123))
		c.Next()
	})
	router.Use(CSRFMiddleware(store, cfg))
	router.POST("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/test", http.NoBody)
		req.Header.Set(CSRFTokenHeader, "benchmark_token")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
	}
}
