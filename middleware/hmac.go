package middleware

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"

	"github.com/4ubak/cg-shared-libs/logger"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// SignatureHeader is the canonical header name for HMAC-SHA256 signatures
// used to authenticate legacy-system webhooks across CTOgram services.
const SignatureHeader = "X-Legacy-Signature"

// maxBodySize caps the request body read by HMACSignatureMiddleware. 5 MB is
// generous for webhook payloads (realistic ≤1 KB) while small enough to bound
// memory consumption against crafted huge requests.
const maxBodySize = 5 << 20 // 5 MiB

// HMACSignatureMiddleware returns a Gin middleware that validates an
// HMAC-SHA256 hex-encoded signature of the raw request body against a shared
// secret loaded from the environment variable named secretEnvKey.
//
// Behaviour:
//   - reads up to maxBodySize bytes via io.LimitReader; oversized bodies → 413
//     {"error":"payload too large"}
//   - restores the body via io.NopCloser(bytes.NewReader(body)) so downstream
//     handlers can read or bind it
//   - compares signatures using hmac.Equal (constant-time)
//   - missing header / malformed hex / mismatched signature → 401
//     {"error":"invalid signature"}
//   - body read error → 400 {"error":"cannot read body"}
//
// Configuration contract:
//   - secretEnvKey MUST be a non-empty argument; an empty argument panics.
//   - The env var MUST resolve to a non-empty secret at construction time;
//     an empty secret panics. Fail-fast — silent runtime degradation is
//     worse than a crashed boot for webhook-auth middleware.
//
// Composition contract (caller responsibility):
//   - Mandatory chain order: IPAllowlistMiddleware → HMACSignatureMiddleware → handler.
//   - Transport MUST be TLS (https://). This middleware does NOT enforce it.
//   - This middleware does NOT touch tls.Config.InsecureSkipVerify anywhere.
//
// Logging contract:
//   - Headers Authorization and X-Legacy-Signature are NEVER logged.
//   - Request and response body bytes are NEVER logged.
//   - Diagnostic logs include only request path and a free-text reason.
func HMACSignatureMiddleware(secretEnvKey string) gin.HandlerFunc {
	if secretEnvKey == "" {
		panic("middleware: HMACSignatureMiddleware requires non-empty secretEnvKey")
	}
	secret := []byte(os.Getenv(secretEnvKey))
	if len(secret) == 0 {
		panic("middleware: HMACSignatureMiddleware: env " + secretEnvKey + " is empty")
	}

	return func(c *gin.Context) {
		provided := c.GetHeader(SignatureHeader)
		if provided == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "invalid signature",
			})
			return
		}

		// Read at most maxBodySize+1 bytes so we can detect overflow without OOM.
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBodySize+1))
		if err != nil {
			logger.Warn("hmac middleware: body read failed",
				zap.String("path", c.Request.URL.Path),
				zap.Error(err))
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": "cannot read body",
			})
			return
		}
		if len(body) > maxBodySize {
			logger.Warn("hmac middleware: payload too large",
				zap.String("path", c.Request.URL.Path),
				zap.Int("read_bytes", len(body)))
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{
				"error": "payload too large",
			})
			return
		}

		// Restore body for downstream handlers (immutable copy via bytes.NewReader).
		c.Request.Body = io.NopCloser(bytes.NewReader(body))

		providedBytes, err := hex.DecodeString(provided)
		if err != nil {
			logger.Warn("hmac middleware: malformed hex signature",
				zap.String("path", c.Request.URL.Path))
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "invalid signature",
			})
			return
		}

		mac := hmac.New(sha256.New, secret)
		mac.Write(body)
		expected := mac.Sum(nil)

		if !hmac.Equal(providedBytes, expected) {
			// Path only — never headers/body.
			logger.Warn("hmac middleware: signature mismatch",
				zap.String("path", c.Request.URL.Path))
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "invalid signature",
			})
			return
		}

		c.Next()
	}
}
