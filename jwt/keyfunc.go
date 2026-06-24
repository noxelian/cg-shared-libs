package jwt

import (
	"crypto/rsa"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

// publicKeyResolver resolves an RSA public key by its key id (kid).
// Implemented by jwksCache; abstracted so the keyfunc is unit-testable.
type publicKeyResolver interface {
	publicKey(kid string) (*rsa.PublicKey, error)
}

// buildKeyFunc returns a jwt.Keyfunc that is algorithm- and kid-aware.
//
// It preserves the algorithm-confusion protection that the legacy Manager had:
// the key returned is bound to the token's declared signing method, so an
// attacker cannot swap an RS256 header onto an HS256-keyed verification (the
// keyfunc never hands an HMAC secret to an RSA token, nor an RSA key to an
// HMAC token). Unexpected methods are rejected outright.
//
//   - RS256 tokens -> resolved against the JWKS by kid (asymmetric, public key)
//   - HS256 tokens -> the shared secret, and ONLY while acceptHS256 is true
//     (the dual-accept migration window). Rejected once HS256 is retired.
func buildKeyFunc(jwks publicKeyResolver, hmacKey []byte, acceptHS256 bool) jwt.Keyfunc {
	return func(token *jwt.Token) (any, error) {
		switch token.Method.(type) {
		case *jwt.SigningMethodRSA:
			// Pin to RS256 specifically; reject RS384/RS512/PS* we never issue.
			if token.Method.Alg() != "RS256" {
				return nil, fmt.Errorf("%w: unexpected RSA alg %q", ErrInvalidToken, token.Method.Alg())
			}
			if jwks == nil {
				return nil, fmt.Errorf("%w: RS256 token but no JWKS configured", ErrInvalidToken)
			}
			kid, _ := token.Header["kid"].(string)
			if kid == "" {
				return nil, fmt.Errorf("%w: RS256 token missing kid", ErrInvalidToken)
			}
			return jwks.publicKey(kid)
		case *jwt.SigningMethodHMAC:
			if !acceptHS256 || len(hmacKey) == 0 {
				return nil, fmt.Errorf("%w: HS256 not accepted", ErrInvalidToken)
			}
			return hmacKey, nil
		default:
			return nil, fmt.Errorf("%w: unexpected signing method %v", ErrInvalidToken, token.Header["alg"])
		}
	}
}

// validMethods lists the alg values the parser will accept up-front (defense in
// depth, applied via jwt.WithValidMethods before the keyfunc runs).
func validMethods(acceptHS256 bool) []string {
	if acceptHS256 {
		return []string{"RS256", "HS256"}
	}
	return []string{"RS256"}
}
