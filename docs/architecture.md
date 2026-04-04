<!-- SPDX-License-Identifier: EUPL-1.2 -->

---
title: Architecture
description: Internals of go-ratelimit -- sliding window algorithm, provider quota system, persistence backends, and concurrency model.
---

# Architecture

go-ratelimit is a provider-agnostic rate limiter for LLM API calls. It enforces
three independent quota dimensions per model -- requests per minute (RPM), tokens
per minute (TPM), and requests per day (RPD) -- using an in-memory sliding window
that can be persisted across process restarts via YAML or SQLite.

Module path: `dappco.re/go/core/go-ratelimit`

---

## Key Types

### RateLimiter

The central struct. Holds the quota definitions, current usage state, a mutex for
thread safety, and an optional SQLite backend reference.

```go
type RateLimiter struct {
    mu       sync.RWMutex
    Quotas   map[string]ModelQuota  // per-model quota definitions
    State    map[string]*UsageStats // per-model sliding window state
    filePath string                 // YAML file path (ignored when SQLite is active)
    sqlite   *sqliteStore           // non-nil when using SQLite backend
}
```

### ModelQuota

Defines the rate limits for a single model. A zero value in any field means that
dimension is unlimited.

```go
type ModelQuota struct {
    MaxRPM int `yaml:"max_rpm"` // Requests per minute (0 = unlimited)
    MaxTPM int `yaml:"max_tpm"` // Tokens per minute (0 = unlimited)
    MaxRPD int `yaml:"max_rpd"` // Requests per day (0 = unlimited)
}
```

### UsageStats

Tracks the sliding window state for a single model.

```go
type UsageStats struct {
    Requests []time.Time  // timestamps of recent requests (1-minute window)
    Tokens   []TokenEntry // token counts with timestamps (1-minute window)
    DayStart time.Time    // when the current 24-hour window started
    DayCount int          // total requests since DayStart
}

type TokenEntry struct {
    Time  time.Time
    Count int       // prompt + output tokens for this request
}
```

### Config

Controls `RateLimiter` initialisation.

```go
type Config struct {
    FilePath  string                 // default: ~/.core/ratelimits.yaml
    Backend   string                 // "yaml" (default) or "sqlite"
    Quotas    map[string]ModelQuota  // explicit per-model overrides
    Providers []Provider             // provider profiles to load
}
```

### Provider

A string type identifying an LLM provider. Four constants are defined:

```go
type Provider string

const (
    ProviderGemini    Provider = "gemini"
    ProviderOpenAI    Provider = "openai"
    ProviderAnthropic Provider = "anthropic"
    ProviderLocal     Provider = "local"
)
```

---

## Sliding Window Algorithm

Every call to `CanSend()` or `Stats()` first calls `prune()`, which removes
entries older than one minute from the `Requests` and `Tokens` slices. Pruning
is done in-place using `slices.DeleteFunc` to minimise allocations:

```go
window := now.Add(-1 * time.Minute)

stats.Requests = slices.DeleteFunc(stats.Requests, func(t time.Time) bool {
    return t.Before(window)
})
stats.Tokens = slices.DeleteFunc(stats.Tokens, func(t TokenEntry) bool {
    return t.Time.Before(window)
})
```

After pruning, `CanSend()` checks each quota dimension. If all three limits
(RPM, TPM, RPD) are zero, the model is treated as fully unlimited and the
check short-circuits before touching any state.

The check order is: RPD, then RPM, then TPM. RPD is checked first because it
is the cheapest comparison (a single integer). TPM is checked last because it
requires summing the token counts in the sliding window.

`Decide()` follows the same path as `CanSend()` but returns a structured
`Decision` containing a machine-readable code, reason, `RetryAfter` guidance,
and a `ModelStats` snapshot. It is agent-facing and does not record usage;
`WaitForCapacity()` consumes its `RetryAfter` hint to avoid unnecessary
one-second polling when limits are saturated.

### Daily Reset

The daily counter resets automatically inside `prune()`. When
`now - stats.DayStart >= 24h`, `DayCount` is set to zero and `DayStart` is
updated to the current time. The daily window is a rolling 24-hour period
anchored to the first request of the day, not a calendar boundary.

### Background Pruning

`BackgroundPrune(interval)` starts a goroutine that periodically prunes all
model states on a configurable interval. It returns a cancel function to stop
the pruner:

```go
stop := rl.BackgroundPrune(30 * time.Second)
defer stop()
```

This prevents memory growth in long-running processes where some models may
accumulate stale entries between calls to `CanSend()`.

### Memory Cleanup

