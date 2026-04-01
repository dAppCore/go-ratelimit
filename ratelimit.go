// SPDX-License-Identifier: EUPL-1.2

package ratelimit

import (
	"context"
	"io"
	"io/fs"
	"iter"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"

	core "dappco.re/go/core"
	"gopkg.in/yaml.v3"
)

// Provider identifies an LLM provider for quota profiles.
//
//	provider := ProviderOpenAI
type Provider string

const (
	// ProviderGemini is Google's Gemini family (default).
	ProviderGemini Provider = "gemini"
	// ProviderOpenAI is OpenAI's GPT/o-series family.
	ProviderOpenAI Provider = "openai"
	// ProviderAnthropic is Anthropic's Claude family.
	ProviderAnthropic Provider = "anthropic"
	// ProviderLocal is for local inference (Ollama, MLX, llama.cpp).
	ProviderLocal Provider = "local"
)

const (
	defaultStateDirName         = ".core"
	defaultYAMLStateFile        = "ratelimits.yaml"
	defaultSQLiteStateFile      = "ratelimits.db"
	backendYAML                 = "yaml"
	backendSQLite               = "sqlite"
	countTokensErrorBodyLimit   = 8 * 1024
	countTokensSuccessBodyLimit = 1 * 1024 * 1024
)

// ModelQuota defines the rate limits for a specific model.
//
//	quota := ModelQuota{MaxRPM: 60, MaxTPM: 90000, MaxRPD: 1000}
type ModelQuota struct {
	MaxRPM int `yaml:"max_rpm"` // Requests per minute (0 = unlimited)
	MaxTPM int `yaml:"max_tpm"` // Tokens per minute (0 = unlimited)
	MaxRPD int `yaml:"max_rpd"` // Requests per day (0 = unlimited)
}

// ProviderProfile bundles model quotas for a provider.
//
//	profile := ProviderProfile{Provider: ProviderGemini, Models: DefaultProfiles()[ProviderGemini].Models}
type ProviderProfile struct {
	// Provider identifies the provider that owns the profile.
	Provider Provider `yaml:"provider"`
	// Models maps model names to quotas.
	Models map[string]ModelQuota `yaml:"models"`
}

// Config controls RateLimiter initialisation.
//
//	cfg := Config{Providers: []Provider{ProviderGemini}, FilePath: "/tmp/ratelimits.yaml"}
type Config struct {
	// FilePath overrides the default state file location.
	// If empty, defaults to ~/.core/ratelimits.yaml.
	FilePath string `yaml:"file_path,omitempty"`

	// Backend selects the persistence backend: "yaml" (default) or "sqlite".
	// An empty string is treated as "yaml" for backward compatibility.
	Backend string `yaml:"backend,omitempty"`

	// Quotas sets per-model rate limits directly.
	// These are merged on top of any provider profile defaults.
	Quotas map[string]ModelQuota `yaml:"quotas,omitempty"`

	// Providers lists provider profiles to load.
	// If empty and Quotas is also empty, Gemini defaults are used.
	Providers []Provider `yaml:"providers,omitempty"`
}

// TokenEntry records a token usage event.
//
//	entry := TokenEntry{Time: time.Now(), Count: 512}
type TokenEntry struct {
	Time  time.Time `yaml:"time"`
	Count int       `yaml:"count"`
}

// UsageStats tracks usage history for a model.
//
//	stats := UsageStats{DayStart: time.Now(), DayCount: 1}
type UsageStats struct {
	Requests []time.Time  `yaml:"requests"` // Sliding window (1m)
	Tokens   []TokenEntry `yaml:"tokens"`   // Sliding window (1m)
	// DayStart is the start of the rolling 24-hour window.
	DayStart time.Time `yaml:"day_start"`
	// DayCount is the number of requests recorded in the rolling 24-hour window.
	DayCount int `yaml:"day_count"`
}

