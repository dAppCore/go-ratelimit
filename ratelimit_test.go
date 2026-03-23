package ratelimit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestLimiter returns a RateLimiter with file path set to a temp directory.
func newTestLimiter(t *testing.T) *RateLimiter {
	t.Helper()
	rl, err := New()
	require.NoError(t, err)
	rl.filePath = filepath.Join(t.TempDir(), "ratelimits.yaml")
	return rl
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

// --- Phase 0: CanSend boundary conditions ---

func TestCanSend(t *testing.T) {
	t.Run("fresh state allows send", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "test-model"
		rl.Quotas[model] = ModelQuota{MaxRPM: 10, MaxTPM: 1000, MaxRPD: 100}

		assert.True(t, rl.CanSend(model, 100), "fresh state should allow send")
	})

	t.Run("unknown model is always allowed", func(t *testing.T) {
		rl := newTestLimiter(t)
		assert.True(t, rl.CanSend("unknown-model", 999999), "unknown model should be allowed")
	})

	t.Run("unlimited model is always allowed", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "test-unlimited"
		rl.Quotas[model] = ModelQuota{MaxRPM: 0, MaxTPM: 0, MaxRPD: 0}

		for range 1000 {
			rl.RecordUsage(model, 100, 100)
		}
		assert.True(t, rl.CanSend(model, 999999), "unlimited model should always allow sends")
	})

	t.Run("RPM at exact limit is rejected", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "test-rpm-boundary"
		rl.Quotas[model] = ModelQuota{MaxRPM: 3, MaxTPM: 1000000, MaxRPD: 1000}

		rl.RecordUsage(model, 1, 1)
		rl.RecordUsage(model, 1, 1)
		assert.True(t, rl.CanSend(model, 1), "should allow at RPM-1")

		rl.RecordUsage(model, 1, 1)
		assert.False(t, rl.CanSend(model, 1), "should reject at exact RPM limit")
	})

	t.Run("RPM exceeded is rejected", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "test-rpm"
		rl.Quotas[model] = ModelQuota{MaxRPM: 2, MaxTPM: 1000000, MaxRPD: 100}

		rl.RecordUsage(model, 10, 10)
		rl.RecordUsage(model, 10, 10)
		assert.False(t, rl.CanSend(model, 10), "should reject after exceeding RPM")
	})

	t.Run("TPM at exact limit is rejected", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "test-tpm-boundary"
		rl.Quotas[model] = ModelQuota{MaxRPM: 100, MaxTPM: 100, MaxRPD: 1000}

		rl.RecordUsage(model, 50, 40) // 90 tokens
		assert.True(t, rl.CanSend(model, 10), "should allow at exact TPM limit (90+10=100)")
		assert.False(t, rl.CanSend(model, 11), "should reject when exceeding TPM by 1 (90+11=101)")
	})

	t.Run("TPM exceeded is rejected", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "test-tpm"
		rl.Quotas[model] = ModelQuota{MaxRPM: 10, MaxTPM: 100, MaxRPD: 100}

		rl.RecordUsage(model, 50, 40) // 90 tokens
		assert.False(t, rl.CanSend(model, 20), "should reject when 90+20=110 > 100")
	})

	t.Run("RPD at exact limit is rejected", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "test-rpd-boundary"
		rl.Quotas[model] = ModelQuota{MaxRPM: 1000, MaxTPM: 10000000, MaxRPD: 3}

		rl.RecordUsage(model, 1, 1)
		rl.RecordUsage(model, 1, 1)
		assert.True(t, rl.CanSend(model, 1), "should allow at RPD-1")

		rl.RecordUsage(model, 1, 1)
		assert.False(t, rl.CanSend(model, 1), "should reject at exact RPD limit")
	})

	t.Run("RPD exceeded is rejected", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "test-rpd"
		rl.Quotas[model] = ModelQuota{MaxRPM: 10, MaxTPM: 1000000, MaxRPD: 2}

		rl.RecordUsage(model, 10, 10)
		rl.RecordUsage(model, 10, 10)
		assert.False(t, rl.CanSend(model, 10), "should reject after exceeding RPD")
	})

	t.Run("RPD unlimited allows infinite requests", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "test-rpd-unlimited"
		rl.Quotas[model] = ModelQuota{MaxRPM: 10000, MaxTPM: 100000000, MaxRPD: 0}

		for range 100 {
			rl.RecordUsage(model, 1, 1)
		}
		assert.True(t, rl.CanSend(model, 1), "RPD=0 should mean unlimited daily requests")
	})

	t.Run("RPM only limit", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "rpm-only"
		rl.Quotas[model] = ModelQuota{MaxRPM: 2, MaxTPM: 0, MaxRPD: 0}

		rl.RecordUsage(model, 100000, 100000)
		assert.True(t, rl.CanSend(model, 999999), "TPM=0 should be unlimited")

		rl.RecordUsage(model, 1, 1)
		assert.False(t, rl.CanSend(model, 1), "RPM should still enforce")
	})

	t.Run("TPM only limit", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "tpm-only"
		rl.Quotas[model] = ModelQuota{MaxRPM: 0, MaxTPM: 100, MaxRPD: 0}

		// RPM=0 and RPD=0 means all-zero => unlimited shortcut
		// Actually: MaxTPM > 0 but MaxRPM == 0 and MaxRPD == 0 means the
		// unlimited check (all three == 0) does NOT trigger.
		// Let's verify.
		rl.RecordUsage(model, 50, 40) // 90 tokens
		assert.True(t, rl.CanSend(model, 10), "should allow under TPM limit")
		assert.False(t, rl.CanSend(model, 11), "should reject over TPM limit")
	})

	t.Run("zero estimated tokens always fits TPM", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "zero-est"
		rl.Quotas[model] = ModelQuota{MaxRPM: 100, MaxTPM: 100, MaxRPD: 100}

		rl.RecordUsage(model, 50, 50) // exactly 100 tokens
		assert.True(t, rl.CanSend(model, 0), "zero estimated tokens should fit even at limit")
	})

	t.Run("negative estimated tokens are rejected", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "negative-est"
		rl.Quotas[model] = ModelQuota{MaxRPM: 100, MaxTPM: 100, MaxRPD: 100}
		rl.RecordUsage(model, 50, 50)

		assert.False(t, rl.CanSend(model, -1), "negative estimated tokens should not bypass TPM limits")
	})
}

// --- Phase 0: Sliding window / prune tests ---

