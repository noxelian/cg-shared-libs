package jwt

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func decodePayload(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}

func keysOf(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

// TestValidator_RejectsNoneAlg ensures unsigned ("none") tokens never validate —
// the WithValidMethods allowlist rejects them before the keyfunc even runs.
func TestValidator_RejectsNoneAlg(t *testing.T) {
	s := newTestSigner(t, "kid-1")
	srv := jwksServer(t, s)
	defer srv.Close()
	v, err := NewValidator(Config{JWKSURL: srv.URL, AcceptHS256: true, SecretKey: testSecret, JWKSRefresh: time.Hour, JWKSTimeout: 2 * time.Second})
	require.NoError(t, err)
	defer v.Close()

	claims, _ := buildClaims(1, "+7", "d", AppContext{}, time.Minute, TokenTypeAccess, "test-issuer")
	tok := gojwt.NewWithClaims(gojwt.SigningMethodNone, claims)
	noneToken, err := tok.SignedString(gojwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	_, err = v.ValidateAccessToken(noneToken)
	assert.Error(t, err, "alg=none must be rejected")
}

// TestValidator_KeyfuncBranches exercises every rejection branch of buildKeyFunc.
func TestValidator_KeyfuncBranches(t *testing.T) {
	s := newTestSigner(t, "kid-1")
	srv := jwksServer(t, s)
	defer srv.Close()
	v, err := NewValidator(Config{JWKSURL: srv.URL, AcceptHS256: false, JWKSRefresh: time.Hour, JWKSTimeout: 2 * time.Second})
	require.NoError(t, err)
	defer v.Close()

	claims, _ := buildClaims(1, "+7", "d", AppContext{}, time.Minute, TokenTypeAccess, "test-issuer")
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	t.Run("rs256_missing_kid", func(t *testing.T) {
		tok := gojwt.NewWithClaims(gojwt.SigningMethodRS256, claims) // no kid header
		str, err := tok.SignedString(otherKey)
		require.NoError(t, err)
		_, err = v.ValidateAccessToken(str)
		assert.Error(t, err)
	})

	t.Run("rs256_unknown_kid", func(t *testing.T) {
		tok := gojwt.NewWithClaims(gojwt.SigningMethodRS256, claims)
		tok.Header["kid"] = "does-not-exist"
		str, err := tok.SignedString(otherKey)
		require.NoError(t, err)
		_, err = v.ValidateAccessToken(str)
		assert.Error(t, err)
	})

	t.Run("rs384_unexpected_alg", func(t *testing.T) {
		tok := gojwt.NewWithClaims(gojwt.SigningMethodRS384, claims)
		tok.Header["kid"] = "kid-1"
		str, err := tok.SignedString(otherKey)
		require.NoError(t, err)
		_, err = v.ValidateAccessToken(str)
		assert.Error(t, err, "RS384 must be rejected (only RS256 pinned)")
	})

	t.Run("rs256_but_no_jwks_configured", func(t *testing.T) {
		// HS256-only validator (no JWKSURL) receiving an RS256 token.
		vHS, err := NewValidator(Config{AcceptHS256: true, SecretKey: testSecret})
		require.NoError(t, err)
		defer vHS.Close()
		at, _, err := s.GenerateAccessToken(1, "+7", "d")
		require.NoError(t, err)
		_, err = vHS.ValidateAccessToken(at)
		assert.Error(t, err, "RS256 token with no JWKS configured must be rejected")
	})
}

func TestValidator_ExpiredRS256(t *testing.T) {
	s := newTestSigner(t, "kid-1")
	srv := jwksServer(t, s)
	defer srv.Close()
	v, err := NewValidator(Config{JWKSURL: srv.URL, AcceptHS256: false, JWKSRefresh: time.Hour, JWKSTimeout: 2 * time.Second})
	require.NoError(t, err)
	defer v.Close()

	// Craft an already-expired RS256 token deterministically (no sleep) by
	// signing past timestamps with the signer's own key.
	now := time.Now()
	claims := Claims{
		UserID:    1,
		TokenType: TokenTypeAccess,
		RegisteredClaims: gojwt.RegisteredClaims{
			ExpiresAt: gojwt.NewNumericDate(now.Add(-time.Hour)),
			IssuedAt:  gojwt.NewNumericDate(now.Add(-2 * time.Hour)),
			Issuer:    "test-issuer",
		},
	}
	tok := gojwt.NewWithClaims(gojwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "kid-1"
	str, err := tok.SignedString(s.priv)
	require.NoError(t, err)

	_, err = v.ValidateAccessToken(str)
	assert.ErrorIs(t, err, ErrTokenExpired)
}

func TestValidator_ValidateRefreshTokenRS256(t *testing.T) {
	s := newTestSigner(t, "kid-1")
	srv := jwksServer(t, s)
	defer srv.Close()
	v, err := NewValidator(Config{JWKSURL: srv.URL, AcceptHS256: false, JWKSRefresh: time.Hour, JWKSTimeout: 2 * time.Second})
	require.NoError(t, err)
	defer v.Close()

	pair, err := s.GenerateTokenPair(55, "+7", "d")
	require.NoError(t, err)

	claims, err := v.ValidateRefreshToken(pair.RefreshToken)
	require.NoError(t, err)
	assert.Equal(t, int64(55), claims.UserID)

	_, err = v.ValidateRefreshToken(pair.AccessToken)
	assert.ErrorIs(t, err, ErrWrongTokenType)
}

func TestSigner_RefreshWrongType(t *testing.T) {
	s := newTestSigner(t, "kid-1")
	at, _, err := s.GenerateAccessToken(1, "+7", "d")
	require.NoError(t, err)
	_, err = s.Refresh(at) // an access token is not a refresh token
	assert.ErrorIs(t, err, ErrWrongTokenType)
}

func TestNewSigner_PKCS1(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der := x509.MarshalPKCS1PrivateKey(priv)
	pemb := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})

	s, err := NewSigner(Config{PrivateKeyPEM: string(pemb), SigningKeyID: "kid-1", Issuer: "i"})
	require.NoError(t, err)
	require.NotNil(t, s)
	at, _, err := s.GenerateAccessToken(1, "+7", "d")
	require.NoError(t, err)
	assert.NotEmpty(t, at)
}

func TestParseJWKS_Errors(t *testing.T) {
	_, err := parseJWKS([]byte("not json"))
	assert.Error(t, err)
	_, err = parseJWKS([]byte(`{"keys":[]}`))
	assert.Error(t, err, "empty key set must error")
	_, err = parseJWKS([]byte(`{"keys":[{"kty":"RSA","kid":"x","n":"!!!bad","e":"AQAB"}]}`))
	assert.Error(t, err, "bad base64 modulus must error")
}

func TestJWKSCache_HTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := newJWKSCache(srv.URL, time.Hour, time.Second, &http.Client{Timeout: time.Second})
	defer c.Close()
	err := c.refreshNow(context.Background())
	assert.Error(t, err, "non-200 JWKS response must error")
}

