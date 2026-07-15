# cg-shared-libs

Shared infrastructure library for CTOgram services. Module:
`github.com/4ubak/cg-shared-libs`.

## Architecture

- Keep this repository domain-neutral. Service-specific business rules belong
  to the service that owns them.
- Packages expose small, stable public APIs; consumers own their application
  ports and adapt this library at the composition boundary.
- Do not add empty `domain/usecase/repository` layers to a library package.
  Dependency direction and one clear responsibility matter more than folder
  symmetry.
- Library code returns errors with context and preserves identity with `%w`.
  Calling services decide how and where to log them.
- Security policy shared by multiple services may live here only when it has
  one contract and one reason to change. `ws.ExtractToken` is the canonical
  WebSocket credential parser.

## Release Contract

1. Run every command in the `Commands` section below.
2. Commit and push `main` to both `origin` (GitHub) and `gitlab`.
3. Create the next semantic version tag and push it to both remotes.
4. Bump consumers using the GitHub module path. Never add a local `replace`.
5. Verify each affected consumer before deploying it.

Breaking changes require a coordinated consumer release. Security tightening
that rejects credentials in URLs must not be bypassed by compatibility shims.

## Platform Role Authorization

- `platform_roles` is signed authorization evidence carried by access JWTs
  only. Refresh JWTs intentionally omit roles; refresh flows must resolve the
  current roles before issuing a new privileged access token.
- `grpc/adminrbac` authorizes only `AuthInfo.PlatformRoles`, populated by the
  JWT auth interceptor. Raw incoming `x-platform-role` metadata is untrusted
  and is never an authorization source.
- A trusted live-role resolver may enrich an already authenticated context
  with `grpc.ContextWithPlatformRoles`. Do not call it from request metadata or
  other client-controlled values.

## JWT Verification Modes

- RS256 verification requires `JWKSURL`.
- Local legacy HS256 verification is available only when `AcceptHS256: true`
  is explicit and a secret is configured.
- `jwt.NewVerifier` fails closed when `AcceptHS256` is false and `JWKSURL` is
  empty. Consumers completing the RS256 migration must configure JWKS before
  disabling HS256.

## Unary Retry Contract

- `MaxRetries` alone does not enable retries. List explicitly idempotent unary
  RPCs in `RetryableMethods` using exact full method names or a `/Service/*`
  suffix pattern.
- `RetryAllMethods` is an explicit opt-in for clients whose every unary RPC is
  safe to replay. Do not enable it for clients with arbitrary writes.
- The shared interceptor retries only `Unavailable`; it does not globally
  retry `Internal`, `Aborted`, or `ResourceExhausted`.
- `Timeout` bounds the total unary call, including all attempts and waits.
  Backoff uses bounded equal jitter capped by `RetryMaxWaitTime`.

## WebSocket Authentication

Accepted credential transports are:

- `Authorization: Bearer <JWT>`; or
- exactly `Sec-WebSocket-Protocol: access_token, <JWT>`.

Query-string tokens and longer/mixed subprotocol lists are rejected because
URLs and handshake metadata are commonly persisted by proxies and telemetry.

## Commands

```bash
go test ./... -cover
go test -race ./...
go build ./...
golangci-lint run
gosec ./...
gitleaks dir --redact --no-banner --exit-code 1 .
```

See `CLAUDE.md` for the package inventory and operational gotchas.