// RateLimiter manages rate limits across multiple models.
//
//	rl, err := New()
//	if err != nil { /* handle error */ }
//	defer rl.Close()
type RateLimiter struct {
	mu sync.RWMutex
	// Quotas holds the configured per-model limits.
	Quotas map[string]ModelQuota `yaml:"quotas"`
	// State holds per-model usage windows.
	State    map[string]*UsageStats `yaml:"state"`
	filePath string
	sqlite   *sqliteStore // non-nil when backend is "sqlite"
}

// DefaultProfiles returns pre-configured quota profiles for each provider.
// Values are based on published rate limits as of Feb 2026.
//
//	profiles := DefaultProfiles()
//	openAI := profiles[ProviderOpenAI]
func DefaultProfiles() map[Provider]ProviderProfile {
	return map[Provider]ProviderProfile{
		ProviderGemini: {
			Provider: ProviderGemini,
			Models: map[string]ModelQuota{
				"gemini-3-pro-preview":   {MaxRPM: 150, MaxTPM: 1000000, MaxRPD: 1000},
				"gemini-3-flash-preview": {MaxRPM: 150, MaxTPM: 1000000, MaxRPD: 1000},
				"gemini-2.5-pro":         {MaxRPM: 150, MaxTPM: 1000000, MaxRPD: 1000},
				"gemini-2.0-flash":       {MaxRPM: 150, MaxTPM: 1000000, MaxRPD: 0}, // Unlimited RPD
				"gemini-2.0-flash-lite":  {MaxRPM: 0, MaxTPM: 0, MaxRPD: 0},         // Unlimited
			},
		},
		ProviderOpenAI: {
			Provider: ProviderOpenAI,
			Models: map[string]ModelQuota{
				"gpt-4o":      {MaxRPM: 500, MaxTPM: 30000, MaxRPD: 0},
				"gpt-4o-mini": {MaxRPM: 500, MaxTPM: 200000, MaxRPD: 0},
				"gpt-4-turbo": {MaxRPM: 500, MaxTPM: 30000, MaxRPD: 0},
				"o1":          {MaxRPM: 500, MaxTPM: 30000, MaxRPD: 0},
				"o1-mini":     {MaxRPM: 500, MaxTPM: 200000, MaxRPD: 0},
				"o3-mini":     {MaxRPM: 500, MaxTPM: 200000, MaxRPD: 0},
			},
		},
		ProviderAnthropic: {
			Provider: ProviderAnthropic,
			Models: map[string]ModelQuota{
				"claude-opus-4":    {MaxRPM: 50, MaxTPM: 40000, MaxRPD: 0},
				"claude-sonnet-4":  {MaxRPM: 50, MaxTPM: 40000, MaxRPD: 0},
				"claude-haiku-3.5": {MaxRPM: 50, MaxTPM: 50000, MaxRPD: 0},
			},
		},
		ProviderLocal: {
			Provider: ProviderLocal,
			Models:   map[string]ModelQuota{
				// Local inference has no external rate limits by default.
				// Users can override per-model if their hardware requires throttling.
			},
		},
	}
}

// New creates a new RateLimiter with Gemini defaults.
// This preserves backward compatibility -- existing callers are unaffected.
//
//	rl, err := New()
func New() (*RateLimiter, error) {
	return NewWithConfig(Config{
		Providers: []Provider{ProviderGemini},
	})
}

// NewWithConfig creates a RateLimiter from explicit configuration.
// If no providers or quotas are specified, Gemini defaults are used.
//
//	rl, err := NewWithConfig(Config{Providers: []Provider{ProviderAnthropic}})
func NewWithConfig(cfg Config) (*RateLimiter, error) {
	backend, err := normaliseBackend(cfg.Backend)
	if err != nil {
		return nil, err
	}

	filePath := cfg.FilePath
	if filePath == "" {
		filePath, err = defaultStatePath(backend)
		if err != nil {
			return nil, err
		}
	}

	if backend == backendSQLite {
		if cfg.FilePath == "" {
			if err := ensureDir(core.PathDir(filePath)); err != nil {
				return nil, core.E("ratelimit.NewWithConfig", "mkdir", err)
			}
		}
		return NewWithSQLiteConfig(filePath, cfg)
	}

	rl := newConfiguredRateLimiter(cfg)
	rl.filePath = filePath
	return rl, nil
}