func TestPrune(t *testing.T) {
	t.Run("removes old entries", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "test-prune"
		rl.Quotas[model] = ModelQuota{MaxRPM: 5, MaxTPM: 1000000, MaxRPD: 100}

		oldTime := time.Now().Add(-2 * time.Minute)
		rl.State[model] = &UsageStats{
			Requests: []time.Time{oldTime, oldTime, oldTime},
			Tokens: []TokenEntry{
				{Time: oldTime, Count: 100},
				{Time: oldTime, Count: 100},
			},
			DayStart: time.Now(),
		}

		assert.True(t, rl.CanSend(model, 10), "old entries should be pruned")
		assert.Empty(t, rl.State[model].Requests, "requests should be empty after pruning")
		assert.Empty(t, rl.State[model].Tokens, "tokens should be empty after pruning")
	})

	t.Run("keeps recent entries", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "test-keep"
		rl.Quotas[model] = ModelQuota{MaxRPM: 10, MaxTPM: 1000000, MaxRPD: 100}

		now := time.Now()
		recent := now.Add(-30 * time.Second)
		old := now.Add(-2 * time.Minute)
		rl.State[model] = &UsageStats{
			Requests: []time.Time{old, recent, old, recent},
			Tokens: []TokenEntry{
				{Time: old, Count: 100},
				{Time: recent, Count: 200},
				{Time: old, Count: 300},
			},
			DayStart: now,
		}

		rl.CanSend(model, 1) // triggers prune

		stats := rl.State[model]
		assert.Len(t, stats.Requests, 2, "should keep 2 recent requests")
		assert.Len(t, stats.Tokens, 1, "should keep 1 recent token entry")
		assert.Equal(t, 200, stats.Tokens[0].Count, "kept token should be the recent one")
	})

	t.Run("daily reset when 24h elapsed", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "test-daily"
		rl.Quotas[model] = ModelQuota{MaxRPM: 100, MaxTPM: 1000000, MaxRPD: 5}

		// Set state with day started 25 hours ago and 5 daily requests used
		rl.State[model] = &UsageStats{
			DayStart: time.Now().Add(-25 * time.Hour),
			DayCount: 5,
		}

		// Should reset daily counter and allow
		assert.True(t, rl.CanSend(model, 1), "should allow after daily reset")
		assert.Equal(t, 0, rl.State[model].DayCount, "day count should be reset")
	})

	t.Run("no daily reset within 24h", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "test-no-reset"
		rl.Quotas[model] = ModelQuota{MaxRPM: 100, MaxTPM: 1000000, MaxRPD: 5}

		rl.State[model] = &UsageStats{
			DayStart: time.Now().Add(-23 * time.Hour),
			DayCount: 5,
		}

		assert.False(t, rl.CanSend(model, 1), "should reject within 24h when RPD exhausted")
		assert.Equal(t, 5, rl.State[model].DayCount, "day count should not be reset")
	})

	t.Run("prune on non-existent model is noop", func(t *testing.T) {
		rl := newTestLimiter(t)
		rl.mu.Lock()
		rl.prune("nonexistent-model")
		rl.mu.Unlock()
		// Should not panic or create state
		_, exists := rl.State["nonexistent-model"]
		assert.False(t, exists, "prune should not create state for non-existent model")
	})

	t.Run("mixed old and new entries at boundary", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "boundary-prune"
		rl.Quotas[model] = ModelQuota{MaxRPM: 100, MaxTPM: 1000000, MaxRPD: 1000}

		now := time.Now()
		// Entry exactly at the 1-minute boundary (should be pruned because After is strict)
		atBoundary := now.Add(-1 * time.Minute)
		justInside := now.Add(-59 * time.Second)

		rl.State[model] = &UsageStats{
			Requests: []time.Time{atBoundary, justInside},
			Tokens: []TokenEntry{
				{Time: atBoundary, Count: 500},
				{Time: justInside, Count: 300},
			},
			DayStart: now,
		}

		rl.CanSend(model, 1)
		stats := rl.State[model]
		assert.Len(t, stats.Requests, 1, "entry at exact boundary should be pruned")
		assert.Len(t, stats.Tokens, 1, "token at exact boundary should be pruned")
	})
}

// --- Phase 0: RecordUsage ---

func TestRecordUsage(t *testing.T) {
	t.Run("records into fresh state", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "record-fresh"

		rl.RecordUsage(model, 100, 50)

		stats := rl.State[model]
		require.NotNil(t, stats)
		assert.Len(t, stats.Requests, 1)
		assert.Len(t, stats.Tokens, 1)
		assert.Equal(t, 150, stats.Tokens[0].Count)
		assert.Equal(t, 1, stats.DayCount)
	})

	t.Run("accumulates multiple recordings", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "record-multi"

		rl.RecordUsage(model, 10, 20)
		rl.RecordUsage(model, 30, 40)
		rl.RecordUsage(model, 50, 60)

		stats := rl.State[model]
		assert.Len(t, stats.Requests, 3)
		assert.Len(t, stats.Tokens, 3)
		assert.Equal(t, 3, stats.DayCount)

		// Verify total tokens
		total := 0
		for _, te := range stats.Tokens {
			total += te.Count
		}
		assert.Equal(t, 210, total, "total tokens should be 10+20+30+40+50+60=210")
	})

	t.Run("records into existing state", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "record-existing"

		rl.State[model] = &UsageStats{
			Requests: []time.Time{time.Now()},
			Tokens:   []TokenEntry{{Time: time.Now(), Count: 100}},
			DayStart: time.Now(),
			DayCount: 5,
		}

		rl.RecordUsage(model, 200, 300)

		stats := rl.State[model]
		assert.Len(t, stats.Requests, 2)
		assert.Len(t, stats.Tokens, 2)
		assert.Equal(t, 6, stats.DayCount)
	})

	t.Run("negative token inputs are clamped to zero", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "record-negative"

		rl.RecordUsage(model, -100, 25)

		stats := rl.State[model]
		require.NotNil(t, stats)
		assert.Len(t, stats.Requests, 1)
		assert.Len(t, stats.Tokens, 1)
		assert.Equal(t, 25, stats.Tokens[0].Count)
	})
}

// --- Phase 0: Reset ---

func TestReset(t *testing.T) {
	t.Run("reset single model", func(t *testing.T) {
		rl := newTestLimiter(t)
		rl.RecordUsage("model-a", 10, 10)
		rl.RecordUsage("model-b", 20, 20)

		rl.Reset("model-a")

		_, existsA := rl.State["model-a"]
		_, existsB := rl.State["model-b"]
		assert.False(t, existsA, "model-a state should be cleared")
		assert.True(t, existsB, "model-b state should remain")
	})

	t.Run("reset all models with empty string", func(t *testing.T) {
		rl := newTestLimiter(t)
		rl.RecordUsage("model-a", 10, 10)
		rl.RecordUsage("model-b", 20, 20)
		rl.RecordUsage("model-c", 30, 30)

		rl.Reset("")

		assert.Empty(t, rl.State, "all state should be cleared")
	})

	t.Run("reset non-existent model is safe", func(t *testing.T) {
		rl := newTestLimiter(t)
		rl.Reset("does-not-exist") // should not panic
		assert.Empty(t, rl.State)
	})
}

