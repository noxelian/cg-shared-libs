package jwt

import "io"

// Verifier is the read-only token surface every non-issuer service needs.
//
// Both *Manager (legacy HS256) and *Validator (RS256 via JWKS, with optional
// HS256 dual-accept) satisfy it, so a service migrates by swapping which one
// NewVerifier returns — no call-site change. This is the lever that keeps the
// 17-service rollout a uniform one-liner instead of bespoke per-service wiring.
type Verifier interface {
	ValidateAccessToken(token string) (*Claims, error)
	ValidateRefreshToken(token string) (*Claims, error)
	Parse(token string) (*Claims, error)
	io.Closer // Manager.Close is a no-op; Validator.Close stops the JWKS refresher
}

// Compile-time guarantee that both implementations stay interchangeable.
var (
	_ Verifier = (*Manager)(nil)
	_ Verifier = (*Validator)(nil)
)

// NewVerifier returns the right verifier for the current migration phase,
// chosen purely from config so Phases 3→4→7 become env flips, not code changes:
//
//   - JWKSURL set               -> *Validator (RS256; +HS256 dual-accept if a secret is present)
//   - JWKSURL empty, secret set  -> *Manager   (legacy HS256 only)
//
// Always pair with `defer v.Close()` — Manager.Close is a no-op, so the same
// shutdown wiring is correct for both and a service can flip phases without
// touching its lifecycle code.
func NewVerifier(cfg Config) (Verifier, error) {
	if cfg.JWKSURL != "" {
		return NewValidator(cfg)
	}
	return NewManager(cfg)
}
