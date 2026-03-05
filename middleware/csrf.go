package middleware

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gitlab.com/xakpro/cg-shared-libs/logger"
	"go.uber.org/zap"
)

const (
	// CSRFTokenHeader is the header name for CSRF token
	CSRFTokenHeader = "X-CSRF-Token"
	// CSRFCookieName is the cookie name for double-submit pattern
	CSRFCookieName = "_csrf"
	// CSRFTokenLength is the length of CSRF token in bytes (before base64 encoding)
	CSRFTokenLength = 32
	// CSRFKeyPrefix is the Redis key prefix for CSRF tokens
	CSRFKeyPrefix = "csrf:"
)

// CSRFConfig holds CSRF middleware configuration
type CSRFConfig struct {
	// Enabled enables/disables CSRF protection
	Enabled bool `yaml:"enabled" env:"CSRF_ENABLED" env-default:"true"`
	// TokenExpiry is the CSRF token expiration time
	TokenExpiry time.Duration `yaml:"token_expiry" env:"CSRF_TOKEN_EXPIRY" env-default:"1h"`
	// CookieDomain sets the domain for the CSRF cookie
	CookieDomain string `yaml:"cookie_domain" env:"CSRF_COOKIE_DOMAIN"`
	// CookiePath sets the path for the CSRF cookie
	CookiePath string `yaml:"cookie_path" env:"CSRF_COOKIE_PATH" env-default:"/"`
	// CookieSecure sets the Secure flag on the CSRF cookie
	CookieSecure bool `yaml:"cookie_secure" env:"CSRF_COOKIE_SECURE" env-default:"true"`
	// CookieSameSite sets the SameSite attribute for the CSRF cookie
	CookieSameSite http.SameSite `yaml:"cookie_same_site" env:"CSRF_COOKIE_SAME_SITE" env-default:"2"`
	// ExcludedPaths lists paths to exclude from CSRF validation
	ExcludedPaths []string `yaml:"excluded_paths"`
}

// DefaultCSRFConfig returns the default CSRF configuration
func DefaultCSRFConfig() CSRFConfig {
	return CSRFConfig{
		Enabled:        true,
		TokenExpiry:    time.Hour,
		CookiePath:     "/",
		CookieSecure:   true,
		CookieSameSite: http.SameSiteStrictMode,
		ExcludedPaths:  []string{},
	}
}

// CSRFStore defines the interface for CSRF token storage
type CSRFStore interface {
	// Set stores a CSRF token with the given key and expiration
	Set(ctx context.Context, key, token string, expiry time.Duration) error
	// Get retrieves a CSRF token by key
	Get(ctx context.Context, key string) (string, error)
	// Delete removes a CSRF token by key
	Delete(ctx context.Context, key string) error
}

// RedisCSRFStore implements CSRFStore using Redis
type RedisCSRFStore struct {
	client *redis.Client
}

// NewRedisCSRFStore creates a new Redis-based CSRF store
func NewRedisCSRFStore(client *redis.Client) *RedisCSRFStore {
	return &RedisCSRFStore{client: client}
}

// Set stores a CSRF token in Redis
func (s *RedisCSRFStore) Set(ctx context.Context, key, token string, expiry time.Duration) error {
	return s.client.Set(ctx, CSRFKeyPrefix+key, token, expiry).Err()
}

// Get retrieves a CSRF token from Redis
func (s *RedisCSRFStore) Get(ctx context.Context, key string) (string, error) {
	return s.client.Get(ctx, CSRFKeyPrefix+key).Result()
}

// Delete removes a CSRF token from Redis
func (s *RedisCSRFStore) Delete(ctx context.Context, key string) error {
	return s.client.Del(ctx, CSRFKeyPrefix+key).Err()
}

