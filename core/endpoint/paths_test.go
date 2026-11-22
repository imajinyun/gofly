package endpoint

import (
	"strings"
	"testing"
)

// Failure modes for the shared operational path constants:
//  1. A path outside the /gofly namespace would let framework diagnostics
//     collide with business routes and bypass operational firewall rules.
//  2. Two constants resolving to the same path would make endpoint ownership
//     ambiguous for operators and control-plane consumers.
//  3. A trailing slash or non-canonical spelling would break exact-match
//     mux patterns and scripted curl/helm probe configuration.
func TestOperationalPathsShareGoflyNamespace(t *testing.T) {
	paths := []string{
		HealthzPath,
		ReadyzPath,
		StartupzPath,
		MetricsPath,
		MetricsJSONPath,
		AdminPrefix,
		ControlPlanePath,
		PprofPrefix,
		RPCAdminPrefix,
	}
	seen := make(map[string]string, len(paths))
	for _, path := range paths {
		if !strings.HasPrefix(path, RootPrefix+"/") {
			t.Fatalf("operational path %q is not mounted under %s", path, RootPrefix)
		}
		if strings.HasSuffix(path, "/") {
			t.Fatalf("operational path %q must not carry a trailing slash", path)
		}
		if previous, ok := seen[path]; ok {
			t.Fatalf("operational path %q claimed by multiple constants (%s)", path, previous)
		}
		seen[path] = path
	}

	want := map[string]string{
		HealthzPath:      "/gofly/healthz",
		ReadyzPath:       "/gofly/readyz",
		StartupzPath:     "/gofly/startupz",
		MetricsPath:      "/gofly/metrics",
		MetricsJSONPath:  "/gofly/metrics.json",
		AdminPrefix:      "/gofly/admin",
		ControlPlanePath: "/gofly/admin/control-plane",
		PprofPrefix:      "/gofly/debug/pprof",
		RPCAdminPrefix:   "/gofly/rpc/admin",
	}
	for path, expected := range want {
		if path != expected {
			t.Fatalf("canonical path = %q, want %q", path, expected)
		}
	}
}
