<!-- SPDX-License-Identifier: EUPL-1.2 -->

# Project History

## Origin

go-ratelimit was extracted from the `pkg/ratelimit` package inside
`forge.lthn.ai/core/go` on 19 February 2026. The package now lives at
`dappco.re/go/core/go-ratelimit`, with its own repository and independent
development cadence.

Initial commit: `fa1a6fc` — `feat: extract go-ratelimit from core/go pkg/ratelimit`

At extraction the package implemented:

- Sliding window rate limiter with 1-minute window
- Daily request caps per model
- Token counting via Google `CountTokens` API
- Hardcoded Gemini quota defaults (`gemini-3-pro-preview`: 150 RPM / 1M TPM / 1000 RPD)
- YAML persistence to `~/.core/ratelimits.yaml`
- Single test file with basic sliding window and quota enforcement tests

---

## Phase 0 — Hardening and Test Coverage

Commit: `3c63b10` — `feat(ratelimit): generalise beyond Gemini with provider profiles and push coverage to 95%`

Supplementary commit: `db958f2` — `test: expand race coverage and benchmarks`

Coverage increased from 77.1% to above the 95% floor. The test suite was rewritten using
testify with table-driven subtests throughout.

### Tests added

- `TestCanSend` — boundary conditions at exact RPM, TPM, and RPD limits;
  RPM-only and TPM-only quotas; zero-token estimates; unknown and unlimited models
- `TestPrune` — pruning of old entries, retention of recent entries, daily reset
  at 24-hour boundary, no-op on non-existent model, boundary-exact timestamps
- `TestRecordUsage` — fresh state, accumulation, insertion into existing state
- `TestReset` — single model, all models (empty string argument), non-existent model
- `TestWaitForCapacity` — context cancellation, pre-cancelled context,
  immediate capacity, unknown model
- `TestStats` / `TestAllStats` — known, unknown, and quota-only models; pruning
  and daily reset inside `AllStats()`
- `TestPersistAndLoad` — round-trip, missing file, corrupt YAML, unreadable file,
  nested directory creation, unwritable directory
- `TestConcurrentAccess` — 20 goroutines x 50 ops each (CanSend + RecordUsage + Stats)
- `TestConcurrentResetAndRecord` — concurrent Reset + RecordUsage + AllStats
- `TestConcurrentMultipleModels` — 5 models, concurrent access
- `TestConcurrentPersistAndLoad` — filesystem race between Persist and Load
- `TestConcurrentWaitForCapacityAndRecordUsage` — WaitForCapacity racing RecordUsage

### Benchmarks added

- `BenchmarkCanSend` — 1,000-entry sliding window
- `BenchmarkRecordUsage`
- `BenchmarkCanSendConcurrent` — parallel goroutines
- `BenchmarkCanSendWithPrune` — 500 old + 500 new entries
- `BenchmarkStats` — 1,000-entry window
- `BenchmarkAllStats` — 5 models x 200 entries
- `BenchmarkPersist` — YAML I/O

`go test -race ./...` passed clean. `go vet ./...` produced no warnings.

---

## Phase 1 — Generalisation Beyond Gemini

Commit: `3c63b10` — included in the same commit as Phase 0

The hardcoded Gemini quotas in `New()` were replaced with a provider-agnostic
configuration system without breaking the existing API.

### New types and functions

- `Provider` string type with constants: `ProviderGemini`, `ProviderOpenAI`,
  `ProviderAnthropic`, `ProviderLocal`
- `ProviderProfile` — bundles a provider identifier with its model quota map
- `Config` — construction configuration accepting `FilePath`, `Backend`,
  `Providers`, and `Quotas` fields
- `DefaultProfiles()` — returns fresh pre-configured profiles for all four providers
- `NewWithConfig(Config)` — creates a limiter from explicit configuration
- `SetQuota(model, quota)` — runtime quota modification, mutex-protected
- `AddProvider(provider)` — loads all default quotas for a provider at runtime,
  additive, mutex-protected

### Backward compatibility

`New()` delegates to `NewWithConfig(Config{Providers: []Provider{ProviderGemini}})`.
`TestNewBackwardCompatibility` asserts exact parity with the original hardcoded
values. No existing call sites required modification.

### Design decision: merge-on-top

Explicit `Config.Quotas` override provider profile defaults. This allows callers
to use a provider profile for most models while customising specific model limits
without forking the entire profile.

