package httpclient

import (
	"io"
	"net/http"
	"time"

	"github.com/4ubak/cg-shared-libs/logger"
	"go.uber.org/zap"
)

// RetryConfig controls the behaviour of a retryable HTTP client.
//
// All fields default to sensible values when zero:
//   - MaxRetries: 3 (total attempts = 1 + MaxRetries)
//   - BaseDelay : 200ms (first backoff before retry #1)
//   - MaxDelay  : 5s   (cap on per-retry backoff)
//   - Timeout   : 10s  (per-request, applied to underlying http.Client.Timeout)
//
// Retry policy is fixed (see package doc): connect errors, network timeouts,
// HTTP 5xx, HTTP 429 — but never other 4xx.
type RetryConfig struct {
	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
	Timeout    time.Duration
}

func (cfg *RetryConfig) applyDefaults() {
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}
	if cfg.BaseDelay == 0 {
		cfg.BaseDelay = 200 * time.Millisecond
	}
	if cfg.MaxDelay == 0 {
		cfg.MaxDelay = 5 * time.Second
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
}

// retryRoundTripper wraps an underlying http.RoundTripper with exponential-
// backoff retry logic. Body replay is handled via req.GetBody — the Go-
// standard mechanism that http.NewRequest sets automatically for body types
// bytes.NewReader / bytes.NewBuffer / strings.NewReader.
type retryRoundTripper struct {
	base http.RoundTripper
	cfg  RetryConfig
}

// shouldRetryStatus reports whether a response status warrants a retry:
// 5xx (server errors) and 429 (rate limited). Other 4xx are caller bugs and
// must not be retried.
func shouldRetryStatus(code int) bool {
	if code >= 500 && code < 600 {
		return true
	}
	if code == http.StatusTooManyRequests { // 429
		return true
	}
	return false
}

// computeBackoff returns BaseDelay * 2^attempt, capped at MaxDelay, computed
// via a multiplication loop to mirror the style used in kafka/retry.go and
// avoid overflow concerns of bit-shifting on time.Duration.
func computeBackoff(cfg RetryConfig, attempt int) time.Duration {
	delay := cfg.BaseDelay
	for i := 0; i < attempt; i++ {
		delay *= 2
		if delay > cfg.MaxDelay {
			delay = cfg.MaxDelay
			break
		}
	}
	return delay
}

// drainAndClose drains the response body to allow connection reuse, then
// closes it. Errors are intentionally ignored — best-effort cleanup.
func drainAndClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

func (rt *retryRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	var lastResp *http.Response
	var lastErr error

	totalAttempts := rt.cfg.MaxRetries + 1
	for attempt := 0; attempt < totalAttempts; attempt++ {
		// Wait between attempts; respect context cancellation during backoff.
		if attempt > 0 {
			delay := computeBackoff(rt.cfg, attempt-1)
			select {
			case <-time.After(delay):
			case <-req.Context().Done():
				drainAndClose(lastResp)
				return nil, req.Context().Err()
			}
		}

		// Replay body if the previous attempt consumed it.
		if attempt > 0 && req.Body != nil {
			if req.GetBody == nil {
				// Cannot safely replay an io.Reader-backed body — return the
				// last result without further retries (documented in godoc).
				if lastResp != nil {
					return lastResp, nil
				}
				return nil, lastErr
			}
			newBody, err := req.GetBody()
			if err != nil {
				drainAndClose(lastResp)
				return nil, err
			}
			req.Body = newBody
		}

		// Drain any previous response body before retrying so the underlying
		// connection can be reused.
		if lastResp != nil {
			drainAndClose(lastResp)
			lastResp = nil
		}

		resp, err := rt.base.RoundTrip(req)
		if err != nil {
			lastErr = err
			logger.Warn("retryable http: transport error",
				zap.Int("attempt", attempt+1),
				zap.Int("total_attempts", totalAttempts),
				zap.String("path", req.URL.Path),
				zap.Error(err))
			continue
		}

		if shouldRetryStatus(resp.StatusCode) {
			lastResp = resp
			lastErr = nil
			logger.Warn("retryable http: retryable status",
				zap.Int("attempt", attempt+1),
				zap.Int("status", resp.StatusCode),
				zap.String("path", req.URL.Path))
			continue
		}

		// 2xx, 3xx, non-429 4xx — return as-is.
		return resp, nil
	}

	// Retries exhausted: return the last response (with OPEN body — caller
	// closes) or the last transport error.
	if lastResp != nil {
		return lastResp, nil
	}
	return nil, lastErr
}

// NewRetryableClient returns an *http.Client that performs automatic retries
// according to cfg. The returned client is a drop-in replacement for
// http.Client. Use it like a regular http.Client:
//
//	client := httpclient.NewRetryableClient(httpclient.RetryConfig{
//	    MaxRetries: 3,
//	    BaseDelay:  200 * time.Millisecond,
//	    MaxDelay:   5 * time.Second,
//	    Timeout:    10 * time.Second,
//	})
//	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
//	resp, err := client.Do(req)
//	if err != nil { return err }
//	defer resp.Body.Close()
//
// See package doc for body-replay and lifetime contracts.
func NewRetryableClient(cfg RetryConfig) *http.Client {
	cfg.applyDefaults()
	return &http.Client{
		Timeout: cfg.Timeout,
		Transport: &retryRoundTripper{
			base: http.DefaultTransport,
			cfg:  cfg,
		},
	}
}
