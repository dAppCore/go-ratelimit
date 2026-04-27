// SPDX-License-Identifier: EUPL-1.2

package ratelimit

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// --- Phase 2: SQLite basic tests ---

func TestSQLite_NewSQLiteStore_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "test.db")
	store, err := newSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	defer store.close()

	// Verify the database file was created.
	if !pathExists(dbPath) {
		t.Fatal(testExpectedTrueMessage("database file should exist"))
	}
}

func TestSQLite_NewSQLiteStore_Bad(t *testing.T) {
	t.Run("invalid path returns error", func(t *testing.T) {
		// Path inside a non-existent directory with no parent.
		_, err := newSQLiteStore("/nonexistent/deep/nested/dir/test.db")
		if err == nil {
			t.Fatal(testExpectedErrorMessage("should fail with invalid path"))
		}
	})
}

func TestSQLite_QuotasRoundTrip_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "quotas.db")
	store, err := newSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	defer store.close()

	quotas := map[string]ModelQuota{
		"model-a": {MaxRPM: 100, MaxTPM: 50000, MaxRPD: 1000},
		"model-b": {MaxRPM: 200, MaxTPM: 100000, MaxRPD: 2000},
		"model-c": {MaxRPM: 0, MaxTPM: 0, MaxRPD: 0}, // Unlimited
	}
	if err := store.saveQuotas(quotas); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	loaded, err := store.loadQuotas()
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if !testEqual(len(quotas), len(loaded)) {
		t.Fatal(testWantGotMessage(len(quotas), len(loaded), "should load same number of quotas"))
	}
	for model, expected := range quotas {
		actual, ok := loaded[model]
		if !ok {
			t.Fatal(testExpectedTrueMessage(fmt.Sprintf("loaded quotas should contain %s", model)))
		}
		if !testEqual(expected.MaxRPM, actual.MaxRPM) {
			t.Fatal(testWantGotMessage(expected.MaxRPM, actual.MaxRPM))
		}
		if !testEqual(expected.MaxTPM, actual.MaxTPM) {
			t.Fatal(testWantGotMessage(expected.MaxTPM, actual.MaxTPM))
		}
		if !testEqual(expected.MaxRPD, actual.MaxRPD) {
			t.Fatal(testWantGotMessage(expected.MaxRPD, actual.MaxRPD))
		}
	}
}

func TestSQLite_QuotasOverwrite_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "overwrite.db")
	store, err := newSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	defer store.close()
	if err :=

		// Save initial quotas.
		store.saveQuotas(map[string]ModelQuota{
			"model-a": {MaxRPM: 100, MaxTPM: 50000, MaxRPD: 1000},
		}); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if err :=

		// Save a second snapshot with updated values.
		store.saveQuotas(map[string]ModelQuota{
			"model-a": {MaxRPM: 999, MaxTPM: 888, MaxRPD: 777},
		}); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	loaded, err := store.loadQuotas()
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	q := loaded["model-a"]
	if !testEqual(999, q.MaxRPM) {
		t.Fatal(testWantGotMessage(999, q.MaxRPM, "should have updated RPM"))
	}
	if !testEqual(888, q.MaxTPM) {
		t.Fatal(testWantGotMessage(888, q.MaxTPM, "should have updated TPM"))
	}
	if !testEqual(777, q.MaxRPD) {
		t.Fatal(testWantGotMessage(777, q.MaxRPD, "should have updated RPD"))
	}
}