// --- Phase 0: WaitForCapacity ---

func TestWaitForCapacity(t *testing.T) {
	t.Run("context cancelled returns error", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "wait-cancel"
		rl.Quotas[model] = ModelQuota{MaxRPM: 1, MaxTPM: 1000000, MaxRPD: 100}

		rl.RecordUsage(model, 10, 10)

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		err := rl.WaitForCapacity(ctx, model, 10)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("immediate capacity returns nil", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "wait-immediate"
		rl.Quotas[model] = ModelQuota{MaxRPM: 10, MaxTPM: 1000000, MaxRPD: 100}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := rl.WaitForCapacity(ctx, model, 10)
		assert.NoError(t, err, "should return immediately when capacity available")
	})

	t.Run("unknown model returns immediately", func(t *testing.T) {
		rl := newTestLimiter(t)

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		err := rl.WaitForCapacity(ctx, "unknown-model", 999999)
		assert.NoError(t, err, "unknown model should return immediately")
	})

	t.Run("context cancelled before first check", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "wait-pre-cancel"
		rl.Quotas[model] = ModelQuota{MaxRPM: 1, MaxTPM: 1000000, MaxRPD: 100}
		rl.RecordUsage(model, 10, 10)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately

		err := rl.WaitForCapacity(ctx, model, 10)
		assert.Error(t, err, "should return error for already-cancelled context")
	})

	t.Run("negative tokens return error", func(t *testing.T) {
		rl := newTestLimiter(t)
		err := rl.WaitForCapacity(context.Background(), "wait-negative", -1)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "negative tokens")
	})
}

func TestNilUsageStats(t *testing.T) {
	t.Run("CanSend replaces nil state without panicking", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "nil-cansend"
		rl.Quotas[model] = ModelQuota{MaxRPM: 10, MaxTPM: 100, MaxRPD: 10}
		rl.State[model] = nil

		assert.NotPanics(t, func() {
			assert.True(t, rl.CanSend(model, 10))
		})
		assert.NotNil(t, rl.State[model])
	})

	t.Run("RecordUsage replaces nil state without panicking", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "nil-record"
		rl.State[model] = nil

		assert.NotPanics(t, func() {
			rl.RecordUsage(model, 10, 10)
		})
		require.NotNil(t, rl.State[model])
		assert.Equal(t, 1, rl.State[model].DayCount)
	})

	t.Run("Stats and AllStats tolerate nil state entries", func(t *testing.T) {
		rl := newTestLimiter(t)
		rl.Quotas["nil-stats"] = ModelQuota{MaxRPM: 1, MaxTPM: 2, MaxRPD: 3}
		rl.State["nil-stats"] = nil
		rl.State["nil-all-stats"] = nil

		assert.NotPanics(t, func() {
			stats := rl.Stats("nil-stats")
			assert.Equal(t, 1, stats.MaxRPM)
			assert.Equal(t, 0, stats.TPM)
		})

		assert.NotPanics(t, func() {
			all := rl.AllStats()
			assert.Contains(t, all, "nil-stats")
			assert.Contains(t, all, "nil-all-stats")
		})
	})
}

// --- Phase 0: Stats ---

func TestStats(t *testing.T) {
	t.Run("returns stats for known model with usage", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "stats-test"
		rl.Quotas[model] = ModelQuota{MaxRPM: 50, MaxTPM: 5000, MaxRPD: 500}
		rl.RecordUsage(model, 100, 200)

		stats := rl.Stats(model)
		assert.Equal(t, 1, stats.RPM)
		assert.Equal(t, 300, stats.TPM)
		assert.Equal(t, 1, stats.RPD)
		assert.Equal(t, 50, stats.MaxRPM)
		assert.Equal(t, 5000, stats.MaxTPM)
		assert.Equal(t, 500, stats.MaxRPD)
	})

	t.Run("returns empty stats for unknown model", func(t *testing.T) {
		rl := newTestLimiter(t)
		stats := rl.Stats("nonexistent")
		assert.Equal(t, 0, stats.RPM)
		assert.Equal(t, 0, stats.TPM)
		assert.Equal(t, 0, stats.RPD)
		assert.Equal(t, 0, stats.MaxRPM)
	})

	t.Run("returns quota info for model without usage", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "quota-only"
		rl.Quotas[model] = ModelQuota{MaxRPM: 100, MaxTPM: 200, MaxRPD: 300}

		stats := rl.Stats(model)
		assert.Equal(t, 0, stats.RPM, "no usage yet")
		assert.Equal(t, 100, stats.MaxRPM, "quota should be present")
		assert.Equal(t, 200, stats.MaxTPM)
		assert.Equal(t, 300, stats.MaxRPD)
	})
}

// --- Phase 0: AllStats ---

func TestAllStats(t *testing.T) {
	t.Run("includes all default quotas plus state-only models", func(t *testing.T) {
		rl := newTestLimiter(t)
		rl.RecordUsage("gemini-3-pro-preview", 1000, 500)
		rl.RecordUsage("custom-model", 100, 200)

		all := rl.AllStats()
		// Should include all default Gemini models + custom-model
		assert.GreaterOrEqual(t, len(all), 6, "should include default quotas + custom model")

		pro := all["gemini-3-pro-preview"]
		assert.Equal(t, 1, pro.RPM)
		assert.Equal(t, 1500, pro.TPM)

		custom := all["custom-model"]
		assert.Equal(t, 1, custom.RPM)
		assert.Equal(t, 300, custom.TPM)
		assert.Equal(t, 0, custom.MaxRPM, "custom model has no quota")
	})

	t.Run("prunes old entries in AllStats", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "allstats-prune"
		rl.Quotas[model] = ModelQuota{MaxRPM: 100, MaxTPM: 1000000, MaxRPD: 100}

		oldTime := time.Now().Add(-2 * time.Minute)
		rl.State[model] = &UsageStats{
			Requests: []time.Time{oldTime, oldTime},
			Tokens:   []TokenEntry{{Time: oldTime, Count: 500}},
			DayStart: time.Now(),
			DayCount: 2,
		}

		all := rl.AllStats()
		stats := all[model]
		assert.Equal(t, 0, stats.RPM, "old requests should be pruned")
		assert.Equal(t, 0, stats.TPM, "old tokens should be pruned")
		assert.Equal(t, 2, stats.RPD, "daily count survives prune")
	})

	t.Run("daily reset in AllStats", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "allstats-daily"
		rl.Quotas[model] = ModelQuota{MaxRPM: 100, MaxTPM: 1000000, MaxRPD: 100}

		rl.State[model] = &UsageStats{
			DayStart: time.Now().Add(-25 * time.Hour),
			DayCount: 50,
		}

		all := rl.AllStats()
		stats := all[model]
		assert.Equal(t, 0, stats.RPD, "daily count should be reset after 24h")
	})
}

