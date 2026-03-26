package ratelimit

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// --- Phase 2: SQLite basic tests ---

func TestSQLite_NewSQLiteStore_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "test.db")
	store, err := newSQLiteStore(dbPath)
	require.NoError(t, err)
	defer store.close()

	// Verify the database file was created.
	assert.True(t, pathExists(dbPath), "database file should exist")
}

func TestSQLite_NewSQLiteStore_Bad(t *testing.T) {
	t.Run("invalid path returns error", func(t *testing.T) {
		// Path inside a non-existent directory with no parent.
		_, err := newSQLiteStore("/nonexistent/deep/nested/dir/test.db")
		assert.Error(t, err, "should fail with invalid path")
	})
}

func TestSQLite_QuotasRoundTrip_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "quotas.db")
	store, err := newSQLiteStore(dbPath)
	require.NoError(t, err)
	defer store.close()

	quotas := map[string]ModelQuota{
		"model-a": {MaxRPM: 100, MaxTPM: 50000, MaxRPD: 1000},
		"model-b": {MaxRPM: 200, MaxTPM: 100000, MaxRPD: 2000},
		"model-c": {MaxRPM: 0, MaxTPM: 0, MaxRPD: 0}, // Unlimited
	}

	require.NoError(t, store.saveQuotas(quotas))

	loaded, err := store.loadQuotas()
	require.NoError(t, err)

	assert.Equal(t, len(quotas), len(loaded), "should load same number of quotas")
	for model, expected := range quotas {
		actual, ok := loaded[model]
		require.True(t, ok, "loaded quotas should contain %s", model)
		assert.Equal(t, expected.MaxRPM, actual.MaxRPM)
		assert.Equal(t, expected.MaxTPM, actual.MaxTPM)
		assert.Equal(t, expected.MaxRPD, actual.MaxRPD)
	}
}

func TestSQLite_QuotasUpsert_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "upsert.db")
	store, err := newSQLiteStore(dbPath)
	require.NoError(t, err)
	defer store.close()

	// Save initial quotas.
	require.NoError(t, store.saveQuotas(map[string]ModelQuota{
		"model-a": {MaxRPM: 100, MaxTPM: 50000, MaxRPD: 1000},
	}))

	// Upsert with updated values.
	require.NoError(t, store.saveQuotas(map[string]ModelQuota{
		"model-a": {MaxRPM: 999, MaxTPM: 888, MaxRPD: 777},
	}))

	loaded, err := store.loadQuotas()
	require.NoError(t, err)

	q := loaded["model-a"]
	assert.Equal(t, 999, q.MaxRPM, "should have updated RPM")
	assert.Equal(t, 888, q.MaxTPM, "should have updated TPM")
	assert.Equal(t, 777, q.MaxRPD, "should have updated RPD")
}

func TestSQLite_StateRoundTrip_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "state.db")
	store, err := newSQLiteStore(dbPath)
	require.NoError(t, err)
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

	require.NoError(t, store.saveState(state))

	loaded, err := store.loadState()
	require.NoError(t, err)

	assert.Equal(t, len(state), len(loaded), "should load same number of models")

	for model, expected := range state {
		actual, ok := loaded[model]
		require.True(t, ok, "loaded state should contain %s", model)

		assert.Len(t, actual.Requests, len(expected.Requests), "request count for %s", model)
		assert.Len(t, actual.Tokens, len(expected.Tokens), "token count for %s", model)
		assert.Equal(t, expected.DayCount, actual.DayCount, "day count for %s", model)

		// Time comparison with nanosecond precision (UnixNano round-trip).
		assert.Equal(t, expected.DayStart.UnixNano(), actual.DayStart.UnixNano(), "day start for %s", model)

		for i, req := range expected.Requests {
			assert.Equal(t, req.UnixNano(), actual.Requests[i].UnixNano(), "request %d for %s", i, model)
		}
		for i, tok := range expected.Tokens {
			assert.Equal(t, tok.Time.UnixNano(), actual.Tokens[i].Time.UnixNano(), "token time %d for %s", i, model)
			assert.Equal(t, tok.Count, actual.Tokens[i].Count, "token count %d for %s", i, model)
		}
	}
}