// SetQuota sets or updates the quota for a specific model at runtime.
//
//	rl.SetQuota("gpt-4o-mini", ModelQuota{MaxRPM: 60, MaxTPM: 200000})
func (rl *RateLimiter) SetQuota(model string, quota ModelQuota) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.Quotas[model] = quota
}

// AddProvider loads all default quotas for a provider.
// Existing quotas for models in the profile are overwritten.
//
//	rl.AddProvider(ProviderOpenAI)
func (rl *RateLimiter) AddProvider(provider Provider) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	profiles := DefaultProfiles()
	if profile, ok := profiles[provider]; ok {
		maps.Copy(rl.Quotas, profile.Models)
	}
}

// Load reads the state from disk (YAML) or database (SQLite).
//
//	if err := rl.Load(); err != nil { /* handle error */ }
func (rl *RateLimiter) Load() error {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if rl.sqlite != nil {
		return rl.loadSQLite()
	}

	content, err := readLocalFile(rl.filePath)
	if core.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	return yaml.Unmarshal([]byte(content), rl)
}

// loadSQLite reads quotas and state from the SQLite backend.
// Caller must hold the lock.
func (rl *RateLimiter) loadSQLite() error {
	quotas, err := rl.sqlite.loadQuotas()
	if err != nil {
		return err
	}
	// Persisted quotas are authoritative when present; otherwise keep config defaults.
	if len(quotas) > 0 {
		rl.Quotas = maps.Clone(quotas)
	}

	state, err := rl.sqlite.loadState()
	if err != nil {
		return err
	}
	// Replace in-memory state with the persisted snapshot.
	rl.State = state
	return nil
}

// Persist writes a snapshot of the state to disk (YAML) or database (SQLite).
// It clones the state under a lock and performs I/O without blocking other callers.
//
//	if err := rl.Persist(); err != nil { /* handle error */ }
func (rl *RateLimiter) Persist() error {
	rl.mu.Lock()
	quotas := maps.Clone(rl.Quotas)
	state := make(map[string]*UsageStats, len(rl.State))
	for k, v := range rl.State {
		if v == nil {
			continue
		}
		state[k] = &UsageStats{
			Requests: slices.Clone(v.Requests),
			Tokens:   slices.Clone(v.Tokens),
			DayStart: v.DayStart,
			DayCount: v.DayCount,
		}
	}
	sqlite := rl.sqlite
	filePath := rl.filePath
	rl.mu.Unlock()

	if sqlite != nil {
		if err := sqlite.saveSnapshot(quotas, state); err != nil {
			return core.E("ratelimit.Persist", "sqlite snapshot", err)
		}
		return nil
	}

	// For YAML, we marshal the entire RateLimiter, but since we want to avoid
	// holding the lock during marshal, we marshal a temporary struct.
	data, err := yaml.Marshal(struct {
		Quotas map[string]ModelQuota  `yaml:"quotas"`
		State  map[string]*UsageStats `yaml:"state"`
	}{
		Quotas: quotas,
		State:  state,
	})
	if err != nil {
		return core.E("ratelimit.Persist", "marshal", err)
	}

	if err := writeLocalFile(filePath, string(data)); err != nil {
		return core.E("ratelimit.Persist", "write", err)
	}
	return nil
}

