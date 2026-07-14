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

1. Run `go test ./... -cover`, `go build ./...`, and `golangci-lint run`.
2. Commit and push `main` to both `origin` (GitHub) and `gitlab`.
3. Create the next semantic version tag and push it to both remotes.
4. Bump consumers using the GitHub module path. Never add a local `replace`.
5. Verify each affected consumer before deploying it.

Breaking changes require a coordinated consumer release. Security tightening
that rejects credentials in URLs must not be bypassed by compatibility shims.

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
