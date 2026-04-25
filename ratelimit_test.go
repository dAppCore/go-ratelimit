// SPDX-License-Identifier: EUPL-1.2

package ratelimit

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"syscall"
	"testing"
	"time"

	core "dappco.re/go/core"
)

func testPath(parts ...string) string {
	return core.Path(parts...)
}

func pathExists(path string) bool {
	var fs core.Fs
	return fs.Exists(path)
}

func writeTestFile(tb testing.TB, path, content string) {
	tb.Helper()
	if err := writeLocalFile(path, content); err != nil {
		tb.Fatal(testUnexpectedErrorMessage(err))
	}
}

func ensureTestDir(tb testing.TB, path string) {
	tb.Helper()
	if err := ensureDir(path); err != nil {
		tb.Fatal(testUnexpectedErrorMessage(err))
	}
}

func setPathMode(tb testing.TB, path string, mode uint32) {
	tb.Helper()
	if err := syscall.Chmod(path, mode); err != nil {
		tb.Fatal(testUnexpectedErrorMessage(err))
	}
}

func overwriteTestFile(tb testing.TB, path, content string) {
	tb.Helper()

	var fs core.Fs
	writer := fs.Create(path)
	if err := resultError(writer); err != nil {
		tb.Fatal(testUnexpectedErrorMessage(err))
	}
	if err := resultError(core.WriteAll(writer.Value, content)); err != nil {
		tb.Fatal(testUnexpectedErrorMessage(err))
	}
}

func isRootUser() bool {
	return syscall.Geteuid() == 0
}

func repeatString(part string, count int) string {
	builder := core.NewBuilder()
	for i := 0; i < count; i++ {
		builder.WriteString(part)
	}
	return builder.String()
}

func substringCount(s, substr string) int {
	if substr == "" {
		return 0
	}
	return len(core.Split(s, substr)) - 1
}

func decodeJSONBody(tb testing.TB, r io.Reader, target any) {
	tb.Helper()

	data, err := io.ReadAll(r)
	if err != nil {
		tb.Fatal(testUnexpectedErrorMessage(err))
	}
	if err := resultError(core.JSONUnmarshal(data, target)); err != nil {
		tb.Fatal(testUnexpectedErrorMessage(err))
	}
}

func writeJSONBody(tb testing.TB, w io.Writer, value any) {
	tb.Helper()

	_, err := io.WriteString(w, core.JSONMarshalString(value))
	if err != nil {
		tb.Fatal(testUnexpectedErrorMessage(err))
	}
}

// newTestLimiter returns a RateLimiter with file path set to a temp directory.
func newTestLimiter(t *testing.T) *RateLimiter {
	t.Helper()
	rl, err := New()
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	rl.filePath = testPath(t.TempDir(), "ratelimits.yaml")
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

func TestRatelimit_CanSend_Good(t *testing.T) {
	t.Run("fresh state allows send", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "test-model"
		rl.Quotas[model] = ModelQuota{MaxRPM: 10, MaxTPM: 1000, MaxRPD: 100}
		if !rl.CanSend(model, 100) {
			t.Fatal(testExpectedTrueMessage("fresh state should allow send"))
		}
	})

	t.Run("unknown model is always allowed", func(t *testing.T) {
		rl := newTestLimiter(t)
		if !rl.CanSend("unknown-model", 999999) {
			t.Fatal(testExpectedTrueMessage("unknown model should be allowed"))
		}
	})

	t.Run("unlimited model is always allowed", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "test-unlimited"
		rl.Quotas[model] = ModelQuota{MaxRPM: 0, MaxTPM: 0, MaxRPD: 0}

		for range 1000 {
			rl.RecordUsage(model, 100, 100)
		}
		if !rl.CanSend(model, 999999) {
			t.Fatal(testExpectedTrueMessage("unlimited model should always allow sends"))
		}
	})

	t.Run("RPM at exact limit is rejected", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "test-rpm-boundary"
		rl.Quotas[model] = ModelQuota{MaxRPM: 3, MaxTPM: 1000000, MaxRPD: 1000}

		rl.RecordUsage(model, 1, 1)
		rl.RecordUsage(model, 1, 1)
		if !rl.CanSend(model, 1) {
			t.Fatal(testExpectedTrueMessage("should allow at RPM-1"))
		}

		rl.RecordUsage(model, 1, 1)
		if rl.CanSend(model, 1) {
			t.Fatal(testExpectedFalseMessage("should reject at exact RPM limit"))
		}
	})

	t.Run("RPM exceeded is rejected", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "test-rpm"
		rl.Quotas[model] = ModelQuota{MaxRPM: 2, MaxTPM: 1000000, MaxRPD: 100}

		rl.RecordUsage(model, 10, 10)
		rl.RecordUsage(model, 10, 10)
		if rl.CanSend(model, 10) {
			t.Fatal(testExpectedFalseMessage("should reject after exceeding RPM"))
		}
	})

	t.Run("TPM at exact limit is rejected", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "test-tpm-boundary"
		rl.Quotas[model] = ModelQuota{MaxRPM: 100, MaxTPM: 100, MaxRPD: 1000}

		rl.RecordUsage(model, 50, 40)
		if // 90 tokens
		!rl.CanSend(model, 10) {
			t.Fatal(testExpectedTrueMessage("should allow at exact TPM limit (90+10=100)"))
		}
		if rl.CanSend(model, 11) {
			t.Fatal(testExpectedFalseMessage("should reject when exceeding TPM by 1 (90+11=101)"))
		}
	})

	t.Run("TPM exceeded is rejected", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "test-tpm"
		rl.Quotas[model] = ModelQuota{MaxRPM: 10, MaxTPM: 100, MaxRPD: 100}

		rl.RecordUsage(model, 50, 40)
		if // 90 tokens
		rl.CanSend(model, 20) {
			t.Fatal(testExpectedFalseMessage("should reject when 90+20=110 > 100"))
		}
	})

	t.Run("RPD at exact limit is rejected", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "test-rpd-boundary"
		rl.Quotas[model] = ModelQuota{MaxRPM: 1000, MaxTPM: 10000000, MaxRPD: 3}

		rl.RecordUsage(model, 1, 1)
		rl.RecordUsage(model, 1, 1)
		if !rl.CanSend(model, 1) {
			t.Fatal(testExpectedTrueMessage("should allow at RPD-1"))
		}

		rl.RecordUsage(model, 1, 1)
		if rl.CanSend(model, 1) {
			t.Fatal(testExpectedFalseMessage("should reject at exact RPD limit"))
		}
	})

	t.Run("RPD exceeded is rejected", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "test-rpd"
		rl.Quotas[model] = ModelQuota{MaxRPM: 10, MaxTPM: 1000000, MaxRPD: 2}

		rl.RecordUsage(model, 10, 10)
		rl.RecordUsage(model, 10, 10)
		if rl.CanSend(model, 10) {
			t.Fatal(testExpectedFalseMessage("should reject after exceeding RPD"))
		}
	})

	t.Run("RPD unlimited allows infinite requests", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "test-rpd-unlimited"
		rl.Quotas[model] = ModelQuota{MaxRPM: 10000, MaxTPM: 100000000, MaxRPD: 0}

		for range 100 {
			rl.RecordUsage(model, 1, 1)
		}
		if !rl.CanSend(model, 1) {
			t.Fatal(testExpectedTrueMessage("RPD=0 should mean unlimited daily requests"))
		}
	})

	t.Run("RPM only limit", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "rpm-only"
		rl.Quotas[model] = ModelQuota{MaxRPM: 2, MaxTPM: 0, MaxRPD: 0}

		rl.RecordUsage(model, 100000, 100000)
		if !rl.CanSend(model, 999999) {
			t.Fatal(testExpectedTrueMessage("TPM=0 should be unlimited"))
		}

		rl.RecordUsage(model, 1, 1)
		if rl.CanSend(model, 1) {
			t.Fatal(testExpectedFalseMessage("RPM should still enforce"))
		}
	})

	t.Run("TPM only limit", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "tpm-only"
		rl.Quotas[model] = ModelQuota{MaxRPM: 0, MaxTPM: 100, MaxRPD: 0}

		// RPM=0 and RPD=0 means all-zero => unlimited shortcut
		// Actually: MaxTPM > 0 but MaxRPM == 0 and MaxRPD == 0 means the
		// unlimited check (all three == 0) does NOT trigger.
		// Let's verify.
		rl.RecordUsage(model, 50, 40)
		if // 90 tokens
		!rl.CanSend(model, 10) {
			t.Fatal(testExpectedTrueMessage("should allow under TPM limit"))
		}
		if rl.CanSend(model, 11) {
			t.Fatal(testExpectedFalseMessage("should reject over TPM limit"))
		}
	})

	t.Run("zero estimated tokens always fits TPM", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "zero-est"
		rl.Quotas[model] = ModelQuota{MaxRPM: 100, MaxTPM: 100, MaxRPD: 100}

		rl.RecordUsage(model, 50, 50)
		if // exactly 100 tokens
		!rl.CanSend(model, 0) {
			t.Fatal(testExpectedTrueMessage("zero estimated tokens should fit even at limit"))
		}
	})

	t.Run("negative estimated tokens are rejected", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "negative-est"
		rl.Quotas[model] = ModelQuota{MaxRPM: 100, MaxTPM: 100, MaxRPD: 100}
		rl.RecordUsage(model, 50, 50)
		if rl.CanSend(model, -1) {
			t.Fatal(testExpectedFalseMessage("negative estimated tokens should not bypass TPM limits"))
		}
	})
}