// prune removes entries older than the sliding window (1 minute) and removes
// empty state for models that haven't been used recently.
// Caller must hold lock.
func (rl *RateLimiter) prune(model string) {
	stats, ok := rl.State[model]
	if !ok {
		return
	}
	if stats == nil {
		delete(rl.State, model)
		return
	}

	now := time.Now()
	window := now.Add(-1 * time.Minute)

	// Prune requests
	stats.Requests = slices.DeleteFunc(stats.Requests, func(t time.Time) bool {
		return t.Before(window)
	})

	// Prune tokens
	stats.Tokens = slices.DeleteFunc(stats.Tokens, func(t TokenEntry) bool {
		return t.Time.Before(window)
	})

	// Reset daily counter if day has passed
	if now.Sub(stats.DayStart) >= 24*time.Hour {
		stats.DayStart = now
		stats.DayCount = 0
	}

	// If everything is empty and it's been more than a minute since last activity,
	// delete the model state entirely to prevent memory leaks.
	if len(stats.Requests) == 0 && len(stats.Tokens) == 0 {
		// We could use a more sophisticated TTL here, but for now just cleanup empty ones.
		// Note: we don't delete if DayCount > 0 and it's still the same day.
		if stats.DayCount == 0 {
			delete(rl.State, model)
		}
	}
}

// BackgroundPrune starts a goroutine that periodically prunes all model states.
// It returns a function to stop the pruner.
//
//	stop := rl.BackgroundPrune(30 * time.Second)
//	defer stop()
func (rl *RateLimiter) BackgroundPrune(interval time.Duration) func() {
	if interval <= 0 {
		return func() {}
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				rl.mu.Lock()
				for m := range rl.State {
					rl.prune(m)
				}
				rl.mu.Unlock()
			}
		}
	}()
	return cancel
}

// CanSend checks if a request can be sent without violating limits.
//
//	ok := rl.CanSend("gemini-3-pro-preview", 1200)
func (rl *RateLimiter) CanSend(model string, estimatedTokens int) bool {
	return rl.Decide(model, estimatedTokens).Allowed
}

// RecordUsage records a successful API call.
//
//	rl.RecordUsage("gemini-3-pro-preview", 900, 300)
func (rl *RateLimiter) RecordUsage(model string, promptTokens, outputTokens int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.prune(model) // Prune before recording to ensure we're not exceeding limits immediately after
	stats, ok := rl.State[model]
	if !ok {
		stats = &UsageStats{DayStart: time.Now()}
		rl.State[model] = stats
	}

	now := time.Now()
	totalTokens := safeTokenSum(promptTokens, outputTokens)
	stats.Requests = append(stats.Requests, now)
	stats.Tokens = append(stats.Tokens, TokenEntry{Time: now, Count: totalTokens})
	stats.DayCount++
}

// WaitForCapacity blocks until capacity is available or context is cancelled.
//
//	err := rl.WaitForCapacity(ctx, "gemini-3-pro-preview", 1200)
func (rl *RateLimiter) WaitForCapacity(ctx context.Context, model string, tokens int) error {
	if tokens < 0 {
		return core.E("ratelimit.WaitForCapacity", "negative tokens", nil)
	}

	for {
		decision := rl.Decide(model, tokens)
		if decision.Allowed {
			return nil
		}

		sleep := decision.RetryAfter
		if sleep <= 0 {
			sleep = time.Second
		}

		timer := time.NewTimer(sleep)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			timer.Stop()
		}
	}
}

// Reset clears stats for a model (or all if model is empty).
//
//	rl.Reset("gemini-3-pro-preview")
func (rl *RateLimiter) Reset(model string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if model == "" {
		rl.State = make(map[string]*UsageStats)
	} else {
		delete(rl.State, model)
	}
}

// ModelStats represents a snapshot of usage.
//
//	stats := rl.Stats("gemini-3-pro-preview")
type ModelStats struct {
	// RPM is the current requests-per-minute usage in the sliding window.
	RPM int
	// MaxRPM is the configured requests-per-minute limit.
	MaxRPM int
	// TPM is the current tokens-per-minute usage in the sliding window.
	TPM int
	// MaxTPM is the configured tokens-per-minute limit.
	MaxTPM int
	// RPD is the current requests-per-day usage in the rolling 24-hour window.
	RPD int
	// MaxRPD is the configured requests-per-day limit.
	MaxRPD int
	// DayStart is the start of the current rolling 24-hour window.
	DayStart time.Time
}

