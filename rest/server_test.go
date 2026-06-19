package rest

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofly/gofly/core/auth"
	"github.com/gofly/gofly/core/breaker"
	coreerrors "github.com/gofly/gofly/core/errors"
	"github.com/gofly/gofly/core/governance"
	"github.com/gofly/gofly/core/limit"
	"github.com/gofly/gofly/core/metadata"
	"github.com/gofly/gofly/core/observability/metrics"
	"github.com/gofly/gofly/core/observability/trace"
)

func TestServer_AddRoute(t *testing.T) {
	s := MustNewServer(Config{})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/ping", Handler: func(ctx *Context) { ctx.JSON(200, map[string]string{"message": "pong"}) }})
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestRouterGroupAddsPrefixMiddlewareAndOptions(t *testing.T) {
	var calls []string
	middleware := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, name)
				next.ServeHTTP(w, r)
			})
		}
	}

	s := MustNewServer(Config{DisableDefaultMiddlewares: true})
	group := s.Group("api", middleware("group"))
	group.Use(middleware("used"))
	group.With(WithPrefix("/v1"))
	group.AddRoute(Route{Method: http.MethodGet, Path: "users", Handler: func(ctx *Context) {
		calls = append(calls, "handler")
		ctx.String(http.StatusOK, "ok")
	}, Middlewares: []Middleware{middleware("route")}})
	group.AddRoutes([]Route{{Method: http.MethodGet, Path: "/teams", Handler: func(ctx *Context) {
		ctx.String(http.StatusOK, "teams")
	}}})

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/api/users", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("group route response = %d %q, want 200 ok", rec.Code, rec.Body.String())
	}
	wantCalls := []string{"group", "used", "route", "handler"}
	if len(calls) != len(wantCalls) {
		t.Fatalf("middleware calls = %v, want %v", calls, wantCalls)
	}
	for i := range wantCalls {
		if calls[i] != wantCalls[i] {
			t.Fatalf("middleware calls = %v, want %v", calls, wantCalls)
		}
	}

	teams := httptest.NewRecorder()
	s.Handler().ServeHTTP(teams, httptest.NewRequest(http.MethodGet, "/v1/api/teams", nil))
	if teams.Code != http.StatusOK || teams.Body.String() != "teams" {
		t.Fatalf("group AddRoutes response = %d %q, want 200 teams", teams.Code, teams.Body.String())
	}
}

func TestNewServerAppliesProductionSafeMiddlewareDefaults(t *testing.T) {
	s := MustNewServer(Config{Name: "orders"})

	if !s.conf.Middlewares.Recover || !s.conf.Middlewares.Log || !s.conf.Middlewares.Metrics || !s.conf.Middlewares.Health || !s.conf.Middlewares.RequestID || !s.conf.Middlewares.Timeout {
		t.Fatalf("default middlewares = %#v, want recover/log/metrics/health/request-id/timeout enabled", s.conf.Middlewares)
	}
	if s.conf.MaxBodyBytes != defaultMaxBodyBytes {
		t.Fatalf("MaxBodyBytes = %d, want %d", s.conf.MaxBodyBytes, defaultMaxBodyBytes)
	}

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("default health status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestNewServerCanDisableProductionDefaults(t *testing.T) {
	s := MustNewServer(Config{DisableDefaultMiddlewares: true})
	if s.conf.Middlewares.Recover || s.conf.Middlewares.Log || s.conf.Middlewares.Metrics || s.conf.Middlewares.Health || s.conf.Middlewares.RequestID || s.conf.Middlewares.Timeout {
		t.Fatalf("default-disabled middlewares = %#v, want all false", s.conf.Middlewares)
	}

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("disabled health status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestAdaptiveRateLimitCanUseInjectedCPUSignal(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	s := MustNewServer(Config{DisableDefaultMiddlewares: true, Middlewares: MiddlewaresConfig{
		AdaptiveRateLimit:   true,
		AdaptiveLimitConfig: AdaptiveLimitConfig{InitialLimit: 4, CPUThreshold: 900},
	}}, WithAdaptiveCPUReader(func() int { return 950 }))
	s.AddRoute(Route{Method: http.MethodGet, Path: "/adaptive", Handler: func(ctx *Context) {
		started <- struct{}{}
		<-release
		ctx.String(http.StatusOK, "ok")
	}})

	results := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() {
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/adaptive", nil))
			results <- rec.Code
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for in-flight adaptive requests")
		}
	}

	rejected := httptest.NewRecorder()
	s.Handler().ServeHTTP(rejected, httptest.NewRequest(http.MethodGet, "/adaptive", nil))
	if rejected.Code != http.StatusTooManyRequests {
		close(release)
		t.Fatalf("overloaded status = %d, want %d", rejected.Code, http.StatusTooManyRequests)
	}
	governance := s.Governance()
	foundOverloadedSnapshot := false
	for _, component := range governance.Components {
		if component.Kind != "adaptive_limiter" {
			continue
		}
		snapshot, ok := component.Snapshot.(limit.AdaptiveSnapshot)
		if ok && snapshot.Overloaded && snapshot.CPULoad == 950 {
			foundOverloadedSnapshot = true
		}
	}
	close(release)
	for i := 0; i < 2; i++ {
		select {
		case code := <-results:
			if code != http.StatusOK {
				t.Fatalf("in-flight request status = %d, want %d", code, http.StatusOK)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for in-flight adaptive response")
		}
	}
	if !foundOverloadedSnapshot {
		t.Fatalf("governance = %#v, want overloaded adaptive limiter snapshot", governance)
	}
}

func TestContextErrorWritesCoreErrorResponse(t *testing.T) {
	s := MustNewServer(Config{})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/missing", Handler: func(ctx *Context) {
		ctx.Error(coreerrors.New(coreerrors.CodeNotFound, "user not found"))
	}})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	var got ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Code != coreerrors.CodeNotFound || got.Text != "user not found" {
		t.Fatalf("error response = %#v, want not_found/user not found", got)
	}
}

func TestRecoverMiddleware(t *testing.T) {
	s := MustNewServer(Config{})
	s.Use(RecoverMiddleware())
	s.AddRoute(Route{Method: http.MethodGet, Path: "/panic", Handler: func(ctx *Context) { panic("boom") }})
	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestHealthAndMetricsRoutes(t *testing.T) {
	s := MustNewServer(Config{Middlewares: MiddlewaresConfig{Health: true, Metrics: true}})
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
		})
	}
}

func TestHealthAndReadyChecks(t *testing.T) {
	s := MustNewServer(Config{Middlewares: MiddlewaresConfig{Health: true}},
		WithHealthCheck("self", func(ctx context.Context) error { return nil }),
		WithReadyCheck("database", func(ctx context.Context) error { return stderrors.New("dial failed") }),
	)

	health := httptest.NewRecorder()
	s.Handler().ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d, want %d", health.Code, http.StatusOK)
	}
	var healthReport CheckReport
	if err := json.NewDecoder(health.Body).Decode(&healthReport); err != nil {
		t.Fatal(err)
	}
	if healthReport.Status != "ok" || healthReport.Checks["self"].Status != "ok" {
		t.Fatalf("health report = %#v, want ok self check", healthReport)
	}

	ready := httptest.NewRecorder()
	s.Handler().ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status = %d, want %d", ready.Code, http.StatusServiceUnavailable)
	}
	var readyReport CheckReport
	if err := json.NewDecoder(ready.Body).Decode(&readyReport); err != nil {
		t.Fatal(err)
	}
	if readyReport.Status != "failed" || readyReport.Checks["database"].Error != "dial failed" {
		t.Fatalf("ready report = %#v, want failed database check", readyReport)
	}
}

