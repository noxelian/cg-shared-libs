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
	"testing"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSecret = "test-secret-key-exactly-32-chars" //nolint:gosec // gitleaks:allow -- deterministic test fixture

func newTestSigner(t *testing.T, kid string) *Signer {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	require.NoError(t, err)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	s, err := NewSigner(Config{
		PrivateKeyPEM:   string(pemBytes),
		SigningKeyID:    kid,
		Issuer:          "test-issuer",
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 720 * time.Hour,
	})
	require.NoError(t, err)
	return s
}

func jwksServer(t *testing.T, s *Signer) *httptest.Server {
	t.Helper()
	body, err := s.JWKSJSON()
	require.NoError(t, err)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
}

func TestSigner_RS256Roundtrip(t *testing.T) {
	s := newTestSigner(t, "kid-xyz")
	srv := jwksServer(t, s)
	defer srv.Close()

	v, err := NewValidator(Config{JWKSURL: srv.URL, AcceptHS256: false, JWKSRefresh: time.Hour, JWKSTimeout: 2 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, v.Close()) })

	pair, err := s.GenerateTokenPairWithContext(123, "+77001234567", "device-001", AppContext{App: "partner", OrgID: "org-1", OrgRole: "owner"})
	require.NoError(t, err)

	claims, err := v.ValidateAccessToken(pair.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, int64(123), claims.UserID)
	assert.Equal(t, "+77001234567", claims.Phone)
	assert.Equal(t, "partner", claims.App)
	assert.Equal(t, "org-1", claims.OrgID)
	assert.Equal(t, "owner", claims.OrgRole)
	assert.Equal(t, "test-issuer", claims.Issuer)

	// Header must carry alg=RS256 + the kid.
	parts := strings.Split(pair.AccessToken, ".")
	require.Len(t, parts, 3)
	hdrJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	require.NoError(t, err)
	var hdr map[string]any
	require.NoError(t, json.Unmarshal(hdrJSON, &hdr))
	assert.Equal(t, "RS256", hdr["alg"])
	assert.Equal(t, "kid-xyz", hdr["kid"])

	// Refresh self-verifies (no JWKS dependency) and mints a fresh pair.
	np, err := s.Refresh(pair.RefreshToken)
	require.NoError(t, err)
	assert.NotEmpty(t, np.AccessToken)
	_, err = v.ValidateAccessToken(np.AccessToken)
	require.NoError(t, err)

	// Token-type guards hold under RS256.
	_, err = v.ValidateAccessToken(pair.RefreshToken)
	assert.ErrorIs(t, err, ErrWrongTokenType)
	_, err = v.ValidateRefreshToken(pair.AccessToken)
	assert.ErrorIs(t, err, ErrWrongTokenType)
}

func TestValidator_RejectsHS256WhenDisabled(t *testing.T) {
	s := newTestSigner(t, "kid-1")
	srv := jwksServer(t, s)
	defer srv.Close()

	v, err := NewValidator(Config{JWKSURL: srv.URL, AcceptHS256: false, JWKSRefresh: time.Hour, JWKSTimeout: 2 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, v.Close()) })

	m, err := NewManager(Config{SecretKey: testSecret, AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour, Issuer: "test-issuer"})
	require.NoError(t, err)
	hs, err := m.GenerateTokenPair(1, "+7", "d")
	require.NoError(t, err)

	_, err = v.ValidateAccessToken(hs.AccessToken)
	assert.Error(t, err, "HS256 must be rejected once dual-accept is off")

	// RS256 from the issuer is accepted.
	at, _, err := s.GenerateAccessToken(2, "+7", "d")
	require.NoError(t, err)
	claims, err := v.ValidateAccessToken(at)
	require.NoError(t, err)
	assert.Equal(t, int64(2), claims.UserID)
}

func TestValidator_DualAccept(t *testing.T) {
	s := newTestSigner(t, "kid-1")
	srv := jwksServer(t, s)
	defer srv.Close()

	v, err := NewValidator(Config{JWKSURL: srv.URL, AcceptHS256: true, SecretKey: testSecret, JWKSRefresh: time.Hour, JWKSTimeout: 2 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, v.Close()) })

	m, err := NewManager(Config{SecretKey: testSecret, AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour, Issuer: "test-issuer"})
	require.NoError(t, err)

	hs, err := m.GenerateTokenPair(7, "+7", "d")
	require.NoError(t, err)
	c1, err := v.ValidateAccessToken(hs.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, int64(7), c1.UserID)

	rs, _, err := s.GenerateAccessToken(8, "+8", "d")
	require.NoError(t, err)
	c2, err := v.ValidateAccessToken(rs)
	require.NoError(t, err)
	assert.Equal(t, int64(8), c2.UserID)
}

// TestValidator_AlgorithmConfusionBlocked forges an HS256 token using the RSA
// PUBLIC key bytes as the HMAC secret (the classic confusion attack). The
// validator must reject it: it verifies HS256 only against the real shared
// secret, never against the public key.
func TestValidator_AlgorithmConfusionBlocked(t *testing.T) {
	s := newTestSigner(t, "kid-1")
	srv := jwksServer(t, s)
	defer srv.Close()

	v, err := NewValidator(Config{JWKSURL: srv.URL, AcceptHS256: true, SecretKey: testSecret, JWKSRefresh: time.Hour, JWKSTimeout: 2 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, v.Close()) })

	pubDER, err := x509.MarshalPKIXPublicKey(s.PublicKey())
	require.NoError(t, err)

	claims, _ := buildClaims(999, "+770", "dev", AppContext{}, time.Minute, TokenTypeAccess, "test-issuer")
	tok := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims)
	forged, err := tok.SignedString(pubDER)
	require.NoError(t, err)

	_, err = v.ValidateAccessToken(forged)
	assert.Error(t, err, "HS256 token signed with the RSA public key must not verify")
}

