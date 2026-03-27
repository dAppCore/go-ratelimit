<!-- SPDX-License-Identifier: EUPL-1.2 -->

---
title: Development Guide
description: How to build, test, and contribute to go-ratelimit -- prerequisites, test patterns, coding standards, and commit conventions.
---

# Development Guide

## Prerequisites

- **Go 1.26** or later (the module declares `go 1.26.0`)
- No CGO required -- `modernc.org/sqlite` is a pure Go port

No C toolchain, no system SQLite library, no external build tools. A plain
`go build ./...` is sufficient.

---

## Build and Test

```bash
# Compile all packages
go build ./...

# Run all tests
go test ./...

# Run all tests with the race detector (required before every commit)
go test -race ./...

# Run a single test by name
go test -v -run TestCanSend ./...

# Run a single subtest
go test -v -run "TestCanSend/RPM_at_exact_limit_is_rejected" ./...

# Run benchmarks
go test -bench=. -benchmem ./...

# Run a specific benchmark
go test -bench=BenchmarkCanSend -benchmem ./...

# Check for vet issues
go vet ./...

# Lint (requires golangci-lint)
golangci-lint run ./...

# Coverage check
go test -cover ./...

# Tidy dependencies
go mod tidy
```

Before a commit is pushed, `go build ./...`, `go test -race ./...`,
`go vet ./...`, `go test -cover ./...`, and `go mod tidy` must all pass
without errors, and coverage must remain at or above 95%.

---

## Test Organisation

### File Layout

| File | Scope |
|------|-------|
| `ratelimit_test.go` | Core sliding window logic, provider profiles, concurrency, benchmarks |
| `sqlite_test.go` | SQLite backend, migration, concurrent persistence |
| `error_test.go` | SQLite and YAML error paths |
| `iter_test.go` | `Models()` and `Iter()` iterators, `CountTokens` edge cases |

All test files are in `package ratelimit` (white-box tests), giving access to
unexported fields and methods such as `prune()`, `filePath`, and `sqlite`.

### Naming Convention

SQLite tests follow the `_Good`, `_Bad`, `_Ugly` suffix pattern:

- `_Good` -- happy path
- `_Bad` -- expected error conditions (invalid paths, corrupt input)
- `_Ugly` -- panic-adjacent edge cases (corrupt database files, truncated files)

Core logic tests use plain descriptive names without suffixes, grouped by method
with table-driven subtests.

### Test Helper

`newTestLimiter(t)` creates a `RateLimiter` with Gemini defaults and redirects
the YAML file path into `t.TempDir()`:

```go
func newTestLimiter(t *testing.T) *RateLimiter {
    t.Helper()
    rl, err := New()
    require.NoError(t, err)
    rl.filePath = filepath.Join(t.TempDir(), "ratelimits.yaml")
    return rl
}
```

Use `t.TempDir()` for all file paths in tests. Go cleans these up automatically
after each test completes.

### Testify Usage

Tests use `github.com/stretchr/testify` exclusively:

- `require.NoError(t, err)` -- fail immediately on setup errors
- `assert.NoError(t, err)` -- record failure but continue
- `assert.Equal(t, expected, actual, "message")` -- prefer over raw comparisons
- `assert.True` / `assert.False` -- for boolean checks
- `assert.Empty` / `assert.Len` -- for slice length checks
- `assert.ErrorIs(t, err, target)` -- for sentinel errors

Do not use `t.Error`, `t.Fatal`, or `t.Log` directly.

### Concurrency Tests

Race tests spin up goroutines and use `sync.WaitGroup`. Some assert specific
outcomes (e.g., correct RPD count after concurrent recordings), while others
rely solely on the race detector to catch data races:

```go
var wg sync.WaitGroup
for range 20 {
    wg.Go(func() {
        for range 50 {
            rl.CanSend(model, 10)
            rl.RecordUsage(model, 5, 5)
            rl.Stats(model)
        }
    })
}
wg.Wait()
```

Always run concurrency tests with `-race`.

### Benchmarks

The following benchmarks are included:

| Benchmark | What it measures |
|-----------|------------------|
| `BenchmarkCanSend` | CanSend with a 1,000-entry sliding window |
| `BenchmarkRecordUsage` | Recording usage on a single model |
| `BenchmarkCanSendConcurrent` | Parallel CanSend across goroutines |
| `BenchmarkCanSendWithPrune` | CanSend with 500 old + 500 new entries |
| `BenchmarkStats` | Stats retrieval with a 1,000-entry window |
| `BenchmarkAllStats` | AllStats across 5 models x 200 entries each |
| `BenchmarkPersist` | YAML persistence I/O |
| `BenchmarkSQLitePersist` | SQLite persistence I/O |
| `BenchmarkSQLiteLoad` | SQLite state loading |

### Coverage

Maintain at least 95% statement coverage. Verify it with `go test -cover ./...`
and document any justified exception in the commit or PR that introduces it.

---

## Coding Standards

### Language

UK English throughout: colour, organisation, serialise, initialise, behaviour.
Do not use American spellings in identifiers, comments, or documentation.

### Go Style

- All exported types, functions, and fields must have doc comments.
- Error strings must be lowercase and not end with punctuation (Go convention).
- Contextual errors use `core.E("ratelimit.Function", "what", err)` so errors
  identify their origin clearly.
- No `init()` functions.
- No global mutable state. `DefaultProfiles()` returns a fresh map on each call.

### Mutex Discipline

The `RateLimiter.mu` mutex is the only synchronisation primitive. Rules:

- Methods that call `prune()` always acquire the write lock (`mu.Lock()`), even
  if they appear read-only, because `prune()` mutates state slices.
- `Persist()` acquires the write lock briefly to clone state, then releases it
  before performing I/O.
- Lock acquisition always happens at the top of the public method, never inside
  a helper. Helpers document "Caller must hold the lock".
- Never call a public method from inside another public method while holding the
  lock (deadlock risk).

### Dependencies

Direct dependencies are intentionally minimal:

| Dependency | Purpose |
|------------|---------|
| `dappco.re/go/core` | File I/O helpers, structured errors, JSON helpers, path/environment utilities |
| `gopkg.in/yaml.v3` | YAML serialisation for legacy backend |
| `modernc.org/sqlite` | Pure Go SQLite for persistent backend |
| `github.com/stretchr/testify` | Test assertions (test-only) |

Do not add `database/sql` drivers beyond `modernc.org/sqlite`. Do not add HTTP
client libraries; the existing `CountTokens` function uses the standard library.

---

## Licence

EUPL-1.2. Confirm with the project lead before adding files under a different
licence.

---

## Commit Convention

Format: `type(scope): description`

Common types: `feat`, `fix`, `test`, `refactor`, `docs`, `perf`, `chore`

Common scopes: `ratelimit`, `sqlite`, `persist`, `config`

Every commit must include:

```
Co-Authored-By: Virgil <virgil@lethean.io>
```

Example:

```
feat(sqlite): add WAL-mode SQLite backend with migration helper

Co-Authored-By: Virgil <virgil@lethean.io>
```

Commits must not be pushed unless `go test -race ./...` and `go vet ./...` both
pass. `go mod tidy` must produce no changes.

---

## Linting

The project uses `golangci-lint` with the following enabled linters (see
`.golangci.yml`):

- `govet`, `errcheck`, `staticcheck`, `unused`, `gosimple`
- `ineffassign`, `typecheck`, `gocritic`, `gofmt`

Disabled linters: `exhaustive`, `wrapcheck`.

Run `golangci-lint run ./...` to check before committing.