func TestAdminRoutes(t *testing.T) {
	s := MustNewServer(Config{Name: "hello", Admin: AdminConfig{Enabled: true, PathPrefix: "/admin", Pprof: true, Token: "secret"}})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/guarded", Handler: func(ctx *Context) { ctx.String(http.StatusOK, "ok") }}, WithBreaker())

	handler := adminAuthHandler(s.Handler(), "secret")
	configRec := httptest.NewRecorder()
	handler.ServeHTTP(configRec, httptest.NewRequest(http.MethodGet, "/admin/config", nil))
	if configRec.Code != http.StatusOK {
		t.Fatalf("config status = %d, want %d", configRec.Code, http.StatusOK)
	}
	var config Config
	if err := json.NewDecoder(configRec.Body).Decode(&config); err != nil {
		t.Fatal(err)
	}
	if config.Name != "hello" || !config.Admin.Enabled {
		t.Fatalf("config = %#v, want admin-enabled hello config", config)
	}

	stateRec := httptest.NewRecorder()
	handler.ServeHTTP(stateRec, httptest.NewRequest(http.MethodGet, "/admin/state", nil))
	if stateRec.Code != http.StatusOK {
		t.Fatalf("state status = %d, want %d", stateRec.Code, http.StatusOK)
	}
	var state StateSnapshot
	if err := json.NewDecoder(stateRec.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if state.Service != "hello" || state.State != "initialized" {
		t.Fatalf("state = %#v, want hello initialized", state)
	}

	pprofRec := httptest.NewRecorder()
	handler.ServeHTTP(pprofRec, httptest.NewRequest(http.MethodGet, "/admin/pprof/", nil))
	if pprofRec.Code != http.StatusOK {
		t.Fatalf("pprof status = %d, want %d", pprofRec.Code, http.StatusOK)
	}

	governanceRec := httptest.NewRecorder()
	handler.ServeHTTP(governanceRec, httptest.NewRequest(http.MethodGet, "/admin/governance", nil))
	if governanceRec.Code != http.StatusOK {
		t.Fatalf("governance status = %d, want %d", governanceRec.Code, http.StatusOK)
	}
	var governance GovernanceSnapshot
	if err := json.NewDecoder(governanceRec.Body).Decode(&governance); err != nil {
		t.Fatal(err)
	}
	if len(governance.Components) == 0 || governance.Components[0].Kind != "adaptive_breaker" {
		t.Fatalf("governance = %#v, want adaptive breaker component", governance)
	}

	routesRec := httptest.NewRecorder()
	handler.ServeHTTP(routesRec, httptest.NewRequest(http.MethodGet, "/admin/routes", nil))
	if routesRec.Code != http.StatusOK {
		t.Fatalf("routes status = %d, want %d", routesRec.Code, http.StatusOK)
	}
	var routes []RouteSpec
	if err := json.NewDecoder(routesRec.Body).Decode(&routes); err != nil {
		t.Fatal(err)
	}
	if !hasRouteSpec(routes, http.MethodGet, "/guarded") || !hasRouteSpec(routes, http.MethodGet, "/admin/diagnostics") {
		t.Fatalf("routes = %#v, want guarded and diagnostics routes", routes)
	}

	openAPIRec := httptest.NewRecorder()
	handler.ServeHTTP(openAPIRec, httptest.NewRequest(http.MethodGet, "/admin/openapi.json", nil))
	if openAPIRec.Code != http.StatusOK {
		t.Fatalf("openapi status = %d, want %d", openAPIRec.Code, http.StatusOK)
	}
	var doc OpenAPIDocument
	if err := json.NewDecoder(openAPIRec.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	if doc.OpenAPI != "3.0.3" || doc.Paths["/guarded"]["get"].Responses["200"].Description != "OK" {
		t.Fatalf("openapi = %#v, want guarded GET operation", doc)
	}

	diagnosticsRec := httptest.NewRecorder()
	handler.ServeHTTP(diagnosticsRec, httptest.NewRequest(http.MethodGet, "/admin/diagnostics", nil))
	if diagnosticsRec.Code != http.StatusOK {
		t.Fatalf("diagnostics status = %d, want %d", diagnosticsRec.Code, http.StatusOK)
	}
	var diagnostics DiagnosticsSnapshot
	if err := json.NewDecoder(diagnosticsRec.Body).Decode(&diagnostics); err != nil {
		t.Fatal(err)
	}
	if diagnostics.Config.Name != "hello" || diagnostics.State.Service != "hello" || diagnostics.Health.Status != "ok" || len(diagnostics.Routes) == 0 {
		t.Fatalf("diagnostics = %#v, want full admin diagnosis", diagnostics)
	}
}

func adminAuthHandler(handler http.Handler, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set(auth.AuthorizationHeader, auth.BearerValue(token))
		handler.ServeHTTP(w, r)
	})
}

func TestAdminRoutesWithoutTokenAllowOnlyLocal(t *testing.T) {
	s := MustNewServer(Config{Host: "127.0.0.1", Admin: AdminConfig{Enabled: true, PathPrefix: "/admin"}})
	local := httptest.NewRequest(http.MethodGet, "/admin/config", nil)
	local.RemoteAddr = "127.0.0.1:12345"
	localRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(localRec, local)
	if localRec.Code != http.StatusOK {
		t.Fatalf("local admin status = %d, want 200", localRec.Code)
	}
	remote := httptest.NewRequest(http.MethodGet, "/admin/config", nil)
	remote.RemoteAddr = "203.0.113.10:12345"
	remoteRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(remoteRec, remote)
	if remoteRec.Code != http.StatusForbidden {
		t.Fatalf("remote admin status = %d, want 403", remoteRec.Code)
	}
}

func hasRouteSpec(routes []RouteSpec, method, path string) bool {
	for _, route := range routes {
		if route.Method == method && route.Path == path {
			return true
		}
	}
	return false
}

func TestAdminRoutesCanRequireBearerToken(t *testing.T) {
	s := MustNewServer(Config{Admin: AdminConfig{Enabled: true, PathPrefix: "/admin", Token: "secret"}})

	unauthorized := httptest.NewRecorder()
	s.Handler().ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/admin/config", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", unauthorized.Code, http.StatusUnauthorized)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/runtime", nil)
	req.Header.Set(auth.AuthorizationHeader, auth.BearerValue("secret"))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("authorized status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestAdminGovernanceExplainRoute(t *testing.T) {
	rules := governance.NewRuleSet(
		governance.Rule{Name: "default-rest", Transport: governance.TransportREST, Service: "orders", Policy: governance.Policy{Timeout: time.Second}},
		governance.Rule{Name: "upload", Priority: 10, Transport: governance.TransportREST, Service: "orders", Method: http.MethodPost, Path: "/upload", Tags: map[string]string{"zone": "cn"}, Policy: governance.Policy{MaxBodyBytes: 4}},
	)
	s := MustNewServer(Config{Name: "orders", Admin: AdminConfig{Enabled: true, PathPrefix: "/admin"}}, WithGovernanceRuleSet(rules))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/governance/explain?method=post&path=/upload&tags=zone=cn", nil)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("explain status = %d, want %d", rec.Code, http.StatusOK)
	}
	var explain governance.RuleExplain
	if err := json.NewDecoder(rec.Body).Decode(&explain); err != nil {
		t.Fatal(err)
	}
	if !explain.Decision.Matched || explain.Decision.RuleName != "upload" || explain.Request.Service != "orders" || explain.Request.Method != http.MethodPost {
		t.Fatalf("explain = %#v, want upload dry-run match", explain)
	}
	if len(explain.Evaluations) != 2 || explain.Evaluations[0].Reason != "matched" {
		t.Fatalf("evaluations = %#v, want matched reason", explain.Evaluations)
	}
	if stats := rules.Stats(); len(stats) != 0 {
		t.Fatalf("stats = %#v, want admin explain not to record hits", stats)
	}
}

