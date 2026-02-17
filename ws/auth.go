package ws

import (
	"fmt"
	"net/http"
	"strings"

	"gitlab.com/xakpro/cg-shared-libs/jwt"
	"gitlab.com/xakpro/cg-shared-libs/logger"
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

// ExtractToken extracts JWT token from request (query param or header)
func ExtractToken(r *http.Request) string {
	// Get token from query parameter
	tokenStr := r.URL.Query().Get("token")
	if tokenStr != "" {
		return tokenStr
	}

	// Try Authorization header as fallback
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {
		return strings.TrimPrefix(authHeader, "Bearer ")
	}

	return ""
}

// AuthFunc is a function type for extracting user ID from request
type AuthFunc func(r *http.Request) (int64, error)