// --- Phase 0: Decide surface area ---

func TestRatelimit_Decide_Good(t *testing.T) {
	t.Run("unknown model remains allowed with unknown code", func(t *testing.T) {
		rl := newTestLimiter(t)

		decision := rl.Decide("unknown-model", 50)
		if !decision.Allowed {
			t.Fatal(testExpectedTrueMessage())
		}
		if !testEqual(DecisionUnknownModel, decision.Code) {
			t.Fatal(testWantGotMessage(DecisionUnknownModel, decision.Code))
		}
		if !testIsZero(decision.RetryAfter) {
			t.Fatal(testZeroMessage(decision.RetryAfter))
		}
	})

	t.Run("unlimited quota reports unlimited decision", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "unlimited"
		rl.Quotas[model] = ModelQuota{}

		decision := rl.Decide(model, 100)
		if !decision.Allowed {
			t.Fatal(testExpectedTrueMessage())
		}
		if !testEqual(DecisionUnlimited, decision.Code) {
			t.Fatal(testWantGotMessage(DecisionUnlimited, decision.Code))
		}
		if !testEqual(0, decision.Stats.MaxRPM) {
			t.Fatal(testWantGotMessage(0, decision.Stats.MaxRPM))
		}
		if !testEqual(0, decision.Stats.MaxTPM) {
			t.Fatal(testWantGotMessage(0, decision.Stats.MaxTPM))
		}
		if !testEqual(0, decision.Stats.MaxRPD) {
			t.Fatal(testWantGotMessage(0, decision.Stats.MaxRPD))
		}
	})

	t.Run("rpd limit returns retry window", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "rpd-limit"
		now := time.Now()
		rl.Quotas[model] = ModelQuota{MaxRPM: 10, MaxTPM: 1000, MaxRPD: 2}
		rl.State[model] = &UsageStats{DayStart: now.Add(-23 * time.Hour), DayCount: 2}

		decision := rl.Decide(model, 10)
		if decision.Allowed {
			t.Fatal(testExpectedFalseMessage())
		}
		if !testEqual(DecisionRPDLimit, decision.Code) {
			t.Fatal(testWantGotMessage(DecisionRPDLimit, decision.Code))
		}
		if !testInDelta(time.Hour.Seconds(), decision.RetryAfter.Seconds(), 2) {
			t.Fatal(testInDeltaMessage(time.Hour.Seconds(), decision.RetryAfter.Seconds(), 2))
		}
		if !testEqual(2, decision.Stats.MaxRPD) {
			t.Fatal(testWantGotMessage(2, decision.Stats.MaxRPD))
		}
		if !testEqual(2, decision.Stats.RPD) {
			t.Fatal(testWantGotMessage(2, decision.Stats.RPD))
		}
	})

	t.Run("rpm limit includes retry-after estimate", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "rpm-limit"
		now := time.Now()
		rl.Quotas[model] = ModelQuota{MaxRPM: 1, MaxTPM: 1000, MaxRPD: 5}
		rl.State[model] = &UsageStats{
			Requests: []time.Time{now.Add(-10 * time.Second)},
			Tokens:   []TokenEntry{{Time: now.Add(-10 * time.Second), Count: 10}},
			DayStart: now,
			DayCount: 1,
		}

		decision := rl.Decide(model, 5)
		if decision.Allowed {
			t.Fatal(testExpectedFalseMessage())
		}
		if !testEqual(DecisionRPMLimit, decision.Code) {
			t.Fatal(testWantGotMessage(DecisionRPMLimit, decision.Code))
		}
		if !testInDelta(50, decision.RetryAfter.Seconds(), 1) {
			t.Fatal(testInDeltaMessage(50, decision.RetryAfter.Seconds(), 1))
		}
	})

	t.Run("tpm limit surfaces earliest expiry", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "tpm-limit"
		now := time.Now()
		rl.Quotas[model] = ModelQuota{MaxRPM: 10, MaxTPM: 100, MaxRPD: 10}
		rl.State[model] = &UsageStats{
			Requests: []time.Time{now.Add(-30 * time.Second)},
			Tokens: []TokenEntry{
				{Time: now.Add(-50 * time.Second), Count: 70},
				{Time: now.Add(-10 * time.Second), Count: 20},
			},
			DayStart: now,
			DayCount: 2,
		}

		decision := rl.Decide(model, 20)
		if decision.Allowed {
			t.Fatal(testExpectedFalseMessage())
		}
		if !testEqual(DecisionTPMLimit, decision.Code) {
			t.Fatal(testWantGotMessage(DecisionTPMLimit, decision.Code))
		}
		if !testInDelta(10, decision.RetryAfter.Seconds(), 1) {
			t.Fatal(testInDeltaMessage(10, decision.RetryAfter.Seconds(), 1))
		}
	})

	t.Run("allowed decision carries stats snapshot", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "decide-allowed"
		rl.Quotas[model] = ModelQuota{MaxRPM: 5, MaxTPM: 200, MaxRPD: 3}
		now := time.Now()
		rl.State[model] = &UsageStats{
			Requests: []time.Time{now.Add(-5 * time.Second)},
			Tokens:   []TokenEntry{{Time: now.Add(-5 * time.Second), Count: 30}},
			DayStart: now,
			DayCount: 1,
		}

		decision := rl.Decide(model, 20)
		if !decision.Allowed {
			t.Fatal(testExpectedTrueMessage())
		}
		if !testEqual(DecisionAllowed, decision.Code) {
			t.Fatal(testWantGotMessage(DecisionAllowed, decision.Code))
		}
		if !testEqual(1, decision.Stats.RPM) {
			t.Fatal(testWantGotMessage(1, decision.Stats.RPM))
		}
		if !testEqual(30, decision.Stats.TPM) {
			t.Fatal(testWantGotMessage(30, decision.Stats.TPM))
		}
		if !testEqual(1, decision.Stats.RPD) {
			t.Fatal(testWantGotMessage(1, decision.Stats.RPD))
		}
		if !testEqual(5, decision.Stats.MaxRPM) {
			t.Fatal(testWantGotMessage(5, decision.Stats.MaxRPM))
		}
		if !testEqual(200, decision.Stats.MaxTPM) {
			t.Fatal(testWantGotMessage(200, decision.Stats.MaxTPM))
		}
		if !testEqual(3, decision.Stats.MaxRPD) {
			t.Fatal(testWantGotMessage(3, decision.Stats.MaxRPD))
		}
	})

	t.Run("negative estimate returns invalid decision", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "neg"
		rl.Quotas[model] = ModelQuota{MaxRPM: 5, MaxTPM: 50, MaxRPD: 5}

		decision := rl.Decide(model, -5)
		if decision.Allowed {
			t.Fatal(testExpectedFalseMessage())
		}
		if !testEqual(DecisionInvalidTokens, decision.Code) {
			t.Fatal(testWantGotMessage(DecisionInvalidTokens, decision.Code))
		}
		if !testIsZero(decision.RetryAfter) {
			t.Fatal(testZeroMessage(decision.RetryAfter))
		}
		if !testContains(rl.State, model) {
			t.Fatal(testContainsMessage(rl.State, model))
		}
		if testIsNil(rl.State[model]) {
			t.Fatal(testExpectedNonNilMessage())
		}
		if !testEqual(0, rl.State[model].DayCount) {
			t.Fatal(testWantGotMessage(0, rl.State[model].DayCount))
		}
	})
}

