package jwt

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"
)

// ErrUnknownKID is returned when a token references a key id absent from the JWKS.
var ErrUnknownKID = errors.New("jwt: unknown key id")

const (
	// minRefetchInterval rate-limits refresh-on-unknown-kid so a flood of
	// forged kids cannot turn into a fetch storm against the issuer.
	minRefetchInterval = 30 * time.Second
	// maxJWKSBytes caps the JWKS response we will read.
	maxJWKSBytes = 1 << 20
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

	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	lastFetch time.Time

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
func (c *jwksCache) publicKey(kid string) (*rsa.PublicKey, error) {
	c.mu.RLock()
	key := c.keys[kid]
	last := c.lastFetch
	c.mu.RUnlock()
	if key != nil {
		return key, nil
	}

	if time.Since(last) > minRefetchInterval {
		if err := c.refreshNow(context.Background()); err == nil {
			c.mu.RLock()
			key = c.keys[kid]
			c.mu.RUnlock()
			if key != nil {
				return key, nil
			}
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
	for _, k := range set.Keys {
		if k.Kty != "RSA" {
			continue // we only sign with RSA; ignore unrelated keys
		}
		pub, err := parseRSAJWK(k)
		if err != nil {
			return nil, err
		}
		out[k.Kid] = pub
	}
	if len(out) == 0 {
		return nil, errors.New("no RSA keys present")
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
	if e == 0 {
		return nil, errors.New("zero RSA exponent")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}, nil
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
