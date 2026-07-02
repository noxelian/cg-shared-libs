package jwt

import (
	"crypto"
	"errors"
	"fmt"
)

// staticKeyResolver resolves RS256 public keys from an in-memory map keyed by
// kid. Unlike jwksCache it performs NO network I/O, so a verifier built on it
// cannot race service startup and does not depend on a (possibly
// self-referential) JWKS endpoint being reachable.
type staticKeyResolver struct {
	keys map[string]crypto.PublicKey
}

func (r staticKeyResolver) publicKey(kid string) (crypto.PublicKey, error) {
	if k, ok := r.keys[kid]; ok && k != nil {
		return k, nil
	}
	return nil, fmt.Errorf("%w: %q", ErrUnknownKID, kid)
}

// NewLocalRS256Verifier builds a Verifier that validates RS256 tokens against an
// in-memory public-key set (by kid) instead of a network JWKS, while still
// accepting legacy HS256 tokens when cfg enables dual-accept (AcceptHS256 +
// SecretKey). It makes no network calls.
//
// This exists for the ISSUER (cg-users auth) to verify its OWN tokens. Fetching
// its own JWKS over HTTP is fragile: the verifier preloads synchronously at
// construction, BEFORE the issuer's JWKS HTTP server is listening, so the
// preload fails and the verifier degrades until a background refresh succeeds —
// a window in which RS256 refresh tokens are rejected on every restart. The
// issuer already holds the signing key in memory, so it should verify locally.
// Every NON-issuer service keeps NewVerifier/JWKS, since only the issuer holds
// signing material.
//
// keys maps kid -> public key: pass the current signing key plus any
// not-yet-retired previous keys (for rotation). At least one RS256 key OR HS256
// dual-accept (AcceptHS256 + SecretKey) must be configured, else construction
// fails as a misconfiguration.
//
// Algorithm-confusion protection is identical to the JWKS Validator: buildKeyFunc
// binds the returned key to the token's declared method (RSA key only for RS256,
// HMAC secret only for HS256), so an RS256 header can never be verified with the
// HMAC secret or vice versa.
func NewLocalRS256Verifier(cfg Config, keys map[string]crypto.PublicKey) (*Validator, error) {
	v := &Validator{expectedIssuer: cfg.ExpectedIssuer}
	if cfg.AcceptHS256 && cfg.SecretKey != "" {
		v.hmacKey = []byte(cfg.SecretKey)
	}
	// Effective dual-accept requires an ACTUAL HMAC key, not just the flag.
	v.acceptHS256 = v.hmacKey != nil
	v.validMethods = validMethods(v.acceptHS256)

	// Defensive copy so callers cannot mutate the key set after construction.
	if len(keys) > 0 {
		cp := make(map[string]crypto.PublicKey, len(keys))
		for kid, k := range keys {
			if kid != "" && k != nil {
				cp[kid] = k
			}
		}
		if len(cp) > 0 {
			v.resolver = staticKeyResolver{keys: cp}
		}
	}

	if v.resolver == nil && !v.acceptHS256 {
		return nil, errors.New("jwt: local verifier needs at least one RS256 key or HS256 dual-accept")
	}
	return v, nil
}
