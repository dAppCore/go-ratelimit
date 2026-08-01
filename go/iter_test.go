// SPDX-License-Identifier: EUPL-1.2

package ratelimit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIter_Iterators_Case(t *testing.T) {
	rl, err := NewWithConfig(Config{
		Quotas: map[string]ModelQuota{
			"model-c": {MaxRPM: 10},
			"model-a": {MaxRPM: 10},
		},
	})
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	rl.RecordUsage("model-b", 1, 1)

	t.Run("Models iterator is sorted", func(t *testing.T) {
		var models []string
		for m := range rl.Models() {
			models = append(models, m)
		}
		if !testContains(
			// Should include Gemini defaults (from NewWithConfig's default) + custom models
			// and be sorted.
			models, "model-a") {
			t.Fatal(testContainsMessage(models, "model-a"))
		}
		if !testContains(models, "model-b") {
			t.Fatal(testContainsMessage(models, "model-b"))
		}
		if !testContains(models, "model-c") {
			t.Fatal(testContainsMessage(models, "model-c"))
		}

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
		if !(foundA < foundB && foundB < foundC) {
			t.Fatal(testExpectedTrueMessage("models should be sorted: a < b < c"))
		}
	})

	t.Run("Iter iterator is sorted", func(t *testing.T) {
		var models []string
		for m, stats := range rl.Iter() {
			models = append(models, m)
			if m == "model-a" {
				if !testEqual(10, stats.MaxRPM) {
					t.Fatal(testWantGotMessage(10, stats.MaxRPM))
				}
			}
		}
		if !testContains(models, "model-a") {
			t.Fatal(testContainsMessage(models, "model-a"))
		}
		if !testContains(models, "model-b") {
			t.Fatal(testContainsMessage(models, "model-b"))
		}
		if !testContains(models, "model-c") {
			t.Fatal(testContainsMessage(models, "model-c"))
		}

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
		if !(foundA < foundB && foundB < foundC) {
			t.Fatal(testExpectedTrueMessage("iter should be sorted: a < b < c"))
		}
	})
}

func TestIter_Iterators_Case_2(t *testing.T) {
	rl, err := NewWithConfig(Config{
		FilePath:  testPath(t.TempDir(), "iter-empty.yaml"),
		Providers: []Provider{ProviderLocal},
	})
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	var models []string
	for model := range rl.Models() {
		models = append(models, model)
	}
	if !testIsEmpty(models) {
		t.Fatal(testEmptyMessage(models))
	}

	var iterated []string
	for model := range rl.Iter() {
		iterated = append(iterated, model)
	}
	if !testIsEmpty(iterated) {
		t.Fatal(testEmptyMessage(iterated))
	}
}

func TestIter_Iterators_Case_3(t *testing.T) {
	rl, err := NewWithConfig(Config{
		FilePath: testPath(t.TempDir(), "iter-break.yaml"),
		Quotas: map[string]ModelQuota{
			"model-a": {MaxRPM: 10},
			"model-b": {MaxRPM: 20},
			"model-c": {MaxRPM: 30},
			"model-d": {MaxRPM: 40},
		},
	})
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	var seen []string
	for model := range rl.Models() {
		seen = append(seen, model)
		if len(seen) == 2 {
			break
		}
	}
	if !testEqual([]string{"model-a", "model-b"}, seen) {
		t.Fatal(testWantGotMessage([]string{"model-a", "model-b"}, seen))
	}

	seen = nil
	for model := range rl.Iter() {
		seen = append(seen, model)
		if len(seen) == 3 {
			break
		}
	}
	if !testEqual([]string{"model-a", "model-b", "model-c"}, seen) {
		t.Fatal(testWantGotMessage([]string{"model-a", "model-b", "model-c"}, seen))
	}
}

func TestIter_IterEarlyBreak_Case(t *testing.T) {
	rl, err := NewWithConfig(Config{
		Quotas: map[string]ModelQuota{
			"model-a": {MaxRPM: 10},
			"model-b": {MaxRPM: 20},
			"model-c": {MaxRPM: 30},
		},
	})
	if err != nil {
		t.Fatal(testUnexpectedErrorMessage(err))
	}

	t.Run("Iter breaks early", func(t *testing.T) {
		var count int
		for range rl.Iter() {
			count++
			if count == 1 {
				break
			}
		}
		if !testEqual(1, count) {
			t.Fatal(testWantGotMessage(1, count, "should stop after first iteration"))
		}
	})

	t.Run("Models early break via manual iteration", func(t *testing.T) {
		var count int
		for range rl.Models() {
			count++
			if count == 2 {
				break
			}
		}
		if !testEqual(2, count) {
			t.Fatal(testWantGotMessage(2, count, "should stop after two models"))
		}
	})
}

func TestIter_CountTokensFull_Case(t *testing.T) {
	t.Run("empty model is rejected", func(t *testing.T) {
		_, err := CountTokens(t.Context(), "key", "", "text")
		if err == nil {
			t.Fatal(testExpectedErrorMessage())
		}
	})

	t.Run("API error non-200", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("bad request"))
		}))
		defer server.Close()

		_, err := countTokensWithClient(t.Context(), server.Client(), server.URL, "key", "model", "text")
		if err == nil {
			t.Fatal(testExpectedErrorMessage())
		}
		if !testContains(err.Error(), "status 400") {
			t.Fatal(testContainsMessage(err.Error(), "status 400"))
		}
	})

	t.Run("context cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := countTokensWithClient(ctx, http.DefaultClient, "https://generativelanguage.googleapis.com", "key", "model", "text")
		if err == nil {
			t.Fatal(testExpectedErrorMessage())
		}
		if !testContains(err.Error(), "do request") {
			t.Fatal(testContainsMessage(err.Error(), "do request"))
		}
	})
}