// --- Phase 0: Sliding window / prune tests ---

func TestRatelimit_Prune_Good(t *testing.T) {
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
		if !rl.CanSend(model, 10) {
			t.Fatal(testExpectedTrueMessage("old entries should be pruned"))
		}
		if !testIsEmpty(rl.State[model].Requests) {
			t.Fatal(testEmptyMessage(rl.State[model].Requests, "requests should be empty after pruning"))
		}
		if !testIsEmpty(rl.State[model].Tokens) {
			t.Fatal(testEmptyMessage(rl.State[model].Tokens, "tokens should be empty after pruning"))
		}
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
		if !testHasLen(stats.Requests, 2) {
			t.Fatal(testLenMessage(stats.Requests, 2, "should keep 2 recent requests"))
		}
		if !testHasLen(stats.Tokens, 1) {
			t.Fatal(testLenMessage(stats.Tokens, 1, "should keep 1 recent token entry"))
		}
		if !testEqual(200, stats.Tokens[0].Count) {
			t.Fatal(testWantGotMessage(200, stats.Tokens[0].Count, "kept token should be the recent one"))
		}
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
		if !rl.CanSend(model, 1) {
			t.Fatal(testExpectedTrueMessage("should allow after daily reset"))
		}
		if !testEqual(0, rl.State[model].DayCount) {
			t.Fatal(testWantGotMessage(0, rl.State[model].DayCount, "day count should be reset"))
		}
	})

	t.Run("no daily reset within 24h", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "test-no-reset"
		rl.Quotas[model] = ModelQuota{MaxRPM: 100, MaxTPM: 1000000, MaxRPD: 5}

		rl.State[model] = &UsageStats{
			DayStart: time.Now().Add(-23 * time.Hour),
			DayCount: 5,
		}
		if rl.CanSend(model, 1) {
			t.Fatal(testExpectedFalseMessage("should reject within 24h when RPD exhausted"))
		}
		if !testEqual(5, rl.State[model].DayCount) {
			t.Fatal(testWantGotMessage(5, rl.State[model].DayCount, "day count should not be reset"))
		}
	})

	t.Run("prune on non-existent model is noop", func(t *testing.T) {
		rl := newTestLimiter(t)
		rl.mu.Lock()
		rl.prune("nonexistent-model")
		rl.mu.Unlock()
		// Should not panic or create state
		_, exists := rl.State["nonexistent-model"]
		if exists {
			t.Fatal(testExpectedFalseMessage("prune should not create state for non-existent model"))
		}
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
		if !testHasLen(stats.Requests, 1) {
			t.Fatal(testLenMessage(stats.Requests, 1, "entry at exact boundary should be pruned"))
		}
		if !testHasLen(stats.Tokens, 1) {
			t.Fatal(testLenMessage(stats.Tokens, 1, "token at exact boundary should be pruned"))
		}
	})
}

// --- Phase 0: RecordUsage ---

func TestRatelimit_RecordUsage_Good(t *testing.T) {
	t.Run("records into fresh state", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "record-fresh"

		rl.RecordUsage(model, 100, 50)

		stats := rl.State[model]
		if testIsNil(stats) {
			t.Fatal(testExpectedNonNilMessage())
		}
		if !testHasLen(stats.Requests, 1) {
			t.Fatal(testLenMessage(stats.Requests, 1))
		}
		if !testHasLen(stats.Tokens, 1) {
			t.Fatal(testLenMessage(stats.Tokens, 1))
		}
		if !testEqual(150, stats.Tokens[0].Count) {
			t.Fatal(testWantGotMessage(150, stats.Tokens[0].Count))
		}
		if !testEqual(1, stats.DayCount) {
			t.Fatal(testWantGotMessage(1, stats.DayCount))
		}
	})

	t.Run("accumulates multiple recordings", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "record-multi"

		rl.RecordUsage(model, 10, 20)
		rl.RecordUsage(model, 30, 40)
		rl.RecordUsage(model, 50, 60)

		stats := rl.State[model]
		if !testHasLen(stats.Requests, 3) {
			t.Fatal(testLenMessage(stats.Requests, 3))
		}
		if !testHasLen(stats.Tokens, 3) {
			t.Fatal(testLenMessage(stats.Tokens, 3))
		}
		if !testEqual(3, stats.DayCount) {
			t.Fatal(testWantGotMessage(3, stats.DayCount))
		}

		// Verify total tokens
		total := 0
		for _, te := range stats.Tokens {
			total += te.Count
		}
		if !testEqual(210, total) {
			t.Fatal(testWantGotMessage(210, total, "total tokens should be 10+20+30+40+50+60=210"))
		}
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
		if !testHasLen(stats.Requests, 2) {
			t.Fatal(testLenMessage(stats.Requests, 2))
		}
		if !testHasLen(stats.Tokens, 2) {
			t.Fatal(testLenMessage(stats.Tokens, 2))
		}
		if !testEqual(6, stats.DayCount) {
			t.Fatal(testWantGotMessage(6, stats.DayCount))
		}
	})

	t.Run("negative token inputs are clamped to zero", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "record-negative"

		rl.RecordUsage(model, -100, 25)

		stats := rl.State[model]
		if testIsNil(stats) {
			t.Fatal(testExpectedNonNilMessage())
		}
		if !testHasLen(stats.Requests, 1) {
			t.Fatal(testLenMessage(stats.Requests, 1))
		}
		if !testHasLen(stats.Tokens, 1) {
			t.Fatal(testLenMessage(stats.Tokens, 1))
		}
		if !testEqual(25, stats.Tokens[0].Count) {
			t.Fatal(testWantGotMessage(25, stats.Tokens[0].Count))
		}
	})
}

// --- Phase 0: Reset ---

func TestRatelimit_Reset_Good(t *testing.T) {
	t.Run("reset single model", func(t *testing.T) {
		rl := newTestLimiter(t)
		rl.RecordUsage("model-a", 10, 10)
		rl.RecordUsage("model-b", 20, 20)

		rl.Reset("model-a")

		_, existsA := rl.State["model-a"]
		_, existsB := rl.State["model-b"]
		if existsA {
			t.Fatal(testExpectedFalseMessage("model-a state should be cleared"))
		}
		if !existsB {
			t.Fatal(testExpectedTrueMessage("model-b state should remain"))
		}
	})

	t.Run("reset all models with empty string", func(t *testing.T) {
		rl := newTestLimiter(t)
		rl.RecordUsage("model-a", 10, 10)
		rl.RecordUsage("model-b", 20, 20)
		rl.RecordUsage("model-c", 30, 30)

		rl.Reset("")
		if !testIsEmpty(rl.State) {
			t.Fatal(testEmptyMessage(rl.State, "all state should be cleared"))
		}
	})

	t.Run("reset non-existent model is safe", func(t *testing.T) {
		rl := newTestLimiter(t)
		rl.Reset("does-not-exist")
		if // should not panic
		!testIsEmpty(rl.State) {
			t.Fatal(testEmptyMessage(rl.State))
		}
	})
}

// --- Phase 0: WaitForCapacity ---

func TestRatelimit_WaitForCapacity_Good(t *testing.T) {
	t.Run("context cancelled returns error", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "wait-cancel"
		rl.Quotas[model] = ModelQuota{MaxRPM: 1, MaxTPM: 1000000, MaxRPD: 100}

		rl.RecordUsage(model, 10, 10)

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		err := rl.WaitForCapacity(ctx, model, 10)
		if !testErrorIs(err, context.DeadlineExceeded) {
			t.Fatal(testErrorIsMessage(err, context.DeadlineExceeded))
		}
	})

	t.Run("immediate capacity returns nil", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "wait-immediate"
		rl.Quotas[model] = ModelQuota{MaxRPM: 10, MaxTPM: 1000000, MaxRPD: 100}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := rl.WaitForCapacity(ctx, model, 10)
		if err != nil {
			t.Fatal(testUnexpectedErrorMessage(err, "should return immediately when capacity available"))
		}
	})

	t.Run("unknown model returns immediately", func(t *testing.T) {
		rl := newTestLimiter(t)

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		err := rl.WaitForCapacity(ctx, "unknown-model", 999999)
		if err != nil {
			t.Fatal(testUnexpectedErrorMessage(err, "unknown model should return immediately"))
		}
	})

	t.Run("context cancelled before first check", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "wait-pre-cancel"
		rl.Quotas[model] = ModelQuota{MaxRPM: 1, MaxTPM: 1000000, MaxRPD: 100}
		rl.RecordUsage(model, 10, 10)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately

		err := rl.WaitForCapacity(ctx, model, 10)
		if err == nil {
			t.Fatal(testExpectedErrorMessage("should return error for already-cancelled context"))
		}
	})

	t.Run("negative tokens return error", func(t *testing.T) {
		rl := newTestLimiter(t)
		err := rl.WaitForCapacity(context.Background(), "wait-negative", -1)
		if err == nil {
			t.Fatal(testExpectedErrorMessage())
		}
		if !testContains(err.Error(), "negative tokens") {
			t.Fatal(testContainsMessage(err.Error(), "negative tokens"))
		}
	})
}