func TestAdminGovernanceRulesCanHotReplaceRuleSet(t *testing.T) {
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "orders-v1",
		Transport: governance.TransportREST,
		Service:   "orders",
		Path:      "/orders",
		Policy:    governance.Policy{Headers: map[string]string{"X-Version": "v1"}},
	})
	s := MustNewServer(Config{Name: "orders", Admin: AdminConfig{Enabled: true, PathPrefix: "/admin", Token: "secret"}}, WithGovernanceRuleSet(rules))
	s.AddRoute(Route{Method: http.MethodGet, Path: "/orders", Handler: func(ctx *Context) {
		ctx.String(http.StatusOK, ctx.Request.Header.Get("X-Version"))
	}})

	req := httptest.NewRequest(http.MethodPut, "/admin/governance/rules", strings.NewReader(`[
		{"name":"orders-v2","transport":"rest","service":"orders","path":"/orders","policy":{"headers":{"X-Version":"v2"}}}
	]`))
	req.Header.Set(auth.AuthorizationHeader, "Bearer secret")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("replace status = %d, body = %s", rec.Code, rec.Body.String())
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/admin/governance/status", nil)
	statusReq.Header.Set(auth.AuthorizationHeader, "Bearer secret")
	statusRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK || !strings.Contains(statusRec.Body.String(), `"rules":1`) {
		t.Fatalf("status = %d body = %s", statusRec.Code, statusRec.Body.String())
	}

	ordersRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(ordersRec, httptest.NewRequest(http.MethodGet, "/orders", nil))
	if ordersRec.Code != http.StatusOK || strings.TrimSpace(ordersRec.Body.String()) != "v2" {
		t.Fatalf("orders status = %d body = %q", ordersRec.Code, ordersRec.Body.String())
	}

	badReq := httptest.NewRequest(http.MethodPut, "/admin/governance/rules", strings.NewReader(`[{"name":"bad","transport":"unknown"}]`))
	badReq.Header.Set(auth.AuthorizationHeader, "Bearer secret")
	badRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest || !strings.Contains(badRec.Body.String(), "unknown transport") {
		t.Fatalf("bad status = %d body = %s", badRec.Code, badRec.Body.String())
	}
}

func TestAdminGovernanceReloadUsesManager(t *testing.T) {
	dir := t.TempDir()
	ruleFile := filepath.Join(dir, "governance.json")
	if err := os.WriteFile(ruleFile, []byte(`[
		{"name":"orders-v1","transport":"rest","service":"orders","method":"GET","path":"/orders","policy":{"headers":{"X-Version":"v1"}}}
	]`), 0o644); err != nil {
		t.Fatal(err)
	}
	manager, err := governance.NewManager(governance.Config{RuleFile: ruleFile})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := MustNewServer(
		Config{Name: "orders", Admin: AdminConfig{Enabled: true, PathPrefix: "/admin", Token: "secret"}},
		WithGovernanceManager(manager),
	)
	s.AddRoute(Route{Method: http.MethodGet, Path: "/orders", Handler: func(ctx *Context) {
		ctx.String(http.StatusOK, ctx.Request.Header.Get("X-Version"))
	}})

	before := httptest.NewRecorder()
	s.Handler().ServeHTTP(before, httptest.NewRequest(http.MethodGet, "/orders", nil))
	if before.Code != http.StatusOK || strings.TrimSpace(before.Body.String()) != "v1" {
		t.Fatalf("before reload status = %d body = %q", before.Code, before.Body.String())
	}

	if err := os.WriteFile(ruleFile, []byte(`[
		{"name":"orders-v2","transport":"rest","service":"orders","method":"GET","path":"/orders","policy":{"headers":{"X-Version":"v2"}}}
	]`), 0o644); err != nil {
		t.Fatal(err)
	}
	reloadReq := httptest.NewRequest(http.MethodPost, "/admin/governance/reload", nil)
	reloadReq.Header.Set(auth.AuthorizationHeader, auth.BearerValue("secret"))
	reloadRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(reloadRec, reloadReq)
	if reloadRec.Code != http.StatusOK {
		t.Fatalf("reload status = %d body = %s", reloadRec.Code, reloadRec.Body.String())
	}

	after := httptest.NewRecorder()
	s.Handler().ServeHTTP(after, httptest.NewRequest(http.MethodGet, "/orders", nil))
	if after.Code != http.StatusOK || strings.TrimSpace(after.Body.String()) != "v2" {
		t.Fatalf("after reload status = %d body = %q", after.Code, after.Body.String())
	}
	if stats := manager.RuleSet().Stats(); len(stats) != 1 || stats[0].RuleName != "orders-v2" {
		t.Fatalf("manager stats = %#v, want reloaded orders-v2 rule", stats)
	}
}