// --- Phase 0: Persist and Load ---

func TestPersistAndLoad(t *testing.T) {
	t.Run("round-trip preserves state", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "ratelimits.yaml")

		rl1, err := New()
		require.NoError(t, err)
		rl1.filePath = path
		model := "persist-test"
		rl1.Quotas[model] = ModelQuota{MaxRPM: 50, MaxTPM: 5000, MaxRPD: 500}
		rl1.RecordUsage(model, 100, 100)

		require.NoError(t, rl1.Persist())

		rl2, err := New()
		require.NoError(t, err)
		rl2.filePath = path
		require.NoError(t, rl2.Load())

		stats := rl2.Stats(model)
		assert.Equal(t, 1, stats.RPM)
		assert.Equal(t, 200, stats.TPM)
	})

	t.Run("load from non-existent file is not an error", func(t *testing.T) {
		rl := newTestLimiter(t)
		rl.filePath = filepath.Join(t.TempDir(), "does-not-exist.yaml")

		err := rl.Load()
		assert.NoError(t, err, "loading non-existent file should not error")
	})

	t.Run("load from corrupt YAML returns error", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "corrupt.yaml")
		require.NoError(t, os.WriteFile(path, []byte("{{{{invalid yaml!!!!"), 0644))

		rl := newTestLimiter(t)
		rl.filePath = path

		err := rl.Load()
		assert.Error(t, err, "corrupt YAML should produce an error")
	})

	t.Run("load from unreadable file returns error", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("chmod 000 does not restrict root")
		}
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "unreadable.yaml")
		require.NoError(t, os.WriteFile(path, []byte("quotas: {}"), 0644))
		require.NoError(t, os.Chmod(path, 0000))

		rl := newTestLimiter(t)
		rl.filePath = path

		err := rl.Load()
		assert.Error(t, err, "unreadable file should produce an error")

		// Clean up permissions for temp dir cleanup
		_ = os.Chmod(path, 0644)
	})

	t.Run("persist to nested non-existent directory creates it", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "nested", "deep", "ratelimits.yaml")

		rl := newTestLimiter(t)
		rl.filePath = path
		rl.RecordUsage("test", 1, 1)

		err := rl.Persist()
		assert.NoError(t, err, "should create nested directories")

		_, statErr := os.Stat(path)
		assert.NoError(t, statErr, "file should exist")
	})

	t.Run("persist to unwritable directory returns error", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("chmod 0555 does not restrict root")
		}
		tmpDir := t.TempDir()
		unwritable := filepath.Join(tmpDir, "readonly")
		require.NoError(t, os.MkdirAll(unwritable, 0555))

		rl := newTestLimiter(t)
		rl.filePath = filepath.Join(unwritable, "sub", "ratelimits.yaml")

		err := rl.Persist()
		assert.Error(t, err, "should fail when directory is unwritable")

		// Clean up
		_ = os.Chmod(unwritable, 0755)
	})
}

// --- Phase 0: Default quotas ---

func TestDefaultQuotas(t *testing.T) {
	rl := newTestLimiter(t)

	tests := []struct {
		model  string
		maxRPM int
		maxTPM int
		maxRPD int
	}{
		{"gemini-3-pro-preview", 150, 1000000, 1000},
		{"gemini-3-flash-preview", 150, 1000000, 1000},
		{"gemini-2.5-pro", 150, 1000000, 1000},
		{"gemini-2.0-flash", 150, 1000000, 0},
		{"gemini-2.0-flash-lite", 0, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			q, ok := rl.Quotas[tt.model]
			require.True(t, ok, "quota should exist for %s", tt.model)
			assert.Equal(t, tt.maxRPM, q.MaxRPM)
			assert.Equal(t, tt.maxTPM, q.MaxTPM)
			assert.Equal(t, tt.maxRPD, q.MaxRPD)
		})
	}
}

// --- Phase 0: Concurrent access (race test) ---

func TestConcurrentAccess(t *testing.T) {
	rl := newTestLimiter(t)
	model := "concurrent-test"
	rl.Quotas[model] = ModelQuota{MaxRPM: 1000, MaxTPM: 10000000, MaxRPD: 10000}

	var wg sync.WaitGroup
	goroutines := 20
	opsPerGoroutine := 50

	for range goroutines {
		wg.Go(func() {
			for range opsPerGoroutine {
				rl.CanSend(model, 10)
				rl.RecordUsage(model, 5, 5)
				rl.Stats(model)
			}
		})
	}

	wg.Wait()

	stats := rl.Stats(model)
	expected := goroutines * opsPerGoroutine
	assert.Equal(t, expected, stats.RPD, "all recordings should be counted")
}

func TestConcurrentResetAndRecord(t *testing.T) {
	rl := newTestLimiter(t)
	model := "concurrent-reset"
	rl.Quotas[model] = ModelQuota{MaxRPM: 10000, MaxTPM: 100000000, MaxRPD: 100000}

	var wg sync.WaitGroup

	// Writers
	for range 5 {
		wg.Go(func() {
			for range 100 {
				rl.RecordUsage(model, 1, 1)
			}
		})
	}

	// Resetters
	for range 3 {
		wg.Go(func() {
			for range 20 {
				rl.Reset(model)
			}
		})
	}

	// Readers
	for range 5 {
		wg.Go(func() {
			for range 100 {
				rl.AllStats()
			}
		})
	}

	wg.Wait()
	// No assertion needed -- if we get here without -race flagging, mutex is sound
}

