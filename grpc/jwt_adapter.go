package grpc

import (
	"slices"

	"github.com/4ubak/cg-shared-libs/jwt"
)

// accessTokenValidator is the verify surface the adapter needs. Both
// *jwt.Manager (legacy HS256) and *jwt.Validator (RS256 via JWKS, with optional
// HS256 dual-accept) satisfy it, so services migrate to RS256 by swapping which
// one they pass in — no call-site change.
type accessTokenValidator interface {
	ValidateAccessToken(token string) (*jwt.Claims, error)
}

// JWTValidatorAdapter adapts a jwt validator to the JWTValidator interface
// required by AuthInterceptor. Use NewJWTValidatorAdapter to construct.
//
// This eliminates the boilerplate that every service was duplicating locally:
//
//	sharedGRPC.AuthInterceptor(
//	    sharedGRPC.NewJWTValidatorAdapter(jwtManager),
//	    sharedGRPC.AuthInterceptorConfig{...},
//	)
type JWTValidatorAdapter struct {
	validator accessTokenValidator
}

// NewJWTValidatorAdapter wraps a jwt validator (*jwt.Manager or *jwt.Validator)
// so it satisfies JWTValidator.
func NewJWTValidatorAdapter(validator accessTokenValidator) *JWTValidatorAdapter {
	return &JWTValidatorAdapter{validator: validator}
}

// ValidateAccessToken implements JWTValidator.
func (a *JWTValidatorAdapter) ValidateAccessToken(token string) (*JWTClaims, error) {
	claims, err := a.validator.ValidateAccessToken(token)
	if err != nil {
		return nil, err
	}
	return &JWTClaims{
		UserID:        claims.UserID,
		Phone:         claims.Phone,
		DeviceID:      claims.DeviceID,
		App:           claims.App,
		OrgID:         claims.OrgID,
		OrgType:       claims.OrgType,
		CityID:        claims.CityID,
		OrgRole:       claims.OrgRole,
		PlatformRoles: slices.Clone(claims.PlatformRoles),
		OrgIDs:        slices.Clone(claims.OrgIDs),
	}, nil
}