func TestSQLite_StateRoundTrip_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "state.db")
	store, err := newSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	defer store.close()

	now := time.Now()
	// Use ascending order so ORDER BY ts in loadState matches insertion order.
	t1 := now.Add(-10 * time.Second)
	t2 := now

	state := map[string]*UsageStats{
		"model-a": {
			Requests: []time.Time{t1, t2},
			Tokens: []TokenEntry{
				{Time: t1, Count: 300},
				{Time: t2, Count: 500},
			},
			DayStart: now.Add(-1 * time.Hour),
			DayCount: 42,
		},
		"model-b": {
			Requests: []time.Time{now.Add(-5 * time.Second)},
			Tokens: []TokenEntry{
				{Time: now.Add(-5 * time.Second), Count: 100},
			},
			DayStart: now,
			DayCount: 1,
		},
	}
	if err := store.saveState(state); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	loaded, err := store.loadState()
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if !testEqual(len(state), len(loaded)) {
		t.Fatal(testWantGotMessage(len(state), len(loaded), "should load same number of models"))
	}

	for model, expected := range state {
		actual, ok := loaded[model]
		if !ok {
			t.Fatal(testExpectedTrueMessage(fmt.Sprintf("loaded state should contain %s", model)))
		}
		if !testHasLen(actual.Requests, len(expected.Requests)) {
			t.Fatal(testLenMessage(actual.Requests, len(expected.Requests), fmt.Sprintf("request count for %s", model)))
		}
		if !testHasLen(actual.Tokens, len(expected.Tokens)) {
			t.Fatal(testLenMessage(actual.Tokens, len(expected.Tokens), fmt.Sprintf("token count for %s", model)))
		}
		if !testEqual(expected.DayCount, actual.DayCount) {
			t.Fatal(testWantGotMessage(expected.DayCount, actual.DayCount, fmt.Sprintf("day count for %s", model)))
		}

		// Time comparison with nanosecond precision (UnixNano round-trip).
		if !testEqual(expected.DayStart.UnixNano(), actual.DayStart.UnixNano()) {
			t.Fatal(testWantGotMessage(expected.DayStart.UnixNano(), actual.DayStart.UnixNano(), fmt.Sprintf("day start for %s", model)))
		}

		for i, req := range expected.Requests {
			if !testEqual(req.UnixNano(), actual.Requests[i].UnixNano()) {
				t.Fatal(testWantGotMessage(req.UnixNano(), actual.Requests[i].UnixNano(), fmt.Sprintf("request %d for %s", i, model)))
			}
		}
		for i, tok := range expected.Tokens {
			if !testEqual(tok.Time.UnixNano(), actual.Tokens[i].Time.UnixNano()) {
				t.Fatal(testWantGotMessage(tok.Time.UnixNano(), actual.Tokens[i].Time.UnixNano(), fmt.Sprintf("token time %d for %s", i, model)))
			}
			if !testEqual(tok.Count, actual.Tokens[i].Count) {
				t.Fatal(testWantGotMessage(tok.Count, actual.Tokens[i].Count, fmt.Sprintf("token count %d for %s", i, model)))
			}
		}
	}
}

func TestSQLite_StateOverwrite_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "overwrite.db")
	store, err := newSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	defer store.close()

	now := time.Now()
	if err :=

		// Save initial state.
		store.saveState(map[string]*UsageStats{
			"model-a": {
				Requests: []time.Time{now, now, now},
				DayStart: now,
				DayCount: 3,
			},
		}); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if err :=

		// Save new state (should replace).
		store.saveState(map[string]*UsageStats{
			"model-b": {
				Requests: []time.Time{now},
				DayStart: now,
				DayCount: 1,
			},
		}); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	loaded, err := store.loadState()
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	_, hasA := loaded["model-a"]
	if hasA {
		t.Fatal(testExpectedFalseMessage("model-a should have been deleted on overwrite"))
	}

	b, hasB := loaded["model-b"]
	if !hasB {
		t.Fatal(testExpectedTrueMessage("model-b should exist"))
	}
	if !testEqual(1, b.DayCount) {
		t.Fatal(testWantGotMessage(1, b.DayCount))
	}
	if !testHasLen(b.Requests, 1) {
		t.Fatal(testLenMessage(b.Requests, 1))
	}
}

func TestSQLite_EmptyState_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "empty.db")
	store, err := newSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	defer store.close()

	// Load from empty database.
	quotas, err := store.loadQuotas()
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if !testIsEmpty(quotas) {
		t.Fatal(testEmptyMessage(quotas, "should return empty quotas from fresh DB"))
	}

	state, err := store.loadState()
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if !testIsEmpty(state) {
		t.Fatal(testEmptyMessage(state, "should return empty state from fresh DB"))
	}
}

func TestSQLite_Close_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "close.db")
	store, err := newSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if err := store.close(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err, "first close should succeed"))
	}
}

// --- Phase 2: SQLite integration tests ---

func TestSQLite_NewWithSQLite_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "limiter.db")
	rl, err := NewWithSQLite(dbPath)
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	defer rl.Close()

	// Should have Gemini defaults.
	_, hasGemini := rl.Quotas["gemini-3-pro-preview"]
	if !hasGemini {
		t.Fatal(testExpectedTrueMessage("should have Gemini defaults"))
	}
	if

	// SQLite backend should be set.
	testIsNil(rl.sqlite) {
		t.Fatal(testExpectedNonNilMessage("SQLite store should be initialised"))
	}
}

func TestSQLite_NewWithSQLiteConfig_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "config.db")
	rl, err := NewWithSQLiteConfig(dbPath, Config{
		Providers: []Provider{ProviderAnthropic},
		Quotas: map[string]ModelQuota{
			"custom-model": {MaxRPM: 10, MaxTPM: 1000, MaxRPD: 50},
		},
	})
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	defer rl.Close()

	_, hasClaude := rl.Quotas["claude-opus-4"]
	if !hasClaude {
		t.Fatal(testExpectedTrueMessage("should have Anthropic models"))
	}

	_, hasCustom := rl.Quotas["custom-model"]
	if !hasCustom {
		t.Fatal(testExpectedTrueMessage("should have custom model"))
	}

	_, hasGemini := rl.Quotas["gemini-3-pro-preview"]
	if hasGemini {
		t.Fatal(testExpectedFalseMessage("should not have Gemini models"))
	}
}