When `prune()` empties both the `Requests` and `Tokens` slices for a model,
and `DayCount` is also zero, the entire `UsageStats` entry is deleted from
the `State` map. This prevents memory leaks from models that were used once
and never again.

---

## Provider and Quota Configuration

### Quota Resolution Order

1. Provider profiles are loaded first from `DefaultProfiles()`.
2. Explicit `Config.Quotas` are merged on top using `maps.Copy`, overriding any
   matching model.
3. If neither `Providers` nor `Quotas` are specified, Gemini defaults are used.

`SetQuota()` and `AddProvider()` allow runtime modification. Both acquire the
write lock. `AddProvider()` is additive -- it does not remove existing quotas for
models outside the new provider's profile.

### Default Quotas (as of February 2026)

| Provider  | Model                  | MaxRPM    | MaxTPM      | MaxRPD    |
|-----------|------------------------|-----------|-------------|-----------|
| Gemini    | gemini-3-pro-preview   | 150       | 1,000,000   | 1,000     |
| Gemini    | gemini-3-flash-preview | 150       | 1,000,000   | 1,000     |
| Gemini    | gemini-2.5-pro         | 150       | 1,000,000   | 1,000     |
| Gemini    | gemini-2.0-flash       | 150       | 1,000,000   | unlimited |
| Gemini    | gemini-2.0-flash-lite  | unlimited | unlimited   | unlimited |
| OpenAI    | gpt-4o                 | 500       | 30,000      | unlimited |
| OpenAI    | gpt-4o-mini            | 500       | 200,000     | unlimited |
| OpenAI    | gpt-4-turbo            | 500       | 30,000      | unlimited |
| OpenAI    | o1                     | 500       | 30,000      | unlimited |
| OpenAI    | o1-mini                | 500       | 200,000     | unlimited |
| OpenAI    | o3-mini                | 500       | 200,000     | unlimited |
| Anthropic | claude-opus-4          | 50        | 40,000      | unlimited |
| Anthropic | claude-sonnet-4        | 50        | 40,000      | unlimited |
| Anthropic | claude-haiku-3.5       | 50        | 50,000      | unlimited |
| Local     | (none by default)      | user-defined                         |

The Local provider exists for local inference backends (Ollama, MLX, llama.cpp)
where the throttle limit is hardware rather than an API quota. No defaults are
provided; callers add per-model limits via `Config.Quotas` or `SetQuota()`.

---

## Constructors

| Function | Backend | Default Provider |
|----------|---------|------------------|
| `New()` | YAML | Gemini |
| `NewWithConfig(cfg)` | YAML | Configurable (Gemini if empty) |
| `NewWithSQLite(dbPath)` | SQLite | Gemini |
| `NewWithSQLiteConfig(dbPath, cfg)` | SQLite | Configurable (Gemini if empty) |

`Close()` releases the database connection for SQLite-backed limiters. It is a
no-op on YAML-backed limiters. Always call `Close()` (or `defer rl.Close()`)
when using the SQLite backend.

---

## Data Flow

A typical request lifecycle:

```
1. CanSend(model, estimatedTokens)
   |-- acquires write lock
   |-- looks up ModelQuota for the model
   |-- if unknown model or all-zero quota: returns true (allowed)
   |-- calls prune(model) to discard stale entries
   |-- checks RPD, RPM, TPM against the pruned state
   '-- returns true/false

2. (caller makes the API call)

3. RecordUsage(model, promptTokens, outputTokens)
   |-- acquires write lock
   |-- calls prune(model)
   |-- appends to Requests and Tokens slices
   '-- increments DayCount

4. Persist()
   |-- acquires write lock, clones state, releases lock
   |-- YAML: marshals to file
   '-- SQLite: saves quotas and state in transactions
```

---

## YAML Persistence

The default backend serialises both the `Quotas` map and the `State` map to a
YAML file at `~/.core/ratelimits.yaml` (configurable via `Config.FilePath`).

```yaml
quotas:
  gemini-3-pro-preview:
    max_rpm: 150
    max_tpm: 1000000
    max_rpd: 1000
state:
  gemini-3-pro-preview:
    requests:
      - 2026-02-20T14:32:01.123456789Z
    tokens:
      - time: 2026-02-20T14:32:01.123456789Z
        count: 1500
    day_start: 2026-02-20T00:00:00Z
    day_count: 42
```

`Persist()` creates parent directories with the `core.Fs` helper before writing.
`Load()` treats a missing file as an empty state (no error). Corrupt or
unreadable files return an error.

**Limitations of the YAML backend:**

