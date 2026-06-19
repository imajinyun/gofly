// Package metrics provides a Prometheus-compatible metrics registry with
// request counters, error counters, latency histograms, and custom metrics.
package metrics

import (
	"fmt"
	"io"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var Default = NewRegistry()

var DefaultDurationBuckets = []time.Duration{
	5 * time.Millisecond,
	10 * time.Millisecond,
	25 * time.Millisecond,
	50 * time.Millisecond,
	100 * time.Millisecond,
	250 * time.Millisecond,
	500 * time.Millisecond,
	time.Second,
	2 * time.Second,
	5 * time.Second,
}

// Registry collects request metrics with latency histograms.
type Registry struct {
	mu       sync.RWMutex
	started  time.Time
	buckets  []time.Duration
	requests int64
	errors   int64
	inFlight int64
	statuses map[int]int64
	routes   map[string]*routeStats
	customs  map[string]*customMetric
}

type routeStats struct {
	Requests      int64         `json:"requests"`
	Errors        int64         `json:"errors"`
	TotalDuration time.Duration `json:"totalDuration"`
	MaxDuration   time.Duration `json:"maxDuration"`
	Buckets       []int64       `json:"buckets"`
}

// Snapshot is a point-in-time view of registry metrics.
type Snapshot struct {
	StartedAt time.Time                       `json:"startedAt"`
	Uptime    time.Duration                   `json:"uptime"`
	Requests  int64                           `json:"requests"`
	Errors    int64                           `json:"errors"`
	InFlight  int64                           `json:"inFlight"`
	Statuses  map[int]int64                   `json:"statuses"`
	Routes    map[string]RouteSnapshot        `json:"routes"`
	Customs   map[string]CustomMetricSnapshot `json:"customs,omitempty"`
	Runtime   RuntimeSnapshot                 `json:"runtime"`
}

// RouteSnapshot holds request metrics for a single route.
type RouteSnapshot struct {
	Requests      int64            `json:"requests"`
	Errors        int64            `json:"errors"`
	TotalDuration time.Duration    `json:"totalDuration"`
	MaxDuration   time.Duration    `json:"maxDuration"`
	AvgDuration   time.Duration    `json:"avgDuration"`
	Buckets       map[string]int64 `json:"buckets"`
}

// RuntimeSnapshot captures Go runtime statistics.
type RuntimeSnapshot struct {
	Goroutines  int    `json:"goroutines"`
	HeapAlloc   uint64 `json:"heapAlloc"`
	HeapSys     uint64 `json:"heapSys"`
	NumGC       uint32 `json:"numGC"`
	LastPauseNS uint64 `json:"lastPauseNs"`
}

// NewRegistry creates a new metrics Registry with default duration buckets.
func NewRegistry() *Registry {
	return &Registry{
		started:  time.Now(),
		buckets:  append([]time.Duration(nil), DefaultDurationBuckets...),
		statuses: make(map[int]int64),
		routes:   make(map[string]*routeStats),
		customs:  make(map[string]*customMetric),
	}
}

// IncInFlight increments the in-flight request counter.
func (r *Registry) IncInFlight() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inFlight++
}

// DecInFlight decrements the in-flight request counter.
func (r *Registry) DecInFlight() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inFlight > 0 {
		r.inFlight--
	}
}

// Observe records a request outcome for the given route.
func (r *Registry) Observe(route string, status int, duration time.Duration) {
	if route == "" {
		route = "unknown"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests++
	r.statuses[status]++
	if status >= 500 {
		r.errors++
	}
	stats := r.routes[route]
	if stats == nil {
		stats = &routeStats{Buckets: make([]int64, len(r.buckets))}
		r.routes[route] = stats
	}
	stats.Requests++
	if status >= 500 {
		stats.Errors++
	}
	stats.TotalDuration += duration
	if duration > stats.MaxDuration {
		stats.MaxDuration = duration
	}
	for i, bucket := range r.buckets {
		if duration <= bucket {
			stats.Buckets[i]++
		}
	}
}

// Snapshot returns a point-in-time copy of all registry metrics.
func (r *Registry) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	statuses := make(map[int]int64, len(r.statuses))
	for k, v := range r.statuses {
		statuses[k] = v
	}
	routes := make(map[string]RouteSnapshot, len(r.routes))
	keys := make([]string, 0, len(r.routes))
	for k := range r.routes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := r.routes[k]
		var avg time.Duration
		if v.Requests > 0 {
			avg = v.TotalDuration / time.Duration(v.Requests)
		}
		buckets := make(map[string]int64, len(r.buckets))
		for i, bucket := range r.buckets {
			if i < len(v.Buckets) {
				buckets[bucket.String()] = v.Buckets[i]
			}
		}
		routes[k] = RouteSnapshot{
			Requests:      v.Requests,
			Errors:        v.Errors,
			TotalDuration: v.TotalDuration,
			MaxDuration:   v.MaxDuration,
			AvgDuration:   avg,
			Buckets:       buckets,
		}
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	return Snapshot{
		StartedAt: r.started,
		Uptime:    time.Since(r.started),
		Requests:  r.requests,
		Errors:    r.errors,
		InFlight:  r.inFlight,
		Statuses:  statuses,
		Routes:    routes,
		Customs:   r.customSnapshotsLocked(),
		Runtime: RuntimeSnapshot{
			Goroutines:  runtime.NumGoroutine(),
			HeapAlloc:   mem.HeapAlloc,
			HeapSys:     mem.HeapSys,
			NumGC:       mem.NumGC,
			LastPauseNS: mem.PauseNs[(mem.NumGC+255)%256],
		},
	}
}

