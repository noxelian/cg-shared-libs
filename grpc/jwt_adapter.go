package grpc

import (
	"github.com/4ubak/cg-shared-libs/jwt"
)

// JWTValidatorAdapter adapts jwt.Manager to the JWTValidator interface
// required by AuthInterceptor. Use NewJWTValidatorAdapter to construct.
//
// This eliminates the boilerplate that every service was duplicating locally:
//
//	sharedGRPC.AuthInterceptor(
//	    sharedGRPC.NewJWTValidatorAdapter(jwtManager),
//	    sharedGRPC.AuthInterceptorConfig{...},
//	)
type JWTValidatorAdapter struct {
	manager *jwt.Manager
}

// NewJWTValidatorAdapter wraps a jwt.Manager so it satisfies JWTValidator.
func NewJWTValidatorAdapter(manager *jwt.Manager) *JWTValidatorAdapter {
	return &JWTValidatorAdapter{manager: manager}
}

// ValidateAccessToken implements JWTValidator.
func (a *JWTValidatorAdapter) ValidateAccessToken(token string) (*JWTClaims, error) {
	claims, err := a.manager.ValidateAccessToken(token)
	if err != nil {
		return nil, err
	}
	return &JWTClaims{
		UserID:   claims.UserID,
		Phone:    claims.Phone,
		DeviceID: claims.DeviceID,
	}, nil
}
