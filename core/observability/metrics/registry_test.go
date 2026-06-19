package metrics

import (
	"bytes"
	"errors"
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

type errWriter struct{ err error }

func (w errWriter) Write([]byte) (int, error) { return 0, w.err }

func TestRegistryWritePrometheusWriterError_BitsUT(t *testing.T) {
	reg := NewRegistry()
	wantErr := errors.New("write failed")
	if err := reg.WritePrometheus(errWriter{err: wantErr}); !errors.Is(err, wantErr) {
		t.Fatalf("WritePrometheus error = %v, want %v", err, wantErr)
	}
}

func TestRegistryObserveUnknownRouteAndInFlightFloor_BitsUT(t *testing.T) {
	reg := NewRegistry()
	reg.DecInFlight()
	reg.IncInFlight()
	reg.DecInFlight()
	reg.Observe("", 503, 11*time.Millisecond)

	snapshot := reg.Snapshot()
	if snapshot.InFlight != 0 || snapshot.Requests != 1 || snapshot.Errors != 1 || snapshot.Statuses[503] != 1 {
		t.Fatalf("snapshot counters = %#v, want in-flight floor and one 503 error", snapshot)
	}
	unknown := snapshot.Routes["unknown"]
	if unknown.Requests != 1 || unknown.Errors != 1 || unknown.AvgDuration != 11*time.Millisecond || unknown.MaxDuration != 11*time.Millisecond {
		t.Fatalf("unknown route = %#v, want recorded 503 latency", unknown)
	}
}

func TestRegistryPrometheusOrderingAndEscaping_BitsUT(t *testing.T) {
	reg := NewRegistry()
	reg.Observe("route\n\"quoted\"\\slash", 201, 6*time.Millisecond)
	reg.Observe("alpha", 404, time.Millisecond)

	var buf bytes.Buffer
	if err := reg.WritePrometheus(&buf); err != nil {
		t.Fatalf("WritePrometheus: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `gofly_route_requests_total{route="route\n\"quoted\"\\slash"} 1`) {
		t.Fatalf("escaped route label missing:\n%s", out)
	}
	idx201 := strings.Index(out, `gofly_request_status_total{status="201"}`)
	idx404 := strings.Index(out, `gofly_request_status_total{status="404"}`)
	if idx201 < 0 || idx404 < 0 || idx201 > idx404 {
		t.Fatalf("status output ordering invalid: idx201=%d idx404=%d\n%s", idx201, idx404, out)
	}
	if got := bucketSeconds("not-a-duration"); got != 0 {
		t.Fatalf("bucketSeconds invalid = %f, want 0", got)
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
