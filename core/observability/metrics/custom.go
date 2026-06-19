// Package metrics provides a Prometheus-compatible metrics registry with
// counters, gauges, histograms and runtime snapshots for gofly services.
package metrics

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

// MetricType enumerates the supported custom business metric kinds.
type MetricType string

const (
	// MetricCounter is a monotonically increasing metric.
	MetricCounter MetricType = "counter"
	// MetricGauge is a metric that can increase and decrease.
	MetricGauge MetricType = "gauge"
	// MetricHistogram samples observations into buckets.
	MetricHistogram MetricType = "histogram"
)

// DefaultHistogramBuckets are the upper bounds used when a histogram is created
// without explicit buckets.
var DefaultHistogramBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// Counter is a monotonically increasing custom metric partitioned by labels.
type Counter struct{ metric *customMetric }

// Inc increments the counter for the given label values by one.
func (c *Counter) Inc(labelValues ...string) { c.Add(1, labelValues...) }

// Add increases the counter for the given label values by delta. Negative
// deltas are ignored to preserve monotonicity.
func (c *Counter) Add(delta float64, labelValues ...string) {
	if delta < 0 {
		return
	}
	c.metric.addCounter(delta, labelValues)
}

// Gauge is a custom metric that can move up and down, partitioned by labels.
type Gauge struct{ metric *customMetric }

// Set replaces the gauge value for the given label values.
func (g *Gauge) Set(value float64, labelValues ...string) { g.metric.setGauge(value, labelValues) }

// Add adjusts the gauge value for the given label values by delta.
func (g *Gauge) Add(delta float64, labelValues ...string) { g.metric.addGauge(delta, labelValues) }

// Inc increments the gauge by one.
func (g *Gauge) Inc(labelValues ...string) { g.Add(1, labelValues...) }

// Dec decrements the gauge by one.
func (g *Gauge) Dec(labelValues ...string) { g.Add(-1, labelValues...) }

// Histogram records distributions of observed values, partitioned by labels.
type Histogram struct{ metric *customMetric }

// Observe records a single value into the histogram for the given label values.
func (h *Histogram) Observe(value float64, labelValues ...string) {
	h.metric.observe(value, labelValues)
}

type customMetric struct {
	name    string
	help    string
	typ     MetricType
	labels  []string
	buckets []float64

	mu     sync.Mutex
	series map[string]*customSeries
}

type customSeries struct {
	labelValues []string
	value       float64
	count       uint64
	sum         float64
	counts      []uint64
}

// CustomMetricSnapshot is the JSON-safe representation of a custom metric.
type CustomMetricSnapshot struct {
	Name    string                 `json:"name"`
	Help    string                 `json:"help,omitempty"`
	Type    MetricType             `json:"type"`
	Labels  []string               `json:"labels,omitempty"`
	Buckets []float64              `json:"buckets,omitempty"`
	Series  []CustomSeriesSnapshot `json:"series,omitempty"`
}

// CustomSeriesSnapshot captures one labeled time series for a custom metric.
type CustomSeriesSnapshot struct {
	Labels map[string]string `json:"labels,omitempty"`
	Value  float64           `json:"value,omitempty"`
	Count  uint64            `json:"count,omitempty"`
	Sum    float64           `json:"sum,omitempty"`
	Counts map[string]uint64 `json:"counts,omitempty"`
}

func seriesKey(values []string) string { return strings.Join(values, "\x1f") }

func (m *customMetric) series_(labelValues []string) *customSeries {
	key := seriesKey(labelValues)
	s := m.series[key]
	if s == nil {
		s = &customSeries{labelValues: append([]string(nil), labelValues...)}
		if m.typ == MetricHistogram {
			s.counts = make([]uint64, len(m.buckets))
		}
		m.series[key] = s
	}
	return s
}

func (m *customMetric) addCounter(delta float64, labelValues []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.series_(labelValues).value += delta
}

func (m *customMetric) setGauge(value float64, labelValues []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.series_(labelValues).value = value
}

func (m *customMetric) addGauge(delta float64, labelValues []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.series_(labelValues).value += delta
}

func (m *customMetric) observe(value float64, labelValues []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.series_(labelValues)
	s.count++
	s.sum += value
	for i, bound := range m.buckets {
		if value <= bound {
			s.counts[i]++
		}
	}
}

