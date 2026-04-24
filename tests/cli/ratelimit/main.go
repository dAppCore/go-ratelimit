// AX-10 CLI driver for go-ratelimit. Exercises the public RateLimiter API
// without depending on the package's own test files.
//
//	task -d tests/cli/ratelimit test
//	go run ./tests/cli/ratelimit
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"dappco.re/go/ratelimit"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	dir, err := os.MkdirTemp("", "go-ratelimit-ax10-")
	if err != nil {
		return fmt.Errorf("mkdtemp: %w", err)
	}
	defer os.RemoveAll(dir)

	cfg := ratelimit.Config{
		FilePath:  filepath.Join(dir, "ratelimits.yaml"),
		Providers: []ratelimit.Provider{ratelimit.ProviderGemini},
	}

	rl, err := ratelimit.NewWithConfig(cfg)
	if err != nil {
		return fmt.Errorf("new ratelimiter: %w", err)
	}

	allowed := rl.CanSend("gemini-2.5-flash", 100)
	decision := rl.Decide("gemini-2.5-flash", 100)
	out := map[string]any{
		"allowed":      allowed,
		"code":         string(decision.Code),
		"reason":       decision.Reason,
		"retryAfterMs": decision.RetryAfter / time.Millisecond,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("encode decision: %w", err)
	}

	if err := rl.Persist(); err != nil {
		return fmt.Errorf("persist: %w", err)
	}

	return nil
}