func TestSQLite_StateOverwrite_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "overwrite.db")
	store, err := newSQLiteStore(dbPath)
	require.NoError(t, err)
	defer store.close()

	now := time.Now()

	// Save initial state.
	require.NoError(t, store.saveState(map[string]*UsageStats{
		"model-a": {
			Requests: []time.Time{now, now, now},
			DayStart: now,
			DayCount: 3,
		},
	}))

	// Save new state (should replace).
	require.NoError(t, store.saveState(map[string]*UsageStats{
		"model-b": {
			Requests: []time.Time{now},
			DayStart: now,
			DayCount: 1,
		},
	}))

	loaded, err := store.loadState()
	require.NoError(t, err)

	_, hasA := loaded["model-a"]
	assert.False(t, hasA, "model-a should have been deleted on overwrite")

	b, hasB := loaded["model-b"]
	require.True(t, hasB, "model-b should exist")
	assert.Equal(t, 1, b.DayCount)
	assert.Len(t, b.Requests, 1)
}

func TestSQLite_EmptyState_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "empty.db")
	store, err := newSQLiteStore(dbPath)
	require.NoError(t, err)
	defer store.close()

	// Load from empty database.
	quotas, err := store.loadQuotas()
	require.NoError(t, err)
	assert.Empty(t, quotas, "should return empty quotas from fresh DB")

	state, err := store.loadState()
	require.NoError(t, err)
	assert.Empty(t, state, "should return empty state from fresh DB")
}

func TestSQLite_Close_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "close.db")
	store, err := newSQLiteStore(dbPath)
	require.NoError(t, err)

	require.NoError(t, store.close(), "first close should succeed")
}

// --- Phase 2: SQLite integration tests ---

func TestSQLite_NewWithSQLite_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "limiter.db")
	rl, err := NewWithSQLite(dbPath)
	require.NoError(t, err)
	defer rl.Close()

	// Should have Gemini defaults.
	_, hasGemini := rl.Quotas["gemini-3-pro-preview"]
	assert.True(t, hasGemini, "should have Gemini defaults")

	// SQLite backend should be set.
	assert.NotNil(t, rl.sqlite, "SQLite store should be initialised")
}

func TestSQLite_NewWithSQLiteConfig_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "config.db")
	rl, err := NewWithSQLiteConfig(dbPath, Config{
		Providers: []Provider{ProviderAnthropic},
		Quotas: map[string]ModelQuota{
			"custom-model": {MaxRPM: 10, MaxTPM: 1000, MaxRPD: 50},
		},
	})
	require.NoError(t, err)
	defer rl.Close()

	_, hasClaude := rl.Quotas["claude-opus-4"]
	assert.True(t, hasClaude, "should have Anthropic models")

	_, hasCustom := rl.Quotas["custom-model"]
	assert.True(t, hasCustom, "should have custom model")

	_, hasGemini := rl.Quotas["gemini-3-pro-preview"]
	assert.False(t, hasGemini, "should not have Gemini models")
}

func TestSQLite_PersistAndLoad_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "persist.db")
	rl, err := NewWithSQLite(dbPath)
	require.NoError(t, err)

	model := "persist-test"
	rl.Quotas[model] = ModelQuota{MaxRPM: 50, MaxTPM: 5000, MaxRPD: 500}
	rl.RecordUsage(model, 100, 200)
	rl.RecordUsage(model, 50, 50)

	require.NoError(t, rl.Persist())
	require.NoError(t, rl.Close())

	// Reload from same database.
	rl2, err := NewWithSQLite(dbPath)
	require.NoError(t, err)
	defer rl2.Close()

	require.NoError(t, rl2.Load())

	stats := rl2.Stats(model)
	assert.Equal(t, 2, stats.RPM, "should have 2 requests after reload")
	assert.Equal(t, 400, stats.TPM, "should have 100+200+50+50=400 tokens after reload")
	assert.Equal(t, 2, stats.RPD, "should have 2 daily requests after reload")
	assert.Equal(t, 50, stats.MaxRPM, "quota should be persisted")
	assert.Equal(t, 5000, stats.MaxTPM)
	assert.Equal(t, 500, stats.MaxRPD)
}

func TestSQLite_PersistMultipleModels_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "multi.db")
	rl, err := NewWithSQLiteConfig(dbPath, Config{
		Providers: []Provider{ProviderGemini, ProviderAnthropic},
	})
	require.NoError(t, err)

	rl.RecordUsage("gemini-3-pro-preview", 500, 500)
	rl.RecordUsage("claude-opus-4", 200, 200)

	require.NoError(t, rl.Persist())
	require.NoError(t, rl.Close())

	rl2, err := NewWithSQLiteConfig(dbPath, Config{
		Providers: []Provider{ProviderGemini, ProviderAnthropic},
	})
	require.NoError(t, err)
	defer rl2.Close()

	require.NoError(t, rl2.Load())

	gemini := rl2.Stats("gemini-3-pro-preview")
	assert.Equal(t, 1, gemini.RPM)
	assert.Equal(t, 1000, gemini.TPM)

	claude := rl2.Stats("claude-opus-4")
	assert.Equal(t, 1, claude.RPM)
	assert.Equal(t, 400, claude.TPM)
}

