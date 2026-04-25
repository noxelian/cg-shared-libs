// Package httpclient provides production HTTP client primitives shared across
// CTOgram services. The primary export is NewRetryableClient, a drop-in
// replacement for *http.Client with exponential-backoff retries on network
// errors, HTTP 5xx, and HTTP 429 responses.
//
// API contract:
//   - Single public call surface: client.Do(req) (the returned *http.Client
//     uses a custom RoundTripper internally). There is intentionally NO
//     DoWithBody helper — body replay is handled through Go's standard
//     req.GetBody mechanism.
//
// Body replay:
//   - For POST/PUT/PATCH with a body, callers MUST construct the request via
//     http.NewRequest(method, url, bytes.NewReader(...)) — or with
//     bytes.NewBuffer / strings.NewReader. These body types cause net/http to
//     set req.GetBody automatically, enabling the transport to replay the body
//     on each retry.
//   - If req.GetBody is nil and a retry is needed (attempt > 0 and req.Body
//     is non-nil), the client returns the last response/error WITHOUT retry —
//     it cannot replay an io.Reader-backed body safely.
//
// Lifetime / response ownership:
//   - On retries exhausted with the last attempt returning an HTTP response,
//     the client returns that *http.Response (status 5xx/429) WITH AN OPEN
//     BODY. Callers MUST close resp.Body in the usual way:
//
//	    resp, err := client.Do(req)
//	    if err != nil { return err }
//	    defer resp.Body.Close()
//
//   - On retries exhausted with a transport error on the last attempt, the
//     client returns (nil, err).
//
// Retry policy (fixed):
//   - retry on transport errors (connect failures, network timeouts)
//   - retry on HTTP 5xx
//   - retry on HTTP 429 (rate limited)
//   - DO NOT retry on other 4xx (400/401/403/404/etc) — caller's bug
//
// Backoff:
//   - exponential: BaseDelay * 2^attempt, capped at MaxDelay
//   - implemented via a multiplication loop (style match with kafka/retry.go;
//     avoids overflow risks of bit-shifting on time.Duration)
//
// Context:
//   - request context cancellation is honoured between attempts — if the
//     caller cancels mid-backoff, the client returns ctx.Err() promptly.
//
// Out of scope (caller responsibility):
//   - Target URL validation (SSRF protection). This client does not parse or
//     validate the URL beyond what http.DefaultTransport does.
//   - TLS configuration (e.g. InsecureSkipVerify). This client never touches
//     *tls.Config.
//   - Header / body logging. This client emits only request path, status
//     code, attempt count, and error class — never request/response headers
//     or body bytes.
//
// This package has no third-party dependencies and does not integrate
// circuit-breaker logic — compose with cg-shared-libs/circuitbreaker in
// caller services if needed.
package httpclient
