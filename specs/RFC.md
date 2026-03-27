# ratelimit
**Import:** `dappco.re/go/core/go-ratelimit`
**Files:** 2

## Types

### `Provider`
`type Provider string`

`Provider` identifies an LLM provider used to select built-in quota profiles. The package defines four exported provider values: `ProviderGemini`, `ProviderOpenAI`, `ProviderAnthropic`, and `ProviderLocal`.

### `ModelQuota`
`type ModelQuota struct`

`ModelQuota` defines the rate limits for a single model. A value of `0` means the corresponding limit is unlimited.

- `MaxRPM int`: requests per minute.
- `MaxTPM int`: tokens per minute.
- `MaxRPD int`: requests per rolling 24-hour window.

### `ProviderProfile`
`type ProviderProfile struct`

`ProviderProfile` bundles a provider identifier with the default quota table for that provider.

- `Provider Provider`: the provider that owns the profile.
- `Models map[string]ModelQuota`: built-in quotas keyed by model name.

### `Config`
`type Config struct`

`Config` controls `RateLimiter` initialisation, backend selection, and default quotas.

- `FilePath string`: overrides the default persistence path. When empty, `NewWithConfig` resolves a default path under `~/.core`, using `ratelimits.yaml` for the YAML backend and `ratelimits.db` for the SQLite backend.
- `Backend string`: selects the persistence backend. `NewWithConfig` accepts `""` or `"yaml"` for YAML and `"sqlite"` for SQLite. `NewWithSQLiteConfig` ignores this field and always uses SQLite.
- `Quotas map[string]ModelQuota`: explicit per-model quotas. These are merged on top of any provider defaults loaded from `Providers`.
- `Providers []Provider`: provider profiles to load from `DefaultProfiles`. If both `Providers` and `Quotas` are empty, Gemini defaults are used.

### `TokenEntry`
`type TokenEntry struct`

`TokenEntry` records a single token-usage event.

- `Time time.Time`: when the token event was recorded.
- `Count int`: how many tokens were counted for that event.

### `UsageStats`
`type UsageStats struct`

`UsageStats` stores the in-memory usage history for one model.

- `Requests []time.Time`: request timestamps inside the sliding one-minute window.
- `Tokens []TokenEntry`: token usage entries inside the sliding one-minute window.
- `DayStart time.Time`: the start of the current rolling 24-hour window.
- `DayCount int`: the number of requests recorded in the current rolling 24-hour window.

### `RateLimiter`
`type RateLimiter struct`

`RateLimiter` is the package’s main concurrency-safe limiter. It stores quotas, tracks usage state per model, supports YAML or SQLite persistence, and prunes expired state as part of normal operations.

- `Quotas map[string]ModelQuota`: configured per-model limits. If a model has no quota entry, `CanSend` allows it.
- `State map[string]*UsageStats`: tracked usage windows keyed by model name.

### `ModelStats`
`type ModelStats struct`

`ModelStats` is the read-only snapshot returned by `Stats`, `AllStats`, and `Iter`.

- `RPM int`: current requests counted in the one-minute window.
- `MaxRPM int`: configured requests-per-minute limit.
- `TPM int`: current tokens counted in the one-minute window.
- `MaxTPM int`: configured tokens-per-minute limit.
- `RPD int`: current requests counted in the rolling 24-hour window.
- `MaxRPD int`: configured requests-per-day limit.
- `DayStart time.Time`: start of the current rolling 24-hour window. This is zero if the model has no recorded state.

## Functions

### `DefaultProfiles() map[Provider]ProviderProfile`
Returns a fresh map of built-in quota profiles for the supported providers. The returned map currently contains Gemini, OpenAI, Anthropic, and Local profiles. Because a new map is built on each call, callers can modify the result without mutating shared package state.

### `New() (*RateLimiter, error)`
Creates a new YAML-backed `RateLimiter` with Gemini defaults. This is equivalent to calling `NewWithConfig(Config{Providers: []Provider{ProviderGemini}})`. It initialises in-memory state only; it does not automatically restore persisted data, so callers that want previous state must call `Load()`.

### `NewWithConfig(cfg Config) (*RateLimiter, error)`
Creates a `RateLimiter` from explicit configuration. If `cfg.Backend` is empty it uses the YAML backend for backward compatibility. If both `cfg.Providers` and `cfg.Quotas` are empty, Gemini defaults are loaded. When `cfg.FilePath` is empty, the constructor resolves a default path under `~/.core`; for the implicit SQLite path it also ensures the parent directory exists. Like `New`, it does not call `Load()` automatically.

### `func (rl *RateLimiter) SetQuota(model string, quota ModelQuota)`
Adds or replaces the quota for `model` in memory. This change affects later `CanSend`, `Stats`, and related calls immediately, but it is not persisted until `Persist()` is called.

### `func (rl *RateLimiter) AddProvider(provider Provider)`
Loads the built-in quota profile for `provider` and copies its model quotas into `rl.Quotas`. Any existing quota entries for matching model names are overwritten. Unknown provider values are ignored.