func TestSQLite_RecordUsageThenPersistReload_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "record.db")
	rl, err := NewWithSQLite(dbPath)
	require.NoError(t, err)

	model := "test-model"
	rl.Quotas[model] = ModelQuota{MaxRPM: 100, MaxTPM: 100000, MaxRPD: 1000}

	// Record multiple usages.
	for range 10 {
		rl.RecordUsage(model, 50, 50)
	}

	require.NoError(t, rl.Persist())

	// Verify CanSend works correctly with persisted state.
	stats := rl.Stats(model)
	assert.Equal(t, 10, stats.RPM)
	assert.Equal(t, 1000, stats.TPM) // 10 * (50+50) = 1000
	assert.Equal(t, 10, stats.RPD)

	require.NoError(t, rl.Close())

	// Reload and verify.
	rl2, err := NewWithSQLite(dbPath)
	require.NoError(t, err)
	defer rl2.Close()

	rl2.Quotas[model] = ModelQuota{MaxRPM: 100, MaxTPM: 100000, MaxRPD: 1000}
	require.NoError(t, rl2.Load())

	assert.True(t, rl2.CanSend(model, 100), "should be able to send after reload")

	stats2 := rl2.Stats(model)
	assert.Equal(t, 10, stats2.RPM, "RPM should survive reload")
	assert.Equal(t, 1000, stats2.TPM, "TPM should survive reload")
}

func TestSQLite_CloseNoOp_Good(t *testing.T) {
	// Close on YAML-backed limiter is a no-op.
	rl := newTestLimiter(t)
	assert.NoError(t, rl.Close(), "Close on YAML limiter should be no-op")
}

// --- Phase 2: Concurrent SQLite ---

func TestSQLite_Concurrent_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "concurrent.db")
	rl, err := NewWithSQLite(dbPath)
	require.NoError(t, err)
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
	assert.Equal(t, goroutines*opsPerGoroutine, stats.RPD,
		"all recordings should be counted despite concurrent operations")

	// Verify the final persisted state survives a reload.
	require.NoError(t, rl.Persist())
	require.NoError(t, rl.Close())

	rl2, err := NewWithSQLite(dbPath)
	require.NoError(t, err)
	defer rl2.Close()

	rl2.Quotas[model] = ModelQuota{MaxRPM: 100000, MaxTPM: 1000000000, MaxRPD: 100000}
	require.NoError(t, rl2.Load())

	stats2 := rl2.Stats(model)
	assert.Equal(t, goroutines*opsPerGoroutine, stats2.RPD,
		"all recordings should survive persist+reload")
}

// --- Phase 2: YAML backward compatibility ---

func TestSQLite_YAMLBackwardCompat_Good(t *testing.T) {
	// Verify that the default YAML backend still works after SQLite additions.
	tmpDir := t.TempDir()
	path := testPath(tmpDir, "compat.yaml")

	rl1, err := New()
	require.NoError(t, err)
	rl1.filePath = path

	model := "compat-test"
	rl1.Quotas[model] = ModelQuota{MaxRPM: 50, MaxTPM: 5000, MaxRPD: 500}
	rl1.RecordUsage(model, 100, 100)

	require.NoError(t, rl1.Persist())
	require.NoError(t, rl1.Close()) // No-op for YAML

	// Reload.
	rl2, err := New()
	require.NoError(t, err)
	rl2.filePath = path
	require.NoError(t, rl2.Load())

	stats := rl2.Stats(model)
	assert.Equal(t, 1, stats.RPM)
	assert.Equal(t, 200, stats.TPM)
}

func TestSQLite_ConfigBackendDefault_Good(t *testing.T) {
	// Empty Backend string should default to YAML behaviour.
	rl, err := NewWithConfig(Config{
		FilePath: testPath(t.TempDir(), "default.yaml"),
	})
	require.NoError(t, err)

	assert.Nil(t, rl.sqlite, "empty backend should use YAML (no sqlite)")
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
	require.NoError(t, err)
	defer rl.Close()

	require.NotNil(t, rl.sqlite)
	rl.RecordUsage("backend-model", 10, 10)
	require.NoError(t, rl.Persist())

	assert.True(t, pathExists(dbPath), "sqlite backend should persist to the configured DB path")
}

