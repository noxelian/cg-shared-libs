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
| `grpc/orgauth` | `EnforceOrgMatch` org-scope guards (depends on `grpc`). User authorization prefers the signed `orgs[]` membership claim; `org_id` remains optional selected-org context. |
| `jwt` | JWT signer/validator; service-to-service token issuance. User tokens may carry signed `orgs[]` memberships separately from optional selected `org_id`. v1.41.0 added `NewLocalRS256Verifier(cfg, keys)` (jwt/localverifier.go): in-memory RS256 self-verification so the issuer does not depend on its own JWKS endpoint. |
| `kafka` | Kafka producer + consumer wrappers (segmentio/kafka-go under the hood). Producers are synchronous and require acknowledgements from all in-sync replicas before `Publish` succeeds. With DLQ enabled, the source offset is committed only after an acknowledged DLQ write; DLQ outages retain the offset until recovery or shutdown. Consumers handling sensitive schemas can configure value and key transforms with `WithDLQValueRedactor` / `WithDLQKeyRedactor`; redaction errors retain the source offset and never fall back to raw bytes. `WithEventDecodeErrorsToDLQ(valueRedactor)` requires a value redactor, drops source keys, and opts malformed top-level envelopes into that sanitized DLQ path instead of the backward-compatible immediate commit. Its privacy invariants are finalized after all options, so option order cannot restore raw data. |
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
| `pushpublisher` | Typed Kafka publisher for `notification.push`. **Now imported** by cg-users/organization (`NEW_ORDER_PUSH_ENABLED`/`SERVICE_COMMENT_PUSH_ENABLED`), cg-services request+bid (`AD_CLASSIFIED_PUSH_ENABLED`/`BID_PUSH_ENABLED`), and cg-communication/chat (`CHAT_OFFLINE_PUSH_ENABLED`). All producers are feature-gated, but chart/env values differ by service/environment (chat stage values currently set the flag on; prod handoff says compose leaves it unset), so verify the target env before assuming the topic is idle. |

Packages removed 2026-07-02 as unwired dead code (zero consumers across all `cg-*` repos, 2-5 months old): `serviceauth` (`grpc/serviceauth`), `featureflags`, `httpclient`, `crypto.MigrateColumn`. Re-add only alongside the consumer that needs them.

`config.HTTPConfig`/`config.ServiceConfig` were briefly deleted the same day and then restored (commit `b650fb0` was wrong — cg-agreement's `internal/config/config.go` embeds both directly, pinned at cg-shared-libs v1.36.0; the "zero consumers" claim only checked for it under a different grep pattern). Keep them; if they're ever genuinely unused, land a companion PR moving cg-agreement to local structs first.

## Domain rules (locked)

- This module is consumed by **every** service. Any breaking change is a coordinated release.
- Version bumps: tag (e.g. `v1.25.0`) → push to GitHub with `--tags` → consumers update via `go get github.com/4ubak/cg-shared-libs@vX.Y.Z`.
- `GOPRIVATE=github.com/4ubak/*` is required in every consumer environment (and CI).
- **Never put service-specific logic here.** If it belongs to one domain, it lives in that service.
- `orgs[]` is signed authorization evidence populated only by the issuer. `orgs: []` is authoritative and grants no organization access; an absent/null claim is legacy-only compatibility. Metadata such as `x-org-id` is selection context and must never be promoted into `AuthInfo.OrgIDs` by a verifier.
- `platform_roles[]` is access-token-only signed authorization evidence. `grpc/adminrbac` ignores raw `x-platform-role` metadata and reads only `AuthInfo.PlatformRoles`. A trusted live-role resolver may enrich an already authenticated context with `grpc.ContextWithPlatformRoles`; refresh flows must re-resolve roles before minting a privileged access token.

## Critical files / packages

- `config/load.go` — `config.Load[T](path)` reads YAML, then overrides from struct `env:` tags
- `grpc/client.go` — `ClientConfig` struct; **see gotcha below**
- `jwt/token.go` — `GenerateAccessToken(userID, phone, deviceID)`
- `kafka/producer.go`, `kafka/consumer.go`

## Gotchas

- **`grpc.ClientConfig` has NO `env:` tags on Host/Port.** Consuming services must override manually in their `Load()` using `config.GetEnv()` / `config.GetEnvInt()`. (`postgres.Config` does have env tags and works automatically.)
- **Unary retries are explicit.** `MaxRetries` alone does not retry any RPC. List idempotent methods in `RetryableMethods` (exact names or `/Service/*`) or deliberately set `RetryAllMethods` only when every call is idempotent. The shared policy retries only `Unavailable`, applies `Timeout` to the total call, and caps jittered backoff with `RetryMaxWaitTime`.
- **HS256 legacy verification is explicit.** `jwt.NewVerifier` requires `AcceptHS256: true` when `JWKSURL` is empty; `AcceptHS256: false` without JWKS fails startup instead of constructing an HS256 manager.
- YAML `${VAR:default}` interpolation does **not** work — Go's yaml.Unmarshal treats `${...}` as a literal string. Use `env:` tags instead.
- Kafka `groupID` lacks a sensible default — set it explicitly or consumers silently misbehave.
- `kafka.NewProducer` deliberately sets `RequiredAcks=RequireAll`; do not replace it with a raw zero-value `kafka.Writer`, whose default is fire-and-forget and is unsafe for outbox relays.
- `ws.ExtractToken` accepts only `Authorization: Bearer <JWT>` or the exact
  `Sec-WebSocket-Protocol: access_token, <JWT>` pair. Query-string tokens are intentionally
  rejected because URLs leak into access logs and browser history. Consumers pinned to an older
  shared-libs tag must keep an equivalent local extractor until they bump the library release.
- Don't add backwards-compat shims for old `env:` tag schemas; cut over consumers and remove the old field.

## Known architectural debt

This module is a pure library — no `domain`/`usecase`/`handler`/`repo` layers exist here, so the usual layering violations (domain importing pgx/redis/logger, degenerate usecase pass-throughs) don't apply. What's not fixed as of 2026-07-02:

- **`pushpublisher` producers are wired but feature-gated.** Imported by cg-users/organization, cg-services (request+bid), and cg-communication/chat; most chart defaults are off, while chat's stage chart is intentionally on. Do not infer live end-to-end push from this library alone: check the per-service chart/env for the target environment and the per-service push-cutover section before relying on `notification.push`.
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