### `func (rl *RateLimiter) Load() error`
Loads persisted state into the limiter. For the YAML backend, it reads the configured file and unmarshals the stored quotas and state; a missing file is treated as an empty state and returns `nil`. For the SQLite backend, it loads persisted quotas and usage state from the database. If the database has stored quotas, those quotas replace the in-memory configuration; if no stored quotas exist, the current in-memory quotas are retained. In both cases, the loaded usage state replaces the current in-memory state.

### `func (rl *RateLimiter) Persist() error`
Writes the current quotas and usage state to the configured backend. The method clones the in-memory snapshot while holding the lock, then performs I/O after releasing it. YAML persistence serialises the quota and state maps into the state file. SQLite persistence writes a full snapshot transactionally so quotas and usage move together.

### `func (rl *RateLimiter) BackgroundPrune(interval time.Duration) func()`
Starts a background goroutine that prunes expired entries from every tracked model on the supplied interval and returns a stop function. If `interval <= 0`, it returns a no-op stop function and does not start a goroutine.

### `func (rl *RateLimiter) CanSend(model string, estimatedTokens int) bool`
Reports whether a request for `model` can be sent without violating the configured limits. Negative token estimates are rejected. Models with no configured quota are allowed. If all three limits for a known model are `0`, the model is treated as unlimited. Before evaluating the request, the limiter prunes entries older than one minute and resets the rolling daily counter when its 24-hour window has elapsed. The method then checks requests-per-day, requests-per-minute, and tokens-per-minute against the estimated token count.

### `func (rl *RateLimiter) RecordUsage(model string, promptTokens, outputTokens int)`
Records a successful request for `model`. The limiter prunes stale entries first, creates state for the model if needed, appends the current timestamp to the request window, appends a token entry containing the combined prompt and output token count, and increments the rolling daily counter. Negative token values are ignored by the internal token summation logic rather than reducing the recorded total.

### `func (rl *RateLimiter) WaitForCapacity(ctx context.Context, model string, tokens int) error`
Blocks until `CanSend(model, tokens)` succeeds or `ctx` is cancelled. The method polls once per second. If `tokens` is negative, it returns an error immediately.

### `func (rl *RateLimiter) Reset(model string)`
Clears usage state without changing quotas. If `model` is empty, it drops all tracked state. Otherwise it removes state only for the named model.

### `func (rl *RateLimiter) Models() iter.Seq[string]`
Returns a sorted iterator of all model names currently known to the limiter. The result is the union of model names present in `rl.Quotas` and `rl.State`, so it includes models that only have stored state as well as models that only have configured quotas.

### `func (rl *RateLimiter) Iter() iter.Seq2[string, ModelStats]`
Returns a sorted iterator of model names paired with their current `ModelStats` snapshots. Internally it builds the snapshot via `AllStats()` and yields entries in lexical model-name order.

### `func (rl *RateLimiter) Stats(model string) ModelStats`
Returns the current snapshot for a single model after pruning expired entries. The result includes both current usage and configured maxima. If the model has no configured quota, the maximum fields are zero. If the model has no recorded state, the usage counters are zero and `DayStart` is the zero time.

### `func (rl *RateLimiter) AllStats() map[string]ModelStats`
Returns a snapshot for every tracked model. The returned map includes model names found in either `rl.Quotas` or `rl.State`. Each model is pruned before its snapshot is computed, so expired one-minute entries are removed and stale daily windows are reset as part of the call.

### `NewWithSQLite(dbPath string) (*RateLimiter, error)`
Creates a SQLite-backed `RateLimiter` with Gemini defaults and opens or creates the database at `dbPath`. Like the YAML constructors, it initialises in-memory configuration but does not automatically call `Load()`. Callers should `defer rl.Close()` when they are done with the limiter.

### `NewWithSQLiteConfig(dbPath string, cfg Config) (*RateLimiter, error)`
Creates a SQLite-backed `RateLimiter` using `cfg` for provider and quota configuration. The `Backend` field in `cfg` is ignored because this constructor always uses SQLite. The database is opened or created at `dbPath`, and callers should `defer rl.Close()` to release the connection. Existing persisted data is not loaded until `Load()` is called.

### `func (rl *RateLimiter) Close() error`
Releases resources held by the limiter. For YAML-backed limiters this is a no-op that returns `nil`. For SQLite-backed limiters it closes the underlying database connection.

### `MigrateYAMLToSQLite(yamlPath, sqlitePath string) error`
Reads a YAML state file into a temporary `RateLimiter` and writes its quotas and usage state into a SQLite database. The SQLite database is created if it does not exist. The migration writes a complete snapshot, so any existing SQLite snapshot tables are replaced by the imported data.

### `CountTokens(ctx context.Context, apiKey, model, text string) (int, error)`
Calls Google’s Gemini `countTokens` API for `model` and returns the `totalTokens` value from the response. The function uses `http.DefaultClient`, posts to the Generative Language API base URL, and sends the API key through the `x-goog-api-key` header. It validates that `model` is non-empty, truncates oversized response bodies when building error messages, and wraps transport, request-building, and decoding failures with package-scoped errors.
