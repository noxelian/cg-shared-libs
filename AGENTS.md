# AGENTS.md

## Cursor Cloud specific instructions

This is a **Go shared library** (`gitlab.com/xakpro/cg-shared-libs`), not a runnable application. There is no `main` package, no Dockerfile, and no services to start.

### Key commands

| Task | Command |
|------|---------|
| Install deps | `go mod download` |
| Build | `go build ./...` |
| Vet | `go vet ./...` |
| Test | `go test ./...` |
| Test (verbose) | `go test -v ./...` |
| Test (coverage) | `go test -cover ./...` |

### Notes

- Requires **Go 1.24.5** (see `go.mod`).
- All tests use mocks and in-memory fakes (e.g. `miniredis` for Redis). No external services (PostgreSQL, Redis, Kafka, Elasticsearch) are needed to run the test suite.
- There is no `golangci-lint` config in the repo; `go vet ./...` is the available static analysis check.
- 10 packages have test files; 11 packages have no tests yet.