func TestGovernanceManagerOverridesExplicitRuleSet(t *testing.T) {
	stale := governance.NewRuleSet(governance.Rule{
		Name:      "orders-stale",
		Transport: governance.TransportREST,
		Service:   "orders",
		Path:      "/orders",
		Policy:    governance.Policy{Headers: map[string]string{"X-Version": "stale"}},
	})
	manager, err := governance.NewManager(governance.Config{Rules: []governance.Rule{{
		Name:      "orders-live",
		Transport: governance.TransportREST,
		Service:   "orders",
		Path:      "/orders",
		Policy:    governance.Policy{Headers: map[string]string{"X-Version": "live"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	s := MustNewServer(
		Config{Name: "orders"},
		WithGovernanceRuleSet(stale),
		WithGovernanceManager(manager),
	)
	s.AddRoute(Route{Method: http.MethodGet, Path: "/orders", Handler: func(ctx *Context) {
		ctx.String(http.StatusOK, ctx.Request.Header.Get("X-Version"))
	}})

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/orders", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "live" {
		t.Fatalf("status = %d body = %q, want manager rule", rec.Code, rec.Body.String())
	}
}

func TestGovernanceSuiteProvidesRules(t *testing.T) {
	suite := governance.MustNewSuite(governance.NewPlugin("rest-default", governance.Rule{
		Name:      "suite",
		Transport: governance.TransportREST,
		Service:   "orders",
		Path:      "/orders",
		Policy:    governance.Policy{Headers: map[string]string{"X-Version": "suite"}},
	}))
	s := MustNewServer(Config{Name: "orders"}, WithGovernanceSuite(suite))
	s.AddRoute(Route{Method: http.MethodGet, Path: "/orders", Handler: func(ctx *Context) {
		ctx.String(http.StatusOK, ctx.Request.Header.Get("X-Version"))
	}})

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/orders", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "suite" {
		t.Fatalf("status = %d body = %q, want suite rule", rec.Code, rec.Body.String())
	}
}

func TestGovernanceManagerOverridesLaterSuite(t *testing.T) {
	manager, err := governance.NewManager(governance.Config{Rules: []governance.Rule{{
		Name:      "orders-live",
		Transport: governance.TransportREST,
		Service:   "orders",
		Path:      "/orders",
		Policy:    governance.Policy{Headers: map[string]string{"X-Version": "live"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	suite := governance.MustNewSuite(governance.NewPlugin("stale", governance.Rule{
		Name:      "orders-stale",
		Transport: governance.TransportREST,
		Service:   "orders",
		Path:      "/orders",
		Policy:    governance.Policy{Headers: map[string]string{"X-Version": "stale"}},
	}))
	s := MustNewServer(Config{Name: "orders"}, WithGovernanceManager(manager), WithGovernanceSuite(suite))
	s.AddRoute(Route{Method: http.MethodGet, Path: "/orders", Handler: func(ctx *Context) {
		ctx.String(http.StatusOK, ctx.Request.Header.Get("X-Version"))
	}})

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/orders", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "live" {
		t.Fatalf("status = %d body = %q, want manager rule", rec.Code, rec.Body.String())
	}
}

func TestServerGovernanceRuleSetAppliesRoutePolicy(t *testing.T) {
	rules := governance.NewRuleSet(governance.Rule{
		Transport: governance.TransportREST,
		Service:   "orders",
		Method:    http.MethodPost,
		Path:      "/upload",
		Policy: governance.Policy{
			MaxBodyBytes: 4,
			Headers:      map[string]string{"X-Governance": "on"},
		},
	})
	s := MustNewServer(Config{Name: "orders"}, WithGovernanceRuleSet(rules))
	s.AddRoute(Route{Method: http.MethodPost, Path: "/upload", Handler: func(ctx *Context) {
		ctx.String(http.StatusOK, ctx.Request.Header.Get("X-Governance"))
	}})

	blocked := httptest.NewRecorder()
	s.Handler().ServeHTTP(blocked, httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("too large")))
	if blocked.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("blocked status = %d, want %d", blocked.Code, http.StatusRequestEntityTooLarge)
	}

	allowed := httptest.NewRecorder()
	s.Handler().ServeHTTP(allowed, httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("ok")))
	if allowed.Code != http.StatusOK || strings.TrimSpace(allowed.Body.String()) != "on" || allowed.Header().Get("X-Governance") != "on" {
		t.Fatalf("allowed status = %d body = %q headers = %#v", allowed.Code, allowed.Body.String(), allowed.Header())
	}
	if got := s.Governance().Rules; len(got) != 1 || got[0].Service != "orders" {
		t.Fatalf("governance rules = %#v, want orders rule", got)
	}
	governanceSnapshot := s.Governance()
	if stats := governanceSnapshot.RuleStats; len(stats) != 1 || stats[0].Hits != 2 {
		t.Fatalf("rule stats = %#v, want two runtime hits", stats)
	}
	if len(governanceSnapshot.RuleEvents) == 0 || governanceSnapshot.RuleStatus.Events != len(governanceSnapshot.RuleEvents) {
		t.Fatalf("governance snapshot = %#v, want rule events in diagnostics", governanceSnapshot)
	}

	rules.Replace(governance.Rule{
		Transport: governance.TransportREST,
		Service:   "orders",
		Method:    http.MethodPost,
		Path:      "/upload",
		Policy: governance.Policy{
			MaxBodyBytes: 16,
			Headers:      map[string]string{"X-Governance": "hot"},
		},
	})
	reloaded := httptest.NewRecorder()
	s.Handler().ServeHTTP(reloaded, httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("larger-ok")))
	if reloaded.Code != http.StatusOK || strings.TrimSpace(reloaded.Body.String()) != "hot" || reloaded.Header().Get("X-Governance") != "hot" {
		t.Fatalf("reloaded status = %d body = %q headers = %#v", reloaded.Code, reloaded.Body.String(), reloaded.Header())
	}
}

func TestServerGovernanceRuleSetAppliesCanaryPolicy(t *testing.T) {
	rules := governance.NewRuleSet(governance.Rule{
		Transport: governance.TransportREST,
		Service:   "orders",
		Method:    http.MethodGet,
		Path:      "/canary",
		Policy: governance.Policy{Canary: governance.CanaryPolicy{
			Ratio:        1,
			Service:      "orders-gray",
			Headers:      map[string]string{"X-Gray": "true"},
			MatchHeaders: map[string]string{"X-Use-Gray": "1"},
		}},
	})
	s := MustNewServer(Config{Name: "orders"}, WithGovernanceRuleSet(rules))
	s.AddRoute(Route{Method: http.MethodGet, Path: "/canary", Handler: func(ctx *Context) {
		ctx.String(http.StatusOK, ctx.Request.Header.Get(governance.HeaderCanary)+":"+ctx.Request.Header.Get(governance.HeaderCanaryService)+":"+ctx.Request.Header.Get("X-Gray"))
	}})

	miss := httptest.NewRecorder()
	s.Handler().ServeHTTP(miss, httptest.NewRequest(http.MethodGet, "/canary", nil))
	if miss.Code != http.StatusOK || strings.TrimSpace(miss.Body.String()) != "::" || miss.Header().Get(governance.HeaderCanary) != "" {
		t.Fatalf("miss status = %d body = %q headers = %#v", miss.Code, miss.Body.String(), miss.Header())
	}

	req := httptest.NewRequest(http.MethodGet, "/canary", nil)
	req.Header.Set("X-Use-Gray", "1")
	hit := httptest.NewRecorder()
	s.Handler().ServeHTTP(hit, req)
	if hit.Code != http.StatusOK || strings.TrimSpace(hit.Body.String()) != "true:orders-gray:true" || hit.Header().Get(governance.HeaderCanaryService) != "orders-gray" || hit.Header().Get("X-Gray") != "true" {
		t.Fatalf("hit status = %d body = %q headers = %#v", hit.Code, hit.Body.String(), hit.Header())
	}
}

func TestServerGovernanceRuleSetEnforcesResiliencePolicy(t *testing.T) {
	t.Run("rate limit", func(t *testing.T) {
		rules := governance.NewRuleSet(governance.Rule{
			Name:      "rest-rate",
			Transport: governance.TransportREST,
			Service:   "orders",
			Method:    http.MethodGet,
			Path:      "/limited",
			Policy:    governance.Policy{RateLimit: governance.RateLimitPolicy{Rate: 1, Burst: 1}},
		})
		s := MustNewServer(Config{Name: "orders"}, WithGovernanceRuleSet(rules))
		s.AddRoute(Route{Method: http.MethodGet, Path: "/limited", Handler: func(ctx *Context) {
			ctx.String(http.StatusOK, "ok")
		}})

		first := httptest.NewRecorder()
		s.Handler().ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/limited", nil))
		if first.Code != http.StatusOK {
			t.Fatalf("first status = %d, want %d", first.Code, http.StatusOK)
		}
		second := httptest.NewRecorder()
		s.Handler().ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/limited", nil))
		if second.Code != http.StatusTooManyRequests {
			t.Fatalf("second status = %d, want %d", second.Code, http.StatusTooManyRequests)
		}
	})

	t.Run("concurrency", func(t *testing.T) {
		rules := governance.NewRuleSet(governance.Rule{
			Name:      "rest-concurrency",
			Transport: governance.TransportREST,
			Service:   "orders",
			Method:    http.MethodGet,
			Path:      "/busy",
			Policy:    governance.Policy{Concurrency: governance.ConcurrencyPolicy{Limit: 1}},
		})
		s := MustNewServer(Config{Name: "orders"}, WithGovernanceRuleSet(rules))
		entered := make(chan struct{})
		release := make(chan struct{})
		done := make(chan struct{})
		s.AddRoute(Route{Method: http.MethodGet, Path: "/busy", Handler: func(ctx *Context) {
			close(entered)
			<-release
			ctx.String(http.StatusOK, "ok")
		}})

		first := httptest.NewRecorder()
		go func() {
			defer close(done)
			s.Handler().ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/busy", nil))
		}()
		<-entered
		second := httptest.NewRecorder()
		s.Handler().ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/busy", nil))
		if second.Code != http.StatusServiceUnavailable {
			t.Fatalf("second status = %d, want %d", second.Code, http.StatusServiceUnavailable)
		}
		close(release)
		<-done
		if first.Code != http.StatusOK {
			t.Fatalf("first status = %d, want %d", first.Code, http.StatusOK)
		}
	})

	t.Run("breaker", func(t *testing.T) {
		rules := governance.NewRuleSet(governance.Rule{
			Name:      "rest-breaker",
			Transport: governance.TransportREST,
			Service:   "orders",
			Method:    http.MethodGet,
			Path:      "/flaky",
			Policy: governance.Policy{Breaker: governance.BreakerPolicy{
				Enabled:      true,
				MinRequests:  1,
				FailureRatio: 0.1,
				OpenTimeout:  time.Second,
			}},
		})
		s := MustNewServer(Config{Name: "orders"}, WithGovernanceRuleSet(rules))
		s.AddRoute(Route{Method: http.MethodGet, Path: "/flaky", Handler: func(ctx *Context) {
			http.Error(ctx.Response, "bad", http.StatusInternalServerError)
		}})

		for i := 0; i < 2; i++ {
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/flaky", nil))
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("failure %d status = %d, want %d", i, rec.Code, http.StatusInternalServerError)
			}
		}
		blocked := httptest.NewRecorder()
		s.Handler().ServeHTTP(blocked, httptest.NewRequest(http.MethodGet, "/flaky", nil))
		if blocked.Code != http.StatusServiceUnavailable {
			t.Fatalf("blocked status = %d, want %d", blocked.Code, http.StatusServiceUnavailable)
		}
	})
}

func TestMetricsMiddlewareRecordsRequests(t *testing.T) {
	reg := metrics.NewRegistry()
	s := MustNewServer(Config{})
	s.Use(MetricsMiddleware(reg))
	s.AddRoute(Route{Method: http.MethodGet, Path: "/ping", Handler: func(ctx *Context) { ctx.String(200, "pong") }})
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var snapshot metrics.Snapshot
	data, _ := json.Marshal(reg.Snapshot())
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Requests != 1 {
		t.Fatalf("requests = %d, want 1", snapshot.Requests)
	}
}

func TestSecurityHeadersMiddleware(t *testing.T) {
	s := MustNewServer(Config{Middlewares: MiddlewaresConfig{SecurityHeaders: &SecurityHeadersConfig{ContentSecurityPolicy: "default-src 'self'", HSTS: "max-age=31536000"}}})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/secure", Handler: func(ctx *Context) { ctx.String(http.StatusOK, "ok") }})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/secure", nil))
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("X-Frame-Options") != "DENY" || rec.Header().Get("Content-Security-Policy") != "default-src 'self'" {
		t.Fatalf("security headers = %#v", rec.Header())
	}
}

func TestLogRedactorMasksConfiguredQueryAndHeaders(t *testing.T) {
	redactor := newLogRedactor(LogRedactionConfig{Headers: []string{"Authorization"}, Queries: []string{"token"}})
	req := httptest.NewRequest(http.MethodGet, "/users/1?token=secret&keep=yes", nil)
	req.Header.Set("Authorization", "Bearer secret")
	if got := redactor.path(req); !strings.Contains(got, "token=%2A%2A%2A") || strings.Contains(got, "secret") {
		t.Fatalf("redacted path = %q", got)
	}
	if got := redactor.safeHeaders(req.Header); got["Authorization"] != "***" {
		t.Fatalf("redacted headers = %#v", got)
	}
}

func TestConfiguredMetricsUseRoutePattern(t *testing.T) {
	reg := metrics.NewRegistry()
	old := metrics.Default
	metrics.Default = reg
	t.Cleanup(func() { metrics.Default = old })

	s := MustNewServer(Config{Middlewares: MiddlewaresConfig{Metrics: true}})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/users/{id}", Handler: func(ctx *Context) { ctx.String(http.StatusOK, ctx.PathValue("id")) }})

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/123", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	snapshot := reg.Snapshot()
	if _, ok := snapshot.Routes["GET /users/{id}"]; !ok {
		t.Fatalf("route metrics = %#v, want GET /users/{id}", snapshot.Routes)
	}
	if _, ok := snapshot.Routes["GET /users/123"]; ok {
		t.Fatalf("route metrics used raw URL path: %#v", snapshot.Routes)
	}
}

func TestAdaptiveRateLimitMiddlewareRejectsWhenSaturated(t *testing.T) {
	limiter := limit.NewAdaptiveLimiter(limit.WithAdaptiveLimits(1, 1), limit.WithAdaptiveInitialLimit(1))
	first, err := limiter.Allow()
	if err != nil {
		t.Fatalf("pre-acquire limiter: %v", err)
	}
	defer first.Done(true)

	s := MustNewServer(Config{})
	s.Use(AdaptiveRateLimitMiddleware(limiter))
	s.AddRoute(Route{Method: http.MethodGet, Path: "/limited", Handler: func(ctx *Context) { ctx.String(http.StatusOK, "ok") }})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/limited", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
}

func TestBearerAuthMiddleware(t *testing.T) {
	s := MustNewServer(Config{})
	s.Use(BearerAuthMiddleware(auth.StaticTokenValidator("secret", "tester")))
	s.AddRoute(Route{Method: http.MethodGet, Path: "/private", Handler: func(ctx *Context) {
		ctx.String(http.StatusOK, auth.SubjectFromContext(ctx.Request.Context()))
	}})

	unauthorized := httptest.NewRecorder()
	s.Handler().ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/private", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", unauthorized.Code, http.StatusUnauthorized)
	}

	req := httptest.NewRequest(http.MethodGet, "/private", nil)
	req.Header.Set(auth.AuthorizationHeader, auth.BearerValue("secret"))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "tester" {
		t.Fatalf("body = %q, want tester", rec.Body.String())
	}
}