func TestSQLite_ConfigBackendSQLiteDefaultPath_Good(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", "")
	t.Setenv("home", "")

	rl, err := NewWithConfig(Config{
		Backend: backendSQLite,
	})
	require.NoError(t, err)
	defer rl.Close()

	require.NotNil(t, rl.sqlite)
	require.NoError(t, rl.Persist())

	assert.True(t, pathExists(testPath(home, defaultStateDirName, defaultSQLiteStateFile)), "sqlite backend should use the default home DB path")
}

// --- Phase 2: MigrateYAMLToSQLite ---

func TestSQLite_MigrateYAMLToSQLite_Good(t *testing.T) {
	tmpDir := t.TempDir()
	yamlPath := testPath(tmpDir, "state.yaml")
	sqlitePath := testPath(tmpDir, "migrated.db")

	// Create a YAML-backed limiter with state.
	rl, err := New()
	require.NoError(t, err)
	rl.filePath = yamlPath

	model := "migrate-test"
	rl.Quotas[model] = ModelQuota{MaxRPM: 42, MaxTPM: 9999, MaxRPD: 100}
	rl.RecordUsage(model, 200, 300)
	rl.RecordUsage(model, 100, 100)

	require.NoError(t, rl.Persist())

	// Migrate.
	require.NoError(t, MigrateYAMLToSQLite(yamlPath, sqlitePath))

	// Verify by loading from SQLite.
	rl2, err := NewWithSQLite(sqlitePath)
	require.NoError(t, err)
	defer rl2.Close()

	require.NoError(t, rl2.Load())

	q, ok := rl2.Quotas[model]
	require.True(t, ok, "migrated quota should exist")
	assert.Equal(t, 42, q.MaxRPM)
	assert.Equal(t, 9999, q.MaxTPM)
	assert.Equal(t, 100, q.MaxRPD)

	stats := rl2.Stats(model)
	assert.Equal(t, 2, stats.RPM, "should have 2 requests after migration")
	assert.Equal(t, 700, stats.TPM, "should have 200+300+100+100=700 tokens")
	assert.Equal(t, 2, stats.RPD, "should have 2 daily requests")
}

func TestSQLite_MigrateYAMLToSQLite_Bad(t *testing.T) {
	t.Run("non-existent YAML file", func(t *testing.T) {
		err := MigrateYAMLToSQLite("/nonexistent/state.yaml", testPath(t.TempDir(), "out.db"))
		assert.Error(t, err, "should fail with non-existent YAML file")
	})

	t.Run("corrupt YAML file", func(t *testing.T) {
		tmpDir := t.TempDir()
		yamlPath := testPath(tmpDir, "corrupt.yaml")
		writeTestFile(t, yamlPath, "{{{{not yaml!")

		err := MigrateYAMLToSQLite(yamlPath, testPath(tmpDir, "out.db"))
		assert.Error(t, err, "should fail with corrupt YAML")
	})
}

func TestSQLite_MigrateYAMLToSQLiteAtomic_Good(t *testing.T) {
	tmpDir := t.TempDir()
	yamlPath := testPath(tmpDir, "atomic.yaml")
	sqlitePath := testPath(tmpDir, "atomic.db")
	now := time.Now().UTC()

	store, err := newSQLiteStore(sqlitePath)
	require.NoError(t, err)

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
	require.NoError(t, store.saveSnapshot(originalQuotas, originalState))

	_, err = store.db.Exec(`CREATE TRIGGER fail_daily_migrate BEFORE INSERT ON daily
		BEGIN SELECT RAISE(ABORT, 'forced daily failure'); END`)
	require.NoError(t, err)
	require.NoError(t, store.close())

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
	require.NoError(t, err)
	writeTestFile(t, yamlPath, string(data))

	err = MigrateYAMLToSQLite(yamlPath, sqlitePath)
	require.Error(t, err)

	store, err = newSQLiteStore(sqlitePath)
	require.NoError(t, err)
	defer store.close()

	quotas, err := store.loadQuotas()
	require.NoError(t, err)
	assert.Equal(t, originalQuotas, quotas)

	state, err := store.loadState()
	require.NoError(t, err)
	require.Contains(t, state, "old-model")
	assert.Equal(t, originalState["old-model"].DayCount, state["old-model"].DayCount)
	assert.Equal(t, originalState["old-model"].Tokens[0].Count, state["old-model"].Tokens[0].Count)
	assert.NotContains(t, state, "new-model")
}

