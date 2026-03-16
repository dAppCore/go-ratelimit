package ratelimit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	coreio "forge.lthn.ai/core/go-io"
	"gopkg.in/yaml.v3"
)

// Provider identifies an LLM provider for quota profiles.
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

// ModelQuota defines the rate limits for a specific model.
type ModelQuota struct {
	MaxRPM int `yaml:"max_rpm"` // Requests per minute (0 = unlimited)
	MaxTPM int `yaml:"max_tpm"` // Tokens per minute (0 = unlimited)
	MaxRPD int `yaml:"max_rpd"` // Requests per day (0 = unlimited)
}

// ProviderProfile bundles model quotas for a provider.
type ProviderProfile struct {
	Provider Provider              `yaml:"provider"`
	Models   map[string]ModelQuota `yaml:"models"`
}

// Config controls RateLimiter initialisation.
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
type TokenEntry struct {
	Time  time.Time `yaml:"time"`
	Count int       `yaml:"count"`
}

// UsageStats tracks usage history for a model.
type UsageStats struct {
	Requests []time.Time  `yaml:"requests"` // Sliding window (1m)
	Tokens   []TokenEntry `yaml:"tokens"`   // Sliding window (1m)
	DayStart time.Time    `yaml:"day_start"`
	DayCount int          `yaml:"day_count"`
}

// RateLimiter manages rate limits across multiple models.
type RateLimiter struct {
	mu       sync.RWMutex
	Quotas   map[string]ModelQuota  `yaml:"quotas"`
	State    map[string]*UsageStats `yaml:"state"`
	filePath string
	sqlite   *sqliteStore // non-nil when backend is "sqlite"
}

// DefaultProfiles returns pre-configured quota profiles for each provider.
// Values are based on published rate limits as of Feb 2026.
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
func New() (*RateLimiter, error) {
	return NewWithConfig(Config{
		Providers: []Provider{ProviderGemini},
	})
}

// NewWithConfig creates a RateLimiter from explicit configuration.
// If no providers or quotas are specified, Gemini defaults are used.
func NewWithConfig(cfg Config) (*RateLimiter, error) {
	filePath := cfg.FilePath
	if filePath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		filePath = filepath.Join(home, ".core", "ratelimits.yaml")
	}

	rl := &RateLimiter{
		Quotas:   make(map[string]ModelQuota),
		State:    make(map[string]*UsageStats),
		filePath: filePath,
	}

	// Load provider profiles
	profiles := DefaultProfiles()
	providers := cfg.Providers

	// If nothing specified at all, default to Gemini
	if len(providers) == 0 && len(cfg.Quotas) == 0 {
		providers = []Provider{ProviderGemini}
	}

	for _, p := range providers {
		if profile, ok := profiles[p]; ok {
			maps.Copy(rl.Quotas, profile.Models)
		}
	}

	// Merge explicit quotas on top (allows overrides)
	maps.Copy(rl.Quotas, cfg.Quotas)

	return rl, nil
}

// SetQuota sets or updates the quota for a specific model at runtime.
func (rl *RateLimiter) SetQuota(model string, quota ModelQuota) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.Quotas[model] = quota
}

// AddProvider loads all default quotas for a provider.
// Existing quotas for models in the profile are overwritten.
func (rl *RateLimiter) AddProvider(provider Provider) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	profiles := DefaultProfiles()
	if profile, ok := profiles[provider]; ok {
		maps.Copy(rl.Quotas, profile.Models)
	}
}

