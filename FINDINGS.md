# FINDINGS.md -- go-ratelimit

## 2026-02-19: Split from core/go (Virgil)

### Origin

Extracted from `forge.lthn.ai/core/go` on 19 Feb 2026.

### Architecture

- Sliding window rate limiter (1-minute window)
- Daily request caps per model
- Token counting via Google `CountTokens` API
- Model-specific quota configuration

### Gemini-Specific Defaults

- `gemini-3-pro-preview`: 150 RPM / 1M TPM / 1000 RPD
- Quotas are currently hardcoded -- needs generalisation (see TODO Phase 1)

### Tests

- 1 test file covering sliding window and quota enforcement

---

## 2026-02-20: Phase 0 -- Hardening (Charon)

### Coverage: 77.1% -> 95.1%

Rewrote test suite with testify assert/require. Table-driven subtests throughout.

#### Tests added

- **CanSend boundaries**: exact RPM/TPM/RPD limits, RPM-only, TPM-only, zero-token estimates, unknown models, unlimited models
- **Prune**: keeps recent entries, prunes old ones, daily reset at 24h, boundary-exact timestamps, noop on non-existent model
- **RecordUsage**: fresh state, accumulation, existing state
- **Reset**: single model, all models (empty string), non-existent model
- **WaitForCapacity**: immediate capacity, context cancellation, pre-cancelled context, unknown model
- **Stats/AllStats**: known/unknown/quota-only models, pruning in AllStats, daily reset in AllStats
- **Persist/Load**: round-trip, non-existent file, corrupt YAML, unreadable file, nested directory creation, unwritable directory
- **Concurrency**: 20 goroutines x 50 ops (CanSend + RecordUsage + Stats), concurrent Reset + RecordUsage + AllStats
- **Benchmarks**: BenchmarkCanSend (1000-entry window), BenchmarkRecordUsage, BenchmarkCanSendConcurrent

#### Remaining uncovered (5%)

- `CountTokens` success path: hardcoded Google URL prevents unit testing without URL injection. Only the connection-error path is covered.
- `yaml.Marshal` error in `Persist()`: virtually impossible to trigger with valid structs.
- `os.UserHomeDir` error in `NewWithConfig()`: only fails when `$HOME` is unset.

### Race detector

`go test -race ./...` passes clean. The `sync.RWMutex` correctly guards all shared state.

### go vet

No warnings.

---

## 2026-02-20: Phase 1 -- Generalisation (Charon)

### Problem

Hardcoded Gemini-specific quotas in `New()`. No way to configure for other providers.

### Solution

Introduced provider-agnostic configuration without breaking existing API.

#### New types

- `Provider` -- string type with constants: `ProviderGemini`, `ProviderOpenAI`, `ProviderAnthropic`, `ProviderLocal`
- `ProviderProfile` -- bundles provider identity with model quotas map
- `Config` -- construction config with `FilePath`, `Providers` list, `Quotas` map

#### New functions

- `DefaultProfiles()` -- returns pre-configured profiles for all four providers
- `NewWithConfig(Config)` -- creates limiter from explicit configuration
- `SetQuota(model, quota)` -- runtime quota modification
- `AddProvider(provider)` -- loads all default quotas for a provider at runtime

#### Provider defaults (Feb 2026)

| Provider | Models | RPM | TPM | RPD |
|----------|--------|-----|-----|-----|
| Gemini | gemini-3-pro-preview, gemini-3-flash-preview, gemini-2.5-pro | 150 | 1M | 1000 |
| Gemini | gemini-2.0-flash | 150 | 1M | unlimited |
| Gemini | gemini-2.0-flash-lite | unlimited | unlimited | unlimited |
| OpenAI | gpt-4o, gpt-4-turbo, o1 | 500 | 30K | unlimited |
| OpenAI | gpt-4o-mini, o1-mini, o3-mini | 500 | 200K | unlimited |
| Anthropic | claude-opus-4, claude-sonnet-4 | 50 | 40K | unlimited |
| Anthropic | claude-haiku-3.5 | 50 | 50K | unlimited |
| Local | (none by default) | -- | -- | -- |

#### Backward compatibility

`New()` delegates to `NewWithConfig(Config{Providers: []Provider{ProviderGemini}})`. Verified by `TestNewBackwardCompatibility` which asserts exact parity with the original hardcoded values.

#### Design notes

- Explicit quotas in `Config.Quotas` override provider defaults (merge-on-top pattern)
- Local provider has no default quotas -- users add per-model limits for hardware throttling
- `AddProvider()` is additive -- calling it does not remove existing quotas
- All new methods are mutex-protected and safe for concurrent use
