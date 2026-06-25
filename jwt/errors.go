package jwt

import "errors"

// Sentinel errors. All carry a "jwt:" prefix so they are unambiguous in wrapped
// service logs; match them with errors.Is, not string comparison.
var (
	// ErrTokenExpired is returned when a token's exp has passed.
	ErrTokenExpired = errors.New("jwt: token expired")
	// ErrInvalidToken is returned for a malformed token, bad signature, or
	// unexpected signing method.
	ErrInvalidToken = errors.New("jwt: invalid token")
	// ErrWrongTokenType is returned when an access token is used where a refresh
	// token is required, or vice versa.
	ErrWrongTokenType = errors.New("jwt: wrong token type")
	// ErrUnknownKID is returned when a token references a key id absent from the
	// JWKS (after a rate-limited refresh attempt).
	ErrUnknownKID = errors.New("jwt: unknown key id")
)