func TestRequireAuthorizationMiddleware(t *testing.T) {
	validator := func(ctx context.Context, token string) (context.Context, error) {
		switch token {
		case "admin":
			return auth.NewContext(ctx, auth.Principal{Subject: "admin", Roles: []string{"admin"}}), nil
		case "viewer":
			return auth.NewContext(ctx, auth.Principal{Subject: "viewer", Roles: []string{"viewer"}}), nil
		default:
			return ctx, auth.ErrInvalidCredentials
		}
	}

	s := MustNewServer(Config{})
	s.Use(BearerAuthMiddleware(validator))
	s.Use(RequireAuthorizationMiddleware(auth.RequireRoles("admin")))
	s.AddRoute(Route{Method: http.MethodGet, Path: "/admin", Handler: func(ctx *Context) {
		ctx.String(http.StatusOK, "ok")
	}})

	// admin allowed
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.Header.Set(auth.AuthorizationHeader, auth.BearerValue("admin"))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin status = %d, want %d", rec.Code, http.StatusOK)
	}

	// viewer forbidden
	req = httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.Header.Set(auth.AuthorizationHeader, auth.BearerValue("viewer"))
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestRouterGroupAndRequestID(t *testing.T) {
	s := MustNewServer(Config{Middlewares: MiddlewaresConfig{RequestID: true}})
	api := s.Group("/api")
	api.AddRoute(Route{Method: http.MethodGet, Path: "/ping", Handler: func(ctx *Context) {
		if got := metadata.RequestIDFromContext(ctx.Request.Context()); got != "rid-123" {
			t.Fatalf("request id in context = %q, want rid-123", got)
		}
		ctx.String(http.StatusOK, "pong")
	}})
	req := httptest.NewRequest(http.MethodGet, "/api/ping", nil)
	req.Header.Set(RequestIDHeader, "rid-123")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Header().Get(RequestIDHeader) != "rid-123" {
		t.Fatalf("response request id = %q, want rid-123", rec.Header().Get(RequestIDHeader))
	}
}

func TestTraceMiddlewarePropagatesTraceParent(t *testing.T) {
	const parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	s := MustNewServer(Config{Name: "hello", Middlewares: MiddlewaresConfig{Trace: true}})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/trace", Handler: func(ctx *Context) {
		sc, ok := trace.FromContext(ctx.Request.Context())
		if !ok {
			t.Fatal("trace context missing")
		}
		if sc.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
			t.Fatalf("trace id = %q", sc.TraceID)
		}
		md, ok := metadata.FromContext(ctx.Request.Context())
		if !ok || md.Get(trace.TraceParentHeader) == "" {
			t.Fatalf("metadata = %#v, want traceparent", md)
		}
		ctx.String(http.StatusOK, sc.SpanID)
	}})
	req := httptest.NewRequest(http.MethodGet, "/trace", nil)
	req.Header.Set(trace.TraceParentHeader, parent)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	responseTraceParent := rec.Header().Get(trace.TraceParentHeader)
	sc, ok := trace.ParseTraceParent(responseTraceParent)
	if !ok {
		t.Fatalf("response traceparent = %q should parse", responseTraceParent)
	}
	if sc.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || sc.SpanID == "00f067aa0ba902b7" {
		t.Fatalf("response span = %#v, want child span", sc)
	}
}