// DecisionCode identifies the reason for an allow or deny outcome from Decide.
type DecisionCode string

const (
	// DecisionAllowed means the request fits within all configured limits.
	DecisionAllowed DecisionCode = "ok"
	// DecisionUnknownModel means the model has no configured quotas and is therefore allowed.
	DecisionUnknownModel DecisionCode = "unknown_model"
	// DecisionUnlimited means the model is configured with no limits.
	DecisionUnlimited DecisionCode = "unlimited"
	// DecisionInvalidTokens means a negative token estimate was provided.
	DecisionInvalidTokens DecisionCode = "invalid_tokens"
	// DecisionRPDLimit means the rolling 24-hour request limit has been reached.
	DecisionRPDLimit DecisionCode = "rpd_exceeded"
	// DecisionRPMLimit means the per-minute request limit has been reached.
	DecisionRPMLimit DecisionCode = "rpm_exceeded"
	// DecisionTPMLimit means the per-minute token limit would be exceeded.
	DecisionTPMLimit DecisionCode = "tpm_exceeded"
)

// Decision captures an allow/deny decision with context for agents.
// RetryAfter is zero when the request is allowed or when no meaningful wait time exists.
type Decision struct {
	Allowed    bool
	Code       DecisionCode
	Reason     string
	RetryAfter time.Duration
	Stats      ModelStats
}

// Models returns a sorted iterator over all model names tracked by the limiter.
//
//	for model := range rl.Models() { println(model) }
func (rl *RateLimiter) Models() iter.Seq[string] {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	// Use maps.Keys and slices.Sorted for idiomatic Go 1.26+
	keys := slices.Collect(maps.Keys(rl.Quotas))
	for m := range rl.State {
		if _, ok := rl.Quotas[m]; !ok {
			keys = append(keys, m)
		}
	}
	slices.Sort(keys)
	return slices.Values(keys)
}

// Iter returns a sorted iterator over all model names and their current stats.
//
//	for model, stats := range rl.Iter() { _ = stats; println(model) }
func (rl *RateLimiter) Iter() iter.Seq2[string, ModelStats] {
	return func(yield func(string, ModelStats) bool) {
		stats := rl.AllStats()
		for _, m := range slices.Sorted(maps.Keys(stats)) {
			if !yield(m, stats[m]) {
				return
			}
		}
	}
}

// Stats returns current stats for a model.
//
//	stats := rl.Stats("gemini-3-pro-preview")
func (rl *RateLimiter) Stats(model string) ModelStats {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.prune(model)

	return rl.snapshotLocked(model)
}

// AllStats returns stats for all tracked models.
//
//	all := rl.AllStats()
func (rl *RateLimiter) AllStats() map[string]ModelStats {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	result := make(map[string]ModelStats)

	// Collect all model names
	for m := range rl.Quotas {
		result[m] = ModelStats{}
	}
	for m := range rl.State {
		result[m] = ModelStats{}
	}

	for m := range result {
		rl.prune(m)

		result[m] = rl.snapshotLocked(m)
	}

	return result
}

