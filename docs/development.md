# Development Guide

## Prerequisites

- Go 1.25 or later (the module declares `go 1.25.5`)
- No CGO required — `modernc.org/sqlite` is a pure Go port

No C toolchain, no system SQLite library, no external build tools. A plain
`go build ./...` is sufficient.

---

## Build and Test

```bash
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

# Tidy dependencies
go mod tidy
```

All three commands (`go test -race ./...`, `go vet ./...`, and `go mod tidy`)
must produce no errors or warnings before a commit is pushed.

---

## Test Patterns

### File Organisation

- `ratelimit_test.go` — Phase 0 (core logic) and Phase 1 (provider profiles)
- `sqlite_test.go` — Phase 2 (SQLite backend)

Both files are in `package ratelimit` (white-box tests) so they can access
unexported fields and methods such as `prune()`, `filePath`, and `sqlite`.

### Naming Convention

SQLite tests follow the `_Good`, `_Bad`, `_Ugly` suffix pattern:

- `_Good` — happy path
- `_Bad` — expected error conditions (invalid paths, corrupt input)
- `_Ugly` — panic-adjacent edge cases (corrupt DB files, truncated files)

Core logic tests use plain descriptive names without suffixes, grouped by
method with table-driven subtests.

### Test Helpers

`newTestLimiter(t *testing.T)` creates a `RateLimiter` with Gemini defaults and
redirects the YAML file path into `t.TempDir()`:

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

- `require.NoError(t, err)` — fail immediately on setup errors
- `assert.NoError(t, err)` — record failure but continue
- `assert.Equal(t, expected, actual, "message")` — prefer over raw comparisons
- `assert.True / assert.False` — for boolean checks
- `assert.Empty / assert.Len` — for slice length checks
- `assert.ErrorIs(t, err, context.DeadlineExceeded)` — for sentinel errors

Do not use `t.Error`, `t.Fatal`, or `t.Log` directly.

### Race Tests

Concurrency tests spin up goroutines and use `sync.WaitGroup`. They do not
assert anything beyond absence of data races (the race detector does the work):

```go
var wg sync.WaitGroup
for i := range 20 {
    wg.Add(1)
    go func() {
        defer wg.Done()
        // concurrent operations
    }()
}
wg.Wait()
```

Run every concurrency test with `-race`. The CI baseline is `go test -race ./...`
clean.

### Coverage

Current coverage: 95.1%. The remaining 5% consists of three paths that cannot
be covered in unit tests without modifying the production code:

1. `CountTokens` success path — hardcoded Google API URL requires network access
2. `yaml.Marshal` error path in `Persist()` — cannot be triggered with valid Go structs
3. `os.UserHomeDir()` error path in `NewWithConfig()` — requires unsetting `$HOME`

Do not lower coverage below 95% without a documented reason.

---

## Coding Standards

### Language

UK English throughout: colour, organisation, serialise, initialise, behaviour.
Do not use American spellings in identifiers, comments, or documentation.

### Go Style

- All exported types, functions, and fields must have doc comments
- Error strings must be lowercase and not end with punctuation (Go convention)
- Contextual errors use `fmt.Errorf("package.Function: what: %w", err)` — the
  prefix `ratelimit.` is included so errors identify their origin clearly
- No `init()` functions
- No global mutable state outside of `DefaultProfiles()` (which returns a fresh
  map on each call)

### Mutex Discipline

The `RateLimiter.mu` mutex is the only synchronisation primitive. Rules:

- Methods that call `prune()` always acquire the write lock (`mu.Lock()`),
  even if they appear read-only, because `prune()` mutates slices
- `Persist()` acquires only the read lock (`mu.RLock()`) because it reads a
  snapshot of state
- Lock acquisition always happens at the top of the public method, never inside
  a helper — helpers document "Caller must hold the lock"
- Never call a public method from inside another public method while holding
  the lock (deadlock risk)

### Dependencies

Direct dependencies are intentionally minimal:

| Dependency | Purpose |
|------------|---------|
| `gopkg.in/yaml.v3` | YAML serialisation for legacy backend |
| `modernc.org/sqlite` | Pure Go SQLite for persistent backend |
| `github.com/stretchr/testify` | Test assertions (test-only) |

Do not add `database/sql` drivers beyond `modernc.org/sqlite`. Do not add HTTP
client libraries; the existing `CountTokens` function uses the standard library.

---

## Licence

EUPL-1.2. Every new source file must carry the standard header if the project
adopts per-file headers in future. Confirm with the project lead before adding
files under a different licence.

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
