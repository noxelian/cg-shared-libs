# cg-shared-libs — CLAUDE.md (session handoff)

Entry point for a new Claude session in this library. Read before editing.

## What this is

**Library, not a service.** Common infrastructure code used by every CTOgram microservice. Module: `github.com/4ubak/cg-shared-libs`. Versioned via git tags; consumers pin in `go.mod` `require` (no `replace` directives — direct GitHub reference works).

## Packages

| Package | Purpose |
|---|---|
| `logger` | Zap-based structured logger; `logger.New(serviceName)` |
| `grpc` | gRPC server + client builders, interceptors (auth/logging/recovery/metrics) |
| `grpc/adminrbac` | Admin RBAC interceptor (depends on `grpc`) |
| `grpc/orgauth` | `EnforceOrgMatch` org-scope guards (depends on `grpc`) — locked, don't touch |
| `jwt` | JWT signer/validator; service-to-service token issuance — locked, don't touch |
| `kafka` | Kafka producer + consumer wrappers (segmentio/kafka-go under the hood) |
| `postgres` | pgx pool wrapper, migrations runner |
| `redis` | go-redis/v9 wrapper |
| `metrics` | Prometheus exporters |
| `health` | health/readiness probes |
| `tracing` | OpenTelemetry setup |
| `audit` | Audit log helpers |
| `circuitbreaker` | Circuit breaker (used by cg-ai for OpenAI↔Anthropic failover) |
| `crypto` | Encryption + password hashing helpers |
| `i18n` | i18n helpers |
| `middleware` | shared HTTP middlewares (CSRF, rate limiting) |
| `config` | YAML loader + `env:` tag overrides |
| `ratelimit` | Rate limiting (token bucket, multi-limiter) |
| `security` | URL validation, host whitelist, SSRF protection |
| `validation` | Input validation (phone, email, UUID, ...) |
| `ws` | WebSocket upgrader, auth, config |
| `pushpublisher` | Typed Kafka publisher for `notification.push`. **Dormant**: no service imports it yet, so nothing currently produces to that topic even though cg-communication's consumer exists. Wire before relying on push notifications. |

Packages removed 2026-07-02 as unwired dead code (zero consumers across all `cg-*` repos, 2-5 months old): `serviceauth` (`grpc/serviceauth`), `featureflags`, `httpclient`, `crypto.MigrateColumn`. Re-add only alongside the consumer that needs them.

`config.HTTPConfig`/`config.ServiceConfig` were briefly deleted the same day and then restored (commit `b650fb0` was wrong — cg-agreement's `internal/config/config.go` embeds both directly, pinned at cg-shared-libs v1.36.0; the "zero consumers" claim only checked for it under a different grep pattern). Keep them; if they're ever genuinely unused, land a companion PR moving cg-agreement to local structs first.

## Domain rules (locked)

- This module is consumed by **every** service. Any breaking change is a coordinated release.
- Version bumps: tag (e.g. `v1.25.0`) → push to GitHub with `--tags` → consumers update via `go get github.com/4ubak/cg-shared-libs@vX.Y.Z`.
- `GOPRIVATE=github.com/4ubak/*` is required in every consumer environment (and CI).
- **Never put service-specific logic here.** If it belongs to one domain, it lives in that service.

## Critical files / packages

- `config/load.go` — `config.Load[T](path)` reads YAML, then overrides from struct `env:` tags
- `grpc/client.go` — `ClientConfig` struct; **see gotcha below**
- `jwt/token.go` — `GenerateAccessToken(userID, phone, deviceID)`
- `kafka/producer.go`, `kafka/consumer.go`

## Gotchas

- **`grpc.ClientConfig` has NO `env:` tags on Host/Port.** Consuming services must override manually in their `Load()` using `config.GetEnv()` / `config.GetEnvInt()`. (`postgres.Config` does have env tags and works automatically.)
- YAML `${VAR:default}` interpolation does **not** work — Go's yaml.Unmarshal treats `${...}` as a literal string. Use `env:` tags instead.
- Kafka `groupID` lacks a sensible default — set it explicitly or consumers silently misbehave.
- Don't add backwards-compat shims for old `env:` tag schemas; cut over consumers and remove the old field.

## Known architectural debt

This module is a pure library — no `domain`/`usecase`/`handler`/`repo` layers exist here, so the usual layering violations (domain importing pgx/redis/logger, degenerate usecase pass-throughs) don't apply. What's not fixed as of 2026-07-02:

- **`pushpublisher` is dormant.** Fully built and tested, but no service imports it, so nothing produces to the `notification.push` Kafka topic — cg-communication's consumer (`services/notification/internal/consumer/push_consumer.go`) is idle. Kept because the consumer already depends on this exact schema. Do not assume push notifications work end-to-end until a producer is wired in.
- **Package table above was stale for ~2-5 months** (missing `ratelimit`, `security`, `validation`, `ws`, `grpc/adminrbac`, `grpc/orgauth`) before this pass; if you add a new top-level package, update this table in the same commit.

## Conventions

- Each package owns its own tests.
- Public APIs only — no `internal/` in this module.
- Errors: sentinel errors per package (`var ErrNotFound = errors.New(...)`).

## Commands

```bash
go build ./...
go test ./... -cover
golangci-lint run

# Release
git tag v1.X.Y
git push --tags

# Consumers then:
GONOSUMDB='github.com/4ubak/*' GOPROXY=direct go get github.com/4ubak/cg-shared-libs@v1.X.Y
go mod tidy && go build ./...
```

## Commit & deploy

- Library only — no service to deploy. Releases = git tags.
- Coordinate breaking changes across consumers in one batch.
