package jwt

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

type countingMinter struct {
	calls       int
	lastSubject string
	lastDevice  string
	lastUserID  int64
	exp         time.Time
	err         error
}

func (m *countingMinter) Mint(_ context.Context, subject, deviceID string, actAsUserID int64) (string, time.Time, error) {
	m.calls++
	m.lastSubject, m.lastDevice, m.lastUserID = subject, deviceID, actAsUserID
	if m.err != nil {
		return "", time.Time{}, m.err
	}
	return fmt.Sprintf("tok-%d", m.calls), m.exp, nil
}

func TestServiceTokenProvider_Caches(t *testing.T) {
	now := time.Unix(1000, 0)
	m := &countingMinter{exp: now.Add(15 * time.Minute)}
	p := NewServiceTokenProvider(m, "cg-orders", withNow(func() time.Time { return now }))

	for range 5 {
		tok, err := p.Token(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "tok-1", tok)
	}
	assert.Equal(t, 1, m.calls, "valid token must be served from cache")
	assert.Equal(t, "cg-orders", m.lastSubject)
	assert.Equal(t, "cg-orders", m.lastDevice, "deviceID defaults to subject")
	assert.Equal(t, int64(0), m.lastUserID)
}

func TestServiceTokenProvider_RefreshesNearExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	cur := now
	m := &countingMinter{exp: now.Add(time.Minute)}
	p := NewServiceTokenProvider(m, "svc", withNow(func() time.Time { return cur }), WithRefreshBuffer(30*time.Second))

	_, err := p.Token(context.Background()) // calls=1, exp = now+60s
	require.NoError(t, err)

	// Advance to within the 30s refresh buffer (exp-now = 20s).
	cur = now.Add(40 * time.Second)
	m.exp = cur.Add(time.Minute)

	tok, err := p.Token(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "tok-2", tok)
	assert.Equal(t, 2, m.calls)
}

func TestServiceTokenProvider_Options(t *testing.T) {
	now := time.Unix(1000, 0)
	m := &countingMinter{exp: now.Add(time.Hour)}
	p := NewServiceTokenProvider(m, "cg-crm",
		withNow(func() time.Time { return now }),
		WithActAsUserID(42),
		WithDeviceID("dev-x"),
	)

	_, err := p.Token(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(42), m.lastUserID)
	assert.Equal(t, "dev-x", m.lastDevice)
}

func TestServiceTokenProvider_Error(t *testing.T) {
	now := time.Unix(1000, 0)
	m := &countingMinter{err: errors.New("mint boom")}
	p := NewServiceTokenProvider(m, "svc", withNow(func() time.Time { return now }))

	_, err := p.Token(context.Background())
	require.Error(t, err)
}

// TestLocalMinter_WithManager proves the provider produces a valid token through
// a real in-process *Manager, and that the s2s claim shape (userID=0, phone=subject)
// is preserved.
func TestLocalMinter_WithManager(t *testing.T) {
	mgr, err := NewManager(Config{SecretKey: testSecret, AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: time.Hour, Issuer: "test-issuer"})
	require.NoError(t, err)

	p := NewServiceTokenProvider(LocalMinter(mgr), "cg-orders")
	tok, err := p.Token(context.Background())
	require.NoError(t, err)

	claims, err := mgr.ValidateAccessToken(tok)
	require.NoError(t, err)
	assert.Equal(t, int64(0), claims.UserID)
	assert.Equal(t, "cg-orders", claims.Phone)
	assert.Equal(t, "cg-orders", claims.DeviceID)
}

func TestServiceTokenProvider_InjectAuth(t *testing.T) {
	now := time.Unix(1000, 0)
	m := &countingMinter{exp: now.Add(time.Hour)}
	p := NewServiceTokenProvider(m, "svc", withNow(func() time.Time { return now }))

	ctx, err := p.InjectAuth(context.Background())
	require.NoError(t, err)
	md, ok := metadata.FromOutgoingContext(ctx)
	require.True(t, ok)
	assert.Equal(t, []string{"Bearer tok-1"}, md.Get("authorization"))
}

func TestServiceTokenProvider_Concurrent(t *testing.T) {
	now := time.Unix(1000, 0)
	m := &countingMinter{exp: now.Add(time.Hour)}
	p := NewServiceTokenProvider(m, "svc", withNow(func() time.Time { return now }))

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() { _, _ = p.Token(context.Background()) })
	}
	wg.Wait()
	assert.Equal(t, 1, m.calls, "concurrent callers must share a single in-flight mint")
}
