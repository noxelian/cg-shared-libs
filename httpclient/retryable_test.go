package httpclient

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newCountingServer returns an httptest.Server whose handler invokes fn(attempt, w, r)
// where attempt is the 1-based count of requests received so far.
func newCountingServer(t *testing.T, fn func(attempt int32, w http.ResponseWriter, r *http.Request)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var counter atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := counter.Add(1)
		fn(n, w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &counter
}

func TestRetryableClient_SuccessNoRetry(t *testing.T) {
	srv, counter := newCountingServer(t, func(_ int32, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	client := NewRetryableClient(RetryConfig{MaxRetries: 3, BaseDelay: 10 * time.Millisecond})
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if got := counter.Load(); got != 1 {
		t.Fatalf("expected exactly 1 attempt, got %d", got)
	}
}

func TestRetryableClient_RetryOn500(t *testing.T) {
	srv, counter := newCountingServer(t, func(n int32, w http.ResponseWriter, _ *http.Request) {
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	client := NewRetryableClient(RetryConfig{MaxRetries: 3, BaseDelay: 5 * time.Millisecond})
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after retry, got %d", resp.StatusCode)
	}
	if got := counter.Load(); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
}

func TestRetryableClient_RetryOn503(t *testing.T) {
	srv, counter := newCountingServer(t, func(n int32, w http.ResponseWriter, _ *http.Request) {
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	client := NewRetryableClient(RetryConfig{MaxRetries: 3, BaseDelay: 5 * time.Millisecond})
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if got := counter.Load(); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
}

func TestRetryableClient_429Retried(t *testing.T) {
	srv, counter := newCountingServer(t, func(n int32, w http.ResponseWriter, _ *http.Request) {
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	client := NewRetryableClient(RetryConfig{MaxRetries: 3, BaseDelay: 5 * time.Millisecond})
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after 429 retry, got %d", resp.StatusCode)
	}
	if got := counter.Load(); got != 2 {
		t.Fatalf("expected 2 attempts (429→200), got %d", got)
	}
}

func TestRetryableClient_NoRetryOn400(t *testing.T) {
	srv, counter := newCountingServer(t, func(_ int32, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})
	client := NewRetryableClient(RetryConfig{MaxRetries: 3, BaseDelay: 5 * time.Millisecond})
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	if got := counter.Load(); got != 1 {
		t.Fatalf("expected 1 attempt for 400, got %d", got)
	}
}

func TestRetryableClient_NoRetryOn401(t *testing.T) {
	srv, counter := newCountingServer(t, func(_ int32, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	client := NewRetryableClient(RetryConfig{MaxRetries: 3, BaseDelay: 5 * time.Millisecond})
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
	if got := counter.Load(); got != 1 {
		t.Fatalf("expected 1 attempt for 401, got %d", got)
	}
}

func TestRetryableClient_NoRetryOn404(t *testing.T) {
	srv, counter := newCountingServer(t, func(_ int32, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	client := NewRetryableClient(RetryConfig{MaxRetries: 3, BaseDelay: 5 * time.Millisecond})
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
	if got := counter.Load(); got != 1 {
		t.Fatalf("expected 1 attempt for 404, got %d", got)
	}
}

func TestRetryableClient_MaxRetriesExhausted(t *testing.T) {
	srv, counter := newCountingServer(t, func(_ int32, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	})
	client := NewRetryableClient(RetryConfig{MaxRetries: 3, BaseDelay: 5 * time.Millisecond})
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("expected resp on exhausted retries, got err=%v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response on exhausted retries")
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}

	// Body must be open and readable.
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		t.Fatalf("body should be readable on exhausted retries: %v", readErr)
	}
	if !strings.Contains(string(body), `"boom"`) {
		t.Fatalf("expected boom payload, got %q", body)
	}
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close should not fail: %v", closeErr)
	}
	if got := counter.Load(); got != 4 { // 1 + 3 retries
		t.Fatalf("expected 4 attempts (1 + MaxRetries=3), got %d", got)
	}
}

// findFreePort returns a TCP port that was open at the moment of the call.
// It's NOT race-free (the port may be reused) but is good enough to provoke
// connect errors against a closed port.
func findFreePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func TestRetryableClient_RetryOnConnectError(t *testing.T) {
	addr := findFreePort(t)
	url := "http://" + addr

	client := NewRetryableClient(RetryConfig{MaxRetries: 2, BaseDelay: 5 * time.Millisecond, Timeout: 500 * time.Millisecond})
	start := time.Now()
	resp, err := client.Get(url)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected transport error against closed port, got resp=%+v", resp)
	}
	if resp != nil {
		t.Fatalf("expected nil response on transport error, got %+v", resp)
	}
	// At least 2 retries means at least 5ms + 10ms = 15ms of backoff.
	if elapsed < 10*time.Millisecond {
		t.Fatalf("retries appear not to have happened, elapsed=%s", elapsed)
	}
}

func TestRetryableClient_BodyReplayedViaGetBody(t *testing.T) {
	const payload = `{"hello":"world"}`

	var bodies []string
	var mu sync.Mutex

	srv, counter := newCountingServer(t, func(n int32, w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	client := NewRetryableClient(RetryConfig{MaxRetries: 3, BaseDelay: 5 * time.Millisecond})
	req, err := http.NewRequest(http.MethodPost, srv.URL, bytes.NewReader([]byte(payload)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after body replay, got %d", resp.StatusCode)
	}
	if got := counter.Load(); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("expected 2 received bodies, got %d", len(bodies))
	}
	if bodies[0] != payload || bodies[1] != payload {
		t.Fatalf("body should be replayed identically: %v", bodies)
	}
}

func TestRetryableClient_NoGetBody_NoRetry(t *testing.T) {
	const payload = `{"once":"only"}`

	srv, counter := newCountingServer(t, func(n int32, w http.ResponseWriter, _ *http.Request) {
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	client := NewRetryableClient(RetryConfig{MaxRetries: 3, BaseDelay: 5 * time.Millisecond})
	req, err := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(payload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	// Manually wipe GetBody so the client cannot replay.
	req.GetBody = nil

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("expected resp on first 500, got err=%v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 (no retry without GetBody), got %d", resp.StatusCode)
	}
	if got := counter.Load(); got != 1 {
		t.Fatalf("expected exactly 1 attempt without GetBody, got %d", got)
	}
}

func TestRetryableClient_ContextCancel_StopsRetry(t *testing.T) {
	srv, _ := newCountingServer(t, func(_ int32, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	client := NewRetryableClient(RetryConfig{
		MaxRetries: 5,
		BaseDelay:  200 * time.Millisecond,
		MaxDelay:   1 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	// Cancel the context after the first attempt completes (during backoff).
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start)

	if resp != nil {
		_ = resp.Body.Close()
		t.Fatalf("expected nil response on context cancellation, got status=%d", resp.StatusCode)
	}
	if err == nil {
		t.Fatal("expected error on context cancellation")
	}
	if !isContextCanceled(err) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	// Should return well before 2*BaseDelay = 400ms.
	if elapsed >= 350*time.Millisecond {
		t.Fatalf("cancel should short-circuit backoff, elapsed=%s", elapsed)
	}
}

// isContextCanceled checks whether err is or wraps context.Canceled. We use
// strings.Contains because http.Client may wrap the error in a *url.Error.
func isContextCanceled(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), context.Canceled.Error())
}

func TestRetryableClient_TimingTolerance(t *testing.T) {
	srv, _ := newCountingServer(t, func(_ int32, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	cfg := RetryConfig{
		MaxRetries: 3,
		BaseDelay:  20 * time.Millisecond,
		MaxDelay:   1 * time.Second,
	}
	client := NewRetryableClient(cfg)

	start := time.Now()
	resp, err := client.Get(srv.URL)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	// Backoff sequence between attempts: 20ms, 40ms, 80ms = 140ms minimum.
	const minExpected = 140 * time.Millisecond
	const tolerance = 50 * time.Millisecond
	lower := minExpected - tolerance
	if elapsed < lower {
		t.Fatalf("backoff too short: elapsed=%s, expected ≥%s", elapsed, lower)
	}
	// Sanity upper bound: don't allow runaway >1s.
	if elapsed > minExpected+10*tolerance {
		t.Fatalf("backoff too long: elapsed=%s, expected ≤%s", elapsed, minExpected+10*tolerance)
	}
}

func TestRetryableClient_BackoffCap(t *testing.T) {
	srv, _ := newCountingServer(t, func(_ int32, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	cfg := RetryConfig{
		MaxRetries: 4,
		BaseDelay:  100 * time.Millisecond,
		MaxDelay:   150 * time.Millisecond,
	}
	client := NewRetryableClient(cfg)

	start := time.Now()
	resp, err := client.Get(srv.URL)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	// Backoff sequence: 100, 150, 150, 150 = 550ms minimum.
	const minExpected = 550 * time.Millisecond
	const tolerance = 200 * time.Millisecond // 4 attempts → looser bound
	if elapsed < minExpected-tolerance {
		t.Fatalf("backoff too short: elapsed=%s, expected ≥%s", elapsed, minExpected-tolerance)
	}
	if elapsed > minExpected+tolerance {
		t.Fatalf("backoff too long: elapsed=%s, expected ≤%s", elapsed, minExpected+tolerance)
	}
}

func TestRetryableClient_NilBody(t *testing.T) {
	// GET without body — no GetBody required, no body to replay.
	srv, counter := newCountingServer(t, func(n int32, w http.ResponseWriter, _ *http.Request) {
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	client := NewRetryableClient(RetryConfig{MaxRetries: 3, BaseDelay: 5 * time.Millisecond})
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after retry on nil body GET, got %d", resp.StatusCode)
	}
	if got := counter.Load(); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
}

func TestRetryableClient_DropInForHttpClient(t *testing.T) {
	srv, _ := newCountingServer(t, func(_ int32, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	client := NewRetryableClient(RetryConfig{})

	// Type assertion — must be a real *http.Client.
	var _ *http.Client = client

	// Both client.Get and client.Do(req) must work.
	if resp, err := client.Get(srv.URL); err != nil {
		t.Fatalf("Get: %v", err)
	} else {
		_ = resp.Body.Close()
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
}

func TestRetryableClient_DefaultsApplied(t *testing.T) {
	// We can't easily inspect the unexported retryRoundTripper from a test,
	// but we CAN verify the user-visible Timeout is the default 10s.
	client := NewRetryableClient(RetryConfig{})
	if client.Timeout != 10*time.Second {
		t.Fatalf("expected default Timeout=10s, got %s", client.Timeout)
	}
}

func TestRetryableClient_RaceConcurrentDo(t *testing.T) {
	srv, _ := newCountingServer(t, func(_ int32, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	client := NewRetryableClient(RetryConfig{MaxRetries: 1, BaseDelay: 1 * time.Millisecond})

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			resp, err := client.Get(srv.URL)
			if err != nil {
				errs <- err
				return
			}
			_ = resp.Body.Close()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent client.Do error: %v", err)
	}
}

// computeBackoff is unexported but reachable from this same package.
func TestComputeBackoff_ExponentialAndCap(t *testing.T) {
	cfg := RetryConfig{BaseDelay: 100 * time.Millisecond, MaxDelay: 350 * time.Millisecond}
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 100 * time.Millisecond},
		{1, 200 * time.Millisecond},
		{2, 350 * time.Millisecond}, // 400 capped to 350
		{3, 350 * time.Millisecond},
	}
	for _, c := range cases {
		got := computeBackoff(cfg, c.attempt)
		if got != c.want {
			t.Errorf("attempt=%d: got %s want %s", c.attempt, got, c.want)
		}
	}
}

func TestShouldRetryStatus(t *testing.T) {
	cases := map[int]bool{
		200: false, 201: false, 204: false,
		301: false, 302: false,
		400: false, 401: false, 403: false, 404: false, 409: false,
		429: true,
		500: true, 502: true, 503: true, 504: true, 599: true,
		600: false,
	}
	for code, want := range cases {
		if got := shouldRetryStatus(code); got != want {
			t.Errorf("code=%d: got %v want %v", code, got, want)
		}
	}
}
