# Architecture

go-ratelimit is a provider-agnostic rate limiter for LLM API calls. It enforces
three independent quota dimensions per model — requests per minute (RPM), tokens
per minute (TPM), and requests per day (RPD) — using an in-memory sliding window
that can be persisted across process restarts via YAML or SQLite.

Module path: `forge.lthn.ai/core/go-ratelimit`

---

## Sliding Window Algorithm

The limiter maintains per-model `UsageStats` structs in memory:

```go
type UsageStats struct {
    Requests []time.Time  // timestamps of recent requests (1-minute window)
    Tokens   []TokenEntry // token counts with timestamps (1-minute window)
    DayStart time.Time    // when the current daily window started
    DayCount int          // total requests recorded since DayStart
}
```

Every call to `CanSend()` or `Stats()` first calls `prune()`, which scans both
slices and discards entries older than `now - 1 minute`. Pruning is done
in-place to avoid allocation on the hot path:

```go
validReqs := 0
for _, t := range stats.Requests {
    if t.After(window) {
        stats.Requests[validReqs] = t
        validReqs++
    }
}
stats.Requests = stats.Requests[:validReqs]
```

The same loop runs for token entries. After pruning, `CanSend()` checks each
quota dimension in priority order: RPD first (cheapest check), then RPM, then
TPM. A zero value for any dimension means that dimension is unlimited. If all
three are zero the model is treated as fully unlimited and the check short-circuits
before touching any state.

### Daily Reset

The daily counter resets automatically inside `prune()`. When
`now - stats.DayStart >= 24h`, `DayCount` is set to zero and `DayStart` is set
to the current time. This means the daily window is a rolling 24-hour period
anchored to the first request of the day, not a calendar boundary.

### Concurrency

All reads and writes are protected by a single `sync.RWMutex`. Methods that
write state — `CanSend()`, `RecordUsage()`, `Reset()`, `Load()` — acquire a
full write lock. `Persist()`, `Stats()`, and `AllStats()` acquire a read lock
where possible. The `CanSend()` method acquires a write lock because it calls
`prune()`, which mutates the state slices.

`go test -race ./...` passes clean with 20 goroutines performing concurrent
`CanSend()`, `RecordUsage()`, and `Stats()` calls.

---

## Provider and Quota Configuration

### Types

```go
type Provider string          // "gemini", "openai", "anthropic", "local"

type ModelQuota struct {
    MaxRPM int `yaml:"max_rpm"` // 0 = unlimited
    MaxTPM int `yaml:"max_tpm"`
    MaxRPD int `yaml:"max_rpd"`
}

type Config struct {
    FilePath  string                 // default: ~/.core/ratelimits.yaml
    Backend   string                 // "yaml" (default) or "sqlite"
    Quotas    map[string]ModelQuota  // explicit per-model overrides
    Providers []Provider             // provider profiles to load
}
```

### Quota Resolution

1. Provider profiles are loaded first (from `DefaultProfiles()`).
2. Explicit `Config.Quotas` are merged on top, overriding any matching model.
3. If neither `Providers` nor `Quotas` are specified, Gemini defaults are used.

`SetQuota()` and `AddProvider()` allow runtime modification; both are
mutex-protected. `AddProvider()` is additive — it does not remove existing
quotas for models outside the new provider's profile.

### Default Quotas (as of February 2026)

| Provider  | Model                  | MaxRPM    | MaxTPM    | MaxRPD    |
|-----------|------------------------|-----------|-----------|-----------|
| Gemini    | gemini-3-pro-preview   | 150       | 1,000,000 | 1,000     |
| Gemini    | gemini-3-flash-preview | 150       | 1,000,000 | 1,000     |
| Gemini    | gemini-2.5-pro         | 150       | 1,000,000 | 1,000     |
| Gemini    | gemini-2.0-flash       | 150       | 1,000,000 | unlimited |
| Gemini    | gemini-2.0-flash-lite  | unlimited | unlimited | unlimited |
| OpenAI    | gpt-4o, gpt-4-turbo    | 500       | 30,000    | unlimited |
| OpenAI    | gpt-4o-mini, o1-mini   | 500       | 200,000   | unlimited |
| OpenAI    | o1, o3-mini            | 500       | varies    | unlimited |
| Anthropic | claude-opus-4          | 50        | 40,000    | unlimited |
| Anthropic | claude-sonnet-4        | 50        | 40,000    | unlimited |
| Anthropic | claude-haiku-3.5       | 50        | 50,000    | unlimited |
| Local     | (none by default)      | user-defined                          |