// Load reads the state from disk (YAML) or database (SQLite).
func (rl *RateLimiter) Load() error {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if rl.sqlite != nil {
		return rl.loadSQLite()
	}

	content, err := coreio.Local.Read(rl.filePath)
	if os.IsNotExist(err) {
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
	// Merge loaded quotas (loaded quotas override in-memory defaults).
	maps.Copy(rl.Quotas, quotas)

	state, err := rl.sqlite.loadState()
	if err != nil {
		return err
	}
	// Replace in-memory state with persisted state.
	maps.Copy(rl.State, state)
	return nil
}

// Persist writes a snapshot of the state to disk (YAML) or database (SQLite).
// It clones the state under a lock and performs I/O without blocking other callers.
func (rl *RateLimiter) Persist() error {
	rl.mu.Lock()
	quotas := maps.Clone(rl.Quotas)
	state := make(map[string]*UsageStats, len(rl.State))
	for k, v := range rl.State {
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
		if err := sqlite.saveQuotas(quotas); err != nil {
			return fmt.Errorf("ratelimit.Persist: sqlite quotas: %w", err)
		}
		if err := sqlite.saveState(state); err != nil {
			return fmt.Errorf("ratelimit.Persist: sqlite state: %w", err)
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
		return fmt.Errorf("ratelimit.Persist: marshal: %w", err)
	}

	if err := coreio.Local.Write(filePath, string(data)); err != nil {
		return fmt.Errorf("ratelimit.Persist: write: %w", err)
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
func (rl *RateLimiter) BackgroundPrune(interval time.Duration) func() {
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
func (rl *RateLimiter) CanSend(model string, estimatedTokens int) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	quota, ok := rl.Quotas[model]
	if !ok {
		return true // Unknown models are allowed
	}

	// Unlimited check
	if quota.MaxRPM == 0 && quota.MaxTPM == 0 && quota.MaxRPD == 0 {
		return true
	}

	rl.prune(model)
	stats, ok := rl.State[model]
	if !ok {
		stats = &UsageStats{DayStart: time.Now()}
		rl.State[model] = stats
	}

	// Check RPD
	if quota.MaxRPD > 0 && stats.DayCount >= quota.MaxRPD {
		return false
	}

	// Check RPM
	if quota.MaxRPM > 0 && len(stats.Requests) >= quota.MaxRPM {
		return false
	}

	// Check TPM
	if quota.MaxTPM > 0 {
		currentTokens := 0
		for _, t := range stats.Tokens {
			currentTokens += t.Count
		}
		if currentTokens+estimatedTokens > quota.MaxTPM {
			return false
		}
	}

	return true
}

// RecordUsage records a successful API call.
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
	stats.Requests = append(stats.Requests, now)
	stats.Tokens = append(stats.Tokens, TokenEntry{Time: now, Count: promptTokens + outputTokens})
	stats.DayCount++
}

// WaitForCapacity blocks until capacity is available or context is cancelled.
func (rl *RateLimiter) WaitForCapacity(ctx context.Context, model string, tokens int) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		if rl.CanSend(model, tokens) {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// check again
		}
	}
}

// Reset clears stats for a model (or all if model is empty).
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
type ModelStats struct {
	RPM      int
	MaxRPM   int
	TPM      int
	MaxTPM   int
	RPD      int
	MaxRPD   int
	DayStart time.Time
}

// Models returns a sorted iterator over all model names tracked by the limiter.
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
func (rl *RateLimiter) Stats(model string) ModelStats {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.prune(model)

	stats := ModelStats{}
	quota, ok := rl.Quotas[model]
	if ok {
		stats.MaxRPM = quota.MaxRPM
		stats.MaxTPM = quota.MaxTPM
		stats.MaxRPD = quota.MaxRPD
	}

	if s, ok := rl.State[model]; ok {
		stats.RPM = len(s.Requests)
		stats.RPD = s.DayCount
		stats.DayStart = s.DayStart
		for _, t := range s.Tokens {
			stats.TPM += t.Count
		}
	}

	return stats
}

// AllStats returns stats for all tracked models.
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

	now := time.Now()
	window := now.Add(-1 * time.Minute)

	for m := range result {
		// Prune inline
		if s, ok := rl.State[m]; ok {
			s.Requests = slices.DeleteFunc(s.Requests, func(t time.Time) bool {
				return !t.After(window)
			})
			s.Tokens = slices.DeleteFunc(s.Tokens, func(t TokenEntry) bool {
				return !t.Time.After(window)
			})

			if now.Sub(s.DayStart) >= 24*time.Hour {
				s.DayStart = now
				s.DayCount = 0
			}
		}

		ms := ModelStats{}
		if q, ok := rl.Quotas[m]; ok {
			ms.MaxRPM = q.MaxRPM
			ms.MaxTPM = q.MaxTPM
			ms.MaxRPD = q.MaxRPD
		}
		if s, ok := rl.State[m]; ok {
			ms.RPM = len(s.Requests)
			ms.RPD = s.DayCount
			ms.DayStart = s.DayStart
			for _, t := range s.Tokens {
				ms.TPM += t.Count
			}
		}
		result[m] = ms
	}

	return result
}