func TestRatelimit_NilUsageStats_Ugly(t *testing.T) {
	t.Run("CanSend replaces nil state without panicking", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "nil-cansend"
		rl.Quotas[model] = ModelQuota{MaxRPM: 10, MaxTPM: 100, MaxRPD: 10}
		rl.State[model] = nil
		if recovered := testRecoverPanic(func() {
			if !rl.CanSend(model, 10) {
				t.Fatal(testExpectedTrueMessage())
			}
		}); recovered != nil {
			t.Fatal(testUnexpectedPanicMessage(recovered))
		}
		if testIsNil(rl.State[model]) {
			t.Fatal(testExpectedNonNilMessage())
		}
	})

	t.Run("RecordUsage replaces nil state without panicking", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "nil-record"
		rl.State[model] = nil
		if recovered := testRecoverPanic(func() {
			rl.RecordUsage(model, 10, 10)
		}); recovered != nil {
			t.Fatal(testUnexpectedPanicMessage(recovered))
		}
		if testIsNil(rl.State[model]) {
			t.Fatal(testExpectedNonNilMessage())
		}
		if !testEqual(1, rl.State[model].DayCount) {
			t.Fatal(testWantGotMessage(1, rl.State[model].DayCount))
		}
	})

	t.Run("Stats and AllStats tolerate nil state entries", func(t *testing.T) {
		rl := newTestLimiter(t)
		rl.Quotas["nil-stats"] = ModelQuota{MaxRPM: 1, MaxTPM: 2, MaxRPD: 3}
		rl.State["nil-stats"] = nil
		rl.State["nil-all-stats"] = nil
		if recovered := testRecoverPanic(func() {
			stats := rl.Stats("nil-stats")
			if !testEqual(1, stats.MaxRPM) {
				t.Fatal(testWantGotMessage(1, stats.MaxRPM))
			}
			if !testEqual(0, stats.TPM) {
				t.Fatal(testWantGotMessage(0, stats.TPM))
			}
		}); recovered != nil {
			t.Fatal(testUnexpectedPanicMessage(recovered))
		}
		if recovered := testRecoverPanic(func() {
			all := rl.AllStats()
			if !testContains(all, "nil-stats") {
				t.Fatal(testContainsMessage(all, "nil-stats"))
			}
			if !testContains(all, "nil-all-stats") {
				t.Fatal(testContainsMessage(all, "nil-all-stats"))
			}
		}); recovered !=

			// --- Phase 0: Stats ---
			nil {
			t.Fatal(testUnexpectedPanicMessage(recovered))
		}

	})
}

func TestRatelimit_Stats_Good(t *testing.T) {
	t.Run("returns stats for known model with usage", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "stats-test"
		rl.Quotas[model] = ModelQuota{MaxRPM: 50, MaxTPM: 5000, MaxRPD: 500}
		rl.RecordUsage(model, 100, 200)

		stats := rl.Stats(model)
		if !testEqual(1, stats.RPM) {
			t.Fatal(testWantGotMessage(1, stats.RPM))
		}
		if !testEqual(300, stats.TPM) {
			t.Fatal(testWantGotMessage(300, stats.TPM))
		}
		if !testEqual(1, stats.RPD) {
			t.Fatal(testWantGotMessage(1, stats.RPD))
		}
		if !testEqual(50, stats.MaxRPM) {
			t.Fatal(testWantGotMessage(50, stats.MaxRPM))
		}
		if !testEqual(5000, stats.MaxTPM) {
			t.Fatal(testWantGotMessage(5000, stats.MaxTPM))
		}
		if !testEqual(500, stats.MaxRPD) {
			t.Fatal(testWantGotMessage(500, stats.MaxRPD))
		}
	})

	t.Run("returns empty stats for unknown model", func(t *testing.T) {
		rl := newTestLimiter(t)
		stats := rl.Stats("nonexistent")
		if !testEqual(0, stats.RPM) {
			t.Fatal(testWantGotMessage(0, stats.RPM))
		}
		if !testEqual(0, stats.TPM) {
			t.Fatal(testWantGotMessage(0, stats.TPM))
		}
		if !testEqual(0, stats.RPD) {
			t.Fatal(testWantGotMessage(0, stats.RPD))
		}
		if !testEqual(0, stats.MaxRPM) {
			t.Fatal(testWantGotMessage(0, stats.MaxRPM))
		}
	})

	t.Run("returns quota info for model without usage", func(t *testing.T) {
		rl := newTestLimiter(t)
		model := "quota-only"
		rl.Quotas[model] = ModelQuota{MaxRPM: 100, MaxTPM: 200, MaxRPD: 300}

		stats := rl.Stats(model)
		if !testEqual(0, stats.RPM) {
			t.Fatal(testWantGotMessage(0, stats.RPM, "no usage yet"))
		}
		if !testEqual(100, stats.MaxRPM) {
			t.Fatal(testWantGotMessage(100, stats.MaxRPM, "quota should be present"))
		}
		if !testEqual(200, stats.MaxTPM) {
			t.Fatal(testWantGotMessage(200, stats.MaxTPM))
		}
		if !testEqual(300, stats.MaxRPD) {
			t.Fatal(testWantGotMessage(300, stats.MaxRPD))
		}
	})
}

// --- Phase 0: AllStats ---

func TestRatelimit_AllStats_Good(t *testing.T) {
	t.Run("includes all default quotas plus state-only models", func(t *testing.T) {
		rl := newTestLimiter(t)
		rl.RecordUsage("gemini-3-pro-preview", 1000, 500)
		rl.RecordUsage("custom-model", 100, 200)

		all := rl.AllStats()
		if !testGreaterOrEqual(
			// Should include all default Gemini models + custom-model
			len(all), 6) {
			t.Fatal(testGreaterOrEqualMessage(len(all), 6, "should include default quotas + custom model"))
		}

		pro := all["gemini-3-pro-preview"]
		if !testEqual(1, pro.RPM) {
			t.Fatal(testWantGotMessage(1, pro.RPM))
		}
		if !testEqual(1500, pro.TPM) {
			t.Fatal(testWantGotMessage(1500, pro.TPM))
		}

		custom := all["custom-model"]
		if !testEqual(1, custom.RPM) {
			t.Fatal(testWantGotMessage(1, custom.RPM))
		}
		if !testEqual(300, custom.TPM) {
			t.Fatal(testWantGotMessage(300, custom.TPM))
		}
		if !testEqual(0, custom.MaxRPM) {
			t.Fatal(testWantGotMessage(0, custom.MaxRPM, "custom model has no quota"))
		}
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
		if !testEqual(0, stats.RPM) {
			t.Fatal(testWantGotMessage(0, stats.RPM, "old requests should be pruned"))
		}
		if !testEqual(0, stats.TPM) {
			t.Fatal(testWantGotMessage(0, stats.TPM, "old tokens should be pruned"))
		}
		if !testEqual(2, stats.RPD) {
			t.Fatal(testWantGotMessage(2, stats.RPD, "daily count survives prune"))
		}
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
		if !testEqual(0, stats.RPD) {
			t.Fatal(testWantGotMessage(0, stats.RPD, "daily count should be reset after 24h"))
		}
	})
}

// --- Phase 0: Persist and Load ---