func TestSQLite_MigrateYAMLToSQLitePreservesAllGeminiModels_Good(t *testing.T) {
	tmpDir := t.TempDir()
	yamlPath := testPath(tmpDir, "full.yaml")
	sqlitePath := testPath(tmpDir, "full.db")

	// Create a full YAML state with all Gemini models.
	rl, err := New()
	require.NoError(t, err)
	rl.filePath = yamlPath

	for model := range rl.Quotas {
		rl.RecordUsage(model, 10, 10)
	}

	require.NoError(t, rl.Persist())
	require.NoError(t, MigrateYAMLToSQLite(yamlPath, sqlitePath))

	rl2, err := NewWithSQLite(sqlitePath)
	require.NoError(t, err)
	defer rl2.Close()

	require.NoError(t, rl2.Load())

	for model := range rl.Quotas {
		q, ok := rl2.Quotas[model]
		require.True(t, ok, "migrated quota should exist for %s", model)
		assert.Equal(t, rl.Quotas[model], q, "quota values should match for %s", model)
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
		// If open itself fails, that's acceptable recovery.
		assert.Contains(t, err.Error(), "ratelimit")
		return
	}
	defer store.close()

	// Try to load quotas -- should fail gracefully.
	_, err = store.loadQuotas()
	assert.Error(t, err, "loading from corrupt DB should return an error")
}

func TestSQLite_TruncatedDB_Ugly(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := testPath(tmpDir, "truncated.db")

	// Create a valid DB first.
	store, err := newSQLiteStore(dbPath)
	require.NoError(t, err)
	require.NoError(t, store.saveQuotas(map[string]ModelQuota{
		"test": {MaxRPM: 1},
	}))
	require.NoError(t, store.close())

	// Truncate the file to simulate corruption.
	overwriteTestFile(t, dbPath, "TRUNC")

	// Opening should either fail or operations should fail.
	store2, err := newSQLiteStore(dbPath)
	if err != nil {
		assert.Contains(t, err.Error(), "ratelimit")
		return
	}
	defer store2.close()

	_, err = store2.loadQuotas()
	assert.Error(t, err, "loading from truncated DB should return an error")
}

func TestSQLite_EmptyModelState_Good(t *testing.T) {
	// State with no requests or tokens but with a daily counter.
	dbPath := testPath(t.TempDir(), "empty-state.db")
	store, err := newSQLiteStore(dbPath)
	require.NoError(t, err)
	defer store.close()

	now := time.Now()
	state := map[string]*UsageStats{
		"empty-model": {
			DayStart: now,
			DayCount: 5,
		},
	}

	require.NoError(t, store.saveState(state))

	loaded, err := store.loadState()
	require.NoError(t, err)

	s, ok := loaded["empty-model"]
	require.True(t, ok)
	assert.Equal(t, 5, s.DayCount)
	assert.Empty(t, s.Requests, "should have no requests")
	assert.Empty(t, s.Tokens, "should have no tokens")
}

// --- Phase 2: End-to-end with persist cycle ---

func TestSQLite_EndToEnd_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "e2e.db")

	// Session 1: Create limiter, record usage, persist.
	rl1, err := NewWithSQLiteConfig(dbPath, Config{
		Providers: []Provider{ProviderGemini, ProviderOpenAI},
	})
	require.NoError(t, err)

	rl1.RecordUsage("gemini-3-pro-preview", 1000, 500)
	rl1.RecordUsage("gpt-4o", 200, 200)
	rl1.SetQuota("custom-local", ModelQuota{MaxRPM: 5, MaxTPM: 10000, MaxRPD: 50})
	rl1.RecordUsage("custom-local", 100, 100)

	require.NoError(t, rl1.Persist())
	require.NoError(t, rl1.Close())

	// Session 2: Reload and verify all state.
	rl2, err := NewWithSQLiteConfig(dbPath, Config{
		Providers: []Provider{ProviderGemini, ProviderOpenAI},
	})
	require.NoError(t, err)
	defer rl2.Close()

	require.NoError(t, rl2.Load())

	// Gemini state.
	gemini := rl2.Stats("gemini-3-pro-preview")
	assert.Equal(t, 1, gemini.RPM)
	assert.Equal(t, 1500, gemini.TPM)
	assert.Equal(t, 150, gemini.MaxRPM)

	// OpenAI state.
	gpt := rl2.Stats("gpt-4o")
	assert.Equal(t, 1, gpt.RPM)
	assert.Equal(t, 400, gpt.TPM)

	// Custom model state.
	custom := rl2.Stats("custom-local")
	assert.Equal(t, 1, custom.RPM)
	assert.Equal(t, 200, custom.TPM)
	assert.Equal(t, 5, custom.MaxRPM)
}