func TestTraceMiddlewareCanSampleOut(t *testing.T) {
	s := MustNewServer(Config{Name: "hello", Middlewares: MiddlewaresConfig{
		Trace:         true,
		TraceSampling: &SamplingConfig{Ratio: 0},
	}})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/trace", Handler: func(ctx *Context) {
		sc, ok := trace.FromContext(ctx.Request.Context())
		if !ok {
			t.Fatal("trace context missing")
		}
		if sc.Sampled {
			t.Fatal("span should be sampled out")
		}
		md, ok := metadata.FromContext(ctx.Request.Context())
		if !ok || md.Get(trace.SampledKey) != "false" {
			t.Fatalf("metadata = %#v, want sampled=false", md)
		}
		ctx.String(http.StatusOK, "ok")
	}})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/trace", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	responseTraceParent := rec.Header().Get(trace.TraceParentHeader)
	sc, ok := trace.ParseTraceParent(responseTraceParent)
	if !ok {
		t.Fatalf("response traceparent = %q should parse", responseTraceParent)
	}
	if sc.Sampled {
		t.Fatalf("response span = %#v, want sampled=false", sc)
	}
}

func TestAddRoutesOptions(t *testing.T) {
	s := MustNewServer(Config{})
	s.AddRoutes([]Route{{Method: http.MethodGet, Path: "/private", Handler: func(ctx *Context) {
		ctx.String(http.StatusOK, auth.SubjectFromContext(ctx.Request.Context()))
	}}}, WithPrefix("/api"), WithAuth(auth.StaticTokenValidator("secret", "tester")))

	unauthorized := httptest.NewRecorder()
	s.Handler().ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/private", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", unauthorized.Code, http.StatusUnauthorized)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/private", nil)
	req.Header.Set(auth.AuthorizationHeader, auth.BearerValue("secret"))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "tester" {
		t.Fatalf("body = %q, want tester", rec.Body.String())
	}
}

func TestRouteOptionsOverrideGlobalMiddlewares(t *testing.T) {
	s := MustNewServer(Config{MaxBodyBytes: 8, Middlewares: MiddlewaresConfig{
		Recover:         true,
		Trace:           true,
		Log:             true,
		Timeout:         true,
		Breaker:         true,
		RateLimit:       true,
		RateLimitConfig: RateLimitConfig{Rate: 2, Burst: 3},
		MaxConcurrency:  true,
		Metrics:         true,
		RequestID:       true,
	}})
	disabled := s.routeOptions(
		WithoutRecover(),
		WithoutTrace(),
		WithoutRequestID(),
		WithoutLog(),
		WithoutMetrics(),
		WithoutRateLimit(),
		WithoutMaxConcurrency(),
		WithoutBreaker(),
		WithoutTimeout(),
		WithoutMaxBodyBytes(),
		WithoutTrimStrings(),
	)
	if boolEnabled(disabled.recover) || boolEnabled(disabled.trace) || boolEnabled(disabled.requestID) || boolEnabled(disabled.log) || boolEnabled(disabled.metrics) || boolEnabled(disabled.breaker) || trimStringsEnabled(disabled.trimStrings) {
		t.Fatalf("disabled route options = %#v, want all bool middlewares disabled", disabled)
	}
	if disabled.rateLimit != nil && disabled.rateLimit.enabled {
		t.Fatalf("rate limit = %#v, want disabled", disabled.rateLimit)
	}
	if disabled.concurrency != nil && disabled.concurrency.enabled {
		t.Fatalf("max concurrency = %#v, want disabled", disabled.concurrency)
	}
	if disabled.timeout != 0 || disabled.maxBodyBytes != 0 {
		t.Fatalf("timeout/maxBodyBytes = %v/%d, want disabled", disabled.timeout, disabled.maxBodyBytes)
	}

	enabled := MustNewServer(Config{}).routeOptions(
		WithRecover(),
		WithTrace(),
		WithRequestID(),
		WithLog(),
		WithMetrics(),
		WithRateLimit(4, 5),
		WithMaxConcurrency(6),
		WithBreaker(),
		WithTimeout(time.Millisecond),
		WithMaxBodyBytes(16),
		WithTrimStrings(),
	)
	if !boolEnabled(enabled.recover) || !boolEnabled(enabled.trace) || !boolEnabled(enabled.requestID) || !boolEnabled(enabled.log) || !boolEnabled(enabled.metrics) || !boolEnabled(enabled.breaker) || !trimStringsEnabled(enabled.trimStrings) {
		t.Fatalf("enabled route options = %#v, want all bool middlewares enabled", enabled)
	}
	if enabled.rateLimit == nil || !enabled.rateLimit.enabled || enabled.rateLimit.rate != 4 || enabled.rateLimit.burst != 5 {
		t.Fatalf("rate limit = %#v, want rate=4 burst=5", enabled.rateLimit)
	}
	if enabled.concurrency == nil || !enabled.concurrency.enabled || enabled.concurrency.limit != 6 {
		t.Fatalf("max concurrency = %#v, want limit=6", enabled.concurrency)
	}
	if enabled.timeout != time.Millisecond || enabled.maxBodyBytes != 16 {
		t.Fatalf("timeout/maxBodyBytes = %v/%d, want 1ms/16", enabled.timeout, enabled.maxBodyBytes)
	}
}

func TestWithTraceRequestIDAndMetricsEnableRouteWhenGlobalDisabled(t *testing.T) {
	reg := metrics.NewRegistry()
	old := metrics.Default
	metrics.Default = reg
	t.Cleanup(func() { metrics.Default = old })

	s := MustNewServer(Config{Name: "hello"})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/observed", Handler: func(ctx *Context) {
		if metadata.RequestIDFromContext(ctx.Request.Context()) == "" {
			t.Fatal("request id missing from context")
		}
		if _, ok := trace.FromContext(ctx.Request.Context()); !ok {
			t.Fatal("trace context missing")
		}
		ctx.String(http.StatusOK, "ok")
	}}, WithTrace(), WithRequestID(), WithMetrics())

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/observed", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Header().Get(RequestIDHeader) == "" {
		t.Fatal("request id header missing")
	}
	if rec.Header().Get(trace.TraceParentHeader) == "" {
		t.Fatal("traceparent header missing")
	}
	if _, ok := reg.Snapshot().Routes["GET /observed"]; !ok {
		t.Fatalf("route metrics = %#v, want GET /observed", reg.Snapshot().Routes)
	}
}

func TestWithoutTraceRequestIDAndMetricsDisableGlobalRoute(t *testing.T) {
	reg := metrics.NewRegistry()
	old := metrics.Default
	metrics.Default = reg
	t.Cleanup(func() { metrics.Default = old })

	s := MustNewServer(Config{Name: "hello", Middlewares: MiddlewaresConfig{Trace: true, RequestID: true, Metrics: true}})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/raw", Handler: func(ctx *Context) {
		if metadata.RequestIDFromContext(ctx.Request.Context()) != "" {
			t.Fatal("request id should be disabled")
		}
		if _, ok := trace.FromContext(ctx.Request.Context()); ok {
			t.Fatal("trace context should be disabled")
		}
		ctx.String(http.StatusOK, "ok")
	}}, WithoutTrace(), WithoutRequestID(), WithoutMetrics())

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/raw", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Header().Get(RequestIDHeader) != "" {
		t.Fatalf("request id header = %q, want empty", rec.Header().Get(RequestIDHeader))
	}
	if rec.Header().Get(trace.TraceParentHeader) != "" {
		t.Fatalf("traceparent header = %q, want empty", rec.Header().Get(trace.TraceParentHeader))
	}
	if got := reg.Snapshot().Requests; got != 0 {
		t.Fatalf("metrics requests = %d, want 0", got)
	}
}