func TestRatelimit_PersistAndLoad_Ugly(t *testing.T) {
	t.Run("round-trip preserves state", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := testPath(tmpDir, "ratelimits.yaml")

		rl1, err := New()
		if err != nil {
			t.Fatal(testUnexpectedErrorMessage(err))
		}
		rl1.filePath = path
		model := "persist-test"
		rl1.Quotas[model] = ModelQuota{MaxRPM: 50, MaxTPM: 5000, MaxRPD: 500}
		rl1.RecordUsage(model, 100, 100)
		if err := rl1.Persist(); err != nil {
			t.Fatal(testUnexpectedErrorMessage(err))
		}

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
	})

	t.Run("load from non-existent file is not an error", func(t *testing.T) {
		rl := newTestLimiter(t)
		rl.filePath = testPath(t.TempDir(), "does-not-exist.yaml")

		err := rl.Load()
		if err != nil {
			t.Fatal(testUnexpectedErrorMessage(err, "loading non-existent file should not error"))
		}
	})

	t.Run("load from corrupt YAML returns error", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := testPath(tmpDir, "corrupt.yaml")
		writeTestFile(t, path, "{{{{invalid yaml!!!!")

		rl := newTestLimiter(t)
		rl.filePath = path

		err := rl.Load()
		if err == nil {
			t.Fatal(testExpectedErrorMessage("corrupt YAML should produce an error"))
		}
	})

	t.Run("load from unreadable file returns error", func(t *testing.T) {
		if isRootUser() {
			t.Skip("chmod 000 does not restrict root")
		}
		tmpDir := t.TempDir()
		path := testPath(tmpDir, "unreadable.yaml")
		writeTestFile(t, path, "quotas: {}")
		setPathMode(t, path, 0o000)

		rl := newTestLimiter(t)
		rl.filePath = path

		err := rl.Load()
		if err == nil {
			t.Fatal(testExpectedErrorMessage("unreadable file should produce an error"))
		}

		// Clean up permissions for temp dir cleanup
		_ = syscall.Chmod(path, 0o644)
	})

	t.Run("persist to nested non-existent directory creates it", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := testPath(tmpDir, "nested", "deep", "ratelimits.yaml")

		rl := newTestLimiter(t)
		rl.filePath = path
		rl.RecordUsage("test", 1, 1)

		err := rl.Persist()
		if err != nil {
			t.Fatal(testUnexpectedErrorMessage(err, "should create nested directories"))
		}
		if !pathExists(path) {
			t.Fatal(testExpectedTrueMessage("file should exist"))
		}
	})

	t.Run("persist to unwritable directory returns error", func(t *testing.T) {
		if isRootUser() {
			t.Skip("chmod 0555 does not restrict root")
		}
		tmpDir := t.TempDir()
		unwritable := testPath(tmpDir, "readonly")
		ensureTestDir(t, unwritable)
		setPathMode(t, unwritable, 0o555)

		rl := newTestLimiter(t)
		rl.filePath = testPath(unwritable, "sub", "ratelimits.yaml")

		err := rl.Persist()
		if err == nil {
			t.Fatal(testExpectedErrorMessage("should fail when directory is unwritable"))
		}

		// Clean up
		_ = syscall.Chmod(unwritable, 0o755)
	})
}

// --- Phase 0: Default quotas ---

func TestRatelimit_DefaultQuotas_Good(t *testing.T) {
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
			if !ok {
				t.Fatal(testExpectedTrueMessage(fmt.Sprintf("quota should exist for %s", tt.model)))
			}
			if !testEqual(tt.maxRPM, q.MaxRPM) {
				t.Fatal(testWantGotMessage(tt.maxRPM, q.MaxRPM))
			}
			if !testEqual(tt.maxTPM, q.MaxTPM) {
				t.Fatal(testWantGotMessage(tt.maxTPM, q.MaxTPM))
			}
			if !testEqual(tt.maxRPD, q.MaxRPD) {
				t.Fatal(testWantGotMessage(tt.maxRPD, q.MaxRPD))
			}
		})
	}
}

// --- Phase 0: Concurrent access (race test) ---

func TestRatelimit_ConcurrentAccess_Good(t *testing.T) {
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
	if !testEqual(expected, stats.RPD) {
		t.Fatal(testWantGotMessage(expected, stats.RPD, "all recordings should be counted"))
	}
}

func TestRatelimit_ConcurrentResetAndRecord_Ugly(t *testing.T) {
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

func TestRatelimit_BackgroundPrune_Good(t *testing.T) {
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
	if !testEventually(

		// Wait for pruner to run.
		func() bool {
			rl.mu.Lock()
			defer rl.mu.Unlock()
			_, exists := rl.State[model]
			return !exists
		}, 1*time.Second, 20*time.Millisecond) {
		t.Fatal(testEventuallyMessage("old empty state should be pruned"))
	}

	t.Run("non-positive interval is a safe no-op", func(t *testing.T) {
		rl := newTestLimiter(t)
		rl.State["still-here"] = &UsageStats{
			Requests: []time.Time{time.Now().Add(-2 * time.Minute)},
		}
		if recovered := testRecoverPanic(func() {
			stop := rl.BackgroundPrune(0)
			stop()
		}); recovered != nil {
			t.Fatal(testUnexpectedPanicMessage(recovered))
		}
		if !testContains(rl.State, "still-here") {
			t.Fatal(testContainsMessage(rl.State, "still-here"))
		}
	})
}

// --- Phase 0: CountTokens (with mock HTTP server) ---

func TestRatelimit_CountTokens_Ugly(t *testing.T) {
	t.Run("successful token count", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !testEqual(http.MethodPost, r.Method) {
				t.Fatal(testWantGotMessage(http.MethodPost, r.Method))
			}
			if !testEqual("application/json", r.Header.Get("Content-Type")) {
				t.Fatal(testWantGotMessage("application/json", r.Header.Get("Content-Type")))
			}
			if !testEqual("test-api-key", r.Header.Get("x-goog-api-key")) {
				t.Fatal(testWantGotMessage("test-api-key", r.Header.Get("x-goog-api-key")))
			}
			if !testEqual("/v1beta/models/test-model:countTokens", r.URL.EscapedPath()) {
				t.Fatal(testWantGotMessage("/v1beta/models/test-model:countTokens", r.URL.EscapedPath()))
			}

			var body struct {
				Contents []struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"contents"`
			}
			decodeJSONBody(t, r.Body, &body)
			if !testHasLen(body.Contents, 1) {
				t.Fatal(testLenMessage(body.Contents, 1))
			}
			if !testHasLen(body.Contents[0].Parts, 1) {
				t.Fatal(testLenMessage(body.Contents[0].Parts, 1))
			}
			if !testEqual("hello", body.Contents[0].Parts[0].Text) {
				t.Fatal(testWantGotMessage("hello", body.Contents[0].Parts[0].Text))
			}

			w.Header().Set("Content-Type", "application/json")
			writeJSONBody(t, w, map[string]int{"totalTokens": 42})
		}))
		defer server.Close()

		tokens, err := countTokensWithClient(context.Background(), server.Client(), server.URL, "test-api-key", "test-model", "hello")
		if err != nil {
			t.Fatal(testUnexpectedErrorMessage(err))
		}
		if !testEqual(42, tokens) {
			t.Fatal(testWantGotMessage(42, tokens))
		}
	})

	t.Run("model name is path escaped", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !testEqual("/v1beta/models/folder%2Fmodel%3Fdebug=1:countTokens", r.URL.EscapedPath()) {
				t.Fatal(testWantGotMessage("/v1beta/models/folder%2Fmodel%3Fdebug=1:countTokens", r.URL.EscapedPath()))
			}
			if !testIsEmpty(r.URL.RawQuery) {
				t.Fatal(testEmptyMessage(r.URL.RawQuery))
			}
			w.Header().Set("Content-Type", "application/json")
			writeJSONBody(t, w, map[string]int{"totalTokens": 7})
		}))
		defer server.Close()

		tokens, err := countTokensWithClient(context.Background(), server.Client(), server.URL, "test-api-key", "folder/model?debug=1", "hello")
		if err != nil {
			t.Fatal(testUnexpectedErrorMessage(err))
		}
		if !testEqual(7, tokens) {
			t.Fatal(testWantGotMessage(7, tokens))
		}
	})

	t.Run("API error body is truncated", func(t *testing.T) {
		largeBody := repeatString("x", countTokensErrorBodyLimit+256)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, err := io.WriteString(w, largeBody)
			if err != nil {
				t.Fatal(testUnexpectedErrorMessage(err))
			}
		}))
		defer server.Close()

		_, err := countTokensWithClient(context.Background(), server.Client(), server.URL, "fake-key", "test-model", "hello")
		if err == nil {
			t.Fatal(testExpectedErrorMessage())
		}
		if !testContains(err.Error(), "api error status 401") {
			t.Fatal(testContainsMessage(err.Error(), "api error status 401"))
		}
		if !(substringCount(err.Error(), "x") < len(largeBody)) {
			t.Fatal(testExpectedTrueMessage("error body should be bounded"))
		}
		if !testContains(err.Error(), "...") {
			t.Fatal(testContainsMessage(err.Error(), "..."))
		}
	})

	t.Run("empty model is rejected before request", func(t *testing.T) {
		_, err := CountTokens(context.Background(), "fake-key", "", "hello")
		if err == nil {
			t.Fatal(testExpectedErrorMessage())
		}
		if !testContains(err.Error(), "build url") {
			t.Fatal(testContainsMessage(err.Error(), "build url"))
		}
	})

	t.Run("invalid base URL returns error", func(t *testing.T) {
		_, err := countTokensWithClient(context.Background(), http.DefaultClient, "://bad-url", "fake-key", "test-model", "hello")
		if err == nil {
			t.Fatal(testExpectedErrorMessage())
		}
		if !testContains(err.Error(), "build url") {
			t.Fatal(testContainsMessage(err.Error(), "build url"))
		}
	})

	t.Run("base URL without host returns error", func(t *testing.T) {
		_, err := countTokensWithClient(context.Background(), http.DefaultClient, "/relative", "fake-key", "test-model", "hello")
		if err == nil {
			t.Fatal(testExpectedErrorMessage())
		}
		if !testContains(err.Error(), "build url") {
			t.Fatal(testContainsMessage(err.Error(), "build url"))
		}
	})

	t.Run("invalid JSON response returns error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"totalTokens":`))
			if err != nil {
				t.Fatal(testUnexpectedErrorMessage(err))
			}
		}))
		defer server.Close()

		_, err := countTokensWithClient(context.Background(), server.Client(), server.URL, "fake-key", "test-model", "hello")
		if err == nil {
			t.Fatal(testExpectedErrorMessage())
		}
		if !testContains(err.Error(), "decode response") {
			t.Fatal(testContainsMessage(err.Error(), "decode response"))
		}
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
		if err == nil {
			t.Fatal(testExpectedErrorMessage())
		}
		if !testContains(err.Error(), "read error body") {
			t.Fatal(testContainsMessage(err.Error(), "read error body"))
		}
	})

	t.Run("nil client falls back to http.DefaultClient", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			writeJSONBody(t, w, map[string]int{"totalTokens": 11})
		}))
		defer server.Close()

		originalClient := http.DefaultClient
		http.DefaultClient = server.Client()
		defer func() {
			http.DefaultClient = originalClient
		}()

		tokens, err := countTokensWithClient(context.Background(), nil, server.URL, "fake-key", "test-model", "hello")
		if err != nil {
			t.Fatal(testUnexpectedErrorMessage(err))
		}
		if !testEqual(11, tokens) {
			t.Fatal(testWantGotMessage(11, tokens))
		}
	})
}