func TestBackgroundPrune(t *testing.T) {
	rl := newTestLimiter(t)
	model := "prune-me"
	rl.Quotas[model] = ModelQuota{MaxRPM: 100}

	// Set state with old usage.
	old := time.Now().Add(-2 * time.Minute)
	rl.State[model] = &UsageStats{
		Requests: []time.Time{old},
		Tokens:   []TokenEntry{{Time: old, Count: 100}},
	}

	stop := rl.BackgroundPrune(10 * time.Millisecond)
	defer stop()

	// Wait for pruner to run.
	assert.Eventually(t, func() bool {
		rl.mu.Lock()
		defer rl.mu.Unlock()
		_, exists := rl.State[model]
		return !exists
	}, 1*time.Second, 20*time.Millisecond, "old empty state should be pruned")

	t.Run("non-positive interval is a safe no-op", func(t *testing.T) {
		rl := newTestLimiter(t)
		rl.State["still-here"] = &UsageStats{
			Requests: []time.Time{time.Now().Add(-2 * time.Minute)},
		}

		assert.NotPanics(t, func() {
			stop := rl.BackgroundPrune(0)
			stop()
		})
		assert.Contains(t, rl.State, "still-here")
	})
}

// --- Phase 0: CountTokens (with mock HTTP server) ---

func TestCountTokens(t *testing.T) {
	t.Run("successful token count", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			assert.Equal(t, "test-api-key", r.Header.Get("x-goog-api-key"))
			assert.Equal(t, "/v1beta/models/test-model:countTokens", r.URL.EscapedPath())

			var body struct {
				Contents []struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"contents"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Len(t, body.Contents, 1)
			require.Len(t, body.Contents[0].Parts, 1)
			assert.Equal(t, "hello", body.Contents[0].Parts[0].Text)

			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]int{"totalTokens": 42}))
		}))
		defer server.Close()

		tokens, err := countTokensWithClient(context.Background(), server.Client(), server.URL, "test-api-key", "test-model", "hello")
		require.NoError(t, err)
		assert.Equal(t, 42, tokens)
	})

	t.Run("model name is path escaped", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/v1beta/models/folder%2Fmodel%3Fdebug=1:countTokens", r.URL.EscapedPath())
			assert.Empty(t, r.URL.RawQuery)
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]int{"totalTokens": 7}))
		}))
		defer server.Close()

		tokens, err := countTokensWithClient(context.Background(), server.Client(), server.URL, "test-api-key", "folder/model?debug=1", "hello")
		require.NoError(t, err)
		assert.Equal(t, 7, tokens)
	})

	t.Run("API error body is truncated", func(t *testing.T) {
		largeBody := strings.Repeat("x", countTokensErrorBodyLimit+256)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, err := fmt.Fprint(w, largeBody)
			require.NoError(t, err)
		}))
		defer server.Close()

		_, err := countTokensWithClient(context.Background(), server.Client(), server.URL, "fake-key", "test-model", "hello")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "api error status 401")
		assert.True(t, strings.Count(err.Error(), "x") < len(largeBody), "error body should be bounded")
		assert.Contains(t, err.Error(), "...")
	})

	t.Run("empty model is rejected before request", func(t *testing.T) {
		_, err := CountTokens(context.Background(), "fake-key", "", "hello")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "build url")
	})

	t.Run("invalid base URL returns error", func(t *testing.T) {
		_, err := countTokensWithClient(context.Background(), http.DefaultClient, "://bad-url", "fake-key", "test-model", "hello")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "build url")
	})

	t.Run("base URL without host returns error", func(t *testing.T) {
		_, err := countTokensWithClient(context.Background(), http.DefaultClient, "/relative", "fake-key", "test-model", "hello")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "build url")
	})

	t.Run("invalid JSON response returns error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"totalTokens":`))
			require.NoError(t, err)
		}))
		defer server.Close()

		_, err := countTokensWithClient(context.Background(), server.Client(), server.URL, "fake-key", "test-model", "hello")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "decode response")
	})

	t.Run("error body read failures are returned", func(t *testing.T) {
		client := &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusBadGateway,
					Body:       io.NopCloser(errReader{}),
					Header:     make(http.Header),
				}, nil
			}),
		}

		_, err := countTokensWithClient(context.Background(), client, "https://generativelanguage.googleapis.com", "fake-key", "test-model", "hello")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "read error body")
	})

	t.Run("nil client falls back to http.DefaultClient", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]int{"totalTokens": 11}))
		}))
		defer server.Close()

		originalClient := http.DefaultClient
		http.DefaultClient = server.Client()
		defer func() {
			http.DefaultClient = originalClient
		}()

		tokens, err := countTokensWithClient(context.Background(), nil, server.URL, "fake-key", "test-model", "hello")
		require.NoError(t, err)
		assert.Equal(t, 11, tokens)
	})
}

func TestPersistSkipsNilState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nil-state.yaml")

	rl, err := New()
	require.NoError(t, err)
	rl.filePath = path
	rl.State["nil-model"] = nil

	require.NoError(t, rl.Persist())

	rl2, err := New()
	require.NoError(t, err)
	rl2.filePath = path
	require.NoError(t, rl2.Load())
	assert.NotContains(t, rl2.State, "nil-model")
}

func TestTokenTotals(t *testing.T) {
	maxInt := int(^uint(0) >> 1)

	assert.Equal(t, 25, safeTokenSum(-100, 25))
	assert.Equal(t, maxInt, safeTokenSum(maxInt, 1))
	assert.Equal(t, 10, totalTokenCount([]TokenEntry{{Count: -5}, {Count: 10}}))
}

// --- Phase 0: Benchmarks ---

func BenchmarkCanSend(b *testing.B) {
	rl, _ := New()
	model := "bench-model"
	rl.Quotas[model] = ModelQuota{MaxRPM: 10000, MaxTPM: 100000000, MaxRPD: 100000}

	// Populate sliding window with 1000 entries
	now := time.Now()
	entries := make([]time.Time, 1000)
	tokens := make([]TokenEntry, 1000)
	for i := range 1000 {
		t := now.Add(-time.Duration(i) * time.Millisecond * 50) // spread over ~50 seconds
		entries[i] = t
		tokens[i] = TokenEntry{Time: t, Count: 10}
	}
	rl.State[model] = &UsageStats{
		Requests: entries,
		Tokens:   tokens,
		DayStart: now,
		DayCount: 500,
	}

	b.ResetTimer()
	for range b.N {
		rl.CanSend(model, 100)
	}
}

func BenchmarkRecordUsage(b *testing.B) {
	rl, _ := New()
	model := "bench-record"
	rl.Quotas[model] = ModelQuota{MaxRPM: 100000, MaxTPM: 1000000000, MaxRPD: 1000000}

	b.ResetTimer()
	for range b.N {
		rl.RecordUsage(model, 100, 100)
	}
}

