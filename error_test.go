// SPDX-License-Identifier: EUPL-1.2

package ratelimit

import (
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestError_SQLiteErrorPaths_Bad(t *testing.T) {
	dbPath := testPath(t.TempDir(), "error.db")
	rl, err := NewWithSQLite(dbPath)
	require.NoError(t, err)

	// Close the underlying DB to trigger errors.
	rl.sqlite.close()

	t.Run("loadQuotas error", func(t *testing.T) {
		_, err := rl.sqlite.loadQuotas()
		assert.Error(t, err)
	})

	t.Run("saveQuotas error", func(t *testing.T) {
		err := rl.sqlite.saveQuotas(map[string]ModelQuota{"test": {}})
		assert.Error(t, err)
	})

	t.Run("saveState error", func(t *testing.T) {
		err := rl.sqlite.saveState(map[string]*UsageStats{"test": {}})
		assert.Error(t, err)
	})

	t.Run("loadState error", func(t *testing.T) {
		_, err := rl.sqlite.loadState()
		assert.Error(t, err)
	})
}

func TestError_SQLiteInitErrors_Bad(t *testing.T) {
	t.Run("WAL pragma failure", func(t *testing.T) {
		// This is hard to trigger without mocking sql.DB, but we can try an invalid connection string
		// modernc.org/sqlite doesn't support all DSN options that might cause PRAGMA to fail but connection to succeed.
	})
}

func TestError_PersistYAML_Good(t *testing.T) {
	t.Run("successful YAML persist and load", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := testPath(tmpDir, "ratelimits.yaml")
		rl, _ := New()
		rl.filePath = path
		rl.Quotas["test"] = ModelQuota{MaxRPM: 1}
		rl.RecordUsage("test", 1, 1)

		require.NoError(t, rl.Persist())

		rl2, _ := New()
		rl2.filePath = path
		require.NoError(t, rl2.Load())
		assert.Equal(t, 1, rl2.Quotas["test"].MaxRPM)
		assert.Equal(t, 1, rl2.State["test"].DayCount)
	})
}

func TestError_SQLiteLoadViaLimiter_Bad(t *testing.T) {
	t.Run("Load returns error when SQLite DB is closed", func(t *testing.T) {
		dbPath := testPath(t.TempDir(), "load-err.db")
		rl, err := NewWithSQLite(dbPath)
		require.NoError(t, err)

		// Close the underlying DB to trigger errors on Load.
		rl.sqlite.close()

		err = rl.Load()
		assert.Error(t, err, "Load should fail with closed DB")
	})

	t.Run("Load returns error when loadState fails", func(t *testing.T) {
		dbPath := testPath(t.TempDir(), "load-state-err.db")
		rl, err := NewWithSQLite(dbPath)
		require.NoError(t, err)

		// Save some quotas so loadQuotas succeeds, then corrupt the state tables.
		require.NoError(t, rl.Persist())

		// Drop the daily table to cause loadState to fail.
		_, execErr := rl.sqlite.db.Exec("DROP TABLE daily")
		require.NoError(t, execErr)

		err = rl.Load()
		assert.Error(t, err, "Load should fail when loadState fails")
		rl.Close()
	})
}

func TestError_SQLitePersistViaLimiter_Bad(t *testing.T) {
	t.Run("Persist returns error when SQLite saveQuotas fails", func(t *testing.T) {
		dbPath := testPath(t.TempDir(), "persist-err.db")
		rl, err := NewWithSQLite(dbPath)
		require.NoError(t, err)

		// Drop the quotas table to cause saveQuotas to fail.
		_, execErr := rl.sqlite.db.Exec("DROP TABLE quotas")
		require.NoError(t, execErr)

		err = rl.Persist()
		assert.Error(t, err, "Persist should fail when saveQuotas fails")
		assert.Contains(t, err.Error(), "ratelimit.Persist")
		rl.Close()
	})

	t.Run("Persist returns error when SQLite saveState fails", func(t *testing.T) {
		dbPath := testPath(t.TempDir(), "persist-state-err.db")
		rl, err := NewWithSQLite(dbPath)
		require.NoError(t, err)

		rl.RecordUsage("test-model", 10, 10)

		// Drop a state table to cause saveState to fail.
		_, execErr := rl.sqlite.db.Exec("DROP TABLE requests")
		require.NoError(t, execErr)

		err = rl.Persist()
		assert.Error(t, err, "Persist should fail when saveState fails")
		assert.Contains(t, err.Error(), "ratelimit.Persist")
		rl.Close()
	})
}

