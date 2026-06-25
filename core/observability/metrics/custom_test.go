package metrics

import (
	"bytes"
	"strings"
	"testing"
)

func TestCustomCounter(t *testing.T) {
	reg := NewRegistry()
	c := reg.Counter("orders_total", "Total orders.", "status")
	c.Inc("ok")
	c.Inc("ok")
	c.Add(3, "failed")
	c.Add(-5, "ok") // ignored, monotonic

	// re-registering returns the same metric
	c2 := reg.Counter("orders_total", "ignored")
	c2.Inc("ok")

	var buf bytes.Buffer
	if err := reg.WritePrometheus(&buf); err != nil {
		t.Fatalf("write prometheus: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "# TYPE orders_total counter") {
		t.Fatalf("missing counter type:\n%s", out)
	}
	if !strings.Contains(out, `orders_total{status="ok"} 3`) {
		t.Fatalf("ok counter wrong:\n%s", out)
	}
	if !strings.Contains(out, `orders_total{status="failed"} 3`) {
		t.Fatalf("failed counter wrong:\n%s", out)
	}
}

func TestCustomGauge(t *testing.T) {
	reg := NewRegistry()
	g := reg.Gauge("queue_depth", "Queue depth.", "queue")
	g.Set(10, "emails")
	g.Inc("emails")
	g.Dec("emails")
	g.Add(5, "emails")

	var buf bytes.Buffer
	if err := reg.WritePrometheus(&buf); err != nil {
		t.Fatalf("write prometheus: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "# TYPE queue_depth gauge") {
		t.Fatalf("missing gauge type:\n%s", out)
	}
	if !strings.Contains(out, `queue_depth{queue="emails"} 15`) {
		t.Fatalf("gauge value wrong:\n%s", out)
	}
}

func TestCustomHistogram(t *testing.T) {
	reg := NewRegistry()
	h := reg.Histogram("op_seconds", "Op duration.", []float64{0.1, 0.5, 1}, "op")
	h.Observe(0.05, "load")
	h.Observe(0.3, "load")
	h.Observe(2, "load")

	var buf bytes.Buffer
	if err := reg.WritePrometheus(&buf); err != nil {
		t.Fatalf("write prometheus: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "# TYPE op_seconds histogram") {
		t.Fatalf("missing histogram type:\n%s", out)
	}
	if !strings.Contains(out, `op_seconds_bucket{op="load",le="0.1"} 1`) {
		t.Fatalf("le=0.1 bucket wrong:\n%s", out)
	}
	if !strings.Contains(out, `op_seconds_bucket{op="load",le="0.5"} 2`) {
		t.Fatalf("le=0.5 bucket wrong:\n%s", out)
	}
	if !strings.Contains(out, `op_seconds_bucket{op="load",le="+Inf"} 3`) {
		t.Fatalf("+Inf bucket wrong:\n%s", out)
	}
	if !strings.Contains(out, `op_seconds_count{op="load"} 3`) {
		t.Fatalf("count wrong:\n%s", out)
	}
	if !strings.Contains(out, `op_seconds_sum{op="load"} 2.35`) {
		t.Fatalf("sum wrong:\n%s", out)
	}
}

func TestCustomMetricsSnapshot(t *testing.T) {
	reg := NewRegistry()
	reg.Counter("orders_total", "Total orders.", "status").Add(2, "ok")
	reg.Gauge("queue_depth", "Queue depth.", "queue").Set(7, "emails")
	reg.Histogram("op_seconds", "Op duration.", []float64{0.1, 0.5}, "op").Observe(0.2, "load")

	snapshot := reg.Snapshot()
	orders := snapshot.Customs["orders_total"]
	if orders.Name != "orders_total" || orders.Type != MetricCounter || len(orders.Series) != 1 || orders.Series[0].Value != 2 {
		t.Fatalf("orders snapshot = %#v, want counter value 2", orders)
	}
	if orders.Series[0].Labels["status"] != "ok" {
		t.Fatalf("orders labels = %#v, want status=ok", orders.Series[0].Labels)
	}
	queue := snapshot.Customs["queue_depth"]
	if queue.Type != MetricGauge || queue.Series[0].Value != 7 {
		t.Fatalf("queue snapshot = %#v, want gauge value 7", queue)
	}
	op := snapshot.Customs["op_seconds"]
	if op.Type != MetricHistogram || op.Series[0].Count != 1 || op.Series[0].Sum != 0.2 {
		t.Fatalf("histogram snapshot = %#v, want count 1 sum 0.2", op)
	}
	if op.Series[0].Counts["0.1"] != 0 || op.Series[0].Counts["0.5"] != 1 || op.Series[0].Counts["+Inf"] != 1 {
		t.Fatalf("histogram buckets = %#v, want cumulative counts", op.Series[0].Counts)
	}
}

func TestPackageLevelCustomMetrics(t *testing.T) {
	c := RegisterCounter("global_events_total", "Global events.")
	c.Inc()
	// re-registering shares the same underlying metric
	RegisterCounter("global_events_total", "ignored").Inc()
	var buf bytes.Buffer
	if err := Default.WritePrometheus(&buf); err != nil {
		t.Fatalf("write prometheus: %v", err)
	}
	if !strings.Contains(buf.String(), "global_events_total 2") {
		t.Fatalf("package counter wrong:\n%s", buf.String())
	}
}

func TestRegisterGaugeAndHistogram(t *testing.T) {
	g := RegisterGauge("global_queue_depth", "Global queue depth.")
	g.Set(42)

	h := RegisterHistogram("global_op_seconds", "Global op duration.", []float64{0.1, 0.5})
	h.Observe(0.3)

	var buf bytes.Buffer
	if err := Default.WritePrometheus(&buf); err != nil {
		t.Fatalf("write prometheus: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "global_queue_depth 42") {
		t.Fatalf("package gauge wrong:\n%s", out)
	}
	if !strings.Contains(out, "# TYPE global_op_seconds histogram") {
		t.Fatalf("package histogram missing:\n%s", out)
	}
}

func TestCustomMetricLabelDefaultsAndEscaping(t *testing.T) {
	reg := NewRegistry()
	reg.Counter("escaped_total", "Escaped labels.", "path", "status").Add(1, "line\n\"quoted\"\\slash")

	var buf bytes.Buffer
	if err := reg.WritePrometheus(&buf); err != nil {
		t.Fatalf("write prometheus: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `escaped_total{path="line\n\"quoted\"\\slash",status=""} 1`) {
		t.Fatalf("escaped/default labels missing:\n%s", out)
	}
	snapshot := reg.Snapshot().Customs["escaped_total"]
	if len(snapshot.Series) != 1 || snapshot.Series[0].Labels["status"] != "" {
		t.Fatalf("snapshot labels = %#v, want missing label defaulted to empty string", snapshot.Series)
	}
}

func TestCustomHistogramDefaultAndSortedBuckets(t *testing.T) {
	reg := NewRegistry()
	h := reg.Histogram("sorted_seconds", "Sorted buckets.", []float64{1, 0.1, 0.5})
	h.Observe(0.2)
	snapshot := reg.Snapshot().Customs["sorted_seconds"]
	if len(snapshot.Buckets) != 3 || snapshot.Buckets[0] != 0.1 || snapshot.Buckets[1] != 0.5 || snapshot.Buckets[2] != 1 {
		t.Fatalf("buckets = %#v, want sorted ascending", snapshot.Buckets)
	}
	if snapshot.Series[0].Labels != nil {
		t.Fatalf("labels = %#v, want nil for unlabeled series", snapshot.Series[0].Labels)
	}

	defaulted := reg.Histogram("default_seconds", "Default buckets.", nil)
	defaulted.Observe(0.001)
	defaultSnapshot := reg.Snapshot().Customs["default_seconds"]
	if len(defaultSnapshot.Buckets) != len(DefaultHistogramBuckets) {
		t.Fatalf("default buckets = %#v, want %d buckets", defaultSnapshot.Buckets, len(DefaultHistogramBuckets))
	}
}
