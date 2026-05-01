// SPDX-License-Identifier: EUPL-1.2

package ratelimit

import (
	"context"

	core "dappco.re/go"
)

// --- AX-7 compliance triplets ---

func serviceTestConfig(t *core.T) Config {
	t.Helper()
	return Config{
		FilePath:  t.TempDir() + "/ratelimits.yaml",
		Providers: []Provider{ProviderGemini},
	}
}

func TestService_NewService_Good(t *core.T) {
	cfg := serviceTestConfig(t)
	factory := NewService(cfg)
	core.AssertNotNil(t, factory)
}

func TestService_NewService_Bad(t *core.T) {
	// NewService alone is a factory; resolution happens when invoked.
	cfg := Config{FilePath: t.TempDir() + "/ratelimits.yaml"}
	factory := NewService(cfg)
	core.AssertNotNil(t, factory)
}

func TestService_NewService_Ugly(t *core.T) {
	a := NewService(serviceTestConfig(t))
	b := NewService(serviceTestConfig(t))
	core.AssertNotNil(t, a)
	core.AssertNotNil(t, b)
}

func serviceForTest(t *core.T) *Service {
	t.Helper()
	c := core.New()
	r := NewService(serviceTestConfig(t))(c)
	core.RequireTrue(t, r.OK)
	return r.Value.(*Service)
}

func TestService_Service_OnStartup_Good(t *core.T) {
	svc := serviceForTest(t)
	startup := svc.OnStartup(context.Background())
	core.AssertTrue(t, startup.OK)
}

func TestService_Service_OnStartup_Bad(t *core.T) {
	var s *Service
	r := s.OnStartup(context.Background())
	core.AssertTrue(t, r.OK)
}

func TestService_Service_OnStartup_Ugly(t *core.T) {
	svc := serviceForTest(t)
	svc.OnStartup(context.Background())
	again := svc.OnStartup(context.Background())
	core.AssertTrue(t, again.OK)
}

func TestService_Service_OnShutdown_Good(t *core.T) {
	svc := serviceForTest(t)
	shutdown := svc.OnShutdown(context.Background())
	core.AssertTrue(t, shutdown.OK)
}

func TestService_Service_OnShutdown_Bad(t *core.T) {
	var s *Service
	r := s.OnShutdown(context.Background())
	core.AssertTrue(t, r.OK)
}

func TestService_Service_OnShutdown_Ugly(t *core.T) {
	svc := serviceForTest(t)
	// Persist on every shutdown; both invocations should succeed.
	svc.OnShutdown(context.Background())
	again := svc.OnShutdown(context.Background())
	core.AssertTrue(t, again.OK)
}

// --- end AX-7 compliance triplets ---