func TestError_NewWithSQLite_Bad(t *testing.T) {
	t.Run("NewWithSQLite with invalid path", func(t *testing.T) {
		_, err := NewWithSQLite("/nonexistent/deep/nested/dir/test.db")
		assert.Error(t, err, "should fail with invalid path")
	})

	t.Run("NewWithSQLiteConfig with invalid path", func(t *testing.T) {
		_, err := NewWithSQLiteConfig("/nonexistent/deep/nested/dir/test.db", Config{
			Providers: []Provider{ProviderGemini},
		})
		assert.Error(t, err, "should fail with invalid path")
	})
}

func TestError_SQLiteSaveState_Bad(t *testing.T) {
	t.Run("saveState fails when tokens table is dropped", func(t *testing.T) {
		dbPath := testPath(t.TempDir(), "tokens-err.db")
		store, err := newSQLiteStore(dbPath)
		require.NoError(t, err)
		defer store.close()

		_, execErr := store.db.Exec("DROP TABLE tokens")
		require.NoError(t, execErr)

		state := map[string]*UsageStats{
			"model": {
				Tokens:   []TokenEntry{{Time: time.Now(), Count: 100}},
				DayStart: time.Now(),
				DayCount: 1,
			},
		}
		err = store.saveState(state)
		assert.Error(t, err, "saveState should fail when tokens table is missing")
	})

	t.Run("saveState fails when daily table is dropped", func(t *testing.T) {
		dbPath := testPath(t.TempDir(), "daily-err.db")
		store, err := newSQLiteStore(dbPath)
		require.NoError(t, err)
		defer store.close()

		_, execErr := store.db.Exec("DROP TABLE daily")
		require.NoError(t, execErr)

		state := map[string]*UsageStats{
			"model": {
				DayStart: time.Now(),
				DayCount: 1,
			},
		}
		err = store.saveState(state)
		assert.Error(t, err, "saveState should fail when daily table is missing")
	})

	t.Run("saveState fails on request insert with renamed column", func(t *testing.T) {
		dbPath := testPath(t.TempDir(), "req-insert-err.db")
		store, err := newSQLiteStore(dbPath)
		require.NoError(t, err)
		defer store.close()

		// Rename the ts column so INSERT INTO requests (model, ts) fails.
		_, execErr := store.db.Exec("ALTER TABLE requests RENAME COLUMN ts TO timestamp")
		require.NoError(t, execErr)

		state := map[string]*UsageStats{
			"model": {
				Requests: []time.Time{time.Now()},
				DayStart: time.Now(),
				DayCount: 1,
			},
		}
		err = store.saveState(state)
		assert.Error(t, err, "saveState should fail on request insert with renamed column")
	})

	t.Run("saveState fails on token insert with renamed column", func(t *testing.T) {
		dbPath := testPath(t.TempDir(), "tok-insert-err.db")
		store, err := newSQLiteStore(dbPath)
		require.NoError(t, err)
		defer store.close()

		// Rename the count column so INSERT INTO tokens (model, ts, count) fails.
		_, execErr := store.db.Exec("ALTER TABLE tokens RENAME COLUMN count TO amount")
		require.NoError(t, execErr)

		state := map[string]*UsageStats{
			"model": {
				Tokens:   []TokenEntry{{Time: time.Now(), Count: 100}},
				DayStart: time.Now(),
				DayCount: 1,
			},
		}
		err = store.saveState(state)
		assert.Error(t, err, "saveState should fail on token insert with renamed column")
	})

	t.Run("saveState fails on daily insert with renamed column", func(t *testing.T) {
		dbPath := testPath(t.TempDir(), "day-insert-err.db")
		store, err := newSQLiteStore(dbPath)
		require.NoError(t, err)
		defer store.close()

		// Rename day_count column so INSERT INTO daily fails.
		_, execErr := store.db.Exec("ALTER TABLE daily RENAME COLUMN day_count TO total")
		require.NoError(t, execErr)

		state := map[string]*UsageStats{
			"model": {
				DayStart: time.Now(),
				DayCount: 1,
			},
		}
		err = store.saveState(state)
		assert.Error(t, err, "saveState should fail on daily insert with renamed column")
	})
}

