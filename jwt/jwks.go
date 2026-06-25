package jwt

import (
	"context"
	"crypto"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// validateJWKSURL rejects a JWKS URL that is unparseable or not http(s). The
// URL should come from trusted config (Helm/k8s), never user input; this is a
// guard against gross misconfiguration, not a substitute for that trust.
func validateJWKSURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("jwt: invalid JWKS URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("jwt: JWKS URL must be http(s), got %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("jwt: JWKS URL has no host")
	}
	return nil
}

const (
	// minRefetchInterval rate-limits refresh-on-unknown-kid so a flood of
	// forged kids cannot turn into a fetch storm against the issuer.
	minRefetchInterval = 30 * time.Second
	// maxJWKSBytes caps the JWKS response we will read.
	maxJWKSBytes = 1 << 20
	// maxRSAModulusBits caps a published key's modulus (RSA-8192) to bound
	// verify cost against a pathologically large key.
	maxRSAModulusBits = 8192
)

// jwkKey / jwkSet model the subset of RFC 7517 we need (RSA signing keys).
type jwkKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use,omitempty"`
	Alg string `json:"alg,omitempty"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwkSet struct {
	Keys []jwkKey `json:"keys"`
}

// jwksFetcher returns raw JWKS bytes. Pluggable so tests need no network.
type jwksFetcher func(ctx context.Context) ([]byte, error)

// jwksCache holds RSA public keys by kid, refreshed in the background.
//
// Availability design: keys are kept until a refresh SUCCEEDS — a failed
// refresh never clears the cache, so a cg-users JWKS outage does not break
// verification of already-known keys (last-known-good). An unknown kid triggers
// at most one rate-limited re-fetch (kid rotation works without redeploy).
type jwksCache struct {
	fetch        jwksFetcher
	refreshEvery time.Duration
	timeout      time.Duration

	mu          sync.RWMutex
	keys        map[string]*rsa.PublicKey
	lastFetch   time.Time // last SUCCESSFUL refresh
	lastAttempt time.Time // last refresh ATTEMPT (success or failure) — drives the rate limiter

	refreshMu sync.Mutex // serializes on-demand (unknown-kid) refreshes
	stop      chan struct{}
	startOnce sync.Once
	stopOnce  sync.Once
}

// newJWKSCache builds a cache backed by an HTTP GET against url.
func newJWKSCache(url string, refresh, timeout time.Duration, client *http.Client) *jwksCache {
	fetch := func(ctx context.Context) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("jwks: unexpected status %d", resp.StatusCode)
		}
		return io.ReadAll(io.LimitReader(resp.Body, maxJWKSBytes))
	}
	return newJWKSCacheWithFetcher(fetch, refresh, timeout)
}

// newJWKSCacheWithFetcher builds a cache from a custom fetcher (used in tests).
func newJWKSCacheWithFetcher(fetch jwksFetcher, refresh, timeout time.Duration) *jwksCache {
	return &jwksCache{
		fetch:        fetch,
		refreshEvery: refresh,
		timeout:      timeout,
		keys:         map[string]*rsa.PublicKey{},
		stop:         make(chan struct{}),
	}
}

// start performs a synchronous preload and launches the background refresher.
// The preload error is returned so the caller can decide whether to degrade
// (HS256 still accepted) or fail fast.
func (c *jwksCache) start(ctx context.Context) error {
	err := c.refreshNow(ctx)
	c.startOnce.Do(func() { go c.loop() })
	return err
}

func (c *jwksCache) loop() {
	t := time.NewTicker(c.refreshEvery)
	defer t.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-t.C:
			// Background refresh: on failure we keep the existing keys.
			_ = c.refreshNow(context.Background())
		}
	}
}

// Close stops the background refresher. Safe to call multiple times.
func (c *jwksCache) Close() {
	c.stopOnce.Do(func() { close(c.stop) })
}

func (c *jwksCache) refreshNow(ctx context.Context) error {
	// Record the attempt time BEFORE fetching, regardless of outcome. The
	// rate limiter keys off lastAttempt (not lastFetch) so it keeps throttling
	// during a JWKS outage — otherwise lastFetch never advances and every
	// unknown-kid token would trigger a fresh blocking fetch against the dead
	// endpoint (DoS amplification exactly when the issuer is already unhealthy).
	c.mu.Lock()
	c.lastAttempt = time.Now()
	c.mu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	raw, err := c.fetch(cctx)
	if err != nil {
		return fmt.Errorf("jwks: fetch: %w", err)
	}
	keys, err := parseJWKS(raw)
	if err != nil {
		return fmt.Errorf("jwks: parse: %w", err)
	}

	c.mu.Lock()
	c.keys = keys
	c.lastFetch = time.Now()
	c.mu.Unlock()
	return nil
}

// publicKey resolves a kid to an RSA public key, refreshing once (rate-limited)
// if the kid is unknown, then falling back to whatever is cached.
func (c *jwksCache) publicKey(kid string) (crypto.PublicKey, error) {
	c.mu.RLock()
	key := c.keys[kid]
	last := c.lastAttempt
	c.mu.RUnlock()
	if key != nil {
		return key, nil
	}

	if time.Since(last) > minRefetchInterval {
		// Serialize on-demand refreshes so a burst of unknown-kid tokens
		// collapses into a single fetch (no thundering herd at key rotation),
		// and double-check under the lock in case a peer just refreshed.
		c.refreshMu.Lock()
		c.mu.RLock()
		key = c.keys[kid]
		throttled := time.Since(c.lastAttempt) <= minRefetchInterval
		c.mu.RUnlock()
		if key == nil && !throttled {
			_ = c.refreshNow(context.Background())
			c.mu.RLock()
			key = c.keys[kid]
			c.mu.RUnlock()
		}
		c.refreshMu.Unlock()
		if key != nil {
			return key, nil
		}
	}

	return nil, fmt.Errorf("%w: %q", ErrUnknownKID, kid)
}

func parseJWKS(raw []byte) (map[string]*rsa.PublicKey, error) {
	var set jwkSet
	if err := json.Unmarshal(raw, &set); err != nil {
		return nil, err
	}
	out := make(map[string]*rsa.PublicKey, len(set.Keys))
	var skipped []string
	for _, k := range set.Keys {
		if k.Kty != "RSA" {
			skipped = append(skipped, k.Kty) // we only sign with RSA today; ignore others
			continue
		}
		pub, err := parseRSAJWK(k)
		if err != nil {
			return nil, err
		}
		out[k.Kid] = pub
	}
	if len(out) == 0 {
		if len(skipped) > 0 {
			return nil, fmt.Errorf("no RSA keys present; found non-RSA kty %v", skipped)
		}
		return nil, errors.New("no keys present")
	}
	return out, nil
}

func parseRSAJWK(k jwkKey) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("decode modulus: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("decode exponent: %w", err)
	}
	e := 0
	for _, b := range eBytes {
		e = e<<8 | int(b)
	}
	if e < 2 {
		return nil, errors.New("invalid RSA exponent")
	}
	n := new(big.Int).SetBytes(nBytes)
	// Bound the modulus size: a pathologically large modulus would make every
	// RS256 verify expensive. The issuer is trusted and uses RSA-2048, so a
	// generous ceiling (RSA-8192) is plenty.
	if bits := n.BitLen(); bits > maxRSAModulusBits {
		return nil, fmt.Errorf("RSA modulus too large: %d bits", bits)
	}
	return &rsa.PublicKey{N: n, E: e}, nil
}

// rsaPublicToJWK encodes an RSA public key as a JWK with the given kid.
// Used by the issuer (Signer) to publish its JWKS endpoint.
func rsaPublicToJWK(pub *rsa.PublicKey, kid string) jwkKey {
	eBytes := big.NewInt(int64(pub.E)).Bytes()
	return jwkKey{
		Kty: "RSA",
		Kid: kid,
		Use: "sig",
		Alg: "RS256",
		N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(eBytes),
	}
}