func TestWithRecoverEnablesRouteWhenGlobalDisabled(t *testing.T) {
	s := MustNewServer(Config{})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/panic", Handler: func(ctx *Context) {
		panic("boom")
	}}, WithRecover())

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestWithoutRecoverDisablesGlobalRouteRecover(t *testing.T) {
	s := MustNewServer(Config{Middlewares: MiddlewaresConfig{Recover: true}})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/panic", Handler: func(ctx *Context) {
		panic("boom")
	}}, WithoutRecover())

	defer func() {
		if recover() == nil {
			t.Fatal("ServeHTTP should panic when route recover is disabled")
		}
	}()
	s.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/panic", nil))
}

func TestMaxBodyBytesMiddleware(t *testing.T) {
	s := MustNewServer(Config{})
	s.AddRoute(Route{Method: http.MethodPost, Path: "/echo", Handler: func(ctx *Context) {
		data, err := io.ReadAll(ctx.Request.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		ctx.String(http.StatusOK, string(data))
	}}, WithMaxBodyBytes(4))

	tooLarge := httptest.NewRecorder()
	s.Handler().ServeHTTP(tooLarge, httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader("12345")))
	if tooLarge.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too large status = %d, want %d", tooLarge.Code, http.StatusRequestEntityTooLarge)
	}

	ok := httptest.NewRecorder()
	s.Handler().ServeHTTP(ok, httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader("1234")))
	if ok.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", ok.Code, http.StatusOK)
	}
	if ok.Body.String() != "1234" {
		t.Fatalf("body = %q, want 1234", ok.Body.String())
	}
}

func TestWithRateLimitLimitsRouteOnly(t *testing.T) {
	s := MustNewServer(Config{})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/limited", Handler: func(ctx *Context) {
		ctx.String(http.StatusOK, "limited")
	}}, WithRateLimit(1, 1))
	s.AddRoute(Route{Method: http.MethodGet, Path: "/open", Handler: func(ctx *Context) {
		ctx.String(http.StatusOK, "open")
	}})

	first := httptest.NewRecorder()
	s.Handler().ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/limited", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want %d", first.Code, http.StatusOK)
	}

	second := httptest.NewRecorder()
	s.Handler().ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/limited", nil))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want %d", second.Code, http.StatusTooManyRequests)
	}

	openFirst := httptest.NewRecorder()
	s.Handler().ServeHTTP(openFirst, httptest.NewRequest(http.MethodGet, "/open", nil))
	if openFirst.Code != http.StatusOK {
		t.Fatalf("open first status = %d, want %d", openFirst.Code, http.StatusOK)
	}
	openSecond := httptest.NewRecorder()
	s.Handler().ServeHTTP(openSecond, httptest.NewRequest(http.MethodGet, "/open", nil))
	if openSecond.Code != http.StatusOK {
		t.Fatalf("open second status = %d, want %d", openSecond.Code, http.StatusOK)
	}
}

func TestWithMaxConcurrencyRejectsOverloadedRoute(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	s := MustNewServer(Config{})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/limited", Handler: func(ctx *Context) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		ctx.String(http.StatusOK, "limited")
	}}, WithMaxConcurrency(1))

	var wg sync.WaitGroup
	first := httptest.NewRecorder()
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.Handler().ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/limited", nil))
	}()
	<-entered

	second := httptest.NewRecorder()
	s.Handler().ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/limited", nil))
	if second.Code != http.StatusServiceUnavailable {
		t.Fatalf("second status = %d, want %d", second.Code, http.StatusServiceUnavailable)
	}
	close(release)
	wg.Wait()
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want %d", first.Code, http.StatusOK)
	}
}

func TestWithoutMaxConcurrencyDisablesGlobalRouteLimit(t *testing.T) {
	s := MustNewServer(Config{Middlewares: MiddlewaresConfig{
		MaxConcurrency:       true,
		MaxConcurrencyConfig: MaxConcurrencyConfig{Limit: 1},
	}})
	entered := make(chan struct{})
	release := make(chan struct{})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/limited", Handler: func(ctx *Context) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		ctx.String(http.StatusOK, "limited")
	}})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/open", Handler: func(ctx *Context) {
		ctx.String(http.StatusOK, "open")
	}}, WithoutMaxConcurrency())

	var wg sync.WaitGroup
	limitedFirst := httptest.NewRecorder()
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.Handler().ServeHTTP(limitedFirst, httptest.NewRequest(http.MethodGet, "/limited", nil))
	}()
	<-entered
	limitedSecond := httptest.NewRecorder()
	s.Handler().ServeHTTP(limitedSecond, httptest.NewRequest(http.MethodGet, "/limited", nil))
	if limitedSecond.Code != http.StatusServiceUnavailable {
		t.Fatalf("limited second status = %d, want %d", limitedSecond.Code, http.StatusServiceUnavailable)
	}
	close(release)
	wg.Wait()

	openFirst := httptest.NewRecorder()
	s.Handler().ServeHTTP(openFirst, httptest.NewRequest(http.MethodGet, "/open", nil))
	if openFirst.Code != http.StatusOK {
		t.Fatalf("open first status = %d, want %d", openFirst.Code, http.StatusOK)
	}
	openSecond := httptest.NewRecorder()
	s.Handler().ServeHTTP(openSecond, httptest.NewRequest(http.MethodGet, "/open", nil))
	if openSecond.Code != http.StatusOK {
		t.Fatalf("open second status = %d, want %d", openSecond.Code, http.StatusOK)
	}
}

func TestWithoutRateLimitDisablesGlobalRouteLimit(t *testing.T) {
	s := MustNewServer(Config{Middlewares: MiddlewaresConfig{
		RateLimit:       true,
		RateLimitConfig: RateLimitConfig{Rate: 1, Burst: 1},
	}})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/limited", Handler: func(ctx *Context) {
		ctx.String(http.StatusOK, "limited")
	}})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/open", Handler: func(ctx *Context) {
		ctx.String(http.StatusOK, "open")
	}}, WithoutRateLimit())

	limitedFirst := httptest.NewRecorder()
	s.Handler().ServeHTTP(limitedFirst, httptest.NewRequest(http.MethodGet, "/limited", nil))
	if limitedFirst.Code != http.StatusOK {
		t.Fatalf("limited first status = %d, want %d", limitedFirst.Code, http.StatusOK)
	}
	limitedSecond := httptest.NewRecorder()
	s.Handler().ServeHTTP(limitedSecond, httptest.NewRequest(http.MethodGet, "/limited", nil))
	if limitedSecond.Code != http.StatusTooManyRequests {
		t.Fatalf("limited second status = %d, want %d", limitedSecond.Code, http.StatusTooManyRequests)
	}

	openFirst := httptest.NewRecorder()
	s.Handler().ServeHTTP(openFirst, httptest.NewRequest(http.MethodGet, "/open", nil))
	if openFirst.Code != http.StatusOK {
		t.Fatalf("open first status = %d, want %d", openFirst.Code, http.StatusOK)
	}
	openSecond := httptest.NewRecorder()
	s.Handler().ServeHTTP(openSecond, httptest.NewRequest(http.MethodGet, "/open", nil))
	if openSecond.Code != http.StatusOK {
		t.Fatalf("open second status = %d, want %d", openSecond.Code, http.StatusOK)
	}
}