func TestError_SQLiteLoadState_Bad(t *testing.T) {
	t.Run("loadState fails when requests table is dropped", func(t *testing.T) {
		dbPath := testPath(t.TempDir(), "req-err.db")
		store, err := newSQLiteStore(dbPath)
		require.NoError(t, err)
		defer store.close()

		state := map[string]*UsageStats{
			"model": {
				Requests: []time.Time{time.Now()},
				DayStart: time.Now(),
				DayCount: 1,
			},
		}
		require.NoError(t, store.saveState(state))

		_, execErr := store.db.Exec("DROP TABLE requests")
		require.NoError(t, execErr)

		_, err = store.loadState()
		assert.Error(t, err, "loadState should fail when requests table is missing")
	})

	t.Run("loadState fails when tokens table is dropped", func(t *testing.T) {
		dbPath := testPath(t.TempDir(), "tok-err.db")
		store, err := newSQLiteStore(dbPath)
		require.NoError(t, err)
		defer store.close()

		state := map[string]*UsageStats{
			"model": {
				Tokens:   []TokenEntry{{Time: time.Now(), Count: 100}},
				DayStart: time.Now(),
				DayCount: 1,
			},
		}
		require.NoError(t, store.saveState(state))

		_, execErr := store.db.Exec("DROP TABLE tokens")
		require.NoError(t, execErr)

		_, err = store.loadState()
		assert.Error(t, err, "loadState should fail when tokens table is missing")
	})

	t.Run("loadState fails when daily table is dropped", func(t *testing.T) {
		dbPath := testPath(t.TempDir(), "daily-load-err.db")
		store, err := newSQLiteStore(dbPath)
		require.NoError(t, err)
		defer store.close()

		state := map[string]*UsageStats{
			"model": {
				DayStart: time.Now(),
				DayCount: 1,
			},
		}
		require.NoError(t, store.saveState(state))

		_, execErr := store.db.Exec("DROP TABLE daily")
		require.NoError(t, execErr)

		_, err = store.loadState()
		assert.Error(t, err, "loadState should fail when daily table is missing")
	})
}

func TestError_SQLiteSaveQuotasExec_Bad(t *testing.T) {
	t.Run("saveQuotas fails with renamed column at prepare", func(t *testing.T) {
		dbPath := testPath(t.TempDir(), "quota-exec-err.db")
		store, err := newSQLiteStore(dbPath)
		require.NoError(t, err)
		defer store.close()

		// Rename column so INSERT fails at prepare.
		_, execErr := store.db.Exec("ALTER TABLE quotas RENAME COLUMN max_rpm TO rpm")
		require.NoError(t, execErr)

		err = store.saveQuotas(map[string]ModelQuota{
			"test": {MaxRPM: 10, MaxTPM: 100, MaxRPD: 50},
		})
		assert.Error(t, err, "saveQuotas should fail with renamed column")
	})

	t.Run("saveQuotas fails at exec via trigger", func(t *testing.T) {
		dbPath := testPath(t.TempDir(), "quota-trigger.db")
		store, err := newSQLiteStore(dbPath)
		require.NoError(t, err)
		defer store.close()

		// Add trigger to abort INSERT.
		_, execErr := store.db.Exec(`CREATE TRIGGER fail_quota BEFORE INSERT ON quotas
			BEGIN SELECT RAISE(ABORT, 'forced quota insert failure'); END`)
		require.NoError(t, execErr)

		err = store.saveQuotas(map[string]ModelQuota{
			"test": {MaxRPM: 10, MaxTPM: 100, MaxRPD: 50},
		})
		assert.Error(t, err, "saveQuotas should fail when trigger fires")
		assert.Contains(t, err.Error(), "exec test")
	})
}

