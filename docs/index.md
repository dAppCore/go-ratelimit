---
title: go-ratelimit
description: Provider-agnostic sliding window rate limiter for LLM API calls, with YAML and SQLite persistence backends.
---

# go-ratelimit

**Module**: `forge.lthn.ai/core/go-ratelimit`
**Licence**: EUPL-1.2
**Go version**: 1.26+

go-ratelimit enforces requests-per-minute (RPM), tokens-per-minute (TPM), and
requests-per-day (RPD) quotas on a per-model basis using an in-memory sliding
window. It ships with default quota profiles for Gemini, OpenAI, Anthropic, and
a local inference provider. State persists across process restarts via YAML
(single-process) or SQLite with WAL mode (multi-process). A YAML-to-SQLite
migration helper is included.

## Quick Start

```go
import "forge.lthn.ai/core/go-ratelimit"

// Create a limiter with Gemini defaults (YAML backend).
rl, err := ratelimit.New()
if err != nil {
    log.Fatal(err)
}

// Check capacity before sending.
if rl.CanSend("gemini-2.0-flash", 1500) {
    // Make the API call...
    rl.RecordUsage("gemini-2.0-flash", 1000, 500) // promptTokens, outputTokens
}

// Persist state to disk for recovery across restarts.
if err := rl.Persist(); err != nil {
    log.Printf("persist failed: %v", err)
}
```

### Multi-provider configuration

```go
rl, err := ratelimit.NewWithConfig(ratelimit.Config{
    Providers: []ratelimit.Provider{
        ratelimit.ProviderGemini,
        ratelimit.ProviderAnthropic,
    },
    Quotas: map[string]ratelimit.ModelQuota{
        // Override a specific model's limits.
        "gemini-3-pro-preview": {MaxRPM: 50, MaxTPM: 500000, MaxRPD: 200},
        // Add a custom model not in any profile.
        "llama-3.3-70b": {MaxRPM: 5, MaxTPM: 50000, MaxRPD: 0},
    },
})
```

### SQLite backend (multi-process safe)

```go
rl, err := ratelimit.NewWithSQLite("~/.core/ratelimits.db")
if err != nil {
    log.Fatal(err)
}
defer rl.Close()

// Load persisted state.
if err := rl.Load(); err != nil {
    log.Fatal(err)
}

// Use exactly as the YAML backend -- CanSend, RecordUsage, Persist, etc.
```

### Blocking until capacity is available

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

if err := rl.WaitForCapacity(ctx, "claude-opus-4", 2000); err != nil {
    log.Printf("timed out waiting for capacity: %v", err)
    return
}
// Capacity is available; proceed with the API call.
```

## Package Layout

The module is a single package with no sub-packages.

| File | Purpose |
|------|---------|
| `ratelimit.go` | Core types (`RateLimiter`, `ModelQuota`, `Config`, `Provider`), sliding window logic, provider profiles, YAML persistence, `CountTokens` helper |
| `sqlite.go` | SQLite persistence backend (`sqliteStore`), schema creation, load/save operations |
| `ratelimit_test.go` | Tests for core logic, provider profiles, concurrency, and benchmarks |
| `sqlite_test.go` | Tests for SQLite backend, migration, and error recovery |
| `error_test.go` | Tests for SQLite and YAML error paths |
| `iter_test.go` | Tests for `Models()` and `Iter()` iterators, plus `CountTokens` edge cases |

## Dependencies

| Dependency | Purpose | Category |
|------------|---------|----------|
| `gopkg.in/yaml.v3` | YAML serialisation for the legacy persistence backend | Direct |
| `modernc.org/sqlite` | Pure Go SQLite driver (no CGO required) | Direct |
| `github.com/stretchr/testify` | Test assertions (`assert`, `require`) | Test only |

All indirect dependencies are pulled in by `modernc.org/sqlite`. No C toolchain
or system SQLite library is needed.

## Further Reading

- [Architecture](architecture.md) -- sliding window algorithm, provider quotas, YAML and SQLite backends, concurrency model
- [Development](development.md) -- build commands, test patterns, coding standards, commit conventions
- [History](history.md) -- completed phases with commit hashes, known limitations
