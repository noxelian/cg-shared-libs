package middleware

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/4ubak/cg-shared-libs/logger"
	"github.com/4ubak/cg-shared-libs/ratelimit"
	"go.uber.org/zap"
)

// RateLimitConfig holds rate limit middleware configuration
type RateLimitConfig struct {
	Enabled   bool   `yaml:"enabled" env:"RATE_LIMIT_ENABLED" env-default:"true"`
	KeyPrefix string `yaml:"key_prefix" env:"RATE_LIMIT_KEY_PREFIX" env-default:"api"`
}

// RateLimitMiddleware creates a rate limiting middleware using the specified tier
// for Gin framework. Uses user ID when available, falls back to IP address.
func RateLimitMiddleware(limiter *ratelimit.MultiLimiter, tier string) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := getRateLimitKey(c, tier)

		result, err := limiter.Allow(c.Request.Context(), tier, key)
		if err != nil {
			logger.Error("rate limit check failed", zap.Error(err), zap.String("tier", tier))
			// On error, allow the request to proceed (fail open)
			c.Next()
			return
		}

		// Set rate limit headers
		setRateLimitHeaders(c, result)

		if !result.Allowed {
			logger.Warn("rate limit exceeded",
				zap.String("tier", tier),
				zap.String("key", key),
				zap.String("path", c.Request.URL.Path),
			)

			c.Header("Retry-After", strconv.FormatInt(int64(result.ResetAfter.Seconds()), 10))
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":   "rate_limit_exceeded",
				"message": "Too many requests. Please try again later.",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// IPRateLimitMiddleware creates an IP-based rate limiter for public endpoints
// using a single-tier rate limiter.
func IPRateLimitMiddleware(limiter *ratelimit.Limiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := GetClientIP(c)

		result, err := limiter.Allow(c.Request.Context(), ip)
		if err != nil {
			logger.Error("IP rate limit check failed", zap.Error(err))
			// On error, allow the request to proceed (fail open)
			c.Next()
			return
		}

		setRateLimitHeaders(c, result)

		if !result.Allowed {
			logger.Warn("IP rate limit exceeded",
				zap.String("ip", ip),
				zap.String("path", c.Request.URL.Path),
			)

			c.Header("Retry-After", strconv.FormatInt(int64(result.ResetAfter.Seconds()), 10))
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":   "rate_limit_exceeded",
				"message": "Too many requests. Please try again later.",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// WebSocketRateLimitMiddleware creates a rate limiter specifically for WebSocket connections.
// It uses the websocket tier and extracts user ID from the request context or token.
func WebSocketRateLimitMiddleware(limiter *ratelimit.MultiLimiter, extractUserID func(r *http.Request) (int64, error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var key string

			// Try to extract user ID from the request
			userID, err := extractUserID(r)
			if err != nil {
				// Fall back to IP-based rate limiting if user extraction fails
				key = fmt.Sprintf("ip:%s", getClientIPFromRequest(r))
			} else {
				key = fmt.Sprintf("user:%d", userID)
			}

			result, err := limiter.Allow(r.Context(), "websocket", key)
			if err != nil {
				logger.Error("websocket rate limit check failed", zap.Error(err))
				// On error, allow the request to proceed (fail open)
				next.ServeHTTP(w, r)
				return
			}

			// Set rate limit headers
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(result.Limit))
			w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(result.Remaining, 10))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(int64(result.ResetAfter.Seconds()), 10))

			if !result.Allowed {
				logger.Warn("websocket rate limit exceeded",
					zap.String("key", key),
					zap.String("path", r.URL.Path),
				)

				w.Header().Set("Retry-After", strconv.FormatInt(int64(result.ResetAfter.Seconds()), 10))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				if _, err := w.Write([]byte(`{"error":"rate_limit_exceeded","message":"Too many WebSocket connections. Please try again later."}`)); err != nil {
					return
				}
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// HTTPRateLimitMiddleware creates a rate limiting middleware for standard http.Handler.
// Useful for services not using Gin framework.
func HTTPRateLimitMiddleware(limiter *ratelimit.MultiLimiter, tier string, extractUserID func(r *http.Request) (int64, error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var key string

			// Try to extract user ID
			if extractUserID != nil {
				if userID, err := extractUserID(r); err == nil {
					key = fmt.Sprintf("user:%d", userID)
				}
			}

			// Fall back to IP if no user ID
			if key == "" {
				key = fmt.Sprintf("ip:%s", getClientIPFromRequest(r))
			}

			result, err := limiter.Allow(r.Context(), tier, key)
			if err != nil {
				logger.Error("rate limit check failed", zap.Error(err), zap.String("tier", tier))
				// On error, allow the request to proceed (fail open)
				next.ServeHTTP(w, r)
				return
			}

			// Set rate limit headers
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(result.Limit))
			w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(result.Remaining, 10))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(int64(result.ResetAfter.Seconds()), 10))

			if !result.Allowed {
				logger.Warn("rate limit exceeded",
					zap.String("tier", tier),
					zap.String("key", key),
					zap.String("path", r.URL.Path),
				)

				w.Header().Set("Retry-After", strconv.FormatInt(int64(result.ResetAfter.Seconds()), 10))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				if _, err := w.Write([]byte(`{"error":"rate_limit_exceeded","message":"Too many requests. Please try again later."}`)); err != nil {
					return
				}
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// getRateLimitKey generates a rate limit key based on tier and context
func getRateLimitKey(c *gin.Context, tier string) string {
	// For auth tier, always use IP address (to prevent brute force)
	if tier == "auth" {
		return fmt.Sprintf("ip:%s", GetClientIP(c))
	}

	// For authenticated tiers, prefer user_id
	if userID, exists := c.Get("user_id"); exists {
		return fmt.Sprintf("user:%v", userID)
	}

	// Fallback to IP address
	return fmt.Sprintf("ip:%s", GetClientIP(c))
}

// GetClientIP extracts the real client IP considering proxies
func GetClientIP(c *gin.Context) string {
	// Check X-Forwarded-For header (common for proxies/load balancers)
	if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
		return extractFirstIP(xff)
	}

	// Check X-Real-IP header
	if xri := c.GetHeader("X-Real-IP"); xri != "" {
		return xri
	}

	// Fallback to Gin's client IP detection
	return c.ClientIP()
}

// getClientIPFromRequest extracts the real client IP from http.Request
func getClientIPFromRequest(r *http.Request) string {
	// Check X-Forwarded-For header
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return extractFirstIP(xff)
	}

	// Check X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	// Extract IP from RemoteAddr (format: "IP:port")
	host := r.RemoteAddr
	for i := len(host) - 1; i >= 0; i-- {
		if host[i] == ':' {
			return host[:i]
		}
	}
	return host
}

// extractFirstIP extracts the first IP from X-Forwarded-For header
func extractFirstIP(xff string) string {
	for i := 0; i < len(xff); i++ {
		if xff[i] == ',' {
			return xff[:i]
		}
	}
	return xff
}

// setRateLimitHeaders sets standard rate limit response headers
func setRateLimitHeaders(c *gin.Context, result ratelimit.Result) {
	c.Header("X-RateLimit-Limit", strconv.Itoa(result.Limit))
	c.Header("X-RateLimit-Remaining", strconv.FormatInt(result.Remaining, 10))
	c.Header("X-RateLimit-Reset", strconv.FormatInt(int64(result.ResetAfter.Seconds()), 10))
}
