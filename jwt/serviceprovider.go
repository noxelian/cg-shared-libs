package jwt

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc/metadata"
)

// defaultRefreshBuffer mirrors the 30s buffer every hand-rolled service-token
// provider used: refresh slightly before expiry so a token is never handed out
// on the edge of expiring mid-call.
const defaultRefreshBuffer = 30 * time.Second

// TokenMinter abstracts where a service-to-service token comes from. Swapping
// the minter is how a service moves from the dual-accept window (local signing)
// to the central-issuer model (remote IssueServiceToken) WITHOUT touching the
// caching/injection logic — the whole point of this type.
//
//   - LocalMinter wraps an in-process *Manager/*Signer.
//   - A RemoteMinter (a ~10-line per-service shim that calls cg-users
//     IssueServiceToken over mTLS) is supplied by each service, keeping
//     cg-shared-libs free of any cg-proto import.
type TokenMinter interface {
	Mint(ctx context.Context, subject, deviceID string, actAsUserID int64) (token string, expiresAt time.Time, err error)
}

// ServiceTokenProvider caches a single service token and refreshes it within
// RefreshBuffer of expiry. It replaces the per-service hand-rolled providers
// (cg-orders, cg-users/organization, cg-subscriptions×2, cg-parser, …) with one
// tested implementation. Safe for concurrent use; at most one mint is in flight
// at a time (concurrent callers share the result).
type ServiceTokenProvider struct {
	minter      TokenMinter
	subject     string
	deviceID    string
	actAsUserID int64
	buffer      time.Duration
	now         func() time.Time

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

// ServiceTokenOption configures a ServiceTokenProvider.
type ServiceTokenOption func(*ServiceTokenProvider)

// WithDeviceID sets the deviceID claim (defaults to the subject).
func WithDeviceID(deviceID string) ServiceTokenOption {
	return func(p *ServiceTokenProvider) { p.deviceID = deviceID }
}

// WithActAsUserID mints act-as-user tokens (userID > 0). Default 0 = pure
// service token. Only callers explicitly allowed by the issuer may use this.
func WithActAsUserID(userID int64) ServiceTokenOption {
	return func(p *ServiceTokenProvider) { p.actAsUserID = userID }
}

// WithRefreshBuffer overrides how long before expiry the token is refreshed.
func WithRefreshBuffer(d time.Duration) ServiceTokenOption {
	return func(p *ServiceTokenProvider) {
		if d > 0 {
			p.buffer = d
		}
	}
}

// withNow injects a clock (tests only).
func withNow(now func() time.Time) ServiceTokenOption {
	return func(p *ServiceTokenProvider) { p.now = now }
}

// NewServiceTokenProvider builds a cached provider that mints tokens for subject.
func NewServiceTokenProvider(minter TokenMinter, subject string, opts ...ServiceTokenOption) *ServiceTokenProvider {
	p := &ServiceTokenProvider{
		minter:  minter,
		subject: subject,
		buffer:  defaultRefreshBuffer,
		now:     time.Now,
	}
	for _, opt := range opts {
		opt(p)
	}
	if p.deviceID == "" {
		p.deviceID = subject
	}
	return p
}

// Token returns a cached token, minting a fresh one when the current token is
// missing or within RefreshBuffer of expiry.
func (p *ServiceTokenProvider) Token(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.token != "" && p.now().Add(p.buffer).Before(p.expiresAt) {
		return p.token, nil
	}

	token, expiresAt, err := p.minter.Mint(ctx, p.subject, p.deviceID, p.actAsUserID)
	if err != nil {
		return "", fmt.Errorf("jwt: mint service token for %q: %w", p.subject, err)
	}
	p.token = token
	p.expiresAt = expiresAt
	return token, nil
}

// InjectAuth returns ctx with "authorization: Bearer <token>" appended to the
// outgoing gRPC metadata — the single call a downstream client needs.
func (p *ServiceTokenProvider) InjectAuth(ctx context.Context) (context.Context, error) {
	token, err := p.Token(ctx)
	if err != nil {
		return ctx, err
	}
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token), nil
}

// localGenerator is the minting surface shared by *Manager and *Signer.
type localGenerator interface {
	GenerateAccessToken(userID int64, phone, deviceID string) (string, time.Time, error)
}

// localMinter adapts an in-process generator to TokenMinter.
type localMinter struct{ gen localGenerator }

// LocalMinter wraps an in-process *Manager or *Signer so a ServiceTokenProvider
// mints locally (the dual-accept window). Phase 5 swaps this for a remote minter
// that calls cg-users IssueServiceToken; nothing else in the provider changes.
func LocalMinter(gen localGenerator) TokenMinter {
	return &localMinter{gen: gen}
}

func (m *localMinter) Mint(_ context.Context, subject, deviceID string, actAsUserID int64) (string, time.Time, error) {
	return m.gen.GenerateAccessToken(actAsUserID, subject, deviceID)
}