// NewWithSQLite creates a SQLite-backed RateLimiter with Gemini defaults.
// The database is created at dbPath if it does not exist. Use Close() to
// release the database connection when finished.
func NewWithSQLite(dbPath string) (*RateLimiter, error) {
	return NewWithSQLiteConfig(dbPath, Config{
		Providers: []Provider{ProviderGemini},
	})
}

// NewWithSQLiteConfig creates a SQLite-backed RateLimiter with custom config.
// The Backend field in cfg is ignored (always "sqlite"). Use Close() to
// release the database connection when finished.
func NewWithSQLiteConfig(dbPath string, cfg Config) (*RateLimiter, error) {
	store, err := newSQLiteStore(dbPath)
	if err != nil {
		return nil, err
	}

	rl := &RateLimiter{
		Quotas: make(map[string]ModelQuota),
		State:  make(map[string]*UsageStats),
		sqlite: store,
	}

	// Load provider profiles.
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

	return rl, nil
}

// Close releases resources held by the RateLimiter. For YAML-backed
// limiters this is a no-op. For SQLite-backed limiters it closes the
// database connection.
func (rl *RateLimiter) Close() error {
	if rl.sqlite != nil {
		return rl.sqlite.close()
	}
	return nil
}

// MigrateYAMLToSQLite reads state from a YAML file and writes it to a new
// SQLite database. Both quotas and usage state are migrated. The SQLite
// database is created if it does not exist.
func MigrateYAMLToSQLite(yamlPath, sqlitePath string) error {
	// Load from YAML.
	content, err := coreio.Local.Read(yamlPath)
	if err != nil {
		return fmt.Errorf("ratelimit.MigrateYAMLToSQLite: read: %w", err)
	}

	var rl RateLimiter
	if err := yaml.Unmarshal([]byte(content), &rl); err != nil {
		return fmt.Errorf("ratelimit.MigrateYAMLToSQLite: unmarshal: %w", err)
	}

	// Write to SQLite.
	store, err := newSQLiteStore(sqlitePath)
	if err != nil {
		return err
	}
	defer store.close()

	if rl.Quotas != nil {
		if err := store.saveQuotas(rl.Quotas); err != nil {
			return err
		}
	}
	if rl.State != nil {
		if err := store.saveState(rl.State); err != nil {
			return err
		}
	}
	return nil
}

// CountTokens calls the Google API to count tokens for a prompt.
func CountTokens(ctx context.Context, apiKey, model, text string) (int, error) {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:countTokens", model)

	reqBody := map[string]any{
		"contents": []any{
			map[string]any{
				"parts": []any{
					map[string]string{"text": text},
				},
			},
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return 0, fmt.Errorf("ratelimit.CountTokens: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return 0, fmt.Errorf("ratelimit.CountTokens: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("ratelimit.CountTokens: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("ratelimit.CountTokens: API error (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		TotalTokens int `json:"totalTokens"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("ratelimit.CountTokens: decode response: %w", err)
	}

	return result.TotalTokens, nil
}