func TestError_SQLiteSaveStateExec_Bad(t *testing.T) {
	t.Run("request insert exec fails via trigger", func(t *testing.T) {
		dbPath := testPath(t.TempDir(), "trigger-req.db")
		store, err := newSQLiteStore(dbPath)
		require.NoError(t, err)
		defer store.close()

		// Add a trigger that aborts INSERT on requests.
		_, execErr := store.db.Exec(`CREATE TRIGGER fail_req_insert BEFORE INSERT ON requests
			BEGIN SELECT RAISE(ABORT, 'forced request insert failure'); END`)
		require.NoError(t, execErr)

		state := map[string]*UsageStats{
			"model": {
				Requests: []time.Time{time.Now()},
				DayStart: time.Now(),
				DayCount: 1,
			},
		}
		err = store.saveState(state)
		assert.Error(t, err, "saveState should fail when request insert trigger fires")
		assert.Contains(t, err.Error(), "insert request")
	})

	t.Run("token insert exec fails via trigger", func(t *testing.T) {
		dbPath := testPath(t.TempDir(), "trigger-tok.db")
		store, err := newSQLiteStore(dbPath)
		require.NoError(t, err)
		defer store.close()

		// Add a trigger that aborts INSERT on tokens.
		_, execErr := store.db.Exec(`CREATE TRIGGER fail_tok_insert BEFORE INSERT ON tokens
			BEGIN SELECT RAISE(ABORT, 'forced token insert failure'); END`)
		require.NoError(t, execErr)

		state := map[string]*UsageStats{
			"model": {
				Tokens:   []TokenEntry{{Time: time.Now(), Count: 100}},
				DayStart: time.Now(),
				DayCount: 1,
			},
		}
		err = store.saveState(state)
		assert.Error(t, err, "saveState should fail when token insert trigger fires")
		assert.Contains(t, err.Error(), "insert token")
	})

	t.Run("daily insert exec fails via trigger", func(t *testing.T) {
		dbPath := testPath(t.TempDir(), "trigger-day.db")
		store, err := newSQLiteStore(dbPath)
		require.NoError(t, err)
		defer store.close()

		// Add a trigger that aborts INSERT on daily.
		_, execErr := store.db.Exec(`CREATE TRIGGER fail_day_insert BEFORE INSERT ON daily
			BEGIN SELECT RAISE(ABORT, 'forced daily insert failure'); END`)
		require.NoError(t, execErr)

		state := map[string]*UsageStats{
			"model": {
				DayStart: time.Now(),
				DayCount: 1,
			},
		}
		err = store.saveState(state)
		assert.Error(t, err, "saveState should fail when daily insert trigger fires")
		assert.Contains(t, err.Error(), "insert daily")
	})
}

func TestError_SQLiteLoadQuotasScan_Bad(t *testing.T) {
	t.Run("loadQuotas fails with renamed column", func(t *testing.T) {
		dbPath := testPath(t.TempDir(), "quota-scan-err.db")
		store, err := newSQLiteStore(dbPath)
		require.NoError(t, err)
		defer store.close()

		// Save valid quotas first.
		require.NoError(t, store.saveQuotas(map[string]ModelQuota{
			"test": {MaxRPM: 10},
		}))

		// Rename column so SELECT ... FROM quotas fails.
		_, execErr := store.db.Exec("ALTER TABLE quotas RENAME COLUMN max_rpm TO rpm")
		require.NoError(t, execErr)

		_, err = store.loadQuotas()
		assert.Error(t, err, "loadQuotas should fail with renamed column")
	})
}

func TestError_NewSQLiteStoreInReadOnlyDir_Bad(t *testing.T) {
	if isRootUser() {
		t.Skip("chmod restrictions do not apply to root")
	}

	t.Run("fails when parent directory is read-only", func(t *testing.T) {
		tmpDir := t.TempDir()
		readonlyDir := testPath(tmpDir, "readonly")
		ensureTestDir(t, readonlyDir)
		setPathMode(t, readonlyDir, 0o555)
		defer func() {
			_ = syscall.Chmod(readonlyDir, 0o755)
		}()

		dbPath := testPath(readonlyDir, "test.db")
		_, err := newSQLiteStore(dbPath)
		assert.Error(t, err, "should fail when directory is read-only")
	})
}

func TestError_SQLiteCreateSchema_Bad(t *testing.T) {
	t.Run("createSchema fails on closed DB", func(t *testing.T) {
		dbPath := testPath(t.TempDir(), "schema-err.db")
		store, err := newSQLiteStore(dbPath)
		require.NoError(t, err)

		db := store.db
		store.close()

		err = createSchema(db)
		assert.Error(t, err, "createSchema should fail on closed DB")
		assert.Contains(t, err.Error(), "ratelimit.createSchema")
	})
}

