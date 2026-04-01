package ws

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/4ubak/cg-shared-libs/jwt"
	"github.com/4ubak/cg-shared-libs/logger"
	"go.uber.org/zap"
)

// TokenValidator interface for JWT validation
type TokenValidator interface {
	ValidateAccessToken(tokenString string) (*jwt.Claims, error)
}

// Authenticator handles WebSocket authentication
type Authenticator struct {
	jwtManager TokenValidator
}

// NewAuthenticator creates a new authenticator with JWT manager
func NewAuthenticator(jwtManager TokenValidator) *Authenticator {
	return &Authenticator{jwtManager: jwtManager}
}

// ExtractUserID extracts user ID from JWT token in request
func (a *Authenticator) ExtractUserID(r *http.Request) (int64, error) {
	tokenStr := ExtractToken(r)
	if tokenStr == "" {
		return 0, fmt.Errorf("token not found")
	}

	claims, err := a.jwtManager.ValidateAccessToken(tokenStr)
	if err != nil {
		logger.Debug("failed to validate access token", zap.Error(err))
		return 0, fmt.Errorf("invalid token: %w", err)
	}

	if claims.UserID == 0 {
		return 0, fmt.Errorf("user_id not found in token claims")
	}

	return claims.UserID, nil
}

// ExtractToken extracts JWT token from request.
// Priority order (most secure first):
//  1. Sec-WebSocket-Protocol header with "access_token" subprotocol
//  2. Authorization: Bearer header
//  3. Query parameter "token" (deprecated - tokens may appear in access logs)
func ExtractToken(r *http.Request) string {
	// 1. Sec-WebSocket-Protocol: access_token, <JWT>
	// This is the recommended way for WebSocket auth (no URL exposure)
	protocols := r.Header.Get("Sec-WebSocket-Protocol")
	if protocols != "" {
		for _, part := range strings.Split(protocols, ",") {
			p := strings.TrimSpace(part)
			if p != "access_token" && p != "" {
				return p
			}
		}
	}

	// 2. Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {
		return strings.TrimPrefix(authHeader, "Bearer ")
	}

	// 3. Query parameter (deprecated - tokens appear in server access logs)
	tokenStr := r.URL.Query().Get("token")
	if tokenStr != "" {
		logger.Warn("websocket auth via query param is deprecated, use Sec-WebSocket-Protocol header",
			zap.String("remote_addr", r.RemoteAddr),
		)
		return tokenStr
	}

	return ""
}

// AuthFunc is a function type for extracting user ID from request
type AuthFunc func(r *http.Request) (int64, error)
