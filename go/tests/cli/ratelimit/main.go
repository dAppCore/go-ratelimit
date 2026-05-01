// AX-10 CLI driver for go-ratelimit. Exercises the public RateLimiter API
// without depending on the package's own test files.
//
//	task -d tests/cli/ratelimit test
//	go run ./tests/cli/ratelimit
package main

import (
	"time"

	core "dappco.re/go"
	"dappco.re/go/ratelimit"
)

func main() {
	if r := run(); !r.OK {
		core.Print(core.Stderr(), "%s\n", r.Error())
		core.Exit(1)
	}
}

func run() core.Result {
	dirResult := core.MkdirTemp("", "go-ratelimit-ax10-")
	if !dirResult.OK {
		return core.Fail(core.E("ratelimit.cli", "mkdtemp", resultFailure(dirResult)))
	}
	dir := dirResult.Value.(string)
	defer core.RemoveAll(dir)

	cfg := ratelimit.Config{
		FilePath:  core.Path(dir, "ratelimits.yaml"),
		Providers: []ratelimit.Provider{ratelimit.ProviderGemini},
	}

	rl, err := ratelimit.NewWithConfig(cfg)
	if err != nil {
		return core.Fail(core.E("ratelimit.cli", "new ratelimiter", err))
	}

	allowed := rl.CanSend("gemini-2.5-flash", 100)
	decision := rl.Decide("gemini-2.5-flash", 100)
	out := map[string]any{
		"allowed":      allowed,
		"code":         string(decision.Code),
		"reason":       decision.Reason,
		"retryAfterMs": decision.RetryAfter / time.Millisecond,
	}
	encoded := core.JSONMarshalIndent(out, "", "  ")
	if !encoded.OK {
		return core.Fail(core.E("ratelimit.cli", "encode decision", resultFailure(encoded)))
	}
	if write := core.WriteString(core.Stdout(), string(encoded.Value.([]byte))+"\n"); !write.OK {
		return write
	}

	if err := rl.Persist(); err != nil {
		return core.Fail(core.E("ratelimit.cli", "persist", err))
	}

	return core.Ok(nil)
}

func resultFailure(r core.Result) error /* core result boundary */ {
	if err, ok := r.Value.(error); ok {
		return err
	}
	return core.E("ratelimit.cli", r.Error(), nil)
}
