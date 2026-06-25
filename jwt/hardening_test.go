package jwt

import (
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewVerifier_PicksImplFromConfig confirms the config-driven constructor
// returns *Validator when a JWKS URL is set and *Manager otherwise — the lever
// that makes the 17-service rollout a uniform one-liner.
func TestNewVerifier_PicksImplFromConfig(t *testing.T) {
	s := newTestSigner(t, "kid-1")
	srv := jwksServer(t, s)
	defer srv.Close()

	v, err := NewVerifier(Config{JWKSURL: srv.URL, AcceptHS256: false, JWKSTimeout: 2 * time.Second})
	require.NoError(t, err)
	defer v.Close()
	_, isValidator := v.(*Validator)
	assert.True(t, isValidator, "JWKSURL set must yield a *Validator")
	at, _, err := s.GenerateAccessToken(1, "+7", "d")
	require.NoError(t, err)
	_, err = v.ValidateAccessToken(at)
	require.NoError(t, err)

	m, err := NewVerifier(Config{SecretKey: testSecret, AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour, Issuer: "i"})
	require.NoError(t, err)
	defer m.Close()
	_, isManager := m.(*Manager)
	assert.True(t, isManager, "no JWKSURL must yield a legacy *Manager")
}

// TestValidator_ExpectedIssuer covers the opt-in iss check: enforced only when
// ExpectedIssuer is set (so the dual-accept window with non-uniform issuers is
// not broken when it is left empty).
func TestValidator_ExpectedIssuer(t *testing.T) {
	s := newTestSigner(t, "kid-1") // signs with issuer "test-issuer"
	srv := jwksServer(t, s)
	defer srv.Close()
	at, _, err := s.GenerateAccessToken(1, "+7", "d")
	require.NoError(t, err)

	vOK, err := NewValidator(Config{JWKSURL: srv.URL, ExpectedIssuer: "test-issuer", JWKSTimeout: 2 * time.Second})
	require.NoError(t, err)
	defer vOK.Close()
	_, err = vOK.ValidateAccessToken(at)
	require.NoError(t, err, "matching issuer must pass")

	vBad, err := NewValidator(Config{JWKSURL: srv.URL, ExpectedIssuer: "someone-else", JWKSTimeout: 2 * time.Second})
	require.NoError(t, err)
	defer vBad.Close()
	_, err = vBad.ValidateAccessToken(at)
	assert.Error(t, err, "mismatched issuer must be rejected")

	vNone, err := NewValidator(Config{JWKSURL: srv.URL, JWKSTimeout: 2 * time.Second})
	require.NoError(t, err)
	defer vNone.Close()
	_, err = vNone.ValidateAccessToken(at)
	require.NoError(t, err, "empty ExpectedIssuer must NOT enforce iss (dual-accept safety)")
}

func TestNewValidator_RejectsBadJWKSURL(t *testing.T) {
	_, err := NewValidator(Config{JWKSURL: "ftp://evil/jwks", AcceptHS256: false})
	assert.Error(t, err, "non-http(s) scheme must be rejected")

	_, err = NewValidator(Config{JWKSURL: "https:///nohost", AcceptHS256: false})
	assert.Error(t, err, "missing host must be rejected")
}

func TestSigner_PublicKeyDefensiveCopy(t *testing.T) {
	s := newTestSigner(t, "kid-1")
	orig := new(big.Int).Set(s.PublicKey().N)

	pub := s.PublicKey()
	pub.N.SetInt64(0) // mutate the returned copy

	assert.Equal(t, 0, s.PublicKey().N.Cmp(orig), "signer's internal key must be immune to caller mutation")
}
