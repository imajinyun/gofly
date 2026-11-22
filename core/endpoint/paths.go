// Package endpoint defines the canonical URL paths for gofly's built-in
// operational endpoints. Every framework-provided diagnostic surface — health
// probes, metrics, the admin control plane, runtime profiling and HTTP-RPC
// administration — is served under the shared /gofly prefix so operational
// routes and business routes never collide and can be exposed or firewalled
// as a single namespace.
package endpoint

const (
	// RootPrefix is the shared prefix for every gofly operational endpoint.
	RootPrefix = "/gofly"

	// HealthzPath is the liveness probe endpoint.
	HealthzPath = RootPrefix + "/healthz"
	// ReadyzPath is the readiness probe endpoint.
	ReadyzPath = RootPrefix + "/readyz"
	// StartupzPath is the startup probe endpoint.
	StartupzPath = RootPrefix + "/startupz"

	// MetricsPath serves Prometheus text-format metrics.
	MetricsPath = RootPrefix + "/metrics"
	// MetricsJSONPath serves the metrics registry as a JSON snapshot.
	MetricsJSONPath = RootPrefix + "/metrics.json"

	// AdminPrefix is the prefix for framework admin and control-plane endpoints.
	AdminPrefix = RootPrefix + "/admin"
	// ControlPlanePath serves the versioned gofly-control-plane.v1 snapshot.
	ControlPlanePath = AdminPrefix + "/control-plane"

	// PprofPrefix is the prefix for net/http/pprof runtime profiling endpoints.
	PprofPrefix = RootPrefix + "/debug/pprof"

	// RPCAdminPrefix is the prefix for the HTTP-RPC administration endpoints.
	RPCAdminPrefix = RootPrefix + "/rpc/admin"
)
