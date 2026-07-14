package ws

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/4ubak/cg-shared-libs/jwt"
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
		return 0, fmt.Errorf("invalid token: %w", err)
	}

	if claims.UserID == 0 {
		return 0, fmt.Errorf("user_id not found in token claims")
	}

	return claims.UserID, nil
}

// ExtractToken extracts JWT token from request.
// Priority order:
//  1. Sec-WebSocket-Protocol header with the "access_token", "<JWT>" pair
//  2. Authorization: Bearer header
//
// Query parameters are intentionally ignored because URLs are routinely
// persisted in browser history, ingress logs, traces, and monitoring systems.
func ExtractToken(r *http.Request) string {
	// 1. Sec-WebSocket-Protocol: access_token, <JWT>
	protocols := r.Header.Get("Sec-WebSocket-Protocol")
	if protocols != "" {
		parts := strings.Split(protocols, ",")
		if len(parts) == 2 && strings.TrimSpace(parts[0]) == "access_token" {
			return strings.TrimSpace(parts[1])
		}
	}

	// 2. Authorization header
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return parts[1]
	}

	return ""
}

// AuthFunc is a function type for extracting user ID from request
type AuthFunc func(r *http.Request) (int64, error)
