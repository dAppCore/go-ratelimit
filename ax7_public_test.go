// SPDX-License-Identifier: EUPL-1.2

package ratelimit_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"time"

	. "dappco.re/go"
	ratelimit "dappco.re/go/ratelimit"
)

type ax7RoundTrip func(*http.Request) (*http.Response, error)

func (f ax7RoundTrip) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func ax7YAMLPath(t *T) string {
	t.Helper()
	return Path(t.TempDir(), "ratelimits.yaml")
}

func ax7Limiter(t *T) *ratelimit.RateLimiter {
	t.Helper()
	rl, err := ratelimit.NewWithConfig(ratelimit.Config{
		FilePath:  ax7YAMLPath(t),
		Providers: []ratelimit.Provider{ratelimit.ProviderLocal},
	})
	RequireNoError(t, err)
	return rl
}

func TestAX7_New_Good(t *T) {
	rl, err := ratelimit.New()

	RequireNoError(t, err)
	AssertTrue(t, rl.CanSend("gemini-3-pro-preview", 1))
}

func TestAX7_New_Bad(t *T) {
	t.Setenv("CORE_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("home", "")
	t.Setenv("USERPROFILE", "")

	rl, err := ratelimit.New()
	AssertError(t, err)
	AssertNil(t, rl)
}

func TestAX7_New_Ugly(t *T) {
	rl1, err := ratelimit.New()
	RequireNoError(t, err)
	rl2, err := ratelimit.New()
	RequireNoError(t, err)

	rl1.RecordUsage("gemini-3-pro-preview", 1, 1)
	AssertEqual(t, 1, rl1.Stats("gemini-3-pro-preview").RPD)
	AssertEqual(t, 0, rl2.Stats("gemini-3-pro-preview").RPD)
}

func TestAX7_NewWithConfig_Good(t *T) {
	rl, err := ratelimit.NewWithConfig(ratelimit.Config{
		FilePath:  ax7YAMLPath(t),
		Providers: []ratelimit.Provider{ratelimit.ProviderOpenAI},
		Quotas: map[string]ratelimit.ModelQuota{
			"custom": {MaxRPM: 7, MaxTPM: 70, MaxRPD: 700},
		},
	})

	RequireNoError(t, err)
	AssertEqual(t, 500, rl.Stats("gpt-4o").MaxRPM)
	AssertEqual(t, 7, rl.Stats("custom").MaxRPM)
}

func TestAX7_NewWithConfig_Bad(t *T) {
	rl, err := ratelimit.NewWithConfig(ratelimit.Config{
		FilePath: ax7YAMLPath(t),
		Backend:  "bogus",
	})

	AssertError(t, err)
	AssertNil(t, rl)
}

func TestAX7_NewWithConfig_Ugly(t *T) {
	rl, err := ratelimit.NewWithConfig(ratelimit.Config{FilePath: ax7YAMLPath(t)})

	RequireNoError(t, err)
	AssertEqual(t, 150, rl.Stats("gemini-3-pro-preview").MaxRPM)
	AssertEqual(t, 0, rl.Stats("missing-model").MaxRPM)
}

func TestAX7_RateLimiter_SetQuota_Good(t *T) {
	rl := ax7Limiter(t)
	rl.SetQuota("model-a", ratelimit.ModelQuota{MaxRPM: 3, MaxTPM: 30, MaxRPD: 300})

	stats := rl.Stats("model-a")
	AssertEqual(t, 3, stats.MaxRPM)
	AssertEqual(t, 30, stats.MaxTPM)
	AssertEqual(t, 300, stats.MaxRPD)
}

func TestAX7_RateLimiter_SetQuota_Bad(t *T) {
	rl := ax7Limiter(t)
	rl.SetQuota("model-a", ratelimit.ModelQuota{MaxRPM: 1})
	rl.SetQuota("model-a", ratelimit.ModelQuota{})

	decision := rl.Decide("model-a", 999999)
	AssertTrue(t, decision.Allowed)
	AssertEqual(t, ratelimit.DecisionUnlimited, decision.Code)
}

func TestAX7_RateLimiter_SetQuota_Ugly(t *T) {
	rl := ax7Limiter(t)
	rl.SetQuota("", ratelimit.ModelQuota{MaxRPM: 1})

	AssertEqual(t, 1, rl.Stats("").MaxRPM)
	AssertFalse(t, rl.CanSend("", -1))
}

func TestAX7_RateLimiter_AddProvider_Good(t *T) {
	rl := ax7Limiter(t)
	rl.AddProvider(ratelimit.ProviderAnthropic)

	AssertEqual(t, 50, rl.Stats("claude-opus-4").MaxRPM)
	AssertEqual(t, 40000, rl.Stats("claude-opus-4").MaxTPM)
}

func TestAX7_RateLimiter_AddProvider_Bad(t *T) {
	rl := ax7Limiter(t)
	rl.AddProvider(ratelimit.Provider("unknown"))

	models := make([]string, 0)
	for model := range rl.Models() {
		models = append(models, model)
	}
	AssertEmpty(t, models)
}

func TestAX7_RateLimiter_AddProvider_Ugly(t *T) {
	rl := ax7Limiter(t)
	rl.SetQuota("local-model", ratelimit.ModelQuota{MaxRPM: 2})
	rl.AddProvider(ratelimit.ProviderLocal)

	AssertEqual(t, 2, rl.Stats("local-model").MaxRPM)
	AssertTrue(t, rl.CanSend("unknown-local", 1))
}

func TestAX7_RateLimiter_Load_Good(t *T) {
	path := ax7YAMLPath(t)
	rl, err := ratelimit.NewWithConfig(ratelimit.Config{
		FilePath: path,
		Quotas:   map[string]ratelimit.ModelQuota{"model-a": {MaxRPM: 5}},
	})
	RequireNoError(t, err)
	rl.RecordUsage("model-a", 1, 2)
	RequireNoError(t, rl.Persist())

	loaded, err := ratelimit.NewWithConfig(ratelimit.Config{FilePath: path, Providers: []ratelimit.Provider{ratelimit.ProviderLocal}})
	RequireNoError(t, err)
	AssertNoError(t, loaded.Load())
	AssertEqual(t, 1, loaded.Stats("model-a").RPD)
}

func TestAX7_RateLimiter_Load_Bad(t *T) {
	path := ax7YAMLPath(t)
	RequireNoError(t, os.WriteFile(path, []byte("{{{not yaml"), 0o600))
	rl, err := ratelimit.NewWithConfig(ratelimit.Config{FilePath: path, Providers: []ratelimit.Provider{ratelimit.ProviderLocal}})
	RequireNoError(t, err)

	err = rl.Load()
	AssertError(t, err)
	AssertContains(t, err.Error(), "yaml")
}

func TestAX7_RateLimiter_Load_Ugly(t *T) {
	rl, err := ratelimit.NewWithConfig(ratelimit.Config{
		FilePath:  Path(t.TempDir(), "missing.yaml"),
		Providers: []ratelimit.Provider{ratelimit.ProviderLocal},
	})
	RequireNoError(t, err)

	AssertNoError(t, rl.Load())
	AssertEmpty(t, rl.AllStats())
}

func TestAX7_RateLimiter_Persist_Good(t *T) {
	path := ax7YAMLPath(t)
	rl, err := ratelimit.NewWithConfig(ratelimit.Config{FilePath: path, Quotas: map[string]ratelimit.ModelQuota{"model-a": {MaxRPM: 2}}})
	RequireNoError(t, err)
	rl.RecordUsage("model-a", 1, 1)

	AssertNoError(t, rl.Persist())
	_, err = os.Stat(path)
	AssertNoError(t, err)
}

func TestAX7_RateLimiter_Persist_Bad(t *T) {
	rl, err := ratelimit.NewWithConfig(ratelimit.Config{
		FilePath:  t.TempDir(),
		Providers: []ratelimit.Provider{ratelimit.ProviderLocal},
	})
	RequireNoError(t, err)
	rl.RecordUsage("model-a", 1, 1)

	err = rl.Persist()
	AssertError(t, err)
}

func TestAX7_RateLimiter_Persist_Ugly(t *T) {
	path := ax7YAMLPath(t)
	rl, err := ratelimit.NewWithConfig(ratelimit.Config{FilePath: path, Providers: []ratelimit.Provider{ratelimit.ProviderLocal}})
	RequireNoError(t, err)

	AssertNoError(t, rl.Persist())
	loaded, err := ratelimit.NewWithConfig(ratelimit.Config{FilePath: path, Providers: []ratelimit.Provider{ratelimit.ProviderLocal}})
	RequireNoError(t, err)
	AssertNoError(t, loaded.Load())
}

func TestAX7_RateLimiter_BackgroundPrune_Good(t *T) {
	rl := ax7Limiter(t)
	stop := rl.BackgroundPrune(10 * time.Millisecond)
	time.Sleep(20 * time.Millisecond)

	AssertNotPanics(t, stop)
	AssertEmpty(t, rl.AllStats())
}

func TestAX7_RateLimiter_BackgroundPrune_Bad(t *T) {
	rl := ax7Limiter(t)
	stop := rl.BackgroundPrune(0)
	rl.RecordUsage("model-a", 1, 1)

	AssertNotPanics(t, stop)
	AssertEqual(t, 1, rl.Stats("model-a").RPD)
}

func TestAX7_RateLimiter_BackgroundPrune_Ugly(t *T) {
	rl := ax7Limiter(t)
	stop := rl.BackgroundPrune(1 * time.Millisecond)

	AssertNotPanics(t, stop)
	AssertNotPanics(t, stop)
}

func TestAX7_RateLimiter_CanSend_Good(t *T) {
	rl := ax7Limiter(t)
	rl.SetQuota("model-a", ratelimit.ModelQuota{MaxRPM: 2, MaxTPM: 100, MaxRPD: 5})

	AssertTrue(t, rl.CanSend("model-a", 10))
	AssertEqual(t, ratelimit.DecisionAllowed, rl.Decide("model-a", 10).Code)
}

func TestAX7_RateLimiter_CanSend_Bad(t *T) {
	rl := ax7Limiter(t)
	rl.SetQuota("model-a", ratelimit.ModelQuota{MaxRPM: 1, MaxTPM: 100, MaxRPD: 5})
	rl.RecordUsage("model-a", 1, 1)

	AssertFalse(t, rl.CanSend("model-a", 1))
	AssertEqual(t, ratelimit.DecisionRPMLimit, rl.Decide("model-a", 1).Code)
}

func TestAX7_RateLimiter_CanSend_Ugly(t *T) {
	rl := ax7Limiter(t)
	rl.SetQuota("model-a", ratelimit.ModelQuota{MaxRPM: 1, MaxTPM: 100, MaxRPD: 5})

	AssertFalse(t, rl.CanSend("model-a", -1))
	AssertTrue(t, rl.CanSend("unknown-model", 999999))
}

func TestAX7_RateLimiter_RecordUsage_Good(t *T) {
	rl := ax7Limiter(t)
	rl.RecordUsage("model-a", 10, 15)

	stats := rl.Stats("model-a")
	AssertEqual(t, 1, stats.RPD)
	AssertEqual(t, 25, stats.TPM)
}

func TestAX7_RateLimiter_RecordUsage_Bad(t *T) {
	rl := ax7Limiter(t)
	rl.RecordUsage("model-a", -10, 15)

	stats := rl.Stats("model-a")
	AssertEqual(t, 1, stats.RPD)
	AssertEqual(t, 15, stats.TPM)
}

func TestAX7_RateLimiter_RecordUsage_Ugly(t *T) {
	rl := ax7Limiter(t)
	rl.RecordUsage("", 0, 0)

	stats := rl.Stats("")
	AssertEqual(t, 1, stats.RPD)
	AssertEqual(t, 0, stats.TPM)
}

func TestAX7_RateLimiter_WaitForCapacity_Good(t *T) {
	rl := ax7Limiter(t)
	rl.SetQuota("model-a", ratelimit.ModelQuota{MaxRPM: 1, MaxTPM: 100, MaxRPD: 5})

	err := rl.WaitForCapacity(context.Background(), "model-a", 1)
	AssertNoError(t, err)
}

func TestAX7_RateLimiter_WaitForCapacity_Bad(t *T) {
	rl := ax7Limiter(t)
	err := rl.WaitForCapacity(context.Background(), "model-a", -1)

	AssertError(t, err)
	AssertContains(t, err.Error(), "negative tokens")
}

func TestAX7_RateLimiter_WaitForCapacity_Ugly(t *T) {
	rl := ax7Limiter(t)
	rl.SetQuota("model-a", ratelimit.ModelQuota{MaxRPM: 1, MaxTPM: 100, MaxRPD: 5})
	rl.RecordUsage("model-a", 1, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := rl.WaitForCapacity(ctx, "model-a", 1)
	AssertErrorIs(t, err, context.Canceled)
}

func TestAX7_RateLimiter_Reset_Good(t *T) {
	rl := ax7Limiter(t)
	rl.RecordUsage("model-a", 1, 1)
	rl.Reset("model-a")

	stats := rl.Stats("model-a")
	AssertEqual(t, 0, stats.RPD)
	AssertEqual(t, 0, stats.TPM)
}

func TestAX7_RateLimiter_Reset_Bad(t *T) {
	rl := ax7Limiter(t)
	rl.RecordUsage("model-a", 1, 1)
	rl.Reset("missing-model")

	AssertEqual(t, 1, rl.Stats("model-a").RPD)
	AssertEqual(t, 0, rl.Stats("missing-model").RPD)
}

func TestAX7_RateLimiter_Reset_Ugly(t *T) {
	rl := ax7Limiter(t)
	rl.RecordUsage("model-a", 1, 1)
	rl.RecordUsage("model-b", 1, 1)
	rl.Reset("")

	AssertEqual(t, 0, rl.Stats("model-a").RPD)
	AssertEqual(t, 0, rl.Stats("model-b").RPD)
}

func TestAX7_RateLimiter_Models_Good(t *T) {
	rl, err := ratelimit.NewWithConfig(ratelimit.Config{
		FilePath: ax7YAMLPath(t),
		Quotas: map[string]ratelimit.ModelQuota{
			"model-b": {MaxRPM: 2},
			"model-a": {MaxRPM: 1},
		},
	})
	RequireNoError(t, err)

	var models []string
	for model := range rl.Models() {
		models = append(models, model)
	}
	AssertEqual(t, []string{"model-a", "model-b"}, models)
}

func TestAX7_RateLimiter_Models_Bad(t *T) {
	rl := ax7Limiter(t)
	var models []string
	for model := range rl.Models() {
		models = append(models, model)
	}

	AssertEmpty(t, models)
	AssertEqual(t, 0, rl.Stats("missing").MaxRPM)
}

func TestAX7_RateLimiter_Models_Ugly(t *T) {
	rl, err := ratelimit.NewWithConfig(ratelimit.Config{
		FilePath: ax7YAMLPath(t),
		Quotas: map[string]ratelimit.ModelQuota{
			"model-a": {MaxRPM: 1},
			"model-b": {MaxRPM: 2},
			"model-c": {MaxRPM: 3},
		},
	})
	RequireNoError(t, err)

	var first string
	for model := range rl.Models() {
		first = model
		break
	}
	AssertEqual(t, "model-a", first)
}

func TestAX7_RateLimiter_Iter_Good(t *T) {
	rl, err := ratelimit.NewWithConfig(ratelimit.Config{
		FilePath: ax7YAMLPath(t),
		Quotas: map[string]ratelimit.ModelQuota{
			"model-b": {MaxRPM: 2},
			"model-a": {MaxRPM: 1},
		},
	})
	RequireNoError(t, err)
	rl.RecordUsage("model-a", 1, 1)

	var models []string
	for model, stats := range rl.Iter() {
		models = append(models, model)
		if model == "model-a" {
			AssertEqual(t, 1, stats.RPD)
		}
	}
	AssertEqual(t, []string{"model-a", "model-b"}, models)
}

func TestAX7_RateLimiter_Iter_Bad(t *T) {
	rl := ax7Limiter(t)
	var models []string
	for model := range rl.Iter() {
		models = append(models, model)
	}

	AssertEmpty(t, models)
	AssertEmpty(t, rl.AllStats())
}

func TestAX7_RateLimiter_Iter_Ugly(t *T) {
	rl, err := ratelimit.NewWithConfig(ratelimit.Config{
		FilePath: ax7YAMLPath(t),
		Quotas: map[string]ratelimit.ModelQuota{
			"model-a": {MaxRPM: 1},
			"model-b": {MaxRPM: 2},
			"model-c": {MaxRPM: 3},
		},
	})
	RequireNoError(t, err)

	var seen []string
	for model := range rl.Iter() {
		seen = append(seen, model)
		if len(seen) == 2 {
			break
		}
	}
	AssertEqual(t, []string{"model-a", "model-b"}, seen)
}

func TestAX7_RateLimiter_Stats_Good(t *T) {
	rl := ax7Limiter(t)
	rl.SetQuota("model-a", ratelimit.ModelQuota{MaxRPM: 10, MaxTPM: 100, MaxRPD: 5})
	rl.RecordUsage("model-a", 2, 3)

	stats := rl.Stats("model-a")
	AssertEqual(t, 1, stats.RPM)
	AssertEqual(t, 5, stats.TPM)
	AssertEqual(t, 10, stats.MaxRPM)
}

func TestAX7_RateLimiter_Stats_Bad(t *T) {
	rl := ax7Limiter(t)
	stats := rl.Stats("missing-model")

	AssertEqual(t, 0, stats.RPM)
	AssertEqual(t, 0, stats.MaxRPM)
	AssertTrue(t, stats.DayStart.IsZero())
}

func TestAX7_RateLimiter_Stats_Ugly(t *T) {
	rl := ax7Limiter(t)
	rl.RecordUsage("model-a", 1, 1)
	rl.Reset("model-a")

	stats := rl.Stats("model-a")
	AssertEqual(t, 0, stats.RPD)
	AssertEqual(t, 0, stats.TPM)
}

func TestAX7_RateLimiter_AllStats_Good(t *T) {
	rl := ax7Limiter(t)
	rl.SetQuota("model-a", ratelimit.ModelQuota{MaxRPM: 10})
	rl.RecordUsage("model-b", 1, 1)

	all := rl.AllStats()
	AssertContains(t, all, "model-a")
	AssertContains(t, all, "model-b")
	AssertEqual(t, 2, all["model-b"].TPM)
}

func TestAX7_RateLimiter_AllStats_Bad(t *T) {
	rl := ax7Limiter(t)
	all := rl.AllStats()

	AssertEmpty(t, all)
	AssertEqual(t, 0, len(all))
}

func TestAX7_RateLimiter_AllStats_Ugly(t *T) {
	rl := ax7Limiter(t)
	rl.SetQuota("model-a", ratelimit.ModelQuota{MaxRPM: 10})
	all := rl.AllStats()

	AssertContains(t, all, "model-a")
	AssertEqual(t, 0, all["model-a"].RPM)
	AssertEqual(t, 10, all["model-a"].MaxRPM)
}

func TestAX7_RateLimiter_Decide_Good(t *T) {
	rl := ax7Limiter(t)
	rl.SetQuota("model-a", ratelimit.ModelQuota{MaxRPM: 2, MaxTPM: 100, MaxRPD: 5})

	decision := rl.Decide("model-a", 10)
	AssertTrue(t, decision.Allowed)
	AssertEqual(t, ratelimit.DecisionAllowed, decision.Code)
}

func TestAX7_RateLimiter_Decide_Bad(t *T) {
	rl := ax7Limiter(t)
	decision := rl.Decide("model-a", -1)

	AssertFalse(t, decision.Allowed)
	AssertEqual(t, ratelimit.DecisionInvalidTokens, decision.Code)
	AssertContains(t, decision.Reason, "non-negative")
}

func TestAX7_RateLimiter_Decide_Ugly(t *T) {
	rl := ax7Limiter(t)
	rl.SetQuota("model-a", ratelimit.ModelQuota{MaxRPM: 1, MaxTPM: 100, MaxRPD: 5})
	rl.RecordUsage("model-a", 1, 1)

	decision := rl.Decide("model-a", 1)
	AssertFalse(t, decision.Allowed)
	AssertEqual(t, ratelimit.DecisionRPMLimit, decision.Code)
}

func TestAX7_NewWithSQLite_Good(t *T) {
	rl, err := ratelimit.NewWithSQLite(Path(t.TempDir(), "limits.db"))

	RequireNoError(t, err)
	defer rl.Close()
	rl.RecordUsage("gemini-3-pro-preview", 1, 1)
	AssertNoError(t, rl.Persist())
}

func TestAX7_NewWithSQLite_Bad(t *T) {
	rl, err := ratelimit.NewWithSQLite(Path(t.TempDir(), "missing", "limits.db"))

	AssertError(t, err)
	AssertNil(t, rl)
}

func TestAX7_NewWithSQLite_Ugly(t *T) {
	path := Path(t.TempDir(), "limits with spaces.db")
	rl, err := ratelimit.NewWithSQLite(path)
	RequireNoError(t, err)
	rl.RecordUsage("gemini-3-pro-preview", 1, 1)

	AssertNoError(t, rl.Persist())
	AssertNoError(t, rl.Close())
}

func TestAX7_NewWithSQLiteConfig_Good(t *T) {
	rl, err := ratelimit.NewWithSQLiteConfig(Path(t.TempDir(), "limits.db"), ratelimit.Config{
		Providers: []ratelimit.Provider{ratelimit.ProviderOpenAI},
		Quotas:    map[string]ratelimit.ModelQuota{"custom": {MaxRPM: 9}},
	})

	RequireNoError(t, err)
	defer rl.Close()
	AssertEqual(t, 500, rl.Stats("gpt-4o").MaxRPM)
	AssertEqual(t, 9, rl.Stats("custom").MaxRPM)
}

func TestAX7_NewWithSQLiteConfig_Bad(t *T) {
	rl, err := ratelimit.NewWithSQLiteConfig(Path(t.TempDir(), "missing", "limits.db"), ratelimit.Config{})

	AssertError(t, err)
	AssertNil(t, rl)
}

func TestAX7_NewWithSQLiteConfig_Ugly(t *T) {
	rl, err := ratelimit.NewWithSQLiteConfig(Path(t.TempDir(), "limits.db"), ratelimit.Config{
		Backend:   "yaml",
		Providers: []ratelimit.Provider{ratelimit.ProviderLocal},
	})

	RequireNoError(t, err)
	defer rl.Close()
	rl.RecordUsage("local", 1, 1)
	AssertNoError(t, rl.Persist())
}

func TestAX7_RateLimiter_Close_Good(t *T) {
	rl := ax7Limiter(t)
	err := rl.Close()

	AssertNoError(t, err)
	AssertNoError(t, rl.Close())
}

func TestAX7_RateLimiter_Close_Bad(t *T) {
	rl, err := ratelimit.NewWithSQLite(Path(t.TempDir(), "limits.db"))
	RequireNoError(t, err)
	RequireNoError(t, rl.Close())

	err = rl.Persist()
	AssertError(t, err)
}

func TestAX7_RateLimiter_Close_Ugly(t *T) {
	rl, err := ratelimit.NewWithSQLite(Path(t.TempDir(), "limits.db"))
	RequireNoError(t, err)

	AssertNoError(t, rl.Close())
	AssertNoError(t, rl.Close())
}

func TestAX7_MigrateYAMLToSQLite_Good(t *T) {
	yamlPath := ax7YAMLPath(t)
	sqlitePath := Path(t.TempDir(), "limits.db")
	rl, err := ratelimit.NewWithConfig(ratelimit.Config{
		FilePath: yamlPath,
		Quotas:   map[string]ratelimit.ModelQuota{"model-a": {MaxRPM: 4}},
	})
	RequireNoError(t, err)
	rl.RecordUsage("model-a", 2, 3)
	RequireNoError(t, rl.Persist())

	AssertNoError(t, ratelimit.MigrateYAMLToSQLite(yamlPath, sqlitePath))
	loaded, err := ratelimit.NewWithSQLite(sqlitePath)
	RequireNoError(t, err)
	defer loaded.Close()
	AssertNoError(t, loaded.Load())
	AssertEqual(t, 1, loaded.Stats("model-a").RPD)
}

func TestAX7_MigrateYAMLToSQLite_Bad(t *T) {
	err := ratelimit.MigrateYAMLToSQLite(Path(t.TempDir(), "missing.yaml"), Path(t.TempDir(), "limits.db"))

	AssertError(t, err)
	AssertContains(t, err.Error(), "read")
}

func TestAX7_MigrateYAMLToSQLite_Ugly(t *T) {
	yamlPath := ax7YAMLPath(t)
	sqlitePath := Path(t.TempDir(), "limits.db")
	RequireNoError(t, os.WriteFile(yamlPath, []byte("{{{not yaml"), 0o600))

	err := ratelimit.MigrateYAMLToSQLite(yamlPath, sqlitePath)
	AssertError(t, err)
	AssertContains(t, err.Error(), "unmarshal")
}

func TestAX7_CountTokens_Good(t *T) {
	oldTransport := http.DefaultTransport
	http.DefaultTransport = ax7RoundTrip(func(req *http.Request) (*http.Response, error) {
		AssertContains(t, req.URL.Path, "/v1beta/models/gemini-3-pro-preview:countTokens")
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(NewReader(`{"totalTokens":7}`)),
			Header:     make(http.Header),
		}, nil
	})
	defer func() { http.DefaultTransport = oldTransport }()

	tokens, err := ratelimit.CountTokens(context.Background(), "key", "gemini-3-pro-preview", "hello")
	AssertNoError(t, err)
	AssertEqual(t, 7, tokens)
}

func TestAX7_CountTokens_Bad(t *T) {
	tokens, err := ratelimit.CountTokens(context.Background(), "key", "", "hello")

	AssertError(t, err)
	AssertEqual(t, 0, tokens)
	AssertContains(t, err.Error(), "empty model")
}

func TestAX7_CountTokens_Ugly(t *T) {
	oldTransport := http.DefaultTransport
	http.DefaultTransport = ax7RoundTrip(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(NewReader(`{"totalTokens":"bad"}`)),
			Header:     make(http.Header),
		}, nil
	})
	defer func() { http.DefaultTransport = oldTransport }()

	tokens, err := ratelimit.CountTokens(context.Background(), "key", "gemini-3-pro-preview", "hello")
	AssertError(t, err)
	AssertEqual(t, 0, tokens)
}
