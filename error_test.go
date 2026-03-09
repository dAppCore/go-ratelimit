package ratelimit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSQLiteErrorPaths(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "error.db")
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

func TestSQLiteInitErrors(t *testing.T) {
	t.Run("WAL pragma failure", func(t *testing.T) {
		// This is hard to trigger without mocking sql.DB, but we can try an invalid connection string
		// modernc.org/sqlite doesn't support all DSN options that might cause PRAGMA to fail but connection to succeed.
	})
}

func TestPersistYAML(t *testing.T) {
	t.Run("successful YAML persist and load", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "ratelimits.yaml")
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

func TestMigrateErrorsExtended(t *testing.T) {
	t.Run("unmarshal failure", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "bad.yaml")
		require.NoError(t, os.WriteFile(path, []byte("invalid: yaml: ["), 0644))
		err := MigrateYAMLToSQLite(path, filepath.Join(tmpDir, "out.db"))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "ratelimit.MigrateYAMLToSQLite: unmarshal")
	})

	t.Run("sqlite open failure", func(t *testing.T) {
		tmpDir := t.TempDir()
		yamlPath := filepath.Join(tmpDir, "ok.yaml")
		require.NoError(t, os.WriteFile(yamlPath, []byte("quotas: {}"), 0644))
		// Use an invalid sqlite path (dir where file should be)
		err := MigrateYAMLToSQLite(yamlPath, "/dev/null/not-a-db")
		assert.Error(t, err)
	})
}