// Decide returns structured allow/deny information for an estimated request.
// It never records usage; call RecordUsage after a successful decision.
func (rl *RateLimiter) Decide(model string, estimatedTokens int) Decision {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	decision := Decision{}

	quota, ok := rl.Quotas[model]
	if !ok {
		decision.Allowed = true
		decision.Code = DecisionUnknownModel
		decision.Reason = "model has no configured quota"
		decision.Stats = rl.snapshotLocked(model)
		return decision
	}

	if quota.MaxRPM == 0 && quota.MaxTPM == 0 && quota.MaxRPD == 0 {
		if estimatedTokens < 0 {
			decision.Allowed = false
			decision.Code = DecisionInvalidTokens
			decision.Reason = "estimated tokens must be non-negative"
			decision.Stats = rl.snapshotLocked(model)
			return decision
		}
		decision.Allowed = true
		decision.Code = DecisionUnlimited
		decision.Reason = "all limits are unlimited"
		decision.Stats = rl.snapshotLocked(model)
		return decision
	}

	rl.prune(model)
	stats, ok := rl.State[model]
	if !ok || stats == nil {
		stats = &UsageStats{DayStart: now}
		rl.State[model] = stats
	}

	if estimatedTokens < 0 {
		decision.Allowed = false
		decision.Code = DecisionInvalidTokens
		decision.Reason = "estimated tokens must be non-negative"
		decision.Stats = rl.snapshotLocked(model)
		return decision
	}

	decision.Stats = rl.snapshotLocked(model)

	if quota.MaxRPD > 0 && stats.DayCount >= quota.MaxRPD {
		decision.Code = DecisionRPDLimit
		decision.Reason = "daily request limit reached"
		decision.RetryAfter = nonNegativeDuration(stats.DayStart.Add(24 * time.Hour).Sub(now))
		return decision
	}

	if quota.MaxRPM > 0 && len(stats.Requests) >= quota.MaxRPM {
		decision.Code = DecisionRPMLimit
		decision.Reason = "per-minute request limit reached"
		if len(stats.Requests) > 0 {
			decision.RetryAfter = nonNegativeDuration(stats.Requests[0].Add(time.Minute).Sub(now))
		}
		return decision
	}

	if quota.MaxTPM > 0 {
		currentTokens := totalTokenCount(stats.Tokens)
		if estimatedTokens > quota.MaxTPM || currentTokens > quota.MaxTPM-estimatedTokens {
			decision.Code = DecisionTPMLimit
			decision.Reason = "per-minute token limit reached"
			decision.RetryAfter = retryAfterForTokens(now, stats.Tokens, quota.MaxTPM, estimatedTokens)
			return decision
		}
	}

	decision.Allowed = true
	decision.Code = DecisionAllowed
	decision.Reason = "within quota"
	return decision
}

// snapshotLocked builds ModelStats for the provided model.
// Caller must hold rl.mu.
func (rl *RateLimiter) snapshotLocked(model string) ModelStats {
	stats := ModelStats{}

	if q, ok := rl.Quotas[model]; ok {
		stats.MaxRPM = q.MaxRPM
		stats.MaxTPM = q.MaxTPM
		stats.MaxRPD = q.MaxRPD
	}

	if s, ok := rl.State[model]; ok && s != nil {
		stats.RPM = len(s.Requests)
		stats.RPD = s.DayCount
		stats.DayStart = s.DayStart
		stats.TPM = totalTokenCount(s.Tokens)
	}
	return stats
}

// NewWithSQLite creates a SQLite-backed RateLimiter with Gemini defaults.
// The database is created at dbPath if it does not exist. Use Close() to
// release the database connection when finished.
//
//	rl, err := NewWithSQLite("/tmp/ratelimits.db")
func NewWithSQLite(dbPath string) (*RateLimiter, error) {
	return NewWithSQLiteConfig(dbPath, Config{
		Providers: []Provider{ProviderGemini},
	})
}

// NewWithSQLiteConfig creates a SQLite-backed RateLimiter with custom config.
// The Backend field in cfg is ignored (always "sqlite"). Use Close() to
// release the database connection when finished.
//
//	rl, err := NewWithSQLiteConfig("/tmp/ratelimits.db", Config{Providers: []Provider{ProviderOpenAI}})
func NewWithSQLiteConfig(dbPath string, cfg Config) (*RateLimiter, error) {
	store, err := newSQLiteStore(dbPath)
	if err != nil {
		return nil, err
	}

	rl := newConfiguredRateLimiter(cfg)
	rl.sqlite = store
	return rl, nil
}

