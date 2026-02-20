# CLAUDE.md

Token counting, model quotas, and sliding window rate limiter.

Module: `forge.lthn.ai/core/go-ratelimit`

## Commands

```bash
go test ./...               # run all tests
go test -race ./...         # race detector (required before commit)
go test -v -run Name ./...  # single test
go vet ./...                # vet check
```

## Standards

- UK English
- `go test -race ./...` and `go vet ./...` must pass before commit
- Conventional commits: `type(scope): description`
- Co-Author: `Co-Authored-By: Virgil <virgil@lethean.io>`
- Coverage must not drop below 95%

## Docs

- `docs/architecture.md` — sliding window algorithm, provider quotas, YAML/SQLite backends
- `docs/development.md` — prerequisites, test patterns, coding standards
- `docs/history.md` — completed phases with commit hashes, known limitations