// CSRFMiddleware creates a CSRF protection middleware for Gin
// It validates CSRF tokens on state-changing requests (POST, PUT, DELETE, PATCH)
// and generates new tokens for GET requests
func CSRFMiddleware(store CSRFStore, cfg CSRFConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !cfg.Enabled {
			c.Next()
			return
		}

		// Skip CSRF for excluded paths
		if isExcludedPath(c.Request.URL.Path, cfg.ExcludedPaths) {
			c.Next()
			return
		}

		// Skip CSRF for mobile API endpoints
		if isMobileEndpoint(c.Request.URL.Path) {
			c.Next()
			return
		}

		// Skip CSRF for requests with Bearer token (API clients, mobile apps)
		if hasBearerToken(c) {
			c.Next()
			return
		}

		// Skip CSRF for WebSocket upgrade requests
		if isWebSocketUpgrade(c) {
			c.Next()
			return
		}

		// Get session identifier (user_id or session cookie)
		sessionID := getSessionIdentifier(c)
		if sessionID == "" {
			// No session - skip CSRF for unauthenticated requests
			// Generate a token for potential future use
			if isReadOnlyMethod(c.Request.Method) {
				token, err := generateCSRFToken()
				if err != nil {
					logger.Error("failed to generate CSRF token", zap.Error(err))
				} else {
					setCSRFResponse(c, token, cfg)
				}
			}
			c.Next()
			return
		}

		// For read-only methods (GET, HEAD, OPTIONS), generate and return a new token
		if isReadOnlyMethod(c.Request.Method) {
			token, err := generateCSRFToken()
			if err != nil {
				logger.Error("failed to generate CSRF token", zap.Error(err))
				c.Next()
				return
			}

			// Store token in Redis with session ID
			if err := store.Set(c.Request.Context(), sessionID, token, cfg.TokenExpiry); err != nil {
				logger.Error("failed to store CSRF token", zap.Error(err), zap.String("session_id", sessionID))
			}

			setCSRFResponse(c, token, cfg)
			c.Next()
			return
		}

		// For state-changing methods, validate the token
		if !validateCSRFToken(c, store, sessionID, cfg) {
			logger.Warn("CSRF validation failed",
				zap.String("path", c.Request.URL.Path),
				zap.String("method", c.Request.Method),
				zap.String("session_id", sessionID),
			)
			c.JSON(http.StatusForbidden, gin.H{
				"error":   "csrf_validation_failed",
				"message": "CSRF token validation failed. Please refresh the page and try again.",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// generateCSRFToken generates a cryptographically secure random token
func generateCSRFToken() (string, error) {
	bytes := make([]byte, CSRFTokenLength)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(bytes), nil
}

// validateCSRFToken validates the CSRF token from the request
func validateCSRFToken(c *gin.Context, store CSRFStore, sessionID string, cfg CSRFConfig) bool {
	// Get token from header
	headerToken := c.GetHeader(CSRFTokenHeader)

	// Get token from cookie (double-submit pattern fallback)
	cookieToken, _ := c.Cookie(CSRFCookieName)

	// Get stored token from Redis
	storedToken, err := store.Get(c.Request.Context(), sessionID)
	if err != nil {
		if err == redis.Nil {
			logger.Debug("no CSRF token found in store", zap.String("session_id", sessionID))
		} else {
			logger.Error("failed to get CSRF token from store", zap.Error(err))
		}
		return false
	}

	// Primary validation: header token matches stored token
	if headerToken != "" && constantTimeCompare(headerToken, storedToken) {
		return true
	}

	// Fallback: double-submit cookie pattern
	// Both header and cookie must be present and match each other AND the stored token
	if headerToken != "" && cookieToken != "" {
		if constantTimeCompare(headerToken, cookieToken) && constantTimeCompare(headerToken, storedToken) {
			return true
		}
	}

	return false
}

// constantTimeCompare performs a constant-time comparison of two strings
func constantTimeCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// setCSRFResponse sets the CSRF token in the response header and cookie
func setCSRFResponse(c *gin.Context, token string, cfg CSRFConfig) {
	// Set token in response header
	c.Header(CSRFTokenHeader, token)

	// Set SameSite attribute BEFORE setting the cookie
	c.SetSameSite(cfg.CookieSameSite)

	// Set token in cookie for double-submit pattern
	c.SetCookie(
		CSRFCookieName,
		token,
		int(cfg.TokenExpiry.Seconds()),
		cfg.CookiePath,
		cfg.CookieDomain,
		cfg.CookieSecure,
		false, // HttpOnly must be false so JavaScript can read it
	)
}

// isReadOnlyMethod checks if the HTTP method is read-only
func isReadOnlyMethod(method string) bool {
	return method == http.MethodGet ||
		method == http.MethodHead ||
		method == http.MethodOptions
}

// isMobileEndpoint checks if the path is a mobile API endpoint
func isMobileEndpoint(path string) bool {
	return strings.HasPrefix(path, "/api/v1/mobile/") ||
		strings.HasPrefix(path, "/mobile/") ||
		strings.Contains(path, "/mobile/")
}

// hasBearerToken checks if the request has a Bearer token in the Authorization header
func hasBearerToken(c *gin.Context) bool {
	authHeader := c.GetHeader("Authorization")
	return strings.HasPrefix(authHeader, "Bearer ")
}

// isWebSocketUpgrade checks if the request is a WebSocket upgrade
func isWebSocketUpgrade(c *gin.Context) bool {
	upgrade := c.GetHeader("Upgrade")
	connection := c.GetHeader("Connection")
	return strings.EqualFold(upgrade, "websocket") ||
		strings.Contains(strings.ToLower(connection), "upgrade")
}

// isExcludedPath checks if the path should be excluded from CSRF validation
func isExcludedPath(path string, excludedPaths []string) bool {
	for _, excluded := range excludedPaths {
		if strings.HasPrefix(path, excluded) {
			return true
		}
	}
	return false
}

// getSessionIdentifier extracts the session identifier from the request
// It first tries to get user_id from context (set by auth middleware)
// then falls back to a session cookie
func getSessionIdentifier(c *gin.Context) string {
	// Try to get user_id from context (set by auth middleware)
	if userID, exists := c.Get("user_id"); exists {
		switch v := userID.(type) {
		case int64:
			return formatInt64(v)
		case string:
			return v
		}
	}

	// Fallback to session cookie
	if sessionID, err := c.Cookie("session_id"); err == nil && sessionID != "" {
		return sessionID
	}

	return ""
}

// formatInt64 converts int64 to string without using strconv
func formatInt64(n int64) string {
	if n == 0 {
		return "0"
	}

	negative := n < 0
	if negative {
		n = -n
	}

	var digits [20]byte
	i := len(digits)

	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}

	if negative {
		i--
		digits[i] = '-'
	}

	return string(digits[i:])
}

// HTTPCSRFMiddleware creates a CSRF middleware for standard http.Handler
func HTTPCSRFMiddleware(store CSRFStore, cfg CSRFConfig, getUserID func(r *http.Request) (string, bool)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !cfg.Enabled {
				next.ServeHTTP(w, r)
				return
			}

			// Skip CSRF for excluded paths
			if isExcludedPath(r.URL.Path, cfg.ExcludedPaths) {
				next.ServeHTTP(w, r)
				return
			}

			// Skip CSRF for mobile API endpoints
			if isMobileEndpoint(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			// Skip CSRF for requests with Bearer token
			if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				next.ServeHTTP(w, r)
				return
			}

			// Skip CSRF for WebSocket upgrade requests
			if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
				next.ServeHTTP(w, r)
				return
			}

			// Get session identifier
			var sessionID string
			if getUserID != nil {
				if id, ok := getUserID(r); ok {
					sessionID = id
				}
			}

			// No session - skip validation but generate token for GET requests
			if sessionID == "" {
				if isReadOnlyMethod(r.Method) {
					token, err := generateCSRFToken()
					if err == nil {
						w.Header().Set(CSRFTokenHeader, token)
						setCSRFCookie(w, token, cfg)
					}
				}
				next.ServeHTTP(w, r)
				return
			}

			// For read-only methods, generate and return a new token
			if isReadOnlyMethod(r.Method) {
				token, err := generateCSRFToken()
				if err != nil {
					logger.Error("failed to generate CSRF token", zap.Error(err))
					next.ServeHTTP(w, r)
					return
				}

				if err := store.Set(r.Context(), sessionID, token, cfg.TokenExpiry); err != nil {
					logger.Error("failed to store CSRF token", zap.Error(err))
				}

				w.Header().Set(CSRFTokenHeader, token)
				setCSRFCookie(w, token, cfg)
				next.ServeHTTP(w, r)
				return
			}

			// For state-changing methods, validate the token
			if !validateHTTPCSRFToken(r, store, sessionID) {
				logger.Warn("CSRF validation failed",
					zap.String("path", r.URL.Path),
					zap.String("method", r.Method),
				)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(`{"error":"csrf_validation_failed","message":"CSRF token validation failed. Please refresh the page and try again."}`))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// validateHTTPCSRFToken validates CSRF token for http.Request
func validateHTTPCSRFToken(r *http.Request, store CSRFStore, sessionID string) bool {
	headerToken := r.Header.Get(CSRFTokenHeader)

	var cookieToken string
	if cookie, err := r.Cookie(CSRFCookieName); err == nil {
		cookieToken = cookie.Value
	}

	storedToken, err := store.Get(r.Context(), sessionID)
	if err != nil {
		return false
	}

	if headerToken != "" && constantTimeCompare(headerToken, storedToken) {
		return true
	}

	if headerToken != "" && cookieToken != "" {
		if constantTimeCompare(headerToken, cookieToken) && constantTimeCompare(headerToken, storedToken) {
			return true
		}
	}

	return false
}

// setCSRFCookie sets the CSRF cookie for http.ResponseWriter
func setCSRFCookie(w http.ResponseWriter, token string, cfg CSRFConfig) {
	cookie := &http.Cookie{
		Name:     CSRFCookieName,
		Value:    token,
		Path:     cfg.CookiePath,
		Domain:   cfg.CookieDomain,
		MaxAge:   int(cfg.TokenExpiry.Seconds()),
		Secure:   cfg.CookieSecure,
		HttpOnly: false, // Must be false so JavaScript can read it
		SameSite: cfg.CookieSameSite,
	}
	http.SetCookie(w, cookie)
}