- Single-process only. Concurrent writes from multiple processes corrupt the
  file because the write is not atomic at the OS level.
- The entire state is serialised on every `Persist()` call.
- Timestamps are serialised as RFC 3339 strings.

---

## SQLite Backend

The SQLite backend supports multi-process scenarios. It uses `modernc.org/sqlite`,
a pure Go port of SQLite that compiles without CGO.

### Connection Settings

```go
db.SetMaxOpenConns(1)                  // single connection for PRAGMA consistency
db.Exec("PRAGMA journal_mode=WAL")     // concurrent readers alongside a single writer
db.Exec("PRAGMA busy_timeout=5000")    // 5-second wait on lock contention
```

WAL mode allows one writer and multiple concurrent readers. The 5-second busy
timeout prevents immediate failure when a second process is mid-commit.

### Schema

```sql
CREATE TABLE IF NOT EXISTS quotas (
    model   TEXT PRIMARY KEY,
    max_rpm INTEGER NOT NULL DEFAULT 0,
    max_tpm INTEGER NOT NULL DEFAULT 0,
    max_rpd INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS requests (
    model TEXT NOT NULL,
    ts    INTEGER NOT NULL         -- UnixNano
);

CREATE TABLE IF NOT EXISTS tokens (
    model TEXT NOT NULL,
    ts    INTEGER NOT NULL,        -- UnixNano
    count INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS daily (
    model     TEXT PRIMARY KEY,
    day_start INTEGER NOT NULL,    -- UnixNano
    day_count INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_requests_model_ts ON requests(model, ts);
CREATE INDEX IF NOT EXISTS idx_tokens_model_ts   ON tokens(model, ts);
```

Timestamps are stored as `INTEGER` UnixNano values. This preserves nanosecond
precision and allows efficient range queries using the composite indices.

### Save Strategy

- **Quotas**: full snapshot replace inside a single transaction. `saveQuotas()`
  clears the table and reinserts the current quota map.
- **State**: Delete-then-insert inside a single transaction. All three state
  tables (`requests`, `tokens`, `daily`) are truncated and rewritten atomically.

---

## Migration Path

`MigrateYAMLToSQLite(yamlPath, sqlitePath)` reads an existing YAML state file
and writes all quotas and usage state to a new SQLite database. The function is
idempotent -- running it again overwrites the SQLite database state.

```go
err := ratelimit.MigrateYAMLToSQLite(
    filepath.Join(home, ".core", "ratelimits.yaml"),
    filepath.Join(home, ".core", "ratelimits.db"),
)
```

After migration, switch the constructor from `New()` to `NewWithSQLite()`. The
YAML file can be kept as a backup; the two backends do not share state.

---

## Iterators

Two Go 1.26+ iterators are provided for inspecting the limiter state:

- `Models() iter.Seq[string]` -- returns a sorted sequence of all model names
  (from both `Quotas` and `State` maps, deduplicated).
- `Iter() iter.Seq2[string, ModelStats]` -- returns sorted model names paired
  with their current `ModelStats` snapshot.

```go
for model, stats := range rl.Iter() {
    fmt.Printf("%s: %d/%d RPM, %d/%d TPM\n",
        model, stats.RPM, stats.MaxRPM, stats.TPM, stats.MaxTPM)
}
```

---

## CountTokens

`CountTokens(ctx, apiKey, model, text)` calls the Google Generative Language API
to obtain an exact token count for a prompt string. It is Gemini-specific and
hardcodes the `generativelanguage.googleapis.com` endpoint.

For other providers, callers must supply `estimatedTokens` directly to
`CanSend()`. Accurate token counts are typically available in API response
metadata after a call completes.

---

## Concurrency Model

All reads and writes are protected by a single `sync.RWMutex` on the
`RateLimiter` struct.

| Method | Lock type | Reason |
|--------|-----------|--------|
| `CanSend()` | Write | Calls `prune()`, which mutates state slices |
| `RecordUsage()` | Write | Appends to state slices |
| `Reset()` | Write | Deletes state entries |
| `Load()` | Write | Replaces in-memory state |
| `SetQuota()` | Write | Modifies quota map |
| `AddProvider()` | Write | Modifies quota map |
| `Persist()` | Write (brief) | Clones state, then releases lock before I/O |
| `Stats()` | Write | Calls `prune()` |
| `AllStats()` | Write | Prunes inline |
| `Models()` | Read | Reads keys only |

`Persist()` minimises lock contention by cloning the state under a write lock,
then performing I/O after releasing the lock. The test suite passes clean under
`go test -race ./...` with 20 goroutines performing concurrent operations.
