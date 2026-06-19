package metrics

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRegistrySnapshot(t *testing.T) {
	reg := NewRegistry()
	reg.IncInFlight()
	reg.Observe("GET /ping", 200, time.Millisecond)
	reg.Observe("GET /panic", 500, 2*time.Millisecond)
	reg.DecInFlight()
	snapshot := reg.Snapshot()
	if snapshot.Requests != 2 {
		t.Fatalf("requests = %d, want 2", snapshot.Requests)
	}
	if snapshot.Errors != 1 {
		t.Fatalf("errors = %d, want 1", snapshot.Errors)
	}
	if snapshot.InFlight != 0 {
		t.Fatalf("inFlight = %d, want 0", snapshot.InFlight)
	}
	if snapshot.Routes["GET /ping"].Requests != 1 {
		t.Fatalf("GET /ping requests = %d, want 1", snapshot.Routes["GET /ping"].Requests)
	}
	if snapshot.Routes["GET /ping"].Buckets["5ms"] != 1 {
		t.Fatalf("GET /ping 5ms bucket = %d, want 1", snapshot.Routes["GET /ping"].Buckets["5ms"])
	}
	if snapshot.Runtime.Goroutines == 0 {
		t.Fatal("runtime goroutines should be populated")
	}
}

func TestRegistryWritePrometheus(t *testing.T) {
	reg := NewRegistry()
	reg.Observe("GET /ping", 200, time.Millisecond)
	var buf bytes.Buffer
	if err := reg.WritePrometheus(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "gofly_requests_total 1") {
		t.Fatalf("prometheus output missing requests: %s", out)
	}
	if !strings.Contains(out, `gofly_route_requests_total{route="GET /ping"} 1`) {
		t.Fatalf("prometheus output missing route requests: %s", out)
	}
	if !strings.Contains(out, `gofly_route_duration_seconds_bucket{route="GET /ping",le="0.005000000"} 1`) {
		t.Fatalf("prometheus output missing histogram bucket: %s", out)
	}
	if !strings.Contains(out, "gofly_runtime_goroutines") {
		t.Fatalf("prometheus output missing runtime metrics: %s", out)
	}
}

func TestRegisterPrometheusCollectors(t *testing.T) {
	reg := NewRegistry()
	reg.Observe("GET /ping", 200, time.Millisecond)
	prom := prometheus.NewRegistry()
	if err := RegisterPrometheusCollectors(prom, "gofly", reg); err != nil {
		t.Fatal(err)
	}
	if err := RegisterPrometheusCollectors(prom, "gofly", reg); err != nil {
		t.Fatal(err)
	}
	if err := testutil.GatherAndCompare(prom, strings.NewReader(`# HELP gofly_requests_total Total number of handled requests.
# TYPE gofly_requests_total gauge
gofly_requests_total 1
`), "gofly_requests_total"); err != nil {
		t.Fatalf("gather prometheus collectors: %v", err)
	}
}
