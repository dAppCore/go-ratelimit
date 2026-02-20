# TODO.md -- go-ratelimit

Dispatched from core/go orchestration. Pick up tasks in order.

---

## Phase 0: Hardening & Test Coverage

- [x] **Expand test coverage** -- `ratelimit_test.go` rewritten with testify. Tests for: `CanSend()` at exact limits (RPM, TPM, RPD boundaries), `RecordUsage()` with concurrent goroutines, `WaitForCapacity()` timeout and immediate-capacity paths, `prune()` sliding window edge cases, daily reset logic (24h boundary), YAML persistence (save + reload), corrupt/unreadable state file recovery, `Reset()` single/all/nonexistent, `Stats()` known/unknown/quota-only models, `AllStats()` with pruning and daily reset.
- [x] **Race condition test** -- `go test -race ./...` with 20 goroutines calling `CanSend()` + `RecordUsage()` + `Stats()` concurrently. Additional tests: concurrent `Reset()` + `RecordUsage()` + `AllStats()`, concurrent multi-model access (5 models), concurrent `Persist()` + `Load()` filesystem race, concurrent `AllStats()` + `RecordUsage()`, concurrent `WaitForCapacity()` + `RecordUsage()`. All pass clean.
- [x] **Benchmark** -- 7 benchmarks: `BenchmarkCanSend` (1000-entry window), `BenchmarkRecordUsage`, `BenchmarkCanSendConcurrent` (parallel), `BenchmarkCanSendWithPrune` (500 old + 500 new), `BenchmarkStats` (1000 entries), `BenchmarkAllStats` (5 models x 200 entries), `BenchmarkPersist` (YAML I/O). Zero allocs on hot paths.
- [x] **`go vet ./...` clean** -- No warnings.
- **Coverage: 95.1%** (up from 77.1%). Remaining uncovered: `CountTokens` success path (hardcoded Google URL), `yaml.Marshal` error path in `Persist()`, `os.UserHomeDir` error path in `NewWithConfig`.

## Phase 1: Generalise Beyond Gemini

- [x] **Provider-agnostic config** -- Added `Provider` type, `ProviderProfile`, `Config` struct, `NewWithConfig()` constructor. Quotas are no longer hardcoded in `New()`.
- [x] **Quota profiles** -- `DefaultProfiles()` returns pre-configured profiles for Gemini, OpenAI (gpt-4o, o1, o3-mini), Anthropic (claude-opus-4, claude-sonnet-4, claude-haiku-3.5), and Local (empty, user-configurable).
- [x] **Configurable defaults** -- `Config` struct accepts `FilePath`, `Providers` list, and explicit `Quotas` map. Explicit quotas override provider defaults. YAML-serialisable.
- [x] **Backward compatibility** -- `New()` delegates to `NewWithConfig(Config{Providers: []Provider{ProviderGemini}})`. Existing API unchanged. Test `TestNewBackwardCompatibility` verifies exact parity.
- [x] **Runtime configuration** -- `SetQuota()` and `AddProvider()` allow modifying quotas after construction. Both are mutex-protected.

## Phase 2: SQLite Persistent State

Current YAML persistence is single-process only. Phase 2 adds multi-process safe SQLite storage following the go-store pattern (`modernc.org/sqlite`, pure Go, no CGO).

### 2.1 SQLite Backend

- [x] **Add `modernc.org/sqlite` dependency** — `go get modernc.org/sqlite`. Pure Go, compiles everywhere.
- [x] **Create `sqlite.go`** — Internal SQLite persistence layer:
  - `type sqliteStore struct { db *sql.DB }` — wraps database/sql connection
  - `func newSQLiteStore(dbPath string) (*sqliteStore, error)` — Open DB, set `PRAGMA journal_mode=WAL`, `PRAGMA busy_timeout=5000`, `db.SetMaxOpenConns(1)`. Create schema:
    ```sql
    CREATE TABLE IF NOT EXISTS quotas (
        model TEXT PRIMARY KEY,
        max_rpm INTEGER NOT NULL DEFAULT 0,
        max_tpm INTEGER NOT NULL DEFAULT 0,
        max_rpd INTEGER NOT NULL DEFAULT 0
    );
    CREATE TABLE IF NOT EXISTS requests (
        model TEXT NOT NULL,
        ts INTEGER NOT NULL  -- UnixNano
    );
    CREATE TABLE IF NOT EXISTS tokens (
        model TEXT NOT NULL,
        ts INTEGER NOT NULL,  -- UnixNano
        count INTEGER NOT NULL
    );
    CREATE TABLE IF NOT EXISTS daily (
        model TEXT PRIMARY KEY,
        day_start INTEGER NOT NULL,
        day_count INTEGER NOT NULL DEFAULT 0
    );
    CREATE INDEX IF NOT EXISTS idx_requests_model_ts ON requests(model, ts);
    CREATE INDEX IF NOT EXISTS idx_tokens_model_ts ON tokens(model, ts);
    ```
  - `func (s *sqliteStore) saveQuotas(quotas map[string]ModelQuota) error` — UPSERT all quotas
  - `func (s *sqliteStore) loadQuotas() (map[string]ModelQuota, error)` — SELECT all quotas
  - `func (s *sqliteStore) saveState(state map[string]*UsageStats) error` — Transaction: DELETE old + INSERT requests/tokens/daily for each model
  - `func (s *sqliteStore) loadState() (map[string]*UsageStats, error)` — SELECT and reconstruct UsageStats map
  - `func (s *sqliteStore) close() error` — Close DB connection

### 2.2 Wire Into RateLimiter

- [x] **Add `Backend` field to Config** — `Backend string` with values `"yaml"` (default), `"sqlite"`. Default `""` maps to `"yaml"` for backward compat.
- [x] **Update `Persist()` and `Load()`** — Check internal backend type. If SQLite, use `sqliteStore`; otherwise use existing YAML. Keep both paths working.
- [x] **Add `NewWithSQLite(dbPath string) (*RateLimiter, error)`** — Convenience constructor that creates a SQLite-backed limiter. Sets backend type, initialises DB.
- [x] **Graceful close** — Add `Close() error` method that closes SQLite DB if open. No-op for YAML backend.

### 2.3 Tests

- [x] **SQLite basic tests** — newSQLiteStore, saveQuotas/loadQuotas round-trip, saveState/loadState round-trip, close.
- [x] **SQLite integration** — NewWithSQLite, RecordUsage → Persist → Load → verify state preserved. Same test matrix as existing YAML tests but with SQLite backend.
- [x] **Concurrent SQLite** — 10 goroutines x 20 ops (RecordUsage + CanSend + Persist). Race-clean.
- [x] **YAML backward compat** — Existing tests pass unchanged (still default to YAML).
- [x] **Migration helper** — `MigrateYAMLToSQLite(yamlPath, sqlitePath string) error` — reads YAML state, writes to SQLite. Test with sample YAML.
- [x] **Corrupt DB recovery** — Truncated DB file → graceful error, fresh start.

## Phase 3: Integration

- [ ] Wire into go-ml backends for automatic rate limiting on inference calls
- [ ] Wire into go-ai facade so all providers share a unified rate limit layer
- [ ] Add metrics export (requests/minute, tokens/minute, rejections) for monitoring

---

## Workflow

1. Virgil in core/go writes tasks here after research
2. This repo's dedicated session picks up tasks in phase order
3. Mark `[x]` when done, note commit hash