// Close releases resources held by the RateLimiter. For YAML-backed
// limiters this is a no-op. For SQLite-backed limiters it closes the
// database connection.
//
//	defer rl.Close()
func (rl *RateLimiter) Close() error {
	if rl.sqlite != nil {
		return rl.sqlite.close()
	}
	return nil
}

// MigrateYAMLToSQLite reads state from a YAML file and writes it to a new
// SQLite database. Both quotas and usage state are migrated. The SQLite
// database is created if it does not exist.
//
//	err := MigrateYAMLToSQLite("ratelimits.yaml", "ratelimits.db")
func MigrateYAMLToSQLite(yamlPath, sqlitePath string) error {
	// Load from YAML.
	content, err := readLocalFile(yamlPath)
	if err != nil {
		return core.E("ratelimit.MigrateYAMLToSQLite", "read", err)
	}

	var rl RateLimiter
	if err := yaml.Unmarshal([]byte(content), &rl); err != nil {
		return core.E("ratelimit.MigrateYAMLToSQLite", "unmarshal", err)
	}

	// Write to SQLite.
	store, err := newSQLiteStore(sqlitePath)
	if err != nil {
		return err
	}
	defer store.close()

	if err := store.saveSnapshot(rl.Quotas, rl.State); err != nil {
		return err
	}
	return nil
}

// CountTokens calls the Google API to count tokens for a prompt.
//
//	tokens, err := CountTokens(ctx, apiKey, "gemini-3-pro-preview", prompt)
func CountTokens(ctx context.Context, apiKey, model, text string) (int, error) {
	return countTokensWithClient(ctx, http.DefaultClient, "https://generativelanguage.googleapis.com", apiKey, model, text)
}

