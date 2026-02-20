# TODO.md -- go-ratelimit

Dispatched from core/go orchestration. Pick up tasks in order.

---

## Phase 0: Hardening & Test Coverage

- [x] **Expand test coverage** -- `ratelimit_test.go` rewritten with testify. Tests for: `CanSend()` at exact limits (RPM, TPM, RPD boundaries), `RecordUsage()` with concurrent goroutines, `WaitForCapacity()` timeout and immediate-capacity paths, `prune()` sliding window edge cases, daily reset logic (24h boundary), YAML persistence (save + reload), corrupt/unreadable state file recovery, `Reset()` single/all/nonexistent, `Stats()` known/unknown/quota-only models, `AllStats()` with pruning and daily reset.
- [x] **Race condition test** -- `go test -race ./...` with 20 goroutines calling `CanSend()` + `RecordUsage()` + `Stats()` concurrently. Additional test with concurrent `Reset()` + `RecordUsage()` + `AllStats()`. All pass clean.
- [x] **Benchmark** -- `BenchmarkCanSend` (1000-entry window), `BenchmarkRecordUsage`, `BenchmarkCanSendConcurrent` (parallel). Measures prune() overhead.
- [x] **`go vet ./...` clean** -- No warnings.
- **Coverage: 95.1%** (up from 77.1%). Remaining uncovered: `CountTokens` success path (hardcoded Google URL), `yaml.Marshal` error path in `Persist()`, `os.UserHomeDir` error path in `NewWithConfig`.

## Phase 1: Generalise Beyond Gemini

- [x] **Provider-agnostic config** -- Added `Provider` type, `ProviderProfile`, `Config` struct, `NewWithConfig()` constructor. Quotas are no longer hardcoded in `New()`.
- [x] **Quota profiles** -- `DefaultProfiles()` returns pre-configured profiles for Gemini, OpenAI (gpt-4o, o1, o3-mini), Anthropic (claude-opus-4, claude-sonnet-4, claude-haiku-3.5), and Local (empty, user-configurable).
- [x] **Configurable defaults** -- `Config` struct accepts `FilePath`, `Providers` list, and explicit `Quotas` map. Explicit quotas override provider defaults. YAML-serialisable.
- [x] **Backward compatibility** -- `New()` delegates to `NewWithConfig(Config{Providers: []Provider{ProviderGemini}})`. Existing API unchanged. Test `TestNewBackwardCompatibility` verifies exact parity.
- [x] **Runtime configuration** -- `SetQuota()` and `AddProvider()` allow modifying quotas after construction. Both are mutex-protected.

## Phase 2: Persistent State

- [ ] Currently stores state in YAML file -- not safe for multi-process access
- [ ] Consider SQLite for concurrent read/write safety (WAL mode)
- [ ] Add state recovery on restart (reload sliding window from persisted data)

## Phase 3: Integration

- [ ] Wire into go-ml backends for automatic rate limiting on inference calls
- [ ] Wire into go-ai facade so all providers share a unified rate limit layer
- [ ] Add metrics export (requests/minute, tokens/minute, rejections) for monitoring

---

## Workflow

1. Virgil in core/go writes tasks here after research
2. This repo's dedicated session picks up tasks in phase order
3. Mark `[x]` when done, note commit hash