func (r *Registry) WritePrometheus(w io.Writer) error {
	snapshot := r.Snapshot()
	if _, err := fmt.Fprintln(w, "# HELP gofly_requests_total Total number of handled requests."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE gofly_requests_total counter"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "gofly_requests_total %d\n", snapshot.Requests); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP gofly_errors_total Total number of failed requests."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE gofly_errors_total counter"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "gofly_errors_total %d\n", snapshot.Errors); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP gofly_inflight_requests Current number of in-flight requests."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE gofly_inflight_requests gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "gofly_inflight_requests %d\n", snapshot.InFlight); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP gofly_runtime_goroutines Current number of goroutines."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE gofly_runtime_goroutines gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "gofly_runtime_goroutines %d\n", snapshot.Runtime.Goroutines); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP gofly_runtime_heap_alloc_bytes Current heap allocation in bytes."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE gofly_runtime_heap_alloc_bytes gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "gofly_runtime_heap_alloc_bytes %d\n", snapshot.Runtime.HeapAlloc); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP gofly_request_status_total Total number of requests by status code."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE gofly_request_status_total counter"); err != nil {
		return err
	}
	statuses := make([]int, 0, len(snapshot.Statuses))
	for status := range snapshot.Statuses {
		statuses = append(statuses, status)
	}
	sort.Ints(statuses)
	for _, status := range statuses {
		if _, err := fmt.Fprintf(w, "gofly_request_status_total{status=%q} %d\n", strconv.Itoa(status), snapshot.Statuses[status]); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w, "# HELP gofly_route_requests_total Total number of requests by route."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE gofly_route_requests_total counter"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP gofly_route_errors_total Total number of failed requests by route."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE gofly_route_errors_total counter"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP gofly_route_duration_seconds_sum Total request duration by route."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE gofly_route_duration_seconds_sum counter"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP gofly_route_duration_seconds Request duration histogram by route."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE gofly_route_duration_seconds histogram"); err != nil {
		return err
	}
	routes := make([]string, 0, len(snapshot.Routes))
	for route := range snapshot.Routes {
		routes = append(routes, route)
	}
	sort.Strings(routes)
	for _, route := range routes {
		stats := snapshot.Routes[route]
		label := prometheusLabel(route)
		if _, err := fmt.Fprintf(w, "gofly_route_requests_total{route=\"%s\"} %d\n", label, stats.Requests); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "gofly_route_errors_total{route=\"%s\"} %d\n", label, stats.Errors); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "gofly_route_duration_seconds_sum{route=\"%s\"} %.9f\n", label, stats.TotalDuration.Seconds()); err != nil {
			return err
		}
		bucketKeys := make([]string, 0, len(stats.Buckets))
		for bucket := range stats.Buckets {
			bucketKeys = append(bucketKeys, bucket)
		}
		sort.Slice(bucketKeys, func(i, j int) bool {
			return bucketSeconds(bucketKeys[i]) < bucketSeconds(bucketKeys[j])
		})
		for _, bucket := range bucketKeys {
			if _, err := fmt.Fprintf(w, "gofly_route_duration_seconds_bucket{route=\"%s\",le=\"%.9f\"} %d\n", label, bucketSeconds(bucket), stats.Buckets[bucket]); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "gofly_route_duration_seconds_bucket{route=\"%s\",le=\"+Inf\"} %d\n", label, stats.Requests); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "gofly_route_duration_seconds_count{route=\"%s\"} %d\n", label, stats.Requests); err != nil {
			return err
		}
	}
	return r.writeCustom(w)
}

func bucketSeconds(bucket string) float64 {
	d, err := time.ParseDuration(bucket)
	if err != nil {
		return 0
	}
	return d.Seconds()
}

func prometheusLabel(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return strings.ReplaceAll(s, "\"", "\\\"")
}