---

## Phase 2 — SQLite Persistent State

Commit: `1afb1d6` — `feat(persist): Phase 2 — SQLite backend with WAL mode`

The YAML backend serialises the full state on every `Persist()` call and is
not safe for concurrent multi-process access. Phase 2 added a SQLite backend
using `modernc.org/sqlite` (pure Go, no CGO) following the go-store pattern
established elsewhere in the ecosystem.

### New constructors

- `NewWithSQLite(dbPath string)` — SQLite-backed limiter with Gemini defaults
- `NewWithSQLiteConfig(dbPath string, cfg Config)` — SQLite-backed with custom config
- `Close() error` — releases the database connection; no-op on YAML-backed limiters

### Migration

- `MigrateYAMLToSQLite(yamlPath, sqlitePath string) error` — one-shot migration
  helper that reads an existing YAML state file and writes all quotas and usage
  state to a new SQLite database

### SQLite connection settings

- `PRAGMA journal_mode=WAL` — enables concurrent reads alongside a single writer
- `PRAGMA busy_timeout=5000` — 5-second wait on lock contention before returning an error
- `db.SetMaxOpenConns(1)` — single connection for PRAGMA consistency

### Tests added (sqlite_test.go)

- `TestNewSQLiteStore_Good / _Bad` — creation and invalid path handling
- `TestSQLiteQuotasRoundTrip_Good` — save/load round-trip
- `TestSQLite_QuotasOverwrite_Good` — the latest quota snapshot replaces previous rows
- `TestSQLiteStateRoundTrip_Good` — multi-model state with nanosecond precision
- `TestSQLiteStateOverwrite_Good` — delete-then-insert atomicity
- `TestSQLiteEmptyState_Good` — fresh database returns empty maps
- `TestNewWithSQLite_Good / TestNewWithSQLiteConfig_Good` — constructor tests
- `TestSQLitePersistAndLoad_Good` — full persist + reload cycle
- `TestSQLitePersistMultipleModels_Good` — multi-provider persistence
- `TestSQLiteConcurrent_Good` — 10 goroutines x 20 ops, race-clean
- `TestYAMLBackwardCompat_Good` — existing YAML tests pass unchanged
- `TestMigrateYAMLToSQLite_Good / _Bad` — migration round-trip and error paths
- `TestSQLiteCorruptDB_Ugly / TestSQLiteTruncatedDB_Ugly` — graceful corrupt DB recovery
- `TestSQLiteEndToEnd_Good` — full two-session scenario

---

## Phase 3 — Integration (Planned)

Not yet implemented. Intended downstream integrations:

- Wire into `go-ml` backends so rate limiting is enforced automatically on
  inference calls without caller involvement
- Wire into the `go-ai` facade so all providers share a single rate limit layer
- Export metrics (requests/minute, tokens/minute, rejection counts) for
  monitoring dashboards

---

## Known Limitations

**CountTokens URL is hardcoded.** The exported `CountTokens` helper calls
`generativelanguage.googleapis.com` directly. Callers cannot redirect it to
Gemini-compatible proxies or alternate endpoints without going through an
internal helper or refactoring the API to accept a base URL or `http.Client`.

**saveState is a full table replace.** On every `Persist()` call, the `requests`,
`tokens`, and `daily` tables are truncated and rewritten. For a limiter tracking
many models with high RPM, this means writing hundreds of rows on every persist
call. A future optimisation would use incremental writes (insert-only, with
periodic vacuuming of expired rows).

**No TTL on SQLite rows.** Historical rows older than one minute are pruned from
the in-memory `UsageStats` on every operation but are written wholesale to
SQLite on `Persist()`. The database does not grow unboundedly between persist
cycles because `saveState` replaces all rows, but if `Persist()` is called
frequently the WAL file can grow transiently.

**WaitForCapacity polling interval is fixed at 1 second.** This is appropriate
for RPM-scale limits but is coarse for sub-second limits. If a caller needs
finer-grained waiting (e.g., smoothing requests within a minute), they must
implement their own loop.

**No automatic persistence.** `Persist()` must be called explicitly. If a
process exits without calling `Persist()`, any usage recorded since the last
persist is lost. Callers are responsible for calling `Persist()` at appropriate
intervals (e.g., after each `RecordUsage()` call, or on a ticker).
