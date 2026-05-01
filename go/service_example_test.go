// SPDX-License-Identifier: EUPL-1.2

package ratelimit_test

import (
	"context"

	core "dappco.re/go"
	"dappco.re/go/ratelimit"
)

// ExampleNewService constructs the ratelimit service factory through
// `NewService` for go-ratelimit Core service registration. The factory
// produces a *ratelimit.Service ready for c.Service() — OnStartup wires
// the ratelimit.* action handlers, OnShutdown persists state.
//
// Usage example: `c.Service("ratelimit", ratelimit.NewService(ratelimit.Config{Providers: []ratelimit.Provider{ratelimit.ProviderGemini}}))`
func ExampleNewService() {
	factory := ratelimit.NewService(ratelimit.Config{
		Providers: []ratelimit.Provider{ratelimit.ProviderGemini},
	})
	core.Println(factory != nil)
	// Output: true
}

// ExampleService_OnStartup registers the ratelimit.* action handlers on
// the attached Core through `Service.OnStartup` for go-ratelimit Core
// service registration. Idempotent — multiple startups won't
// double-register.
//
// Usage example: `r := svc.OnStartup(ctx)`
func ExampleService_OnStartup() {
	c := core.New()
	r := ratelimit.NewService(ratelimit.Config{
		Providers: []ratelimit.Provider{ratelimit.ProviderGemini},
	})(c)
	if !r.OK {
		core.Println("startup-init-failed")
		return
	}
	svc := r.Value.(*ratelimit.Service)
	startup := svc.OnStartup(context.Background())
	core.Println(startup.OK)
	// Output: true
}

// ExampleService_OnShutdown persists the in-memory limiter state to
// disk through `Service.OnShutdown` for go-ratelimit Core service
// registration.
//
// Usage example: `r := svc.OnShutdown(ctx)`
func ExampleService_OnShutdown() {
	c := core.New()
	r := ratelimit.NewService(ratelimit.Config{
		Providers: []ratelimit.Provider{ratelimit.ProviderGemini},
	})(c)
	if !r.OK {
		core.Println("startup-init-failed")
		return
	}
	svc := r.Value.(*ratelimit.Service)
	shutdown := svc.OnShutdown(context.Background())
	core.Println(shutdown.OK)
	// Output: true
}