func (m *customMetric) write(w io.Writer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.help != "" {
		if _, err := fmt.Fprintf(w, "# HELP %s %s\n", m.name, m.help); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "# TYPE %s %s\n", m.name, m.typ); err != nil {
		return err
	}
	keys := make([]string, 0, len(m.series))
	for k := range m.series {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s := m.series[k]
		switch m.typ {
		case MetricHistogram:
			if err := m.writeHistogram(w, s); err != nil {
				return err
			}
		default:
			if _, err := fmt.Fprintf(w, "%s%s %g\n", m.name, m.labelString(s.labelValues, ""), s.value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *customMetric) writeHistogram(w io.Writer, s *customSeries) error {
	for i, bound := range m.buckets {
		le := fmt.Sprintf("%g", bound)
		if _, err := fmt.Fprintf(w, "%s_bucket%s %d\n", m.name, m.labelString(s.labelValues, le), s.counts[i]); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "%s_bucket%s %d\n", m.name, m.labelString(s.labelValues, "+Inf"), s.count); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "%s_sum%s %g\n", m.name, m.labelString(s.labelValues, ""), s.sum); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "%s_count%s %d\n", m.name, m.labelString(s.labelValues, ""), s.count); err != nil {
		return err
	}
	return nil
}

func (m *customMetric) labelString(values []string, le string) string {
	pairs := make([]string, 0, len(m.labels)+1)
	for i, name := range m.labels {
		value := ""
		if i < len(values) {
			value = values[i]
		}
		pairs = append(pairs, fmt.Sprintf("%s=\"%s\"", name, prometheusLabel(value)))
	}
	if le != "" {
		pairs = append(pairs, fmt.Sprintf("le=\"%s\"", le))
	}
	if len(pairs) == 0 {
		return ""
	}
	return "{" + strings.Join(pairs, ",") + "}"
}

func (m *customMetric) snapshot() CustomMetricSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := make([]string, 0, len(m.series))
	for key := range m.series {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	snapshot := CustomMetricSnapshot{
		Name:    m.name,
		Help:    m.help,
		Type:    m.typ,
		Labels:  append([]string(nil), m.labels...),
		Buckets: append([]float64(nil), m.buckets...),
		Series:  make([]CustomSeriesSnapshot, 0, len(keys)),
	}
	for _, key := range keys {
		series := m.series[key]
		snapshot.Series = append(snapshot.Series, m.seriesSnapshot(series))
	}
	return snapshot
}

func (m *customMetric) seriesSnapshot(series *customSeries) CustomSeriesSnapshot {
	labels := make(map[string]string, len(m.labels))
	for i, name := range m.labels {
		if i < len(series.labelValues) {
			labels[name] = series.labelValues[i]
		} else {
			labels[name] = ""
		}
	}
	snapshot := CustomSeriesSnapshot{Labels: labels}
	if len(snapshot.Labels) == 0 {
		snapshot.Labels = nil
	}
	if m.typ == MetricHistogram {
		snapshot.Count = series.count
		snapshot.Sum = series.sum
		snapshot.Counts = make(map[string]uint64, len(m.buckets)+1)
		for i, bucket := range m.buckets {
			if i < len(series.counts) {
				snapshot.Counts[fmt.Sprintf("%g", bucket)] = series.counts[i]
			}
		}
		snapshot.Counts["+Inf"] = series.count
		return snapshot
	}
	snapshot.Value = series.value
	return snapshot
}

// Counter registers (or returns an existing) custom counter. Calling it again
// with the same name returns the previously registered metric, so it is safe to
// call from package initializers.
func (r *Registry) Counter(name, help string, labels ...string) *Counter {
	return &Counter{metric: r.custom(name, help, MetricCounter, labels, nil)}
}

// Gauge registers (or returns an existing) custom gauge.
func (r *Registry) Gauge(name, help string, labels ...string) *Gauge {
	return &Gauge{metric: r.custom(name, help, MetricGauge, labels, nil)}
}

// Histogram registers (or returns an existing) custom histogram. When buckets
// is nil DefaultHistogramBuckets is used. Buckets are sorted ascending.
func (r *Registry) Histogram(name, help string, buckets []float64, labels ...string) *Histogram {
	if len(buckets) == 0 {
		buckets = DefaultHistogramBuckets
	}
	sorted := append([]float64(nil), buckets...)
	sort.Float64s(sorted)
	return &Histogram{metric: r.custom(name, help, MetricHistogram, labels, sorted)}
}

func (r *Registry) custom(name, help string, typ MetricType, labels []string, buckets []float64) *customMetric {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.customs == nil {
		r.customs = make(map[string]*customMetric)
	}
	if existing, ok := r.customs[name]; ok {
		return existing
	}
	m := &customMetric{
		name:    name,
		help:    help,
		typ:     typ,
		labels:  append([]string(nil), labels...),
		buckets: buckets,
		series:  make(map[string]*customSeries),
	}
	r.customs[name] = m
	return m
}

// Counter, Gauge and Histogram on the package-level Default registry.
func RegisterCounter(name, help string, labels ...string) *Counter {
	return Default.Counter(name, help, labels...)
}

func RegisterGauge(name, help string, labels ...string) *Gauge {
	return Default.Gauge(name, help, labels...)
}

func RegisterHistogram(name, help string, buckets []float64, labels ...string) *Histogram {
	return Default.Histogram(name, help, buckets, labels...)
}

func (r *Registry) writeCustom(w io.Writer) error {
	r.mu.RLock()
	metrics := make([]*customMetric, 0, len(r.customs))
	for _, m := range r.customs {
		metrics = append(metrics, m)
	}
	r.mu.RUnlock()
	sort.Slice(metrics, func(i, j int) bool { return metrics[i].name < metrics[j].name })
	for _, m := range metrics {
		if err := m.write(w); err != nil {
			return err
		}
	}
	return nil
}

func (r *Registry) customSnapshotsLocked() map[string]CustomMetricSnapshot {
	if len(r.customs) == 0 {
		return nil
	}
	out := make(map[string]CustomMetricSnapshot, len(r.customs))
	for name, metric := range r.customs {
		out[name] = metric.snapshot()
	}
	return out
}
