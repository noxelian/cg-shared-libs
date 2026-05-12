# cg-shared-libs — CLAUDE.md (session handoff)

Entry point for a new Claude session in this library. Read before editing.

## What this is

**Library, not a service.** Common infrastructure code used by every CTOgram microservice. Module: `github.com/4ubak/cg-shared-libs`. Versioned via git tags; consumers pin in `go.mod` `require` (no `replace` directives — direct GitHub reference works).

## Packages

| Package | Purpose |
|---|---|
| `logger` | Zap-based structured logger; `logger.New(serviceName)` |
| `grpc` | gRPC server + client builders, interceptors (auth/logging/recovery/metrics) |
| `jwt` | JWT signer/validator; service-to-service token issuance |
| `kafka` | Kafka producer + consumer wrappers (segmentio/kafka-go under the hood) |
| `postgres` | pgx pool wrapper, migrations runner |
| `redis` | go-redis/v9 wrapper |
| `metrics` | Prometheus exporters |
| `health` | health/readiness probes |
| `tracing` | OpenTelemetry setup |
| `audit` | Audit log helpers |
| `circuitbreaker` | Circuit breaker (used by cg-ai for OpenAI↔Anthropic failover) |
| `crypto` | Crypto helpers |
| `featureflags` | Feature flag client |
| `httpclient` | Resilient HTTP client (retry/backoff) |
| `i18n` | i18n helpers |
| `middleware` | shared middlewares |
| `config` | YAML loader + `env:` tag overrides |
| `pushpublisher` | Push notification publisher |

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
