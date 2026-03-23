# API Contract

Test coverage is marked `yes` when the symbol is exercised by the existing test suite in `ratelimit_test.go`, `sqlite_test.go`, `error_test.go`, or `iter_test.go`.

| Kind | Name | Signature | Description | Test coverage |
| --- | --- | --- | --- | --- |
| Type | `Provider` | `type Provider string` | Identifies an LLM provider used to select default quota profiles. | yes |
| Type | `ModelQuota` | `type ModelQuota struct { MaxRPM int; MaxTPM int; MaxRPD int }` | Declares per-model RPM, TPM, and RPD limits; `0` means unlimited. | yes |
| Type | `ProviderProfile` | `type ProviderProfile struct { Provider Provider; Models map[string]ModelQuota }` | Bundles a provider identifier with its default model quota map. | yes |
| Type | `Config` | `type Config struct { FilePath string; Backend string; Quotas map[string]ModelQuota; Providers []Provider }` | Configures limiter initialisation, persistence settings, explicit quotas, and provider defaults. | yes |
| Type | `TokenEntry` | `type TokenEntry struct { Time time.Time; Count int }` | Records a timestamped token-usage event. | yes |
| Type | `UsageStats` | `type UsageStats struct { Requests []time.Time; Tokens []TokenEntry; DayStart time.Time; DayCount int }` | Stores per-model sliding-window request and token history plus rolling daily usage state. | yes |
| Type | `RateLimiter` | `type RateLimiter struct { Quotas map[string]ModelQuota; State map[string]*UsageStats }` | Manages quotas, usage state, persistence, and concurrency across models. | yes |
| Type | `ModelStats` | `type ModelStats struct { RPM int; MaxRPM int; TPM int; MaxTPM int; RPD int; MaxRPD int; DayStart time.Time }` | Represents a snapshot of current usage and configured limits for a model. | yes |
| Function | `DefaultProfiles` | `func DefaultProfiles() map[Provider]ProviderProfile` | Returns the built-in quota profiles for the supported providers. | yes |
| Function | `New` | `func New() (*RateLimiter, error)` | Creates a new limiter with Gemini defaults for backward-compatible YAML-backed usage. | yes |
| Function | `NewWithConfig` | `func NewWithConfig(cfg Config) (*RateLimiter, error)` | Creates a YAML-backed limiter from explicit configuration, defaulting to Gemini when config is empty. | yes |
| Function | `NewWithSQLite` | `func NewWithSQLite(dbPath string) (*RateLimiter, error)` | Creates a SQLite-backed limiter with Gemini defaults and opens or creates the database. | yes |
| Function | `NewWithSQLiteConfig` | `func NewWithSQLiteConfig(dbPath string, cfg Config) (*RateLimiter, error)` | Creates a SQLite-backed limiter from explicit configuration and always uses SQLite persistence. | yes |
| Function | `MigrateYAMLToSQLite` | `func MigrateYAMLToSQLite(yamlPath, sqlitePath string) error` | Migrates quotas and usage state from a YAML state file into a SQLite database. | yes |
| Function | `CountTokens` | `func CountTokens(ctx context.Context, apiKey, model, text string) (int, error)` | Calls the Gemini `countTokens` API for a prompt and returns the reported token count. | yes |
| Method | `SetQuota` | `func (rl *RateLimiter) SetQuota(model string, quota ModelQuota)` | Sets or replaces a model quota at runtime. | yes |
| Method | `AddProvider` | `func (rl *RateLimiter) AddProvider(provider Provider)` | Loads a built-in provider profile and overwrites any matching model quotas. | yes |
| Method | `Load` | `func (rl *RateLimiter) Load() error` | Loads quotas and usage state from YAML or SQLite into memory. | yes |
| Method | `Persist` | `func (rl *RateLimiter) Persist() error` | Persists a snapshot of quotas and usage state to YAML or SQLite. | yes |
| Method | `BackgroundPrune` | `func (rl *RateLimiter) BackgroundPrune(interval time.Duration) func()` | Starts periodic pruning of expired usage state and returns a stop function. | yes |
| Method | `CanSend` | `func (rl *RateLimiter) CanSend(model string, estimatedTokens int) bool` | Reports whether a request with the estimated token count fits within current limits. | yes |
| Method | `RecordUsage` | `func (rl *RateLimiter) RecordUsage(model string, promptTokens, outputTokens int)` | Records a successful request into the sliding-window and daily counters. | yes |
| Method | `WaitForCapacity` | `func (rl *RateLimiter) WaitForCapacity(ctx context.Context, model string, tokens int) error` | Blocks until `CanSend` succeeds or the context is cancelled. | yes |
| Method | `Reset` | `func (rl *RateLimiter) Reset(model string)` | Clears usage state for one model or for all models when `model` is empty. | yes |
| Method | `Models` | `func (rl *RateLimiter) Models() iter.Seq[string]` | Returns a sorted iterator of all model names known from quotas or state. | yes |
| Method | `Iter` | `func (rl *RateLimiter) Iter() iter.Seq2[string, ModelStats]` | Returns a sorted iterator of model names paired with current stats snapshots. | yes |
| Method | `Stats` | `func (rl *RateLimiter) Stats(model string) ModelStats` | Returns current stats for a single model after pruning expired usage. | yes |
| Method | `AllStats` | `func (rl *RateLimiter) AllStats() map[string]ModelStats` | Returns stats snapshots for every tracked model. | yes |
| Method | `Close` | `func (rl *RateLimiter) Close() error` | Closes SQLite resources for SQLite-backed limiters and is a no-op for YAML-backed limiters. | yes |