func BenchmarkCanSendConcurrent(b *testing.B) {
	rl, _ := New()
	model := "bench-concurrent"
	rl.Quotas[model] = ModelQuota{MaxRPM: 100000, MaxTPM: 1000000000, MaxRPD: 1000000}

	// Populate window
	now := time.Now()
	for range 100 {
		rl.State[model] = &UsageStats{DayStart: now}
		rl.RecordUsage(model, 10, 10)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			rl.CanSend(model, 100)
		}
	})
}

// --- Phase 1: Provider profiles and NewWithConfig ---

func TestDefaultProfiles(t *testing.T) {
	profiles := DefaultProfiles()

	t.Run("contains all four providers", func(t *testing.T) {
		assert.Contains(t, profiles, ProviderGemini)
		assert.Contains(t, profiles, ProviderOpenAI)
		assert.Contains(t, profiles, ProviderAnthropic)
		assert.Contains(t, profiles, ProviderLocal)
	})

	t.Run("Gemini profile has expected models", func(t *testing.T) {
		gemini := profiles[ProviderGemini]
		assert.Equal(t, ProviderGemini, gemini.Provider)
		assert.Contains(t, gemini.Models, "gemini-3-pro-preview")
		assert.Contains(t, gemini.Models, "gemini-2.0-flash")
		assert.Contains(t, gemini.Models, "gemini-2.0-flash-lite")
	})

	t.Run("OpenAI profile has expected models", func(t *testing.T) {
		openai := profiles[ProviderOpenAI]
		assert.Equal(t, ProviderOpenAI, openai.Provider)
		assert.Contains(t, openai.Models, "gpt-4o")
		assert.Contains(t, openai.Models, "gpt-4o-mini")
		assert.Contains(t, openai.Models, "o3-mini")
	})

	t.Run("Anthropic profile has expected models", func(t *testing.T) {
		anthropic := profiles[ProviderAnthropic]
		assert.Equal(t, ProviderAnthropic, anthropic.Provider)
		assert.Contains(t, anthropic.Models, "claude-opus-4")
		assert.Contains(t, anthropic.Models, "claude-sonnet-4")
		assert.Contains(t, anthropic.Models, "claude-haiku-3.5")
	})

	t.Run("Local profile has no models by default", func(t *testing.T) {
		local := profiles[ProviderLocal]
		assert.Equal(t, ProviderLocal, local.Provider)
		assert.Empty(t, local.Models, "local provider should have no default quotas")
	})
}

func TestNewWithConfig(t *testing.T) {
	t.Run("empty config defaults to Gemini", func(t *testing.T) {
		rl, err := NewWithConfig(Config{
			FilePath: filepath.Join(t.TempDir(), "test.yaml"),
		})
		require.NoError(t, err)

		_, hasGemini := rl.Quotas["gemini-3-pro-preview"]
		assert.True(t, hasGemini, "empty config should load Gemini defaults")
	})

	t.Run("single provider loads only its models", func(t *testing.T) {
		rl, err := NewWithConfig(Config{
			FilePath:  filepath.Join(t.TempDir(), "test.yaml"),
			Providers: []Provider{ProviderOpenAI},
		})
		require.NoError(t, err)

		_, hasGPT := rl.Quotas["gpt-4o"]
		assert.True(t, hasGPT, "should have OpenAI models")

		_, hasGemini := rl.Quotas["gemini-3-pro-preview"]
		assert.False(t, hasGemini, "should not have Gemini models")
	})

	t.Run("multiple providers merge models", func(t *testing.T) {
		rl, err := NewWithConfig(Config{
			FilePath:  filepath.Join(t.TempDir(), "test.yaml"),
			Providers: []Provider{ProviderGemini, ProviderAnthropic},
		})
		require.NoError(t, err)

		_, hasGemini := rl.Quotas["gemini-3-pro-preview"]
		_, hasClaude := rl.Quotas["claude-opus-4"]
		assert.True(t, hasGemini, "should have Gemini models")
		assert.True(t, hasClaude, "should have Anthropic models")

		_, hasGPT := rl.Quotas["gpt-4o"]
		assert.False(t, hasGPT, "should not have OpenAI models")
	})

	t.Run("explicit quotas override provider defaults", func(t *testing.T) {
		rl, err := NewWithConfig(Config{
			FilePath:  filepath.Join(t.TempDir(), "test.yaml"),
			Providers: []Provider{ProviderGemini},
			Quotas: map[string]ModelQuota{
				"gemini-3-pro-preview": {MaxRPM: 999, MaxTPM: 888, MaxRPD: 777},
			},
		})
		require.NoError(t, err)

		q := rl.Quotas["gemini-3-pro-preview"]
		assert.Equal(t, 999, q.MaxRPM, "explicit quota should override provider default")
		assert.Equal(t, 888, q.MaxTPM)
		assert.Equal(t, 777, q.MaxRPD)
	})

	t.Run("explicit quotas without providers", func(t *testing.T) {
		rl, err := NewWithConfig(Config{
			FilePath: filepath.Join(t.TempDir(), "test.yaml"),
			Quotas: map[string]ModelQuota{
				"my-custom-model": {MaxRPM: 10, MaxTPM: 1000, MaxRPD: 50},
			},
		})
		require.NoError(t, err)

		assert.Len(t, rl.Quotas, 1, "should only have the explicit quota")
		q := rl.Quotas["my-custom-model"]
		assert.Equal(t, 10, q.MaxRPM)
	})

	t.Run("custom file path is respected", func(t *testing.T) {
		customPath := filepath.Join(t.TempDir(), "custom", "limits.yaml")
		rl, err := NewWithConfig(Config{
			FilePath:  customPath,
			Providers: []Provider{ProviderLocal},
		})
		require.NoError(t, err)

		rl.RecordUsage("test", 1, 1)
		require.NoError(t, rl.Persist())

		_, statErr := os.Stat(customPath)
		assert.NoError(t, statErr, "file should be created at custom path")
	})

	t.Run("unknown provider is silently skipped", func(t *testing.T) {
		rl, err := NewWithConfig(Config{
			FilePath:  filepath.Join(t.TempDir(), "test.yaml"),
			Providers: []Provider{"nonexistent-provider"},
		})
		require.NoError(t, err)
		assert.Empty(t, rl.Quotas, "unknown provider should produce no quotas")
	})

	t.Run("local provider with custom quotas", func(t *testing.T) {
		rl, err := NewWithConfig(Config{
			FilePath:  filepath.Join(t.TempDir(), "test.yaml"),
			Providers: []Provider{ProviderLocal},
			Quotas: map[string]ModelQuota{
				"llama-3.3-70b": {MaxRPM: 5, MaxTPM: 50000, MaxRPD: 0},
			},
		})
		require.NoError(t, err)

		assert.Len(t, rl.Quotas, 1, "should only have the custom local model")
		q := rl.Quotas["llama-3.3-70b"]
		assert.Equal(t, 5, q.MaxRPM)
		assert.Equal(t, 50000, q.MaxTPM)
	})

	t.Run("invalid backend returns error", func(t *testing.T) {
		_, err := NewWithConfig(Config{
			Backend: "bogus",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown backend")
	})

	t.Run("default YAML path uses home directory", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", "")
		t.Setenv("home", "")

		rl, err := NewWithConfig(Config{})
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(home, defaultStateDirName, defaultYAMLStateFile), rl.filePath)
	})
}

