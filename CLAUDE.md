# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

Provider-agnostic sliding window rate limiter for LLM API calls. Single Go package (no sub-packages) with two persistence backends: YAML (single-process, default) and SQLite (multi-process, WAL mode). Enforces RPM, TPM, and RPD quotas per model. Ships default profiles for Gemini, OpenAI, Anthropic, and Local providers.

Module: `forge.lthn.ai/core/go-ratelimit` — Go 1.26, no CGO required.

## Commands

```bash
go test ./...                                              # run all tests
go test -race ./...                                        # race detector (required before commit)
go test -v -run TestCanSend ./...                          # single test
go test -v -run "TestCanSend/RPM_at_exact_limit" ./...     # single subtest
go test -bench=. -benchmem ./...                           # benchmarks
go vet ./...                                               # vet check
golangci-lint run ./...                                    # lint
```

Pre-commit gate: `go test -race ./...` and `go vet ./...` must both pass.

## Standards

- **UK English** everywhere: colour, organisation, serialise, initialise, behaviour
- **Conventional commits**: `type(scope): description` — scopes: `ratelimit`, `sqlite`, `persist`, `config`
- **Co-Author line** on every commit: `Co-Authored-By: Virgil <virgil@lethean.io>`
- **Coverage** must not drop below 95%
- **Error format**: `fmt.Errorf("ratelimit.FunctionName: what: %w", err)` — lowercase, no trailing punctuation
- **No `init()` functions**, no global mutable state
- **Mutex discipline**: lock at the top of public methods, never inside helpers. Helpers that need the lock document "Caller must hold the lock". `prune()` mutates state, so even "read-only" methods that call it take the write lock. Never call a public method from another public method while holding the lock.

## Architecture

All code lives in the root package. Key files:

- `ratelimit.go` — core types (`RateLimiter`, `ModelQuota`, `UsageStats`, `Config`, `Provider`), sliding window logic (`prune`, `CanSend`, `RecordUsage`), YAML persistence, `CountTokens` (Gemini-specific), iterators (`Models`, `Iter`)
- `sqlite.go` — `sqliteStore` internal type, schema creation, load/save for quotas and state

Constructor matrix: `New()` / `NewWithConfig()` for YAML, `NewWithSQLite()` / `NewWithSQLiteConfig()` for SQLite. Always `defer rl.Close()` with SQLite.

### Sliding window

1-minute window pruned on every `CanSend`/`Stats`/`RecordUsage` call. Daily counter is a rolling 24h window from first request, not a calendar boundary. Empty state entries are garbage-collected by `prune()` to prevent memory leaks.

## Test Organisation

White-box tests (`package ratelimit`), all assertions via `testify` (`require` for fatal, `assert` for non-fatal). Do not use `t.Error`/`t.Fatal` directly.

| File | Scope |
|------|-------|
| `ratelimit_test.go` | Core logic, provider profiles, concurrency, benchmarks |
| `sqlite_test.go` | SQLite backend, migration, concurrent persistence |
| `error_test.go` | Error paths for SQLite and YAML |
| `iter_test.go` | Iterators, `CountTokens` edge cases |

SQLite tests use `_Good`/`_Bad`/`_Ugly` suffixes (happy path / expected errors / edge cases). Core tests use plain descriptive names with table-driven subtests. Use `t.TempDir()` for all file paths.

## Dependencies

Only three direct dependencies — do not add more without justification:

- `gopkg.in/yaml.v3` — YAML backend
- `modernc.org/sqlite` — pure Go SQLite (no CGO)
- `github.com/stretchr/testify` — test-only

## Docs

- `docs/architecture.md` — sliding window algorithm, provider quotas, YAML/SQLite backends, concurrency model
- `docs/development.md` — prerequisites, test patterns, coding standards
- `docs/history.md` — completed phases with commit hashes, known limitations
