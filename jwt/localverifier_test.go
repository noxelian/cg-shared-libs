package jwt

import (
	"crypto"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The whole point: this verifier makes NO network calls (no JWKS server), so
// the issuer can verify its own RS256 tokens the instant it boots.
func TestLocalRS256Verifier_VerifiesOwnRS256_NoNetwork(t *testing.T) {
	s := newTestSigner(t, "cg-users-2026-06")
	v, err := NewLocalRS256Verifier(Config{Issuer: "test-issuer"},
		map[string]crypto.PublicKey{s.KeyID(): s.PublicKey()})
	require.NoError(t, err)

	pair, err := s.GenerateTokenPair(7711, "+77029990503", "dev-1")
	require.NoError(t, err)

	// Access + refresh both verify against the in-memory key.
	ac, err := v.ValidateAccessToken(pair.AccessToken)
	require.NoError(t, err)
	require.Equal(t, int64(7711), ac.UserID)

	rc, err := v.ValidateRefreshToken(pair.RefreshToken)
	require.NoError(t, err, "refresh must verify locally — this is the bug we are fixing")
	require.Equal(t, TokenTypeRefresh, rc.TokenType)
}

// Dual-accept: while migrating, legacy HS256 tokens must still verify.
func TestLocalRS256Verifier_DualAcceptHS256(t *testing.T) {
	s := newTestSigner(t, "kid-1")
	v, err := NewLocalRS256Verifier(
		Config{Issuer: "test-issuer", AcceptHS256: true, SecretKey: testSecret},
		map[string]crypto.PublicKey{s.KeyID(): s.PublicKey()})
	require.NoError(t, err)

	m, err := NewManager(Config{SecretKey: testSecret, AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour, Issuer: "test-issuer"})
	require.NoError(t, err)
	hsPair, err := m.GenerateTokenPair(1, "+77000000000", "d")
	require.NoError(t, err)

	_, err = v.ValidateRefreshToken(hsPair.RefreshToken)
	require.NoError(t, err, "legacy HS256 refresh must still verify during dual-accept")
}

// An RS256 token with a kid not in the local set is rejected (no silent accept).
func TestLocalRS256Verifier_RejectsUnknownKID(t *testing.T) {
	known := newTestSigner(t, "known-kid")
	other := newTestSigner(t, "rogue-kid")
	v, err := NewLocalRS256Verifier(Config{Issuer: "test-issuer"},
		map[string]crypto.PublicKey{known.KeyID(): known.PublicKey()})
	require.NoError(t, err)

	pair, err := other.GenerateTokenPair(9, "+77000000001", "d")
	require.NoError(t, err)
	_, err = v.ValidateAccessToken(pair.AccessToken)
	require.Error(t, err)
}

// Algorithm confusion: with HS256 disabled, an HS256-signed token is rejected
// (the RSA key is never handed to an HMAC token, nor vice versa).
func TestLocalRS256Verifier_RejectsHS256WhenDisabled(t *testing.T) {
	s := newTestSigner(t, "kid-1")
	v, err := NewLocalRS256Verifier(Config{Issuer: "test-issuer"}, // AcceptHS256 not set
		map[string]crypto.PublicKey{s.KeyID(): s.PublicKey()})
	require.NoError(t, err)

	m, err := NewManager(Config{SecretKey: testSecret, AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour, Issuer: "test-issuer"})
	require.NoError(t, err)
	hsPair, err := m.GenerateTokenPair(1, "+77000000000", "d")
	require.NoError(t, err)

	_, err = v.ValidateAccessToken(hsPair.AccessToken)
	require.Error(t, err, "HS256 must be rejected when dual-accept is off")
}

// Misconfiguration: no RS256 key and no HS256 dual-accept => construction fails.
func TestLocalRS256Verifier_RequiresAPath(t *testing.T) {
	_, err := NewLocalRS256Verifier(Config{}, nil)
	require.Error(t, err)
}

// A rotation-friendly key set (current + previous) verifies tokens from both.
func TestLocalRS256Verifier_MultipleKeys(t *testing.T) {
	cur := newTestSigner(t, "cur")
	prev := newTestSigner(t, "prev")
	v, err := NewLocalRS256Verifier(Config{Issuer: "test-issuer"}, map[string]crypto.PublicKey{
		cur.KeyID():  cur.PublicKey(),
		prev.KeyID(): prev.PublicKey(),
	})
	require.NoError(t, err)

	for _, s := range []*Signer{cur, prev} {
		tok, _, err := s.GenerateAccessToken(5, "+77000000002", "d")
		require.NoError(t, err)
		_, err = v.ValidateAccessToken(tok)
		require.NoError(t, err)
	}
}
