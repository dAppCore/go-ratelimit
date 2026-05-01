// SPDX-License-Identifier: EUPL-1.2

package ratelimit

import (
	"context"
	"time"
)

func ExampleDefaultProfiles() {
	profiles := DefaultProfiles()
	_ = profiles[ProviderGemini]
}

func ExampleNew() {
	rl, err := New()
	if err != nil {
		return
	}
	defer rl.Close()
	_ = rl.CanSend("gemini-3-pro-preview", 1)
}

func ExampleNewWithConfig() {
	rl, err := NewWithConfig(Config{Providers: []Provider{ProviderLocal}})
	if err != nil {
		return
	}
	defer rl.Close()
	rl.SetQuota("local-model", ModelQuota{MaxRPM: 1})
}

func ExampleRateLimiter_SetQuota() {
	rl, err := NewWithConfig(Config{Providers: []Provider{ProviderLocal}})
	if err != nil {
		return
	}
	rl.SetQuota("local-model", ModelQuota{MaxRPM: 10})
}

func ExampleRateLimiter_AddProvider() {
	rl, err := NewWithConfig(Config{Providers: []Provider{ProviderLocal}})
	if err != nil {
		return
	}
	rl.AddProvider(ProviderOpenAI)
}

func ExampleRateLimiter_Load() {
	rl, err := NewWithConfig(Config{Providers: []Provider{ProviderLocal}})
	if err != nil {
		return
	}
	_ = rl.Load()
}

func ExampleRateLimiter_Persist() {
	rl, err := NewWithConfig(Config{Providers: []Provider{ProviderLocal}})
	if err != nil {
		return
	}
	_ = rl.Persist()
}

func ExampleRateLimiter_BackgroundPrune() {
	rl, err := NewWithConfig(Config{Providers: []Provider{ProviderLocal}})
	if err != nil {
		return
	}
	stop := rl.BackgroundPrune(time.Minute)
	defer stop()
}

func ExampleRateLimiter_CanSend() {
	rl, err := NewWithConfig(Config{Providers: []Provider{ProviderLocal}})
	if err != nil {
		return
	}
	_ = rl.CanSend("local-model", 1)
}

func ExampleRateLimiter_RecordUsage() {
	rl, err := NewWithConfig(Config{Providers: []Provider{ProviderLocal}})
	if err != nil {
		return
	}
	rl.RecordUsage("local-model", 1, 1)
}

func ExampleRateLimiter_WaitForCapacity() {
	rl, err := NewWithConfig(Config{Providers: []Provider{ProviderLocal}})
	if err != nil {
		return
	}
	_ = rl.WaitForCapacity(context.Background(), "local-model", 1)
}

func ExampleRateLimiter_Reset() {
	rl, err := NewWithConfig(Config{Providers: []Provider{ProviderLocal}})
	if err != nil {
		return
	}
	rl.Reset("")
}

func ExampleRateLimiter_Models() {
	rl, err := NewWithConfig(Config{Providers: []Provider{ProviderGemini}})
	if err != nil {
		return
	}
	for model := range rl.Models() {
		_ = model
		break
	}
}

func ExampleRateLimiter_Iter() {
	rl, err := NewWithConfig(Config{Providers: []Provider{ProviderGemini}})
	if err != nil {
		return
	}
	for model, stats := range rl.Iter() {
		_, _ = model, stats
		break
	}
}

func ExampleRateLimiter_Stats() {
	rl, err := NewWithConfig(Config{Providers: []Provider{ProviderLocal}})
	if err != nil {
		return
	}
	_ = rl.Stats("local-model")
}

func ExampleRateLimiter_AllStats() {
	rl, err := NewWithConfig(Config{Providers: []Provider{ProviderLocal}})
	if err != nil {
		return
	}
	_ = rl.AllStats()
}

func ExampleRateLimiter_Decide() {
	rl, err := NewWithConfig(Config{Providers: []Provider{ProviderLocal}})
	if err != nil {
		return
	}
	_ = rl.Decide("local-model", 1)
}

func ExampleNewWithSQLite() {
	rl, err := NewWithSQLite(":memory:")
	if err != nil {
		return
	}
	defer rl.Close()
}

func ExampleNewWithSQLiteConfig() {
	rl, err := NewWithSQLiteConfig(":memory:", Config{Providers: []Provider{ProviderLocal}})
	if err != nil {
		return
	}
	defer rl.Close()
}

func ExampleRateLimiter_Close() {
	rl, err := NewWithSQLite(":memory:")
	if err != nil {
		return
	}
	_ = rl.Close()
}

func ExampleMigrateYAMLToSQLite() {
	_ = MigrateYAMLToSQLite("ratelimits.yaml", "ratelimits.db")
}

func ExampleCountTokens() {
	_, _ = CountTokens(context.Background(), "api-key", "gemini-3-pro-preview", "hello")
}