func TestNewValidator_RequiresVerificationPath(t *testing.T) {
	_, err := NewValidator(Config{AcceptHS256: false})
	assert.Error(t, err)
}

func TestNewValidator_DegradedWhenJWKSDownButHS256On(t *testing.T) {
	// Unreachable JWKS but HS256 still accepted -> construction succeeds (degraded).
	v, err := NewValidator(Config{JWKSURL: "http://127.0.0.1:1/jwks", AcceptHS256: true, SecretKey: testSecret, JWKSRefresh: time.Hour, JWKSTimeout: 200 * time.Millisecond})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, v.Close()) })

	m, err := NewManager(Config{SecretKey: testSecret, AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour, Issuer: "i"})
	require.NoError(t, err)
	hs, err := m.GenerateTokenPair(5, "+7", "d")
	require.NoError(t, err)
	_, err = v.ValidateAccessToken(hs.AccessToken)
	require.NoError(t, err)
}

func TestNewValidator_FatalWhenJWKSDownAndHS256Off(t *testing.T) {
	_, err := NewValidator(Config{JWKSURL: "http://127.0.0.1:1/jwks", AcceptHS256: false, JWKSTimeout: 200 * time.Millisecond})
	require.Error(t, err)
}

func TestNewSigner_Errors(t *testing.T) {
	_, err := NewSigner(Config{})
	assert.Error(t, err, "empty PEM must error")

	_, err = NewSigner(Config{PrivateKeyPEM: "not-a-pem", SigningKeyID: "k"})
	assert.Error(t, err, "bad PEM must error")

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	require.NoError(t, err)
	pemb := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	_, err = NewSigner(Config{PrivateKeyPEM: string(pemb)})
	assert.Error(t, err, "missing kid must error")
}

func TestSigner_JWKSJSONRoundtrip(t *testing.T) {
	s := newTestSigner(t, "kid-1")
	body, err := s.JWKSJSON()
	require.NoError(t, err)

	keys, err := parseJWKS(body)
	require.NoError(t, err)
	pk := keys["kid-1"]
	require.NotNil(t, pk)
	assert.Equal(t, 0, pk.N.Cmp(s.PublicKey().N))
	assert.Equal(t, s.PublicKey().E, pk.E)
}

func TestJWKSCache_RefreshOnUnknownKid(t *testing.T) {
	s1 := newTestSigner(t, "kid-1")
	s2 := newTestSigner(t, "kid-2")
	current := s1
	fetch := func(_ context.Context) ([]byte, error) { return current.JWKSJSON() }

	c := newJWKSCacheWithFetcher(fetch, time.Hour, time.Second)
	require.NoError(t, c.start(context.Background()))
	defer c.Close()

	_, err := c.publicKey("kid-1")
	require.NoError(t, err)

	// Rotate the issuer's published key. Within the rate-limit window the new
	// kid is not yet fetched.
	current = s2
	_, err = c.publicKey("kid-2")
	assert.ErrorIs(t, err, ErrUnknownKID)

	// Age the cache past the rate limit -> refresh-on-unknown-kid resolves it.
	// (lastAttempt drives the limiter, not lastFetch.)
	c.mu.Lock()
	c.lastAttempt = time.Now().Add(-2 * minRefetchInterval)
	c.mu.Unlock()

	pk, err := c.publicKey("kid-2")
	require.NoError(t, err)
	require.NotNil(t, pk)
	rsaPub, ok := pk.(*rsa.PublicKey)
	require.True(t, ok)
	assert.Equal(t, 0, rsaPub.N.Cmp(s2.PublicKey().N))
}

func TestJWKSCache_LastKnownGoodOnRefreshFailure(t *testing.T) {
	s := newTestSigner(t, "kid-1")
	fail := false
	fetch := func(_ context.Context) ([]byte, error) {
		if fail {
			return nil, assertErr{}
		}
		return s.JWKSJSON()
	}
	c := newJWKSCacheWithFetcher(fetch, time.Hour, time.Second)
	require.NoError(t, c.start(context.Background()))
	defer c.Close()

	// Subsequent fetches fail, but the known kid is still served from cache.
	fail = true
	require.Error(t, c.refreshNow(context.Background()))
	pk, err := c.publicKey("kid-1")
	require.NoError(t, err)
	require.NotNil(t, pk)
}

func TestJWKSCache_FailsClosedBeyondMaxStale(t *testing.T) {
	s := newTestSigner(t, "kid-1")
	fail := false
	fetch := func(_ context.Context) ([]byte, error) {
		if fail {
			return nil, assertErr{}
		}
		return s.JWKSJSON()
	}
	maxStale := time.Hour
	c := newJWKSCacheWithFetcherMaxStale(fetch, time.Hour, time.Second, maxStale)
	require.NoError(t, c.start(context.Background()))
	defer c.Close()

	fail = true
	c.mu.Lock()
	c.lastFetch = time.Now().Add(-2 * maxStale)
	c.lastAttempt = time.Now().Add(-2 * minRefetchInterval)
	c.mu.Unlock()

	_, err := c.publicKey("kid-1")
	require.ErrorIs(t, err, ErrStaleJWKS)
}

type assertErr struct{}

func (assertErr) Error() string { return "synthetic fetch failure" }