func TestNewBackwardCompatibility(t *testing.T) {
	// New() should produce the exact same result as before Phase 1
	rl, err := New()
	require.NoError(t, err)

	expected := map[string]ModelQuota{
		"gemini-3-pro-preview":   {MaxRPM: 150, MaxTPM: 1000000, MaxRPD: 1000},
		"gemini-3-flash-preview": {MaxRPM: 150, MaxTPM: 1000000, MaxRPD: 1000},
		"gemini-2.5-pro":         {MaxRPM: 150, MaxTPM: 1000000, MaxRPD: 1000},
		"gemini-2.0-flash":       {MaxRPM: 150, MaxTPM: 1000000, MaxRPD: 0},
		"gemini-2.0-flash-lite":  {MaxRPM: 0, MaxTPM: 0, MaxRPD: 0},
	}

	assert.Len(t, rl.Quotas, len(expected), "New() should produce exactly the Gemini defaults")
	for model, expectedQ := range expected {
		t.Run(model, func(t *testing.T) {
			q, ok := rl.Quotas[model]
			require.True(t, ok, "quota should exist")
			assert.Equal(t, expectedQ, q)
		})
	}
}

func TestSetQuota(t *testing.T) {
	t.Run("adds new model quota", func(t *testing.T) {
		rl := newTestLimiter(t)
		rl.SetQuota("custom-model", ModelQuota{MaxRPM: 42, MaxTPM: 9999, MaxRPD: 100})

		q, ok := rl.Quotas["custom-model"]
		require.True(t, ok)
		assert.Equal(t, 42, q.MaxRPM)
		assert.Equal(t, 9999, q.MaxTPM)
		assert.Equal(t, 100, q.MaxRPD)
	})

	t.Run("overwrites existing model quota", func(t *testing.T) {
		rl := newTestLimiter(t)
		rl.SetQuota("gemini-3-pro-preview", ModelQuota{MaxRPM: 1, MaxTPM: 1, MaxRPD: 1})

		q := rl.Quotas["gemini-3-pro-preview"]
		assert.Equal(t, 1, q.MaxRPM, "should overwrite existing")
	})

	t.Run("is safe for concurrent use", func(t *testing.T) {
		rl := newTestLimiter(t)
		var wg sync.WaitGroup

		for i := range 10 {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				model := fmt.Sprintf("model-%d", n)
				rl.SetQuota(model, ModelQuota{MaxRPM: n, MaxTPM: n * 100, MaxRPD: n * 10})
			}(i)
		}
		wg.Wait()

		assert.GreaterOrEqual(t, len(rl.Quotas), 10, "all concurrent SetQuota calls should succeed")
	})
}

func TestAddProvider(t *testing.T) {
	t.Run("adds OpenAI models to existing limiter", func(t *testing.T) {
		rl := newTestLimiter(t) // starts with Gemini defaults
		geminiCount := len(rl.Quotas)

		rl.AddProvider(ProviderOpenAI)

		// Should now have both Gemini and OpenAI models
		assert.Greater(t, len(rl.Quotas), geminiCount, "should have more models after adding OpenAI")
		_, hasGPT := rl.Quotas["gpt-4o"]
		assert.True(t, hasGPT, "should have OpenAI models")

		// Gemini models should still be present
		_, hasGemini := rl.Quotas["gemini-3-pro-preview"]
		assert.True(t, hasGemini, "Gemini models should remain")
	})

	t.Run("adds Anthropic models to existing limiter", func(t *testing.T) {
		rl := newTestLimiter(t)
		rl.AddProvider(ProviderAnthropic)

		_, hasClaude := rl.Quotas["claude-opus-4"]
		assert.True(t, hasClaude, "should have Anthropic models")
	})

	t.Run("unknown provider is a noop", func(t *testing.T) {
		rl := newTestLimiter(t)
		before := len(rl.Quotas)

		rl.AddProvider("nonexistent")

		assert.Equal(t, before, len(rl.Quotas), "unknown provider should not change quotas")
	})

	t.Run("adding local provider does not remove existing quotas", func(t *testing.T) {
		rl := newTestLimiter(t)
		before := len(rl.Quotas)

		rl.AddProvider(ProviderLocal)

		assert.Equal(t, before, len(rl.Quotas), "local provider has no models, count unchanged")
	})

	t.Run("is safe for concurrent use", func(t *testing.T) {
		rl := newTestLimiter(t)
		var wg sync.WaitGroup

		providers := []Provider{ProviderGemini, ProviderOpenAI, ProviderAnthropic, ProviderLocal}
		for _, p := range providers {
			wg.Add(1)
			go func(prov Provider) {
				defer wg.Done()
				for range 10 {
					rl.AddProvider(prov)
				}
			}(p)
		}
		wg.Wait()
		// Should not panic
	})
}

func TestProviderConstants(t *testing.T) {
	// Verify the string values are stable (they may be used in YAML configs)
	assert.Equal(t, Provider("gemini"), ProviderGemini)
	assert.Equal(t, Provider("openai"), ProviderOpenAI)
	assert.Equal(t, Provider("anthropic"), ProviderAnthropic)
	assert.Equal(t, Provider("local"), ProviderLocal)
}

// --- Phase 0 addendum: Additional concurrent and multi-model race tests ---

func TestConcurrentMultipleModels(t *testing.T) {
	rl := newTestLimiter(t)
	models := []string{"model-a", "model-b", "model-c", "model-d", "model-e"}
	for _, m := range models {
		rl.Quotas[m] = ModelQuota{MaxRPM: 1000, MaxTPM: 10000000, MaxRPD: 10000}
	}

	var wg sync.WaitGroup
	iterations := 50

	for _, m := range models {
		wg.Add(1)
		go func(model string) {
			defer wg.Done()
			for range iterations {
				rl.CanSend(model, 10)
				rl.RecordUsage(model, 10, 10)
				rl.Stats(model)
			}
		}(m)
	}

	wg.Wait()

	for _, m := range models {
		stats := rl.Stats(m)
		assert.Equal(t, iterations, stats.RPD, "each model should have correct RPD")
	}
}

func TestConcurrentPersistAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "concurrent.yaml")

	rl := newTestLimiter(t)
	rl.filePath = path
	model := "race-persist"
	rl.Quotas[model] = ModelQuota{MaxRPM: 10000, MaxTPM: 100000000, MaxRPD: 100000}

	var wg sync.WaitGroup

	// Writers + persist
	for range 3 {
		wg.Go(func() {
			for range 50 {
				rl.RecordUsage(model, 10, 10)
				_ = rl.Persist()
			}
		})
	}

	// Loaders
	for range 3 {
		wg.Go(func() {
			for range 50 {
				_ = rl.Load()
			}
		})
	}

	wg.Wait()
	// No panics or data races = pass
}

func TestConcurrentAllStatsAndRecordUsage(t *testing.T) {
	rl := newTestLimiter(t)
	models := []string{"stats-a", "stats-b", "stats-c"}
	for _, m := range models {
		rl.Quotas[m] = ModelQuota{MaxRPM: 1000, MaxTPM: 10000000, MaxRPD: 10000}
	}

	var wg sync.WaitGroup

	for _, m := range models {
		wg.Add(1)
		go func(model string) {
			defer wg.Done()
			for range 100 {
				rl.RecordUsage(model, 10, 10)
			}
		}(m)
	}

	// Read AllStats concurrently
	for range 3 {
		wg.Go(func() {
			for range 50 {
				_ = rl.AllStats()
			}
		})
	}

	wg.Wait()
}

func TestConcurrentWaitForCapacityAndRecordUsage(t *testing.T) {
	rl := newTestLimiter(t)
	model := "race-wait"
	rl.Quotas[model] = ModelQuota{MaxRPM: 100, MaxTPM: 10000000, MaxRPD: 10000}

	var wg sync.WaitGroup

	for range 5 {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			_ = rl.WaitForCapacity(ctx, model, 10)
		})
	}

	// Record usage concurrently
	for range 5 {
		wg.Go(func() {
			for range 20 {
				rl.RecordUsage(model, 10, 10)
			}
		})
	}

	wg.Wait()
}

// --- Phase 0 addendum: Additional benchmarks ---

func BenchmarkCanSendWithPrune(b *testing.B) {
	rl, _ := New()
	model := "bench-prune"
	rl.Quotas[model] = ModelQuota{MaxRPM: 10000000, MaxTPM: 10000000000, MaxRPD: 10000000}

	// Pre-fill with a mix of old and new entries to trigger pruning
	now := time.Now()
	rl.State[model] = &UsageStats{DayStart: now}
	for range 500 {
		old := now.Add(-2 * time.Minute)
		rl.State[model].Requests = append(rl.State[model].Requests, old)
		rl.State[model].Tokens = append(rl.State[model].Tokens, TokenEntry{Time: old, Count: 100})
	}
	for i := range 500 {
		recent := now.Add(-time.Duration(i) * time.Millisecond * 100)
		rl.State[model].Requests = append(rl.State[model].Requests, recent)
		rl.State[model].Tokens = append(rl.State[model].Tokens, TokenEntry{Time: recent, Count: 100})
	}
	rl.State[model].DayCount = 1000

	b.ResetTimer()
	for range b.N {
		rl.CanSend(model, 100)
	}
}

func BenchmarkStats(b *testing.B) {
	rl, _ := New()
	model := "bench-stats"
	rl.Quotas[model] = ModelQuota{MaxRPM: 10000, MaxTPM: 100000000, MaxRPD: 100000}

	now := time.Now()
	rl.State[model] = &UsageStats{DayStart: now, DayCount: 500}
	for i := range 1000 {
		t := now.Add(-time.Duration(i) * time.Millisecond * 50)
		rl.State[model].Requests = append(rl.State[model].Requests, t)
		rl.State[model].Tokens = append(rl.State[model].Tokens, TokenEntry{Time: t, Count: 100})
	}

	b.ResetTimer()
	for range b.N {
		rl.Stats(model)
	}
}

func BenchmarkAllStats(b *testing.B) {
	rl, _ := New()
	models := []string{"bench-a", "bench-b", "bench-c", "bench-d", "bench-e"}
	now := time.Now()

	for _, m := range models {
		rl.Quotas[m] = ModelQuota{MaxRPM: 10000, MaxTPM: 100000000, MaxRPD: 100000}
		rl.State[m] = &UsageStats{DayStart: now, DayCount: 200}
		for i := range 200 {
			t := now.Add(-time.Duration(i) * time.Millisecond * 250)
			rl.State[m].Requests = append(rl.State[m].Requests, t)
			rl.State[m].Tokens = append(rl.State[m].Tokens, TokenEntry{Time: t, Count: 100})
		}
	}

	b.ResetTimer()
	for range b.N {
		rl.AllStats()
	}
}

func BenchmarkPersist(b *testing.B) {
	tmpDir := b.TempDir()
	path := filepath.Join(tmpDir, "bench.yaml")

	rl, _ := New()
	rl.filePath = path
	model := "bench-persist"
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

func TestEndToEndMultiProvider(t *testing.T) {
	// Simulate a real-world scenario: limiter for both Gemini and Anthropic
	rl, err := NewWithConfig(Config{
		FilePath:  filepath.Join(t.TempDir(), "multi.yaml"),
		Providers: []Provider{ProviderGemini, ProviderAnthropic},
	})
	require.NoError(t, err)

	// Use Gemini model
	assert.True(t, rl.CanSend("gemini-3-pro-preview", 1000))
	rl.RecordUsage("gemini-3-pro-preview", 500, 500)

	// Use Anthropic model
	assert.True(t, rl.CanSend("claude-opus-4", 1000))
	rl.RecordUsage("claude-opus-4", 500, 500)

	// Check stats for both
	geminiStats := rl.Stats("gemini-3-pro-preview")
	assert.Equal(t, 1, geminiStats.RPM)
	assert.Equal(t, 150, geminiStats.MaxRPM)

	claudeStats := rl.Stats("claude-opus-4")
	assert.Equal(t, 1, claudeStats.RPM)
	assert.Equal(t, 50, claudeStats.MaxRPM)

	// Persist and reload
	require.NoError(t, rl.Persist())

	rl2, err := NewWithConfig(Config{
		FilePath:  rl.filePath,
		Providers: []Provider{ProviderGemini, ProviderAnthropic},
	})
	require.NoError(t, err)
	require.NoError(t, rl2.Load())

	reloaded := rl2.Stats("claude-opus-4")
	assert.Equal(t, 1, reloaded.RPM, "state should survive persist/reload")
}