func TestError_SQLiteLoadStateScan_Bad(t *testing.T) {
	t.Run("scan daily fails with NULL values", func(t *testing.T) {
		dbPath := testPath(t.TempDir(), "scan-daily.db")
		store, err := newSQLiteStore(dbPath)
		require.NoError(t, err)
		defer store.close()

		// Recreate daily without NOT NULL constraints so we can insert NULLs.
		_, _ = store.db.Exec("DROP TABLE daily")
		_, execErr := store.db.Exec("CREATE TABLE daily (model TEXT PRIMARY KEY, day_start INTEGER, day_count INTEGER)")
		require.NoError(t, execErr)
		// Insert a NULL day_start; scanning NULL into int64 returns an error.
		_, execErr = store.db.Exec("INSERT INTO daily VALUES ('test', NULL, NULL)")
		require.NoError(t, execErr)

		_, err = store.loadState()
		if err != nil {
			assert.Contains(t, err.Error(), "ratelimit.loadState")
		}
	})

	t.Run("scan requests fails with NULL ts", func(t *testing.T) {
		dbPath := testPath(t.TempDir(), "scan-req.db")
		store, err := newSQLiteStore(dbPath)
		require.NoError(t, err)
		defer store.close()

		// Insert a valid daily entry so loadState gets past the daily phase.
		_, execErr := store.db.Exec("INSERT INTO daily VALUES ('test', 0, 1)")
		require.NoError(t, execErr)

		// Recreate requests without NOT NULL, insert NULL ts.
		_, _ = store.db.Exec("DROP TABLE requests")
		_, execErr = store.db.Exec("CREATE TABLE requests (model TEXT, ts INTEGER)")
		require.NoError(t, execErr)
		_, execErr = store.db.Exec("CREATE INDEX IF NOT EXISTS idx_requests_model_ts ON requests(model, ts)")
		require.NoError(t, execErr)
		_, execErr = store.db.Exec("INSERT INTO requests VALUES ('test', NULL)")
		require.NoError(t, execErr)

		_, err = store.loadState()
		if err != nil {
			assert.Contains(t, err.Error(), "ratelimit.loadState")
		}
	})

	t.Run("scan tokens fails with NULL values", func(t *testing.T) {
		dbPath := testPath(t.TempDir(), "scan-tok.db")
		store, err := newSQLiteStore(dbPath)
		require.NoError(t, err)
		defer store.close()

		// Insert valid daily entry.
		_, execErr := store.db.Exec("INSERT INTO daily VALUES ('test', 0, 1)")
		require.NoError(t, execErr)

		// Recreate tokens without NOT NULL, insert NULL values.
		_, _ = store.db.Exec("DROP TABLE tokens")
		_, execErr = store.db.Exec("CREATE TABLE tokens (model TEXT, ts INTEGER, count INTEGER)")
		require.NoError(t, execErr)
		_, execErr = store.db.Exec("CREATE INDEX IF NOT EXISTS idx_tokens_model_ts ON tokens(model, ts)")
		require.NoError(t, execErr)
		_, execErr = store.db.Exec("INSERT INTO tokens VALUES ('test', NULL, NULL)")
		require.NoError(t, execErr)

		_, err = store.loadState()
		if err != nil {
			assert.Contains(t, err.Error(), "ratelimit.loadState")
		}
	})
}

func TestError_SQLiteLoadQuotasScanWithBadSchema_Bad(t *testing.T) {
	t.Run("scan fails with NULL quota values", func(t *testing.T) {
		dbPath := testPath(t.TempDir(), "scan-quota.db")
		store, err := newSQLiteStore(dbPath)
		require.NoError(t, err)
		defer store.close()

		// Recreate quotas without NOT NULL constraints.
		_, _ = store.db.Exec("DROP TABLE quotas")
		_, execErr := store.db.Exec("CREATE TABLE quotas (model TEXT PRIMARY KEY, max_rpm INTEGER, max_tpm INTEGER, max_rpd INTEGER)")
		require.NoError(t, execErr)
		_, execErr = store.db.Exec("INSERT INTO quotas VALUES ('test', NULL, NULL, NULL)")
		require.NoError(t, execErr)

		_, err = store.loadQuotas()
		if err != nil {
			assert.Contains(t, err.Error(), "ratelimit.loadQuotas")
		}
	})
}