The Local provider exists for local inference backends (Ollama, MLX, llama.cpp)
where the throttle limit is hardware rather than an API quota. No defaults are
provided; callers add per-model limits via `Config.Quotas` or `SetQuota()`.

---

## YAML Persistence (Legacy)

The default backend serialises the entire `RateLimiter` struct — both the
`Quotas` map and the `State` map — to a YAML file at `~/.core/ratelimits.yaml`.

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

`Persist()` creates parent directories with `os.MkdirAll` before writing.
`Load()` treats a missing file as an empty state (no error). Corrupt or
unreadable files return an error.

**Limitations of YAML backend:**
- Single-process only. Concurrent writes from multiple processes corrupt the
  file because the write is not atomic at the OS level.
- The entire state is serialised on every `Persist()` call, which grows linearly
  with the number of tracked models and entries.
- Timestamps are serialised as RFC3339 strings; sub-nanosecond precision is
  preserved by Go's time marshaller but depends on the YAML library.

---

## SQLite Backend

The SQLite backend was added in Phase 2 to support multi-process scenarios and
provide a more robust persistence layer. It uses `modernc.org/sqlite` — a pure
Go port of SQLite that compiles without CGO.

### Connection Settings

```go
db.SetMaxOpenConns(1)                      // single connection for PRAGMA consistency
db.Exec("PRAGMA journal_mode=WAL")         // WAL mode for concurrent readers
db.Exec("PRAGMA busy_timeout=5000")        // 5-second busy timeout
```

WAL mode allows one writer and multiple concurrent readers. The 5-second busy
timeout prevents immediate failure when a second process is mid-commit. A single
`sql.DB` connection is used because SQLite's WAL mode handles reader concurrency
at the file level; multiple Go connections to the same file through a single
process would not add throughput but would complicate locking.

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
    day_start INTEGER NOT NULL,   -- UnixNano
    day_count INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_requests_model_ts ON requests(model, ts);
CREATE INDEX IF NOT EXISTS idx_tokens_model_ts   ON tokens(model, ts);
```

Timestamps are stored as `INTEGER` UnixNano values. This preserves nanosecond
precision without relying on SQLite's text date format, and allows efficient
range queries using the composite indices.

### Save Strategy

`saveState()` uses a delete-then-insert pattern inside a single transaction.
All three state tables are truncated and rewritten atomically:

```go
tx.Exec("DELETE FROM requests")
tx.Exec("DELETE FROM tokens")
tx.Exec("DELETE FROM daily")
// then INSERT for every model in state
tx.Commit()
```

`saveQuotas()` uses `INSERT ... ON CONFLICT(model) DO UPDATE` (upsert) so
existing quota rows are updated in place without deleting unrelated models.

### Constructors

```go
// YAML backend (default)
rl, err := ratelimit.New()
rl, err := ratelimit.NewWithConfig(cfg)

// SQLite backend
rl, err := ratelimit.NewWithSQLite(dbPath)
rl, err := ratelimit.NewWithSQLiteConfig(dbPath, cfg)

defer rl.Close()  // releases the database connection
```

`Close()` is a no-op on YAML-backed limiters.

---

## Migration Path

`MigrateYAMLToSQLite(yamlPath, sqlitePath string) error` reads an existing YAML
state file and writes all quotas and usage state to a new SQLite database. The
function is idempotent — running it again on the same YAML file overwrites the
SQLite database state.

Typical one-time migration:

```go
err := ratelimit.MigrateYAMLToSQLite(
    filepath.Join(home, ".core", "ratelimits.yaml"),
    filepath.Join(home, ".core", "ratelimits.db"),
)
```

After migration, switch the constructor:

```go
// Before
rl, _ := ratelimit.New()

// After
rl, _ := ratelimit.NewWithSQLite(filepath.Join(home, ".core", "ratelimits.db"))
defer rl.Close()
```

The YAML file can be kept as a backup; the two backends do not share state.

---

## CountTokens

`CountTokens(apiKey, model, text string) (int, error)` calls the Google
Generative Language API to obtain an exact token count for a prompt string. It
is Gemini-specific and hardcodes the `generativelanguage.googleapis.com`
endpoint. The URL is not configurable, which prevents unit testing of the
success path without network access.

For other providers, callers must supply `estimatedTokens` directly to
`CanSend()` and `RecordUsage()`. Accurate token counts are typically available
in API response metadata after a call completes.