func TestRatelimit_PersistSkipsNilState_Good(t *testing.T) {
	path := testPath(t.TempDir(), "nil-state.yaml")

	rl, err := New()
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	rl.filePath = path
	rl.State["nil-model"] = nil
	if err := rl.Persist(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	rl2, err := New()
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	rl2.filePath = path
	if err := rl2.Load(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if testContains(rl2.State, "nil-model") {
		t.Fatal(testNotContainsMessage(rl2.State, "nil-model"))
	}
}

func TestRatelimit_TokenTotals_Good(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	if !testEqual(25, safeTokenSum(-100, 25)) {
		t.Fatal(testWantGotMessage(25, safeTokenSum(-100, 25)))
	}
	if !testEqual(maxInt, safeTokenSum(maxInt, 1)) {
		t.Fatal(testWantGotMessage(maxInt, safeTokenSum(maxInt, 1)))
	}
	if !testEqual(10, totalTokenCount([]TokenEntry{{Count: -5}, {Count: 10}})) {
		t.Fatal(testWantGotMessage(10, totalTokenCount([]TokenEntry{{Count: -5}, {Count: 10}})))
	}
}

func TestRatelimit_ThreatClockSkewFuturePersistedEntries(t *testing.T) {
	rl := newTestLimiter(t)
	model := "threat-clock-skew-future"
	rl.Quotas[model] = ModelQuota{MaxRPM: 1, MaxTPM: 100, MaxRPD: 0}

	now := time.Unix(1_700_000_000, 0)
	rl.now = func() time.Time { return now }
	future := now.Add(10 * time.Minute)
	rl.State[model] = &UsageStats{
		Requests: []time.Time{future},
		Tokens:   []TokenEntry{{Time: future, Count: 90}},
		DayStart: future,
	}

	decision := rl.Decide(model, 1)
	require.False(t, decision.Allowed)
	assert.Equal(t, DecisionRPMLimit, decision.Code)
	assert.True(t, rl.State[model].Requests[0].Equal(now))
	assert.True(t, rl.State[model].Tokens[0].Time.Equal(now))
	assert.True(t, rl.State[model].DayStart.Equal(now))

	now = now.Add(61 * time.Second)
	decision = rl.Decide(model, 1)
	assert.True(t, decision.Allowed)
}

func TestRatelimit_ThreatIntegerOverflowRetryAfterForTokens(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	retryAfter := retryAfterForTokens(now, []TokenEntry{{Time: now, Count: maxInt()}}, 10, 1)

	assert.Equal(t, time.Minute, retryAfter)
}

func TestRatelimit_ThreatIntegerOverflowDayCountSaturates(t *testing.T) {
	rl := newTestLimiter(t)
	model := "threat-day-count-overflow"
	now := time.Unix(1_700_000_000, 0)
	rl.now = func() time.Time { return now }
	rl.State[model] = &UsageStats{DayStart: now, DayCount: maxInt()}

	rl.RecordUsage(model, 1, 1)

	assert.Equal(t, maxInt(), rl.State[model].DayCount)
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

func TestRatelimit_DefaultProfiles_Good(t *testing.T) {
	profiles := DefaultProfiles()

	t.Run("contains all four providers", func(t *testing.T) {
		if !testContains(profiles, ProviderGemini) {
			t.Fatal(testContainsMessage(profiles, ProviderGemini))
		}
		if !testContains(profiles, ProviderOpenAI) {
			t.Fatal(testContainsMessage(profiles, ProviderOpenAI))
		}
		if !testContains(profiles, ProviderAnthropic) {
			t.Fatal(testContainsMessage(profiles, ProviderAnthropic))
		}
		if !testContains(profiles, ProviderLocal) {
			t.Fatal(testContainsMessage(profiles, ProviderLocal))
		}
	})

	t.Run("Gemini profile has expected models", func(t *testing.T) {
		gemini := profiles[ProviderGemini]
		if !testEqual(ProviderGemini, gemini.Provider) {
			t.Fatal(testWantGotMessage(ProviderGemini, gemini.Provider))
		}
		if !testContains(gemini.Models, "gemini-3-pro-preview") {
			t.Fatal(testContainsMessage(gemini.Models, "gemini-3-pro-preview"))
		}
		if !testContains(gemini.Models, "gemini-2.0-flash") {
			t.Fatal(testContainsMessage(gemini.Models, "gemini-2.0-flash"))
		}
		if !testContains(gemini.Models, "gemini-2.0-flash-lite") {
			t.Fatal(testContainsMessage(gemini.Models, "gemini-2.0-flash-lite"))
		}
	})

	t.Run("OpenAI profile has expected models", func(t *testing.T) {
		openai := profiles[ProviderOpenAI]
		if !testEqual(ProviderOpenAI, openai.Provider) {
			t.Fatal(testWantGotMessage(ProviderOpenAI, openai.Provider))
		}
		if !testContains(openai.Models, "gpt-4o") {
			t.Fatal(testContainsMessage(openai.Models, "gpt-4o"))
		}
		if !testContains(openai.Models, "gpt-4o-mini") {
			t.Fatal(testContainsMessage(openai.Models, "gpt-4o-mini"))
		}
		if !testContains(openai.Models, "o3-mini") {
			t.Fatal(testContainsMessage(openai.Models, "o3-mini"))
		}
	})

	t.Run("Anthropic profile has expected models", func(t *testing.T) {
		anthropic := profiles[ProviderAnthropic]
		if !testEqual(ProviderAnthropic, anthropic.Provider) {
			t.Fatal(testWantGotMessage(ProviderAnthropic, anthropic.Provider))
		}
		if !testContains(anthropic.Models, "claude-opus-4") {
			t.Fatal(testContainsMessage(anthropic.Models, "claude-opus-4"))
		}
		if !testContains(anthropic.Models, "claude-sonnet-4") {
			t.Fatal(testContainsMessage(anthropic.Models, "claude-sonnet-4"))
		}
		if !testContains(anthropic.Models, "claude-haiku-3.5") {
			t.Fatal(testContainsMessage(anthropic.Models, "claude-haiku-3.5"))
		}
	})

	t.Run("Local profile has no models by default", func(t *testing.T) {
		local := profiles[ProviderLocal]
		if !testEqual(ProviderLocal, local.Provider) {
			t.Fatal(testWantGotMessage(ProviderLocal, local.Provider))
		}
		if !testIsEmpty(local.Models) {
			t.Fatal(testEmptyMessage(local.Models, "local provider should have no default quotas"))
		}
	})
}

func TestRatelimit_NewWithConfig_Ugly(t *testing.T) {
	t.Run("empty config defaults to Gemini", func(t *testing.T) {
		rl, err := NewWithConfig(Config{
			FilePath: testPath(t.TempDir(), "test.yaml"),
		})
		if err != nil {
			t.Fatal(testUnexpectedErrorMessage(err))
		}

		_, hasGemini := rl.Quotas["gemini-3-pro-preview"]
		if !hasGemini {
			t.Fatal(testExpectedTrueMessage("empty config should load Gemini defaults"))
		}
	})

	t.Run("single provider loads only its models", func(t *testing.T) {
		rl, err := NewWithConfig(Config{
			FilePath:  testPath(t.TempDir(), "test.yaml"),
			Providers: []Provider{ProviderOpenAI},
		})
		if err != nil {
			t.Fatal(testUnexpectedErrorMessage(err))
		}

		_, hasGPT := rl.Quotas["gpt-4o"]
		if !hasGPT {
			t.Fatal(testExpectedTrueMessage("should have OpenAI models"))
		}

		_, hasGemini := rl.Quotas["gemini-3-pro-preview"]
		if hasGemini {
			t.Fatal(testExpectedFalseMessage("should not have Gemini models"))
		}
	})

	t.Run("multiple providers merge models", func(t *testing.T) {
		rl, err := NewWithConfig(Config{
			FilePath:  testPath(t.TempDir(), "test.yaml"),
			Providers: []Provider{ProviderGemini, ProviderAnthropic},
		})
		if err != nil {
			t.Fatal(testUnexpectedErrorMessage(err))
		}

		_, hasGemini := rl.Quotas["gemini-3-pro-preview"]
		_, hasClaude := rl.Quotas["claude-opus-4"]
		if !hasGemini {
			t.Fatal(testExpectedTrueMessage("should have Gemini models"))
		}
		if !hasClaude {
			t.Fatal(testExpectedTrueMessage("should have Anthropic models"))
		}

		_, hasGPT := rl.Quotas["gpt-4o"]
		if hasGPT {
			t.Fatal(testExpectedFalseMessage("should not have OpenAI models"))
		}
	})

	t.Run("explicit quotas override provider defaults", func(t *testing.T) {
		rl, err := NewWithConfig(Config{
			FilePath:  testPath(t.TempDir(), "test.yaml"),
			Providers: []Provider{ProviderGemini},
			Quotas: map[string]ModelQuota{
				"gemini-3-pro-preview": {MaxRPM: 999, MaxTPM: 888, MaxRPD: 777},
			},
		})
		if err != nil {
			t.Fatal(testUnexpectedErrorMessage(err))
		}

		q := rl.Quotas["gemini-3-pro-preview"]
		if !testEqual(999, q.MaxRPM) {
			t.Fatal(testWantGotMessage(999, q.MaxRPM, "explicit quota should override provider default"))
		}
		if !testEqual(888, q.MaxTPM) {
			t.Fatal(testWantGotMessage(888, q.MaxTPM))
		}
		if !testEqual(777, q.MaxRPD) {
			t.Fatal(testWantGotMessage(777, q.MaxRPD))
		}
	})

	t.Run("explicit quotas without providers", func(t *testing.T) {
		rl, err := NewWithConfig(Config{
			FilePath: testPath(t.TempDir(), "test.yaml"),
			Quotas: map[string]ModelQuota{
				"my-custom-model": {MaxRPM: 10, MaxTPM: 1000, MaxRPD: 50},
			},
		})
		if err != nil {
			t.Fatal(testUnexpectedErrorMessage(err))
		}
		if !testHasLen(rl.Quotas, 1) {
			t.Fatal(testLenMessage(rl.Quotas, 1, "should only have the explicit quota"))
		}
		q := rl.Quotas["my-custom-model"]
		if !testEqual(10, q.MaxRPM) {
			t.Fatal(testWantGotMessage(10, q.MaxRPM))
		}
	})

	t.Run("custom file path is respected", func(t *testing.T) {
		customPath := testPath(t.TempDir(), "custom", "limits.yaml")
		rl, err := NewWithConfig(Config{
			FilePath:  customPath,
			Providers: []Provider{ProviderLocal},
		})
		if err != nil {
			t.Fatal(testUnexpectedErrorMessage(err))
		}

		rl.RecordUsage("test", 1, 1)
		if err := rl.Persist(); err != nil {
			t.Fatal(testUnexpectedErrorMessage(err))
		}
		if !pathExists(customPath) {
			t.Fatal(testExpectedTrueMessage("file should be created at custom path"))
		}
	})

	t.Run("unknown provider is silently skipped", func(t *testing.T) {
		rl, err := NewWithConfig(Config{
			FilePath:  testPath(t.TempDir(), "test.yaml"),
			Providers: []Provider{"nonexistent-provider"},
		})
		if err != nil {
			t.Fatal(testUnexpectedErrorMessage(err))
		}
		if !testIsEmpty(rl.Quotas) {
			t.Fatal(testEmptyMessage(rl.Quotas, "unknown provider should produce no quotas"))
		}
	})

	t.Run("local provider with custom quotas", func(t *testing.T) {
		rl, err := NewWithConfig(Config{
			FilePath:  testPath(t.TempDir(), "test.yaml"),
			Providers: []Provider{ProviderLocal},
			Quotas: map[string]ModelQuota{
				"llama-3.3-70b": {MaxRPM: 5, MaxTPM: 50000, MaxRPD: 0},
			},
		})
		if err != nil {
			t.Fatal(testUnexpectedErrorMessage(err))
		}
		if !testHasLen(rl.Quotas, 1) {
			t.Fatal(testLenMessage(rl.Quotas, 1, "should only have the custom local model"))
		}
		q := rl.Quotas["llama-3.3-70b"]
		if !testEqual(5, q.MaxRPM) {
			t.Fatal(testWantGotMessage(5, q.MaxRPM))
		}
		if !testEqual(50000, q.MaxTPM) {
			t.Fatal(testWantGotMessage(50000, q.MaxTPM))
		}
	})

	t.Run("invalid backend returns error", func(t *testing.T) {
		_, err := NewWithConfig(Config{
			Backend: "bogus",
		})
		if err == nil {
			t.Fatal(testExpectedErrorMessage())
		}
		if !testContains(err.Error(), "unknown backend") {
			t.Fatal(testContainsMessage(err.Error(), "unknown backend"))
		}
	})

	t.Run("default YAML path uses home directory", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", "")
		t.Setenv("home", "")

		rl, err := NewWithConfig(Config{})
		if err != nil {
			t.Fatal(testUnexpectedErrorMessage(err))
		}
		if !testEqual(testPath(home, defaultStateDirName, defaultYAMLStateFile), rl.filePath) {
			t.Fatal(testWantGotMessage(testPath(home, defaultStateDirName, defaultYAMLStateFile), rl.filePath))
		}
	})
}

func TestRatelimit_NewBackwardCompatibility_Good(t *testing.T) {
	// New() should produce the exact same result as before Phase 1
	rl, err := New()
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	expected := map[string]ModelQuota{
		"gemini-3-pro-preview":   {MaxRPM: 150, MaxTPM: 1000000, MaxRPD: 1000},
		"gemini-3-flash-preview": {MaxRPM: 150, MaxTPM: 1000000, MaxRPD: 1000},
		"gemini-2.5-pro":         {MaxRPM: 150, MaxTPM: 1000000, MaxRPD: 1000},
		"gemini-2.0-flash":       {MaxRPM: 150, MaxTPM: 1000000, MaxRPD: 0},
		"gemini-2.0-flash-lite":  {MaxRPM: 0, MaxTPM: 0, MaxRPD: 0},
	}
	if !testHasLen(rl.Quotas, len(expected)) {
		t.Fatal(testLenMessage(rl.Quotas, len(expected), "New() should produce exactly the Gemini defaults"))
	}
	for model, expectedQ := range expected {
		t.Run(model, func(t *testing.T) {
			q, ok := rl.Quotas[model]
			if !ok {
				t.Fatal(testExpectedTrueMessage("quota should exist"))
			}
			if !testEqual(expectedQ, q) {
				t.Fatal(testWantGotMessage(expectedQ, q))
			}
		})
	}
}

func TestRatelimit_SetQuota_Good(t *testing.T) {
	t.Run("adds new model quota", func(t *testing.T) {
		rl := newTestLimiter(t)
		rl.SetQuota("custom-model", ModelQuota{MaxRPM: 42, MaxTPM: 9999, MaxRPD: 100})

		q, ok := rl.Quotas["custom-model"]
		if !ok {
			t.Fatal(testExpectedTrueMessage())
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
	})

	t.Run("overwrites existing model quota", func(t *testing.T) {
		rl := newTestLimiter(t)
		rl.SetQuota("gemini-3-pro-preview", ModelQuota{MaxRPM: 1, MaxTPM: 1, MaxRPD: 1})

		q := rl.Quotas["gemini-3-pro-preview"]
		if !testEqual(1, q.MaxRPM) {
			t.Fatal(testWantGotMessage(1, q.MaxRPM, "should overwrite existing"))
		}
	})

	t.Run("is safe for concurrent use", func(t *testing.T) {
		rl := newTestLimiter(t)
		var wg sync.WaitGroup

		for i := range 10 {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				model := core.Sprintf("model-%d", n)
				rl.SetQuota(model, ModelQuota{MaxRPM: n, MaxTPM: n * 100, MaxRPD: n * 10})
			}(i)
		}
		wg.Wait()
		if !testGreaterOrEqual(len(rl.Quotas), 10) {
			t.Fatal(testGreaterOrEqualMessage(len(rl.Quotas), 10, "all concurrent SetQuota calls should succeed"))
		}
	})
}

func TestRatelimit_AddProvider_Good(t *testing.T) {
	t.Run("adds OpenAI models to existing limiter", func(t *testing.T) {
		rl := newTestLimiter(t) // starts with Gemini defaults
		geminiCount := len(rl.Quotas)

		rl.AddProvider(ProviderOpenAI)
		if !testGreater(

			// Should now have both Gemini and OpenAI models
			len(rl.Quotas), geminiCount) {
			t.Fatal(testGreaterMessage(len(rl.Quotas), geminiCount, "should have more models after adding OpenAI"))
		}
		_, hasGPT := rl.Quotas["gpt-4o"]
		if !hasGPT {
			t.Fatal(testExpectedTrueMessage("should have OpenAI models"))
		}

		// Gemini models should still be present
		_, hasGemini := rl.Quotas["gemini-3-pro-preview"]
		if !hasGemini {
			t.Fatal(testExpectedTrueMessage("Gemini models should remain"))
		}
	})

	t.Run("adds Anthropic models to existing limiter", func(t *testing.T) {
		rl := newTestLimiter(t)
		rl.AddProvider(ProviderAnthropic)

		_, hasClaude := rl.Quotas["claude-opus-4"]
		if !hasClaude {
			t.Fatal(testExpectedTrueMessage("should have Anthropic models"))
		}
	})

	t.Run("unknown provider is a noop", func(t *testing.T) {
		rl := newTestLimiter(t)
		before := len(rl.Quotas)

		rl.AddProvider("nonexistent")
		if !testEqual(before, len(rl.Quotas)) {
			t.Fatal(testWantGotMessage(before, len(rl.Quotas), "unknown provider should not change quotas"))
		}
	})

	t.Run("adding local provider does not remove existing quotas", func(t *testing.T) {
		rl := newTestLimiter(t)
		before := len(rl.Quotas)

		rl.AddProvider(ProviderLocal)
		if !testEqual(before, len(rl.Quotas)) {
			t.Fatal(testWantGotMessage(before, len(rl.Quotas), "local provider has no models, count unchanged"))
		}
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

func TestRatelimit_ProviderConstants_Good(t *testing.T) {
	// Verify the string values are stable (they may be used in YAML configs)
	if !testEqual(Provider("gemini"), ProviderGemini) {
		t.Fatal(testWantGotMessage(Provider("gemini"), ProviderGemini))
	}
	if !testEqual(Provider("openai"), ProviderOpenAI) {
		t.Fatal(testWantGotMessage(Provider("openai"), ProviderOpenAI))
	}
	if !testEqual(Provider("anthropic"), ProviderAnthropic) {
		t.Fatal(testWantGotMessage(Provider("anthropic"), ProviderAnthropic))
	}
	if !testEqual(Provider("local"), ProviderLocal) {
		t.Fatal(testWantGotMessage(Provider("local"), ProviderLocal))
	}
}

// --- Phase 0 addendum: Additional concurrent and multi-model race tests ---

func TestRatelimit_ConcurrentMultipleModels_Good(t *testing.T) {
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
		if !testEqual(iterations, stats.RPD) {
			t.Fatal(testWantGotMessage(iterations, stats.RPD, "each model should have correct RPD"))
		}
	}
}

func TestRatelimit_ConcurrentPersistAndLoad_Ugly(t *testing.T) {
	tmpDir := t.TempDir()
	path := testPath(tmpDir, "concurrent.yaml")

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

func TestRatelimit_ConcurrentAllStatsAndRecordUsage_Good(t *testing.T) {
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

func TestRatelimit_ConcurrentWaitForCapacityAndRecordUsage_Good(t *testing.T) {
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
	path := testPath(tmpDir, "bench.yaml")

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

func TestRatelimit_EndToEndMultiProvider_Good(t *testing.T) {
	// Simulate a real-world scenario: limiter for both Gemini and Anthropic
	rl, err := NewWithConfig(Config{
		FilePath:  testPath(t.TempDir(), "multi.yaml"),
		Providers: []Provider{ProviderGemini, ProviderAnthropic},
	})
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	// Use Gemini model
	if !rl.CanSend("gemini-3-pro-preview", 1000) {
		t.Fatal(testExpectedTrueMessage())
	}
	rl.RecordUsage("gemini-3-pro-preview", 500, 500)

	// Use Anthropic model
	if !rl.CanSend("claude-opus-4", 1000) {
		t.Fatal(testExpectedTrueMessage())
	}
	rl.RecordUsage("claude-opus-4", 500, 500)

	// Check stats for both
	geminiStats := rl.Stats("gemini-3-pro-preview")
	if !testEqual(1, geminiStats.RPM) {
		t.Fatal(testWantGotMessage(1, geminiStats.RPM))
	}
	if !testEqual(150, geminiStats.MaxRPM) {
		t.Fatal(testWantGotMessage(150, geminiStats.MaxRPM))
	}

	claudeStats := rl.Stats("claude-opus-4")
	if !testEqual(1, claudeStats.RPM) {
		t.Fatal(testWantGotMessage(1, claudeStats.RPM))
	}
	if !testEqual(50, claudeStats.MaxRPM) {
		t.Fatal(testWantGotMessage(50, claudeStats.MaxRPM))
	}
	if err :=

		// Persist and reload
		rl.Persist(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	rl2, err := NewWithConfig(Config{
		FilePath:  rl.filePath,
		Providers: []Provider{ProviderGemini, ProviderAnthropic},
	})
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}
	if err := rl2.Load(); err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	reloaded := rl2.Stats("claude-opus-4")
	if !testEqual(1, reloaded.RPM) {
		t.Fatal(testWantGotMessage(1, reloaded.RPM, "state should survive persist/reload"))
	}
}

func TestRatelimit_CanSend_Bad(t *testing.T) {
	rl := newTestLimiter(t)
	model := "cansend-rpm-over-limit"
	rl.Quotas[model] = ModelQuota{MaxRPM: 2, MaxTPM: 1000000, MaxRPD: 100}

	rl.RecordUsage(model, 10, 10)
	rl.RecordUsage(model, 10, 10)
	if rl.CanSend(model, 10) {
		t.Fatal(testExpectedFalseMessage("should reject when RPM quota is exhausted"))
	}
}
