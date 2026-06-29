// SPDX-License-Identifier: EUPL-1.2

// Service registration for the ratelimit package. Exposes the
// *RateLimiter surface (CanSend, RecordUsage, Stats, Decide, Reset) as
// a Core service with action handlers so consumers can wire rate-limit
// operations through the same plumbing as every other core service.
//
// Usage example: `c, _ := core.New(core.WithName("ratelimit", ratelimit.NewService(ratelimit.Config{Providers: []ratelimit.Provider{ratelimit.ProviderGemini}})))`

package ratelimit

import (
	"context"

	core "dappco.re/go"
)

// Service is the registerable handle for the ratelimit package — embeds
// *core.ServiceRuntime[Config] for typed options access and holds a
// live *RateLimiter ready for direct method calls or action use.
//
// Usage example: `svc := core.MustServiceFor[*ratelimit.Service](c, "ratelimit"); svc.Limiter.RecordUsage("gemini-2.0", 100, 200)`
type Service struct {
	*core.ServiceRuntime[Config]
	// Limiter is the live *RateLimiter the service was constructed with.
	// Usage example: `svc.Limiter.CanSend("gemini-2.0", 1024)`
	Limiter       *RateLimiter
	registrations core.Once
}

// NewService returns a factory that constructs the rate limiter and
// produces a *Service ready for c.Service() registration. Use through
// core.WithName so the framework wires lifecycle (OnStartup registers
// actions).
//
// Usage example: `c, _ := core.New(core.WithName("ratelimit", ratelimit.NewService(ratelimit.Config{Providers: []ratelimit.Provider{ratelimit.ProviderGemini}})))`
func NewService(config Config) func(*core.Core) core.Result {
	return func(c *core.Core) core.Result {
		rl, err := NewWithConfig(config)
		if err != nil {
			return core.Fail(core.E("ratelimit.NewService", "rate limiter init failed", err))
		}
		return core.Ok(&Service{
			ServiceRuntime: core.NewServiceRuntime(c, config),
			Limiter:        rl,
		})
	}
}

// OnStartup registers the ratelimit action handlers on the attached
// Core. Implements core.Startable. Idempotent via core.Once.
//
// Usage example: `r := svc.OnStartup(ctx)`
func (s *Service) OnStartup(context.Context) core.Result {
	if s == nil {
		return core.Ok(nil)
	}
	s.registrations.Do(func() {
		c := s.Core()
		if c == nil {
			return
		}
		c.Action("ratelimit.check", s.handleCheck)
		c.Action("ratelimit.record", s.handleRecord)
		c.Action("ratelimit.stats", s.handleStats)
		c.Action("ratelimit.decide", s.handleDecide)
		c.Action("ratelimit.reset", s.handleReset)
	})
	return core.Ok(nil)
}

// OnShutdown persists the limiter state and is otherwise a no-op.
// Implements core.Stoppable.
//
// Usage example: `r := svc.OnShutdown(ctx)`
func (s *Service) OnShutdown(context.Context) core.Result {
	if s == nil || s.Limiter == nil {
		return core.Ok(nil)
	}
	if err := s.Limiter.Persist(); err != nil {
		return core.Fail(core.E("ratelimit.OnShutdown", "persist on shutdown failed", err))
	}
	return core.Ok(nil)
}

// handleCheck — `ratelimit.check` action handler. Reads opts.model +
// opts.tokens and returns the boolean CanSend decision in r.Value.
//
//	r := c.Action("ratelimit.check").Run(ctx, core.NewOptions(
//	    core.Option{Key: "model", Value: "gemini-2.0-flash"},
//	    core.Option{Key: "tokens", Value: 1024},
//	))
//	allowed, _ := r.Value.(bool)
func (s *Service) handleCheck(_ core.Context, opts core.Options) core.Result {
	if s == nil || s.Limiter == nil {
		return core.Fail(core.E("ratelimit.check", "service not initialised", nil))
	}
	model := opts.String("model")
	if model == "" {
		return core.Fail(core.E("ratelimit.check", "model is required", nil))
	}
	tokens := opts.Int("tokens")
	return core.Ok(s.Limiter.CanSend(model, tokens))
}

// handleRecord — `ratelimit.record` action handler. Reads opts.model +
// opts.prompt_tokens + opts.output_tokens and writes the usage event.
//
//	r := c.Action("ratelimit.record").Run(ctx, core.NewOptions(
//	    core.Option{Key: "model", Value: "gemini-2.0-flash"},
//	    core.Option{Key: "prompt_tokens", Value: 100},
//	    core.Option{Key: "output_tokens", Value: 200},
//	))
func (s *Service) handleRecord(_ core.Context, opts core.Options) core.Result {
	if s == nil || s.Limiter == nil {
		return core.Fail(core.E("ratelimit.record", "service not initialised", nil))
	}
	model := opts.String("model")
	if model == "" {
		return core.Fail(core.E("ratelimit.record", "model is required", nil))
	}
	s.Limiter.RecordUsage(model, opts.Int("prompt_tokens"), opts.Int("output_tokens"))
	return core.Ok(nil)
}

// handleStats — `ratelimit.stats` action handler. Reads opts.model and
// returns the ModelStats snapshot in r.Value. If opts.model is empty,
// returns the full map[string]ModelStats.
//
//	r := c.Action("ratelimit.stats").Run(ctx, core.NewOptions(
//	    core.Option{Key: "model", Value: "gemini-2.0-flash"},
//	))
//	stats, _ := r.Value.(ModelStats)
func (s *Service) handleStats(_ core.Context, opts core.Options) core.Result {
	if s == nil || s.Limiter == nil {
		return core.Fail(core.E("ratelimit.stats", "service not initialised", nil))
	}
	if model := opts.String("model"); model != "" {
		return core.Ok(s.Limiter.Stats(model))
	}
	return core.Ok(s.Limiter.AllStats())
}

// handleDecide — `ratelimit.decide` action handler. Reads opts.model +
// opts.tokens and returns the full Decision struct (Allow + Reason +
// retry hint) in r.Value.
//
//	r := c.Action("ratelimit.decide").Run(ctx, core.NewOptions(
//	    core.Option{Key: "model", Value: "gemini-2.0-flash"},
//	    core.Option{Key: "tokens", Value: 1024},
//	))
//	d, _ := r.Value.(Decision)
func (s *Service) handleDecide(_ core.Context, opts core.Options) core.Result {
	if s == nil || s.Limiter == nil {
		return core.Fail(core.E("ratelimit.decide", "service not initialised", nil))
	}
	model := opts.String("model")
	if model == "" {
		return core.Fail(core.E("ratelimit.decide", "model is required", nil))
	}
	return core.Ok(s.Limiter.Decide(model, opts.Int("tokens")))
}

// handleReset — `ratelimit.reset` action handler. Reads opts.model and
// clears the per-model usage history.
//
//	r := c.Action("ratelimit.reset").Run(ctx, core.NewOptions(
//	    core.Option{Key: "model", Value: "gemini-2.0-flash"},
//	))
func (s *Service) handleReset(_ core.Context, opts core.Options) core.Result {
	if s == nil || s.Limiter == nil {
		return core.Fail(core.E("ratelimit.reset", "service not initialised", nil))
	}
	model := opts.String("model")
	if model == "" {
		return core.Fail(core.E("ratelimit.reset", "model is required", nil))
	}
	s.Limiter.Reset(model)
	return core.Ok(nil)
}