func TestTrimStringsDefaultEnabled(t *testing.T) {
	s := MustNewServer(Config{})
	s.AddRoute(Route{Method: http.MethodPost, Path: "/trim", Handler: func(ctx *Context) {
		var req struct {
			Name   string   `json:"name"`
			Tags   []string `json:"tags"`
			Nested struct {
				Value string `json:"value"`
			} `json:"nested"`
		}
		if err := ctx.Bind(&req); err != nil {
			ctx.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusOK, map[string]string{
			"query":  ctx.Query("q"),
			"name":   req.Name,
			"tag":    req.Tags[0],
			"nested": req.Nested.Value,
		})
	}})

	req := httptest.NewRequest(http.MethodPost, "/trim?q=%20hello%20", strings.NewReader(` {"name":"  gofly  ","tags":["  rpc  "],"nested":{"value":"  ok  "}} `))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"query": "hello", "name": "gofly", "tag": "rpc", "nested": "ok"} {
		if got[key] != want {
			t.Fatalf("%s = %q, want %q", key, got[key], want)
		}
	}
}

func TestWithoutTrimStringsDisablesRoute(t *testing.T) {
	s := MustNewServer(Config{})
	s.AddRoute(Route{Method: http.MethodPost, Path: "/raw", Handler: func(ctx *Context) {
		var req struct {
			Name string `json:"name"`
		}
		if err := ctx.Bind(&req); err != nil {
			ctx.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusOK, map[string]string{"query": ctx.Query("q"), "name": req.Name})
	}}, WithoutTrimStrings())

	req := httptest.NewRequest(http.MethodPost, "/raw?q=%20hello%20", strings.NewReader(`{"name":"  gofly  "}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["query"] != " hello " || got["name"] != "  gofly  " {
		t.Fatalf("got = %#v, want untrimmed values", got)
	}
}

func TestWithTrimStringsEnablesRouteWhenGlobalDisabled(t *testing.T) {
	disabled := false
	s := MustNewServer(Config{Middlewares: MiddlewaresConfig{TrimStrings: &disabled}})
	s.AddRoute(Route{Method: http.MethodPost, Path: "/trim", Handler: func(ctx *Context) {
		var req struct {
			Name string `json:"name"`
		}
		if err := ctx.Bind(&req); err != nil {
			ctx.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusOK, map[string]string{"query": ctx.Query("q"), "name": req.Name})
	}}, WithTrimStrings())

	req := httptest.NewRequest(http.MethodPost, "/trim?q=%20hello%20", strings.NewReader(` {"name":"  gofly  "} `))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["query"] != "hello" || got["name"] != "gofly" {
		t.Fatalf("got = %#v, want trimmed values", got)
	}
}

func TestTimeoutMiddlewareWritesGatewayTimeout(t *testing.T) {
	s := MustNewServer(Config{})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/slow", Handler: func(ctx *Context) {
		<-ctx.Request.Context().Done()
		time.Sleep(10 * time.Millisecond)
		ctx.String(http.StatusOK, "late")
	}}, WithTimeout(time.Millisecond))

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/slow", nil))
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusGatewayTimeout)
	}
}

func TestAdaptiveBreakerMiddleware(t *testing.T) {
	s := MustNewServer(Config{})
	s.Use(AdaptiveBreakerMiddleware(breaker.NewAdaptive(
		breaker.WithAdaptiveMinRequests(1),
		breaker.WithAdaptiveFailureRatio(0.1),
		breaker.WithAdaptiveK(1),
		breaker.WithAdaptiveOpenTimeout(time.Second),
	)))
	s.AddRoute(Route{Method: http.MethodGet, Path: "/unstable", Handler: func(ctx *Context) {
		http.Error(ctx.Response, "failed", http.StatusInternalServerError)
	}})

	first := httptest.NewRecorder()
	s.Handler().ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/unstable", nil))
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("first status = %d, want 500", first.Code)
	}

	second := httptest.NewRecorder()
	s.Handler().ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/unstable", nil))
	if second.Code != http.StatusServiceUnavailable {
		t.Fatalf("second status = %d, want 503", second.Code)
	}
}

func TestConfiguredAdaptiveBreakerUsesBreakerConfig(t *testing.T) {
	s := MustNewServer(Config{Middlewares: MiddlewaresConfig{
		Breaker: true,
		BreakerConfig: BreakerConfig{
			MinRequests:  1,
			FailureRatio: 0.1,
			K:            1,
			OpenTimeout:  time.Second,
		},
	}})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/configured-unstable", Handler: func(ctx *Context) {
		http.Error(ctx.Response, "failed", http.StatusInternalServerError)
	}})

	first := httptest.NewRecorder()
	s.Handler().ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/configured-unstable", nil))
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("first status = %d, want 500", first.Code)
	}

	second := httptest.NewRecorder()
	s.Handler().ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/configured-unstable", nil))
	if second.Code != http.StatusServiceUnavailable {
		t.Fatalf("second status = %d, want 503", second.Code)
	}
}

func TestConfiguredAdaptiveRateLimitRejectsWhenSaturated(t *testing.T) {
	s := MustNewServer(Config{Middlewares: MiddlewaresConfig{
		AdaptiveRateLimit: true,
		AdaptiveLimitConfig: AdaptiveLimitConfig{
			MinLimit:     1,
			MaxLimit:     1,
			InitialLimit: 1,
		},
	}})
	started := make(chan struct{})
	release := make(chan struct{})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/adaptive", Handler: func(ctx *Context) {
		close(started)
		<-release
		ctx.String(http.StatusOK, "ok")
	}})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	done := make(chan error, 1)
	go func() {
		resp, err := http.Get(ts.URL + "/adaptive")
		if err == nil {
			_ = resp.Body.Close()
		}
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first request")
	}
	resp, err := http.Get(ts.URL + "/adaptive")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	snapshot := s.Governance()
	if len(snapshot.Components) == 0 || snapshot.Components[0].Kind != "adaptive_limiter" {
		t.Fatalf("governance = %#v, want adaptive limiter component", snapshot)
	}
}

func TestConfiguredTimeoutUsesTimeoutConfigDuration(t *testing.T) {
	s := MustNewServer(Config{Timeout: time.Second, Middlewares: MiddlewaresConfig{
		Timeout:       true,
		TimeoutConfig: TimeoutConfig{Duration: time.Millisecond},
	}})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/configured-slow", Handler: func(ctx *Context) {
		<-ctx.Request.Context().Done()
		time.Sleep(10 * time.Millisecond)
		ctx.String(http.StatusOK, "late")
	}})

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/configured-slow", nil))
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusGatewayTimeout)
	}
}

func TestConfiguredHealthTimeoutUsesTimeoutConfig(t *testing.T) {
	s := MustNewServer(Config{Timeout: time.Second, Middlewares: MiddlewaresConfig{
		Health:        true,
		TimeoutConfig: TimeoutConfig{HealthTimeout: time.Millisecond},
	}}, WithHealthCheck("slow", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}))

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestConfiguredMaxBodyBytesUsesMiddlewareConfig(t *testing.T) {
	s := MustNewServer(Config{MaxBodyBytes: 1024, Middlewares: MiddlewaresConfig{
		MaxBodyBytesConfig: MaxBodyBytesConfig{Limit: 4},
	}})
	s.AddRoute(Route{Method: http.MethodPost, Path: "/limited-body", Handler: func(ctx *Context) {
		_, _ = io.ReadAll(ctx.Request.Body)
		ctx.String(http.StatusOK, "ok")
	}})

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/limited-body", strings.NewReader("too large")))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestTrimStringsConfigCanDisableBodyTrim(t *testing.T) {
	disabled := false
	s := MustNewServer(Config{Middlewares: MiddlewaresConfig{
		TrimStringsConfig: TrimStringsConfig{Body: &disabled},
	}})
	s.AddRoute(Route{Method: http.MethodPost, Path: "/trim-config", Handler: func(ctx *Context) {
		body, _ := io.ReadAll(ctx.Request.Body)
		ctx.JSON(http.StatusOK, map[string]string{"query": ctx.Query("name"), "body": string(body)})
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/trim-config?name=+gofly+", strings.NewReader("  body  "))
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["query"] != "gofly" || got["body"] != "  body  " {
		t.Fatalf("got = %#v, want query trimmed and body preserved", got)
	}
}