// TestClaimsIdentity_ManagerVsSigner proves design intent #5: HS256 (Manager)
// and RS256 (Signer) produce identical claim sets (so service tokens look the
// same pre/post migration). Time-based iat/exp are excluded.
func TestClaimsIdentity_ManagerVsSigner(t *testing.T) {
	m, err := NewManager(Config{SecretKey: testSecret, AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: 720 * time.Hour, Issuer: "test-issuer"})
	require.NoError(t, err)
	s := newTestSigner(t, "kid-1")

	hsPair, err := m.GenerateTokenPair(42, "+77", "dev")
	require.NoError(t, err)
	rsTok, _, err := s.GenerateAccessToken(42, "+77", "dev")
	require.NoError(t, err)

	hs := decodePayload(t, hsPair.AccessToken)
	rs := decodePayload(t, rsTok)

	assert.ElementsMatch(t, keysOf(hs), keysOf(rs), "claim key sets must match")
	for k := range hs {
		if k == "iat" || k == "exp" {
			continue
		}
		assert.Equal(t, hs[k], rs[k], "claim %q must be identical across HS256/RS256", k)
	}
}

// TestJWKSCache_RateLimitedDuringOutage is the regression guard for the DoS
// amplification fix: while the JWKS endpoint is down, unknown-kid lookups must
// be throttled by lastAttempt (which advances even on failed refreshes), not
// hammer the dead endpoint on every request.
func TestJWKSCache_RateLimitedDuringOutage(t *testing.T) {
	var calls atomic.Int32
	fetch := func(_ context.Context) ([]byte, error) {
		calls.Add(1)
		return nil, assertErr{}
	}
	c := newJWKSCacheWithFetcher(fetch, time.Hour, 200*time.Millisecond)
	_ = c.start(context.Background()) // preload fails -> calls=1, lastAttempt set
	defer c.Close()

	for range 50 {
		_, err := c.publicKey("any")
		assert.ErrorIs(t, err, ErrUnknownKID)
	}
	assert.Equal(t, int32(1), calls.Load(), "must not re-fetch within the rate-limit window during an outage")

	// After the window elapses, exactly one more attempt is permitted.
	c.mu.Lock()
	c.lastAttempt = time.Now().Add(-2 * minRefetchInterval)
	c.mu.Unlock()
	_, _ = c.publicKey("any")
	assert.Equal(t, int32(2), calls.Load())
}

// TestJWKSCache_ConcurrentUnknownKid stresses the on-demand refresh path under
// the race detector (go test -race).
func TestJWKSCache_ConcurrentUnknownKid(t *testing.T) {
	s := newTestSigner(t, "kid-1")
	fetch := func(_ context.Context) ([]byte, error) { return s.JWKSJSON() }
	c := newJWKSCacheWithFetcher(fetch, time.Hour, time.Second)
	require.NoError(t, c.start(context.Background()))
	defer c.Close()

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			_, _ = c.publicKey("unknown")
			_, _ = c.publicKey("kid-1")
		})
	}
	wg.Wait()
}

// TestNewValidator_HS256NoSecretWithJWKS confirms the effective-acceptHS256 fix:
// AcceptHS256=true but no secret + a JWKS => the validator becomes RS256-only
// (validMethods drops HS256) instead of silently pretending to dual-accept.
func TestNewValidator_HS256NoSecretWithJWKS(t *testing.T) {
	s := newTestSigner(t, "kid-1")
	srv := jwksServer(t, s)
	defer srv.Close()
	v, err := NewValidator(Config{JWKSURL: srv.URL, AcceptHS256: true, SecretKey: "", JWKSRefresh: time.Hour, JWKSTimeout: 2 * time.Second})
	require.NoError(t, err)
	defer v.Close()

	assert.False(t, v.acceptHS256, "no secret => effective acceptHS256 must be false")
	assert.Equal(t, []string{"RS256"}, v.validMethods, "validMethods must not advertise HS256 without a key")

	// RS256 still works.
	at, _, err := s.GenerateAccessToken(1, "+7", "d")
	require.NoError(t, err)
	_, err = v.ValidateAccessToken(at)
	require.NoError(t, err)
}
