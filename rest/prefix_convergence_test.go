package rest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	coreendpoint "github.com/imajinyun/gofly/core/endpoint"
)

// Failure modes for operational endpoint convergence:
//  1. Legacy root-level probe/metrics/admin paths must stop being served,
//     otherwise operational and business routes keep colliding and the
//     /gofly firewall namespace cannot be enforced.
//  2. The canonical /gofly paths must remain reachable with the same
//     token/localhost protection as before.
//  3. An unset AdminConfig.PathPrefix must resolve to /gofly/admin so
//     generated projects converge without explicit configuration.
func TestOperationalRoutesConvergeUnderGoflyPrefix(t *testing.T) {
	s := MustNewServer(Config{
		Name: "convergence",
		Admin: AdminConfig{
			Enabled: true,
			Pprof:   true,
			Token:   "secret",
		},
		Middlewares: MiddlewaresConfig{Health: true, Metrics: true},
	})

	cases := []struct {
		name       string
		path       string
		legacy     bool
		wantStatus int
	}{
		{name: "canonical healthz", path: coreendpoint.HealthzPath, wantStatus: http.StatusOK},
		{name: "legacy healthz removed", path: "/healthz", legacy: true, wantStatus: http.StatusNotFound},
		{name: "canonical readyz", path: coreendpoint.ReadyzPath, wantStatus: http.StatusOK},
		{name: "legacy readyz removed", path: "/readyz", legacy: true, wantStatus: http.StatusNotFound},
		{name: "canonical metrics", path: coreendpoint.MetricsPath, wantStatus: http.StatusOK},
		{name: "legacy metrics removed", path: "/metrics", legacy: true, wantStatus: http.StatusNotFound},
		{name: "canonical startupz", path: coreendpoint.StartupzPath, wantStatus: http.StatusOK},
		{name: "legacy startupz removed", path: "/startupz", legacy: true, wantStatus: http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != tc.wantStatus {
				t.Fatalf("%s status = %d, want %d", tc.path, rec.Code, tc.wantStatus)
			}
		})
	}

	if got := s.adminPrefix(); got != coreendpoint.AdminPrefix {
		t.Fatalf("default admin prefix = %q, want %q", got, coreendpoint.AdminPrefix)
	}

	adminCases := []struct {
		name       string
		path       string
		legacy     bool
		wantStatus int
	}{
		{name: "canonical admin config", path: coreendpoint.AdminPrefix + "/config", wantStatus: http.StatusOK},
		{name: "legacy admin config removed", path: "/admin/config", legacy: true, wantStatus: http.StatusNotFound},
		{name: "canonical control plane", path: coreendpoint.ControlPlanePath, wantStatus: http.StatusOK},
		{name: "legacy control plane removed", path: "/admin/control-plane", legacy: true, wantStatus: http.StatusNotFound},
		{name: "canonical pprof", path: coreendpoint.PprofPrefix + "/", wantStatus: http.StatusOK},
		{name: "legacy admin pprof removed", path: "/admin/pprof/", legacy: true, wantStatus: http.StatusNotFound},
	}
	for _, tc := range adminCases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Authorization", "Bearer secret")
			s.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("%s status = %d, want %d", tc.path, rec.Code, tc.wantStatus)
			}
		})
	}

	if got := s.ControlPlaneRuntime().AdminPrefix; got != coreendpoint.AdminPrefix {
		t.Fatalf("control-plane admin prefix = %q, want %q", got, coreendpoint.AdminPrefix)
	}
}