func TestSQLite_LoadReplacesPersistedSnapshot_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "replace.db")
	rl, err := NewWithSQLiteConfig(dbPath, Config{
		Quotas: map[string]ModelQuota{
			"model-a": {MaxRPM: 1, MaxTPM: 100, MaxRPD: 10},
		},
	})
	require.NoError(t, err)

	rl.RecordUsage("model-a", 10, 10)
	require.NoError(t, rl.Persist())

	delete(rl.Quotas, "model-a")
	rl.Quotas["model-b"] = ModelQuota{MaxRPM: 2, MaxTPM: 200, MaxRPD: 20}
	rl.Reset("")
	rl.RecordUsage("model-b", 5, 5)
	require.NoError(t, rl.Persist())
	require.NoError(t, rl.Close())

	rl2, err := NewWithSQLiteConfig(dbPath, Config{
		Providers: []Provider{ProviderGemini},
	})
	require.NoError(t, err)
	defer rl2.Close()

	rl2.State["stale-memory"] = &UsageStats{DayStart: time.Now(), DayCount: 99}
	require.NoError(t, rl2.Load())

	assert.NotContains(t, rl2.Quotas, "gemini-3-pro-preview")
	assert.NotContains(t, rl2.Quotas, "model-a")
	assert.Contains(t, rl2.Quotas, "model-b")
	assert.NotContains(t, rl2.State, "stale-memory")
	assert.NotContains(t, rl2.State, "model-a")
	assert.Equal(t, 1, rl2.Stats("model-b").RPD)
}

func TestSQLite_PersistAtomic_Good(t *testing.T) {
	dbPath := testPath(t.TempDir(), "persist-atomic.db")
	rl, err := NewWithSQLiteConfig(dbPath, Config{
		Quotas: map[string]ModelQuota{
			"old-model": {MaxRPM: 1, MaxTPM: 100, MaxRPD: 10},
		},
	})
	require.NoError(t, err)

	rl.RecordUsage("old-model", 10, 10)
	require.NoError(t, rl.Persist())

	_, err = rl.sqlite.db.Exec(`CREATE TRIGGER fail_daily_persist BEFORE INSERT ON daily
		BEGIN SELECT RAISE(ABORT, 'forced daily failure'); END`)
	require.NoError(t, err)

	delete(rl.Quotas, "old-model")
	rl.Quotas["new-model"] = ModelQuota{MaxRPM: 2, MaxTPM: 200, MaxRPD: 20}
	rl.Reset("")
	rl.RecordUsage("new-model", 50, 50)

	err = rl.Persist()
	require.Error(t, err)
	require.NoError(t, rl.Close())

	rl2, err := NewWithSQLiteConfig(dbPath, Config{})
	require.NoError(t, err)
	defer rl2.Close()
	require.NoError(t, rl2.Load())

	assert.Contains(t, rl2.Quotas, "old-model")
	assert.NotContains(t, rl2.Quotas, "new-model")
	assert.Equal(t, 1, rl2.Stats("old-model").RPD)
	assert.Equal(t, 0, rl2.Stats("new-model").RPD)
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
	require.NoError(t, err)
	writeTestFile(t, yamlPath, string(data))

	// Migrate.
	require.NoError(t, MigrateYAMLToSQLite(yamlPath, sqlitePath))

	// Verify.
	rl2, err := NewWithSQLiteConfig(sqlitePath, Config{})
	require.NoError(t, err)
	defer rl2.Close()
	require.NoError(t, rl2.Load())

	gemini := rl2.Stats("gemini-3-pro-preview")
	assert.Equal(t, 2, gemini.RPM)
	assert.Equal(t, 800, gemini.TPM) // 500 + 300
	assert.Equal(t, 25, gemini.RPD)
	assert.Equal(t, 150, gemini.MaxRPM)

	claude := rl2.Stats("claude-opus-4")
	assert.Equal(t, 1, claude.RPM)
	assert.Equal(t, 1000, claude.TPM)
	assert.Equal(t, 3, claude.RPD)
	assert.Equal(t, 50, claude.MaxRPM)
}
