package jwt

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
)

// Validator verifies tokens without any signing material. It validates RS256
// tokens against the issuer's JWKS (fetched + cached by kid) and, while
// AcceptHS256 is true, also accepts legacy HS256 tokens — this is the
// dual-accept window that lets every verifier be ready for RS256 BEFORE the
// issuer starts minting it.
//
// Validator satisfies the same verify surface as Manager
// (ValidateAccessToken/ValidateRefreshToken/Parse), so the grpc adapter and the
// ws authenticator accept it unchanged.
type Validator struct {
	jwks         *jwksCache
	hmacKey      []byte
	acceptHS256  bool
	validMethods []string
}

// NewValidator builds a Validator from config.
//
// Behavior:
//   - JWKSURL set -> RS256 verification enabled; the JWKS is preloaded
//     synchronously. If preload fails AND AcceptHS256 is true, construction
//     still succeeds (HS256 keeps working; the background refresher retries).
//     If preload fails and HS256 is off, construction fails (misconfiguration).
//   - AcceptHS256 set -> legacy HS256 tokens are accepted using SecretKey.
//
// At least one verification path (JWKS or HS256) must be configured.
func NewValidator(cfg Config) (*Validator, error) {
	v := &Validator{
		acceptHS256:  cfg.AcceptHS256,
		validMethods: validMethods(cfg.AcceptHS256),
	}
	if cfg.AcceptHS256 && cfg.SecretKey != "" {
		v.hmacKey = []byte(cfg.SecretKey)
	}

	if cfg.JWKSURL != "" {
		refresh := cfg.JWKSRefresh
		if refresh == 0 {
			refresh = 15 * time.Minute
		}
		timeout := cfg.JWKSTimeout
		if timeout == 0 {
			timeout = 5 * time.Second
		}
		v.jwks = newJWKSCache(cfg.JWKSURL, refresh, timeout, &http.Client{Timeout: timeout})

		if err := v.jwks.start(context.Background()); err != nil {
			if !cfg.AcceptHS256 {
				v.jwks.Close()
				return nil, fmt.Errorf("jwt: JWKS preload failed: %w", err)
			}
			// Degraded start: HS256 still verifies; background refresh will
			// pick up the keys once cg-users is reachable.
		}
	} else if !cfg.AcceptHS256 {
		return nil, errors.New("jwt: validator needs JWKSURL or AcceptHS256")
	}

	return v, nil
}

func (v *Validator) keyFunc() gojwt.Keyfunc {
	var resolver publicKeyResolver
	if v.jwks != nil {
		resolver = v.jwks
	}
	return buildKeyFunc(resolver, v.hmacKey, v.acceptHS256)
}

// Parse validates the token signature + standard claims and returns the claims.
func (v *Validator) Parse(tokenString string) (*Claims, error) {
	token, err := gojwt.ParseWithClaims(tokenString, &Claims{}, v.keyFunc(),
		gojwt.WithValidMethods(v.validMethods))
	if err != nil {
		if errors.Is(err, gojwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, fmt.Errorf("parse token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// ValidateAccessToken validates an access token and asserts its token_type.
func (v *Validator) ValidateAccessToken(tokenString string) (*Claims, error) {
	claims, err := v.Parse(tokenString)
	if err != nil {
		return nil, err
	}
	if claims.TokenType != TokenTypeAccess {
		return nil, ErrWrongTokenType
	}
	return claims, nil
}

// ValidateRefreshToken validates a refresh token and asserts its token_type.
func (v *Validator) ValidateRefreshToken(tokenString string) (*Claims, error) {
	claims, err := v.Parse(tokenString)
	if err != nil {
		return nil, err
	}
	if claims.TokenType != TokenTypeRefresh {
		return nil, ErrWrongTokenType
	}
	return claims, nil
}

// Close stops the background JWKS refresher. Safe to call multiple times.
func (v *Validator) Close() {
	if v.jwks != nil {
		v.jwks.Close()
	}
}
