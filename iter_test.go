package ratelimit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIterators(t *testing.T) {
	rl, err := NewWithConfig(Config{
		Quotas: map[string]ModelQuota{
			"model-c": {MaxRPM: 10},
			"model-a": {MaxRPM: 10},
		},
	})
	require.NoError(t, err)

	rl.RecordUsage("model-b", 1, 1)

	t.Run("Models iterator is sorted", func(t *testing.T) {
		var models []string
		for m := range rl.Models() {
			models = append(models, m)
		}
		// Should include Gemini defaults (from NewWithConfig's default) + custom models
		// and be sorted.
		assert.Contains(t, models, "model-a")
		assert.Contains(t, models, "model-b")
		assert.Contains(t, models, "model-c")

		// Check sorting of our specific models
		foundA, foundB, foundC := -1, -1, -1
		for i, m := range models {
			if m == "model-a" {
				foundA = i
			}
			if m == "model-b" {
				foundB = i
			}
			if m == "model-c" {
				foundC = i
			}
		}
		assert.True(t, foundA < foundB && foundB < foundC, "models should be sorted: a < b < c")
	})

	t.Run("Iter iterator is sorted", func(t *testing.T) {
		var models []string
		for m, stats := range rl.Iter() {
			models = append(models, m)
			if m == "model-a" {
				assert.Equal(t, 10, stats.MaxRPM)
			}
		}
		assert.Contains(t, models, "model-a")
		assert.Contains(t, models, "model-b")
		assert.Contains(t, models, "model-c")

		// Check sorting
		foundA, foundB, foundC := -1, -1, -1
		for i, m := range models {
			if m == "model-a" {
				foundA = i
			}
			if m == "model-b" {
				foundB = i
			}
			if m == "model-c" {
				foundC = i
			}
		}
		assert.True(t, foundA < foundB && foundB < foundC, "iter should be sorted: a < b < c")
	})
}

func TestIterEarlyBreak(t *testing.T) {
	rl, err := NewWithConfig(Config{
		Quotas: map[string]ModelQuota{
			"model-a": {MaxRPM: 10},
			"model-b": {MaxRPM: 20},
			"model-c": {MaxRPM: 30},
		},
	})
	require.NoError(t, err)

	t.Run("Iter breaks early", func(t *testing.T) {
		var count int
		for range rl.Iter() {
			count++
			if count == 1 {
				break
			}
		}
		assert.Equal(t, 1, count, "should stop after first iteration")
	})

	t.Run("Models early break via manual iteration", func(t *testing.T) {
		var count int
		for range rl.Models() {
			count++
			if count == 2 {
				break
			}
		}
		assert.Equal(t, 2, count, "should stop after two models")
	})
}

func TestCountTokensFull(t *testing.T) {
	t.Run("empty model is rejected", func(t *testing.T) {
		_, err := CountTokens(context.Background(), "key", "", "text")
		assert.Error(t, err)
	})

	t.Run("API error non-200", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("bad request"))
		}))
		defer server.Close()

		_, err := countTokensWithClient(context.Background(), server.Client(), server.URL, "key", "model", "text")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "status 400")
	})

	t.Run("context cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := countTokensWithClient(ctx, http.DefaultClient, "https://generativelanguage.googleapis.com", "key", "model", "text")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "do request")
	})
}