func countTokensWithClient(ctx context.Context, client *http.Client, baseURL, apiKey, model, text string) (int, error) {
	requestURL, err := countTokensURL(baseURL, model)
	if err != nil {
		return 0, core.E("ratelimit.CountTokens", "build url", err)
	}

	reqBody := map[string]any{
		"contents": []any{
			map[string]any{
				"parts": []any{
					map[string]string{"text": text},
				},
			},
		},
	}

	jsonBody := core.JSONMarshal(reqBody)
	if !jsonBody.OK {
		return 0, core.E("ratelimit.CountTokens", "marshal request", resultError(jsonBody))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, core.NewReader(string(jsonBody.Value.([]byte))))
	if err != nil {
		return 0, core.E("ratelimit.CountTokens", "new request", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", apiKey)

	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, core.E("ratelimit.CountTokens", "do request", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, err := readLimitedBody(resp.Body, countTokensErrorBodyLimit)
		if err != nil {
			return 0, core.E("ratelimit.CountTokens", "read error body", err)
		}
		return 0, core.E("ratelimit.CountTokens", core.Sprintf("api error status %d: %s", resp.StatusCode, body), nil)
	}

	body, err := readLimitedBody(resp.Body, countTokensSuccessBodyLimit)
	if err != nil {
		return 0, core.E("ratelimit.CountTokens", "decode response", err)
	}

	var result struct {
		TotalTokens int `json:"totalTokens"`
	}
	decode := core.JSONUnmarshalString(body, &result)
	if !decode.OK {
		return 0, core.E("ratelimit.CountTokens", "decode response", resultError(decode))
	}

	return result.TotalTokens, nil
}

func newConfiguredRateLimiter(cfg Config) *RateLimiter {
	rl := &RateLimiter{
		Quotas: make(map[string]ModelQuota),
		State:  make(map[string]*UsageStats),
	}
	applyConfig(rl, cfg)
	return rl
}

func applyConfig(rl *RateLimiter, cfg Config) {
	profiles := DefaultProfiles()
	providers := cfg.Providers

	if len(providers) == 0 && len(cfg.Quotas) == 0 {
		providers = []Provider{ProviderGemini}
	}

	for _, p := range providers {
		if profile, ok := profiles[p]; ok {
			maps.Copy(rl.Quotas, profile.Models)
		}
	}

	maps.Copy(rl.Quotas, cfg.Quotas)
}

func normaliseBackend(backend string) (string, error) {
	switch core.Lower(core.Trim(backend)) {
	case "", backendYAML:
		return backendYAML, nil
	case backendSQLite:
		return backendSQLite, nil
	default:
		return "", core.E("ratelimit.NewWithConfig", core.Sprintf("unknown backend %q", backend), nil)
	}
}

func defaultStatePath(backend string) (string, error) {
	home := currentHomeDir()
	if home == "" {
		return "", core.E("ratelimit.defaultStatePath", "home dir unavailable", nil)
	}

	fileName := defaultYAMLStateFile
	if backend == backendSQLite {
		fileName = defaultSQLiteStateFile
	}

	return core.Path(home, defaultStateDirName, fileName), nil
}

func currentHomeDir() string {
	for _, key := range []string{"CORE_HOME", "HOME", "home", "USERPROFILE"} {
		if value := core.Trim(core.Env(key)); value != "" {
			return value
		}
	}
	return ""
}

func safeTokenSum(a, b int) int {
	return safeTokenTotal([]TokenEntry{{Count: a}, {Count: b}})
}

func totalTokenCount(tokens []TokenEntry) int {
	return safeTokenTotal(tokens)
}

func safeTokenTotal(tokens []TokenEntry) int {
	const maxInt = int(^uint(0) >> 1)

	total := 0
	for _, token := range tokens {
		count := token.Count
		if count < 0 {
			continue
		}
		if total > maxInt-count {
			return maxInt
		}
		total += count
	}
	return total
}

func retryAfterForTokens(now time.Time, tokens []TokenEntry, maxTPM, estimatedTokens int) time.Duration {
	if maxTPM <= 0 {
		return 0
	}

	deficit := totalTokenCount(tokens) + estimatedTokens - maxTPM
	if deficit <= 0 {
		return 0
	}

	remaining := deficit
	for _, entry := range tokens {
		if entry.Count < 0 {
			continue
		}
		remaining -= entry.Count
		if remaining <= 0 {
			return nonNegativeDuration(entry.Time.Add(time.Minute).Sub(now))
		}
	}

	return 0
}

func nonNegativeDuration(value time.Duration) time.Duration {
	if value < 0 {
		return 0
	}
	return value
}

func countTokensURL(baseURL, model string) (string, error) {
	if core.Trim(model) == "" {
		return "", core.E("ratelimit.countTokensURL", "empty model", nil)
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", core.E("ratelimit.countTokensURL", "invalid base url", nil)
	}

	return core.Concat(core.TrimSuffix(parsed.String(), "/"), "/v1beta/models/", url.PathEscape(model), ":countTokens"), nil
}

func readLimitedBody(r io.Reader, limit int64) (string, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return "", err
	}

	truncated := int64(len(body)) > limit
	if truncated {
		body = body[:limit]
	}

	result := string(body)
	if truncated {
		result += "..."
	}
	return result, nil
}

func readLocalFile(path string) (string, error) {
	var fs core.Fs
	result := fs.Read(path)
	if !result.OK {
		return "", resultError(result)
	}

	content, ok := result.Value.(string)
	if !ok {
		return "", core.E("ratelimit.readLocalFile", "read returned non-string", nil)
	}
	return content, nil
}

func writeLocalFile(path, content string) error {
	var fs core.Fs
	return resultError(fs.Write(path, content))
}

func ensureDir(path string) error {
	var fs core.Fs
	return resultError(fs.EnsureDir(path))
}

func resultError(result core.Result) error {
	if result.OK {
		return nil
	}
	if err, ok := result.Value.(error); ok {
		return err
	}
	if result.Value == nil {
		return nil
	}
	return core.E("ratelimit.resultError", core.Sprint(result.Value), nil)
}