func TestSQLite_PersistAndLoad_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "persist.db")
	rl, err := NewWithSQLite(dbPath)
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	model := "persist-test"
	rl.Quotas[model] = ModelQuota{MaxRPM: 50, MaxTPM: 5000, MaxRPD: 500}
	rl.RecordUsage(model, 100, 200)
	rl.RecordUsage(model, 50, 50)
	if err := rl.Persist(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if err := rl.Close(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	// Reload from same database.
	rl2, err := NewWithSQLite(dbPath)
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	defer rl2.Close()
	if err := rl2.Load(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	stats := rl2.Stats(model)
	if !testEqual(2, stats.RPM) {
		t.Fatal(testWantGotMessage(2, stats.RPM, "should have 2 requests after reload"))
	}
	if !testEqual(400, stats.TPM) {
		t.Fatal(testWantGotMessage(400, stats.TPM, "should have 100+200+50+50=400 tokens after reload"))
	}
	if !testEqual(2, stats.RPD) {
		t.Fatal(testWantGotMessage(2, stats.RPD, "should have 2 daily requests after reload"))
	}
	if !testEqual(50, stats.MaxRPM) {
		t.Fatal(testWantGotMessage(50, stats.MaxRPM, "quota should be persisted"))
	}
	if !testEqual(5000, stats.MaxTPM) {
		t.Fatal(testWantGotMessage(5000, stats.MaxTPM))
	}
	if !testEqual(500, stats.MaxRPD) {
		t.Fatal(testWantGotMessage(500, stats.MaxRPD))
	}
}

func TestSQLite_PersistMultipleModels_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "multi.db")
	rl, err := NewWithSQLiteConfig(dbPath, Config{
		Providers: []Provider{ProviderGemini, ProviderAnthropic},
	})
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	rl.RecordUsage("gemini-3-pro-preview", 500, 500)
	rl.RecordUsage("claude-opus-4", 200, 200)
	if err := rl.Persist(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if err := rl.Close(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	rl2, err := NewWithSQLiteConfig(dbPath, Config{
		Providers: []Provider{ProviderGemini, ProviderAnthropic},
	})
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	defer rl2.Close()
	if err := rl2.Load(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	gemini := rl2.Stats("gemini-3-pro-preview")
	if !testEqual(1, gemini.RPM) {
		t.Fatal(testWantGotMessage(1, gemini.RPM))
	}
	if !testEqual(1000, gemini.TPM) {
		t.Fatal(testWantGotMessage(1000, gemini.TPM))
	}

	claude := rl2.Stats("claude-opus-4")
	if !testEqual(1, claude.RPM) {
		t.Fatal(testWantGotMessage(1, claude.RPM))
	}
	if !testEqual(400, claude.TPM) {
		t.Fatal(testWantGotMessage(400, claude.TPM))
	}
}

func TestSQLite_RecordUsageThenPersistReload_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "record.db")
	rl, err := NewWithSQLite(dbPath)
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	model := "test-model"
	rl.Quotas[model] = ModelQuota{MaxRPM: 100, MaxTPM: 100000, MaxRPD: 1000}

	// Record multiple usages.
	for range 10 {
		rl.RecordUsage(model, 50, 50)
	}
	if err := rl.Persist(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	// Verify CanSend works correctly with persisted state.
	stats := rl.Stats(model)
	if !testEqual(10, stats.RPM) {
		t.Fatal(testWantGotMessage(10, stats.RPM))
	}
	if !testEqual(1000, stats.TPM) {
		t.Fatal(testWantGotMessage(1000, stats.TPM))
	} // 10 * (50+50) = 1000
	if !testEqual(10, stats.RPD) {
		t.Fatal(testWantGotMessage(10, stats.RPD))
	}
	if err := rl.Close(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	// Reload and verify.
	rl2, err := NewWithSQLite(dbPath)
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	defer rl2.Close()

	rl2.Quotas[model] = ModelQuota{MaxRPM: 100, MaxTPM: 100000, MaxRPD: 1000}
	if err := rl2.Load(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if !rl2.CanSend(model, 100) {
		t.Fatal(testExpectedTrueMessage("should be able to send after reload"))
	}

	stats2 := rl2.Stats(model)
	if !testEqual(10, stats2.RPM) {
		t.Fatal(testWantGotMessage(10, stats2.RPM, "RPM should survive reload"))
	}
	if !testEqual(1000, stats2.TPM) {
		t.Fatal(testWantGotMessage(1000, stats2.TPM, "TPM should survive reload"))
	}
}

func TestSQLite_CloseNoOp_Good(t *testing.T) {
	// Close on YAML-backed limiter is a no-op.
	rl := newTestLimiter(t)
	if err := rl.Close(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err, "Close on YAML limiter should be no-op"))
	}
}

// --- Phase 2: Concurrent SQLite ---

func TestSQLite_Concurrent_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "concurrent.db")
	rl, err := NewWithSQLite(dbPath)
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	defer rl.Close()

	model := "concurrent-sqlite"
	rl.Quotas[model] = ModelQuota{MaxRPM: 100000, MaxTPM: 1000000000, MaxRPD: 100000}

	var wg sync.WaitGroup
	goroutines := 10
	opsPerGoroutine := 20

	// Concurrent RecordUsage + CanSend + Persist (no Load, which would
	// overwrite in-memory state and lose recordings between cycles).
	for range goroutines {
		wg.Go(func() {
			for range opsPerGoroutine {
				rl.RecordUsage(model, 5, 5)
				rl.CanSend(model, 10)
				_ = rl.Persist()
			}
		})
	}

	wg.Wait()

	// All recordings should be counted.
	stats := rl.Stats(model)
	if !testEqual(goroutines*opsPerGoroutine, stats.RPD) {
		t.Fatal(testWantGotMessage(goroutines*opsPerGoroutine, stats.RPD,
			"all recordings should be counted despite concurrent operations"))
	}
	if err :=

		// Verify the final persisted state survives a reload.
		rl.Persist(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if err := rl.Close(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	rl2, err := NewWithSQLite(dbPath)
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	defer rl2.Close()

	rl2.Quotas[model] = ModelQuota{MaxRPM: 100000, MaxTPM: 1000000000, MaxRPD: 100000}
	if err := rl2.Load(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	stats2 := rl2.Stats(model)
	if !testEqual(goroutines*opsPerGoroutine, stats2.RPD) {
		t.Fatal(testWantGotMessage(goroutines*opsPerGoroutine, stats2.RPD,
			"all recordings should survive persist+reload"))
	}
}

// --- Phase 2: YAML backward compatibility ---

func TestSQLite_YAMLBackwardCompat_Good(t *testing.T) {
	// Verify that the default YAML backend still works after SQLite additions.
	tmpDir := t.TempDir()
	path := testPath(tmpDir, "compat.yaml")

	rl1, err := New()
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	rl1.filePath = path

	model := "compat-test"
	rl1.Quotas[model] = ModelQuota{MaxRPM: 50, MaxTPM: 5000, MaxRPD: 500}
	rl1.RecordUsage(model, 100, 100)
	if err := rl1.Persist(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if err := rl1.Close(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	} // No-op for YAML

	// Reload.
	rl2, err := New()
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	rl2.filePath = path
	if err := rl2.Load(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	stats := rl2.Stats(model)
	if !testEqual(1, stats.RPM) {
		t.Fatal(testWantGotMessage(1, stats.RPM))
	}
	if !testEqual(200, stats.TPM) {
		t.Fatal(testWantGotMessage(200, stats.TPM))
	}
}

func TestSQLite_ConfigBackendDefault_Good(t *testing.T) {
	// Empty Backend string should default to YAML behaviour.
	rl, err := NewWithConfig(Config{
		FilePath: testPath(t.TempDir(), "default.yaml"),
	})
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if !testIsNil(rl.sqlite) {
		t.Fatal(testExpectedNilMessage(rl.sqlite, "empty backend should use YAML (no sqlite)"))
	}
}

func TestSQLite_ConfigBackendSQLite_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "config-backend.db")
	rl, err := NewWithConfig(Config{
		Backend:  backendSQLite,
		FilePath: dbPath,
		Quotas: map[string]ModelQuota{
			"backend-model": {MaxRPM: 10, MaxTPM: 1000, MaxRPD: 50},
		},
	})
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	defer rl.Close()
	if testIsNil(rl.sqlite) {
		t.Fatal(testExpectedNonNilMessage())
	}
	rl.RecordUsage("backend-model", 10, 10)
	if err := rl.Persist(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if !pathExists(dbPath) {
		t.Fatal(testExpectedTrueMessage("sqlite backend should persist to the configured DB path"))
	}
}

func TestSQLite_ConfigBackendSQLiteDefaultPath_Good(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", "")
	t.Setenv("home", "")

	rl, err := NewWithConfig(Config{
		Backend: backendSQLite,
	})
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	defer rl.Close()
	if testIsNil(rl.sqlite) {
		t.Fatal(testExpectedNonNilMessage())
	}
	if err := rl.Persist(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if !pathExists(testPath(home, defaultStateDirName, defaultSQLiteStateFile)) {
		t.Fatal(testExpectedTrueMessage("sqlite backend should use the default home DB path"))
	}
}

// --- Phase 2: MigrateYAMLToSQLite ---

func TestSQLite_MigrateYAMLToSQLite_Good(t *testing.T) {
	tmpDir := t.TempDir()
	yamlPath := testPath(tmpDir, "state.yaml")
	sqlitePath := testPath(tmpDir, "migrated.db")

	// Create a YAML-backed limiter with state.
	rl, err := New()
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	rl.filePath = yamlPath

	model := "migrate-test"
	rl.Quotas[model] = ModelQuota{MaxRPM: 42, MaxTPM: 9999, MaxRPD: 100}
	rl.RecordUsage(model, 200, 300)
	rl.RecordUsage(model, 100, 100)
	if err := rl.Persist(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if err :=

		// Migrate.
		MigrateYAMLToSQLite(yamlPath, sqlitePath); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	// Verify by loading from SQLite.
	rl2, err := NewWithSQLite(sqlitePath)
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	defer rl2.Close()
	if err := rl2.Load(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	q, ok := rl2.Quotas[model]
	if !ok {
		t.Fatal(testExpectedTrueMessage("migrated quota should exist"))
	}
	if !testEqual(42, q.MaxRPM) {
		t.Fatal(testWantGotMessage(42, q.MaxRPM))
	}
	if !testEqual(9999, q.MaxTPM) {
		t.Fatal(testWantGotMessage(9999, q.MaxTPM))
	}
	if !testEqual(100, q.MaxRPD) {
		t.Fatal(testWantGotMessage(100, q.MaxRPD))
	}

	stats := rl2.Stats(model)
	if !testEqual(2, stats.RPM) {
		t.Fatal(testWantGotMessage(2, stats.RPM, "should have 2 requests after migration"))
	}
	if !testEqual(700, stats.TPM) {
		t.Fatal(testWantGotMessage(700, stats.TPM, "should have 200+300+100+100=700 tokens"))
	}
	if !testEqual(2, stats.RPD) {
		t.Fatal(testWantGotMessage(2, stats.RPD, "should have 2 daily requests"))
	}
}

func TestSQLite_MigrateYAMLToSQLite_Bad(t *testing.T) {
	t.Run("non-existent YAML file", func(t *testing.T) {
		err := MigrateYAMLToSQLite("/nonexistent/state.yaml", testPath(t.TempDir(), "out.db"))
		if err == nil {
			t.Fatal(testExpectedErrorMessage("should fail with non-existent YAML file"))
		}
	})

	t.Run("corrupt YAML file", func(t *testing.T) {
		tmpDir := t.TempDir()
		yamlPath := testPath(tmpDir, "corrupt.yaml")
		writeTestFile(t, yamlPath, "{{{{not yaml!")

		err := MigrateYAMLToSQLite(yamlPath, testPath(tmpDir, "out.db"))
		if err == nil {
			t.Fatal(testExpectedErrorMessage("should fail with corrupt YAML"))
		}
	})
}

func TestSQLite_MigrateYAMLToSQLiteAtomic_Good(t *testing.T) {
	tmpDir := t.TempDir()
	yamlPath := testPath(tmpDir, "atomic.yaml")
	sqlitePath := testPath(tmpDir, "atomic.db")
	now := time.Now().UTC()

	store, err := newSQLiteStore(sqlitePath)
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	originalQuotas := map[string]ModelQuota{
		"old-model": {MaxRPM: 1, MaxTPM: 2, MaxRPD: 3},
	}
	originalState := map[string]*UsageStats{
		"old-model": {
			Requests: []time.Time{now},
			Tokens:   []TokenEntry{{Time: now, Count: 9}},
			DayStart: now,
			DayCount: 1,
		},
	}
	if err := store.saveSnapshot(originalQuotas, originalState); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	_, err = store.db.Exec(`CREATE TRIGGER fail_daily_migrate BEFORE INSERT ON daily
		BEGIN SELECT RAISE(ABORT, 'forced daily failure'); END`)
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if err := store.close(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	migrated := &RateLimiter{
		Quotas: map[string]ModelQuota{
			"new-model": {MaxRPM: 10, MaxTPM: 20, MaxRPD: 30},
		},
		State: map[string]*UsageStats{
			"new-model": {
				Requests: []time.Time{now.Add(5 * time.Second)},
				Tokens:   []TokenEntry{{Time: now.Add(5 * time.Second), Count: 99}},
				DayStart: now.Add(5 * time.Second),
				DayCount: 2,
			},
		},
	}
	data, err := yaml.Marshal(migrated)
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	writeTestFile(t, yamlPath, string(data))

	err = MigrateYAMLToSQLite(yamlPath, sqlitePath)
	if err == nil {
		t.Fatal(testExpectedErrorMessage())
	}

	store, err = newSQLiteStore(sqlitePath)
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	defer store.close()

	quotas, err := store.loadQuotas()
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if !testEqual(originalQuotas, quotas) {
		t.Fatal(testWantGotMessage(originalQuotas, quotas))
	}

	state, err := store.loadState()
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if !testContains(state, "old-model") {
		t.Fatal(testContainsMessage(state, "old-model"))
	}
	if !testEqual(originalState["old-model"].DayCount, state["old-model"].DayCount) {
		t.Fatal(testWantGotMessage(originalState["old-model"].DayCount, state["old-model"].DayCount))
	}
	if !testEqual(originalState["old-model"].Tokens[0].Count, state["old-model"].Tokens[0].Count) {
		t.Fatal(testWantGotMessage(originalState["old-model"].Tokens[0].Count, state["old-model"].Tokens[0].Count))
	}
	if testContains(state, "new-model") {
		t.Fatal(testNotContainsMessage(state, "new-model"))
	}
}

func TestSQLite_MigrateYAMLToSQLitePreservesAllGeminiModels_Good(t *testing.T) {
	tmpDir := t.TempDir()
	yamlPath := testPath(tmpDir, "full.yaml")
	sqlitePath := testPath(tmpDir, "full.db")

	// Create a full YAML state with all Gemini models.
	rl, err := New()
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	rl.filePath = yamlPath

	for model := range rl.Quotas {
		rl.RecordUsage(model, 10, 10)
	}
	if err := rl.Persist(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if err := MigrateYAMLToSQLite(yamlPath, sqlitePath); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	rl2, err := NewWithSQLite(sqlitePath)
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	defer rl2.Close()
	if err := rl2.Load(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	for model := range rl.Quotas {
		q, ok := rl2.Quotas[model]
		if !ok {
			t.Fatal(testExpectedTrueMessage(fmt.Sprintf("migrated quota should exist for %s", model)))
		}
		if !testEqual(rl.Quotas[model], q) {
			t.Fatal(testWantGotMessage(rl.Quotas[model], q, fmt.Sprintf("quota values should match for %s", model)))
		}
	}
}

// --- Phase 2: Corrupt DB recovery ---

func TestSQLite_CorruptDB_Ugly(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := testPath(tmpDir, "corrupt.db")

	// Write garbage to the DB file.
	writeTestFile(t, dbPath, "THIS IS NOT A SQLITE DATABASE")

	// Opening a corrupt DB may succeed (sqlite is lazy about validation),
	// but operations on it should fail gracefully.
	store, err := newSQLiteStore(dbPath)
	if err != nil {
		if !testContains(
			// If open itself fails, that's acceptable recovery.
			err.Error(), "ratelimit") {
			t.Fatal(testContainsMessage(err.Error(), "ratelimit"))
		}
		return
	}
	defer store.close()

	// Try to load quotas -- should fail gracefully.
	_, err = store.loadQuotas()
	if err == nil {
		t.Fatal(testExpectedErrorMessage("loading from corrupt DB should return an error"))
	}
}

func TestSQLite_TruncatedDB_Ugly(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := testPath(tmpDir, "truncated.db")

	// Create a valid DB first.
	store, err := newSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if err := store.saveQuotas(map[string]ModelQuota{
		"test": {MaxRPM: 1},
	}); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if err := store.close(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	// Truncate the file to simulate corruption.
	overwriteTestFile(t, dbPath, "TRUNC")

	// Opening should either fail or operations should fail.
	store2, err := newSQLiteStore(dbPath)
	if err != nil {
		if !testContains(err.Error(), "ratelimit") {
			t.Fatal(testContainsMessage(err.Error(), "ratelimit"))
		}
		return
	}
	defer store2.close()

	_, err = store2.loadQuotas()
	if err == nil {
		t.Fatal(testExpectedErrorMessage("loading from truncated DB should return an error"))
	}
}

func TestSQLite_EmptyModelState_Good(t *testing.T) {
	// State with no requests or tokens but with a daily counter.
	dbPath := testPath(t.TempDir(), "empty-state.db")
	store, err := newSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	defer store.close()

	now := time.Now()
	state := map[string]*UsageStats{
		"empty-model": {
			DayStart: now,
			DayCount: 5,
		},
	}
	if err := store.saveState(state); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	loaded, err := store.loadState()
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	s, ok := loaded["empty-model"]
	if !ok {
		t.Fatal(testExpectedTrueMessage())
	}
	if !testEqual(5, s.DayCount) {
		t.Fatal(testWantGotMessage(5, s.DayCount))
	}
	if !testIsEmpty(s.Requests) {
		t.Fatal(testEmptyMessage(s.Requests, "should have no requests"))
	}
	if !testIsEmpty(s.Tokens) {
		t.Fatal(testEmptyMessage(s.Tokens, "should have no tokens"))
	}
}

// --- Phase 2: End-to-end with persist cycle ---

func TestSQLite_EndToEnd_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "e2e.db")

	// Session 1: Create limiter, record usage, persist.
	rl1, err := NewWithSQLiteConfig(dbPath, Config{
		Providers: []Provider{ProviderGemini, ProviderOpenAI},
	})
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	rl1.RecordUsage("gemini-3-pro-preview", 1000, 500)
	rl1.RecordUsage("gpt-4o", 200, 200)
	rl1.SetQuota("custom-local", ModelQuota{MaxRPM: 5, MaxTPM: 10000, MaxRPD: 50})
	rl1.RecordUsage("custom-local", 100, 100)
	if err := rl1.Persist(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if err := rl1.Close(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	// Session 2: Reload and verify all state.
	rl2, err := NewWithSQLiteConfig(dbPath, Config{
		Providers: []Provider{ProviderGemini, ProviderOpenAI},
	})
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	defer rl2.Close()
	if err := rl2.Load(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	// Gemini state.
	gemini := rl2.Stats("gemini-3-pro-preview")
	if !testEqual(1, gemini.RPM) {
		t.Fatal(testWantGotMessage(1, gemini.RPM))
	}
	if !testEqual(1500, gemini.TPM) {
		t.Fatal(testWantGotMessage(1500, gemini.TPM))
	}
	if !testEqual(150, gemini.MaxRPM) {
		t.Fatal(testWantGotMessage(150, gemini.MaxRPM))
	}

	// OpenAI state.
	gpt := rl2.Stats("gpt-4o")
	if !testEqual(1, gpt.RPM) {
		t.Fatal(testWantGotMessage(1, gpt.RPM))
	}
	if !testEqual(400, gpt.TPM) {
		t.Fatal(testWantGotMessage(400, gpt.TPM))
	}

	// Custom model state.
	custom := rl2.Stats("custom-local")
	if !testEqual(1, custom.RPM) {
		t.Fatal(testWantGotMessage(1, custom.RPM))
	}
	if !testEqual(200, custom.TPM) {
		t.Fatal(testWantGotMessage(200, custom.TPM))
	}
	if !testEqual(5, custom.MaxRPM) {
		t.Fatal(testWantGotMessage(5, custom.MaxRPM))
	}
}

func TestSQLite_LoadReplacesPersistedSnapshot_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "replace.db")
	rl, err := NewWithSQLiteConfig(dbPath, Config{
		Quotas: map[string]ModelQuota{
			"model-a": {MaxRPM: 1, MaxTPM: 100, MaxRPD: 10},
		},
	})
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	rl.RecordUsage("model-a", 10, 10)
	if err := rl.Persist(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	delete(rl.Quotas, "model-a")
	rl.Quotas["model-b"] = ModelQuota{MaxRPM: 2, MaxTPM: 200, MaxRPD: 20}
	rl.Reset("")
	rl.RecordUsage("model-b", 5, 5)
	if err := rl.Persist(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if err := rl.Close(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	rl2, err := NewWithSQLiteConfig(dbPath, Config{
		Providers: []Provider{ProviderGemini},
	})
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	defer rl2.Close()

	rl2.State["stale-memory"] = &UsageStats{DayStart: time.Now(), DayCount: 99}
	if err := rl2.Load(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if testContains(rl2.Quotas, "gemini-3-pro-preview") {
		t.Fatal(testNotContainsMessage(rl2.Quotas, "gemini-3-pro-preview"))
	}
	if testContains(rl2.Quotas, "model-a") {
		t.Fatal(testNotContainsMessage(rl2.Quotas, "model-a"))
	}
	if !testContains(rl2.Quotas, "model-b") {
		t.Fatal(testContainsMessage(rl2.Quotas, "model-b"))
	}
	if testContains(rl2.State, "stale-memory") {
		t.Fatal(testNotContainsMessage(rl2.State, "stale-memory"))
	}
	if testContains(rl2.State, "model-a") {
		t.Fatal(testNotContainsMessage(rl2.State, "model-a"))
	}
	if !testEqual(1, rl2.Stats("model-b").RPD) {
		t.Fatal(testWantGotMessage(1, rl2.Stats("model-b").RPD))
	}
}

func TestSQLite_PersistAtomic_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "persist-atomic.db")
	rl, err := NewWithSQLiteConfig(dbPath, Config{
		Quotas: map[string]ModelQuota{
			"old-model": {MaxRPM: 1, MaxTPM: 100, MaxRPD: 10},
		},
	})
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	rl.RecordUsage("old-model", 10, 10)
	if err := rl.Persist(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	_, err = rl.sqlite.db.Exec(`CREATE TRIGGER fail_daily_persist BEFORE INSERT ON daily
		BEGIN SELECT RAISE(ABORT, 'forced daily failure'); END`)
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	delete(rl.Quotas, "old-model")
	rl.Quotas["new-model"] = ModelQuota{MaxRPM: 2, MaxTPM: 200, MaxRPD: 20}
	rl.Reset("")
	rl.RecordUsage("new-model", 50, 50)

	err = rl.Persist()
	if err == nil {
		t.Fatal(testExpectedErrorMessage())
	}
	if err := rl.Close(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	rl2, err := NewWithSQLiteConfig(dbPath, Config{})
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	defer rl2.Close()
	if err := rl2.Load(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if !testContains(rl2.Quotas, "old-model") {
		t.Fatal(testContainsMessage(rl2.Quotas, "old-model"))
	}
	if testContains(rl2.Quotas, "new-model") {
		t.Fatal(testNotContainsMessage(rl2.Quotas, "new-model"))
	}
	if !testEqual(1, rl2.Stats("old-model").RPD) {
		t.Fatal(testWantGotMessage(1, rl2.Stats("old-model").RPD))
	}
	if !testEqual(0, rl2.Stats("new-model").RPD) {
		t.Fatal(testWantGotMessage(0, rl2.Stats("new-model").RPD))
	}
}

// --- Phase 2: Benchmark ---

func BenchmarkSQLitePersist(b *testing.B) {
	dbPath := testPath(b.TempDir(), "bench.db")
	rl, err := NewWithSQLite(dbPath)
	if err != nil {
		b.Fatal(err)
	}
	defer rl.Close()

	model := "bench-sqlite"
	rl.Quotas[model] = ModelQuota{MaxRPM: 1000, MaxTPM: 100000, MaxRPD: 10000}

	now := time.Now()
	rl.State[model] = &UsageStats{DayStart: now, DayCount: 100}
	for i := range 100 {
		t := now.Add(-time.Duration(i) * time.Second)
		rl.State[model].Requests = append(rl.State[model].Requests, t)
		rl.State[model].Tokens = append(rl.State[model].Tokens, TokenEntry{Time: t, Count: 100})
	}

	b.ResetTimer()
	for range b.N {
		_ = rl.Persist()
	}
}

func BenchmarkSQLiteLoad(b *testing.B) {
	dbPath := testPath(b.TempDir(), "bench-load.db")
	rl, err := NewWithSQLite(dbPath)
	if err != nil {
		b.Fatal(err)
	}
	defer rl.Close()

	model := "bench-sqlite-load"
	rl.Quotas[model] = ModelQuota{MaxRPM: 1000, MaxTPM: 100000, MaxRPD: 10000}

	now := time.Now()
	rl.State[model] = &UsageStats{DayStart: now, DayCount: 100}
	for i := range 100 {
		t := now.Add(-time.Duration(i) * time.Second)
		rl.State[model].Requests = append(rl.State[model].Requests, t)
		rl.State[model].Tokens = append(rl.State[model].Tokens, TokenEntry{Time: t, Count: 100})
	}
	_ = rl.Persist()

	b.ResetTimer()
	for range b.N {
		_ = rl.Load()
	}
}

// --- Phase 2: Verify YAML tests still pass (this is tested implicitly) ---
// All existing tests in ratelimit_test.go use YAML backend by default.
// The fact that they still pass proves backward compatibility.

// TestMigrateYAMLToSQLiteWithFullState tests migration of a realistic YAML
// file that contains the full serialised RateLimiter struct.
func TestSQLite_MigrateYAMLToSQLiteWithFullState_Good(t *testing.T) {
	tmpDir := t.TempDir()
	yamlPath := testPath(tmpDir, "realistic.yaml")
	sqlitePath := testPath(tmpDir, "realistic.db")

	now := time.Now()

	// Create a realistic YAML file by serialising a RateLimiter.
	rl := &RateLimiter{
		Quotas: map[string]ModelQuota{
			"gemini-3-pro-preview": {MaxRPM: 150, MaxTPM: 1000000, MaxRPD: 1000},
			"claude-opus-4":        {MaxRPM: 50, MaxTPM: 40000, MaxRPD: 0},
		},
		State: map[string]*UsageStats{
			"gemini-3-pro-preview": {
				Requests: []time.Time{now, now.Add(-10 * time.Second)},
				Tokens: []TokenEntry{
					{Time: now, Count: 500},
					{Time: now.Add(-10 * time.Second), Count: 300},
				},
				DayStart: now.Add(-2 * time.Hour),
				DayCount: 25,
			},
			"claude-opus-4": {
				Requests: []time.Time{now.Add(-5 * time.Second)},
				Tokens: []TokenEntry{
					{Time: now.Add(-5 * time.Second), Count: 1000},
				},
				DayStart: now.Add(-30 * time.Minute),
				DayCount: 3,
			},
		},
	}

	data, err := yaml.Marshal(rl)
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	writeTestFile(t, yamlPath, string(data))
	if err :=

		// Migrate.
		MigrateYAMLToSQLite(yamlPath, sqlitePath); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	// Verify.
	rl2, err := NewWithSQLiteConfig(sqlitePath, Config{})
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	defer rl2.Close()
	if err := rl2.Load(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	gemini := rl2.Stats("gemini-3-pro-preview")
	if !testEqual(2, gemini.RPM) {
		t.Fatal(testWantGotMessage(2, gemini.RPM))
	}
	if !testEqual(800, gemini.TPM) {
		t.Fatal(testWantGotMessage(800, gemini.TPM))
	} // 500 + 300
	if !testEqual(25, gemini.RPD) {
		t.Fatal(testWantGotMessage(25, gemini.RPD))
	}
	if !testEqual(150, gemini.MaxRPM) {
		t.Fatal(testWantGotMessage(150, gemini.MaxRPM))
	}

	claude := rl2.Stats("claude-opus-4")
	if !testEqual(1, claude.RPM) {
		t.Fatal(testWantGotMessage(1, claude.RPM))
	}
	if !testEqual(1000, claude.TPM) {
		t.Fatal(testWantGotMessage(1000, claude.TPM))
	}
	if !testEqual(3, claude.RPD) {
		t.Fatal(testWantGotMessage(3, claude.RPD))
	}
	if !testEqual(50, claude.MaxRPM) {
		t.Fatal(testWantGotMessage(50, claude.MaxRPM))
	}
}