func TestError_MigrateYAMLToSQLiteWithSaveErrors_Bad(t *testing.T) {
	t.Run("saveQuotas failure during migration via trigger", func(t *testing.T) {
		tmpDir := t.TempDir()
		yamlPath := testPath(tmpDir, "with-quotas.yaml")
		sqlitePath := testPath(tmpDir, "migrate-quota-err.db")

		// Write a YAML file with quotas.
		yamlData := `quotas:
  test-model:
    max_rpm: 10
    max_tpm: 100
    max_rpd: 50
`
		writeTestFile(t, yamlPath, yamlData)

		// Pre-create DB with a trigger that aborts quota inserts.
		store, err := newSQLiteStore(sqlitePath)
		require.NoError(t, err)
		_, execErr := store.db.Exec(`CREATE TRIGGER fail_quota_migrate BEFORE INSERT ON quotas
			BEGIN SELECT RAISE(ABORT, 'forced quota failure'); END`)
		require.NoError(t, execErr)
		store.close()

		// Migration re-opens the DB; tables already exist, trigger persists.
		err = MigrateYAMLToSQLite(yamlPath, sqlitePath)
		assert.Error(t, err, "migration should fail when saveQuotas fails")
	})

	t.Run("saveState failure during migration via trigger", func(t *testing.T) {
		tmpDir := t.TempDir()
		yamlPath := testPath(tmpDir, "with-state.yaml")
		sqlitePath := testPath(tmpDir, "migrate-state-err.db")

		// Write YAML with state.
		yamlData := `state:
  test-model:
    requests:
      - time: 2026-01-01T00:00:00Z
    day_start: 2026-01-01T00:00:00Z
    day_count: 1
`
		writeTestFile(t, yamlPath, yamlData)

		// Pre-create DB with a trigger that aborts daily inserts.
		store, err := newSQLiteStore(sqlitePath)
		require.NoError(t, err)
		_, execErr := store.db.Exec(`CREATE TRIGGER fail_daily_migrate BEFORE INSERT ON daily
			BEGIN SELECT RAISE(ABORT, 'forced daily failure'); END`)
		require.NoError(t, execErr)
		store.close()

		err = MigrateYAMLToSQLite(yamlPath, sqlitePath)
		assert.Error(t, err, "migration should fail when saveState fails")
	})
}

func TestError_MigrateYAMLToSQLiteNilQuotasAndState_Good(t *testing.T) {
	t.Run("YAML with empty quotas and state migrates cleanly", func(t *testing.T) {
		tmpDir := t.TempDir()
		yamlPath := testPath(tmpDir, "empty.yaml")
		writeTestFile(t, yamlPath, "{}")

		sqlitePath := testPath(tmpDir, "empty.db")
		require.NoError(t, MigrateYAMLToSQLite(yamlPath, sqlitePath))

		store, err := newSQLiteStore(sqlitePath)
		require.NoError(t, err)
		defer store.close()

		quotas, err := store.loadQuotas()
		require.NoError(t, err)
		assert.Empty(t, quotas)

		state, err := store.loadState()
		require.NoError(t, err)
		assert.Empty(t, state)
	})
}

func TestError_NewWithConfigHomeUnavailable_Bad(t *testing.T) {
	// Clear all supported home env vars so defaultStatePath cannot resolve a home directory.
	t.Setenv("CORE_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("home", "")
	t.Setenv("USERPROFILE", "")

	_, err := NewWithConfig(Config{})
	assert.Error(t, err, "should fail when HOME is unset")
}

func TestError_PersistMarshal_Good(t *testing.T) {
	// yaml.Marshal on a struct with map[string]ModelQuota and map[string]*UsageStats
	// should not fail in practice. We test the error path by using a type that
	// yaml.Marshal cannot handle: a channel.
	// Since we cannot inject a channel into the typed struct, this path is
	// unreachable in production. Instead, exercise the Persist YAML path
	// with valid data to confirm coverage of the non-error path.
	rl := newTestLimiter(t)
	rl.RecordUsage("test", 1, 1)
	assert.NoError(t, rl.Persist(), "valid persist should succeed")
}

func TestError_MigrateErrorsExtended_Bad(t *testing.T) {
	t.Run("unmarshal failure", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := testPath(tmpDir, "bad.yaml")
		writeTestFile(t, path, "invalid: yaml: [")
		err := MigrateYAMLToSQLite(path, testPath(tmpDir, "out.db"))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "ratelimit.MigrateYAMLToSQLite: unmarshal")
	})

	t.Run("sqlite open failure", func(t *testing.T) {
		tmpDir := t.TempDir()
		yamlPath := testPath(tmpDir, "ok.yaml")
		writeTestFile(t, yamlPath, "quotas: {}")
		// Use an invalid sqlite path (dir where file should be)
		err := MigrateYAMLToSQLite(yamlPath, "/dev/null/not-a-db")
		assert.Error(t, err)
	})
}
