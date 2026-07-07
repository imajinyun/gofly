package rpc

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	core "github.com/imajinyun/gofly/core"
)

type ExperimentalMuxConnectionManager struct {
	mu                sync.Mutex
	resolver          Resolver
	balancer          Balancer
	idleTimeout       time.Duration
	maxStreamsPerConn int
	options           []ExperimentalMuxTransportOption
	adapters          map[string][]*muxManagedAdapter
	retired           map[string][]*muxManagedAdapter
	closed            bool
	watchUpdates      int64
	removed           []string
	closedAdapters    int64
	closeReasons      map[string]int64
	drainReasons      map[string]int64
	lastUpdated       time.Time
}

type muxManagedAdapter struct {
	endpoint string
	adapter  *ExperimentalMuxClientAdapter
	lastUsed time.Time
	retired  bool
}

type ExperimentalMuxConnectionManagerOption func(*ExperimentalMuxConnectionManager)

type ExperimentalMuxConnectionManagerSnapshot struct {
	Closed            bool                              `json:"closed"`
	IdleTimeout       time.Duration                     `json:"idleTimeout,omitempty"`
	MaxStreamsPerConn int                               `json:"maxStreamsPerConn,omitempty"`
	Endpoints         []ExperimentalMuxEndpointSnapshot `json:"endpoints,omitempty"`
	RetiredAdapters   int                               `json:"retiredAdapters,omitempty"`
	WatchUpdates      int64                             `json:"watchUpdates,omitempty"`
	Removed           []string                          `json:"removed,omitempty"`
	ClosedAdapters    int64                             `json:"closedAdapters,omitempty"`
	CloseReasons      map[string]int64                  `json:"closeReasons,omitempty"`
	DrainReasons      map[string]int64                  `json:"drainReasons,omitempty"`
	LastUpdated       time.Time                         `json:"lastUpdated,omitempty"`
}

type ExperimentalMuxEndpointSnapshot struct {
	Endpoint string                         `json:"endpoint"`
	LastUsed time.Time                      `json:"lastUsed,omitempty"`
	Retired  bool                           `json:"retired,omitempty"`
	Adapter  ExperimentalMuxAdapterSnapshot `json:"adapter"`
}

func NewExperimentalMuxConnectionManager(resolver Resolver, opts ...ExperimentalMuxConnectionManagerOption) (*ExperimentalMuxConnectionManager, error) {
	if resolver == nil {
		return nil, errors.New("mux connection manager resolver is required")
	}
	m := &ExperimentalMuxConnectionManager{
		resolver:     resolver,
		balancer:     &RoundRobinBalancer{},
		idleTimeout:  time.Minute,
		adapters:     make(map[string][]*muxManagedAdapter),
		retired:      make(map[string][]*muxManagedAdapter),
		closeReasons: make(map[string]int64),
		drainReasons: make(map[string]int64),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	if m.balancer == nil {
		m.balancer = &RoundRobinBalancer{}
	}
	if m.idleTimeout < 0 {
		m.idleTimeout = 0
	}
	return m, nil
}

func WithExperimentalMuxConnectionManagerBalancer(balancer Balancer) ExperimentalMuxConnectionManagerOption {
	return func(m *ExperimentalMuxConnectionManager) {
		if balancer != nil {
			m.balancer = balancer
		}
	}
}

func WithExperimentalMuxConnectionManagerIdleTimeout(timeout time.Duration) ExperimentalMuxConnectionManagerOption {
	return func(m *ExperimentalMuxConnectionManager) {
		m.idleTimeout = timeout
	}
}

func WithExperimentalMuxConnectionManagerMaxStreamsPerConn(max int) ExperimentalMuxConnectionManagerOption {
	return func(m *ExperimentalMuxConnectionManager) {
		if max > 0 {
			m.maxStreamsPerConn = max
		}
	}
}

func WithExperimentalMuxConnectionManagerTransportOptions(opts ...ExperimentalMuxTransportOption) ExperimentalMuxConnectionManagerOption {
	return func(m *ExperimentalMuxConnectionManager) {
		m.options = append([]ExperimentalMuxTransportOption(nil), opts...)
	}
}

func (m *ExperimentalMuxConnectionManager) OpenStream(ctx context.Context, method string) (*ExperimentalMuxStream, string, error) {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	endpoints, err := m.resolveAndSync(ctx)
	if err != nil {
		return nil, "", err
	}
	endpoint, err := m.pickEndpoint(ctx, endpoints)
	if err != nil {
		return nil, "", err
	}
	adapter, err := m.adapter(ctx, endpoint)
	if err != nil {
		return nil, "", err
	}
	stream, err := adapter.OpenStream(ctx, method)
	if err != nil {
		return nil, endpoint, err
	}
	m.touch(endpoint)
	return stream, endpoint, nil
}

func (m *ExperimentalMuxConnectionManager) SyncResolver(ctx context.Context) error {
	_, err := m.resolveAndSync(core.Context(ctx))
	return err
}

func (m *ExperimentalMuxConnectionManager) Watch(ctx context.Context) error {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	if m == nil {
		return ErrExperimentalMuxTransportClosed
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrExperimentalMuxTransportClosed
	}
	resolver := m.resolver
	m.mu.Unlock()
	watcher, ok := resolver.(WatchResolver)
	if !ok {
		return errors.New("mux connection manager resolver does not support watch")
	}
	updates, err := watcher.Watch(ctx)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case endpoints, ok := <-updates:
			if !ok {
				return nil
			}
			endpoints = normalizeEndpoints(endpoints)
			if len(endpoints) == 0 {
				continue
			}
			m.recordWatchUpdate()
			if err := m.removeMissingEndpoints(ctx, endpoints); err != nil {
				return err
			}
		}
	}
}

func (m *ExperimentalMuxConnectionManager) CloseIdle(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if err := core.Context(ctx).Err(); err != nil {
		return err
	}
	if m.idleTimeout <= 0 {
		return nil
	}
	now := time.Now()
	var idle []*muxManagedAdapter
	m.mu.Lock()
	for endpoint, adapters := range m.adapters {
		kept := adapters[:0]
		for _, adapter := range adapters {
			if adapter == nil {
				continue
			}
			if adapterActiveStreams(adapter) > 0 || now.Sub(adapter.lastUsed) < m.idleTimeout {
				kept = append(kept, adapter)
				continue
			}
			idle = append(idle, adapter)
		}
		if len(kept) == 0 {
			delete(m.adapters, endpoint)
		} else {
			m.adapters[endpoint] = kept
		}
	}
	for endpoint, adapters := range m.retired {
		kept := adapters[:0]
		for _, adapter := range adapters {
			if adapter == nil {
				continue
			}
			if adapterActiveStreams(adapter) > 0 {
				kept = append(kept, adapter)
				continue
			}
			idle = append(idle, adapter)
		}
		if len(kept) == 0 {
			delete(m.retired, endpoint)
		} else {
			m.retired[endpoint] = kept
		}
	}
	m.recordClosedAdaptersLocked(idle, "idle")
	m.mu.Unlock()
	var err error
	for _, adapter := range idle {
		if adapter == nil || adapter.adapter == nil {
			continue
		}
		if !adapter.retired {
			err = errors.Join(err, m.drainManagedAdapter(ctx, adapter, "idle"))
		}
		err = errors.Join(err, adapter.adapter.Close())
	}
	return err
}

func (m *ExperimentalMuxConnectionManager) Drain(ctx context.Context, reason string) error {
	if m == nil {
		return nil
	}
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	adapters := m.snapshotAdaptersLocked()
	m.mu.Unlock()
	var err error
	for _, adapter := range adapters {
		if adapter != nil && adapter.adapter != nil && adapter.adapter.transport != nil {
			err = errors.Join(err, adapter.adapter.transport.Drain(ctx, reason))
		}
	}
	return err
}

func (m *ExperimentalMuxConnectionManager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	adapters := m.adapters
	retired := m.retired
	m.adapters = nil
	m.retired = nil
	closed := flattenManagedAdapters(adapters, retired)
	m.recordClosedAdaptersLocked(closed, "manager_closed")
	m.mu.Unlock()
	var err error
	for _, adapter := range closed {
		if adapter != nil && adapter.adapter != nil {
			err = errors.Join(err, adapter.adapter.Close())
		}
	}
	return err
}

func (m *ExperimentalMuxConnectionManager) Snapshot() ExperimentalMuxConnectionManagerSnapshot {
	if m == nil {
		return ExperimentalMuxConnectionManagerSnapshot{}
	}
	m.mu.Lock()
	closed := m.closed
	idleTimeout := m.idleTimeout
	maxStreamsPerConn := m.maxStreamsPerConn
	watchUpdates := m.watchUpdates
	removed := append([]string(nil), m.removed...)
	closedAdapters := m.closedAdapters
	retiredAdapters := countManagedAdapters(m.retired)
	closeReasons := cloneStringInt64Map(m.closeReasons)
	drainReasons := cloneStringInt64Map(m.drainReasons)
	lastUpdated := m.lastUpdated
	endpoints := m.snapshotEndpointSnapshotsLocked()
	m.mu.Unlock()
	sort.Slice(endpoints, func(i, j int) bool {
		if endpoints[i].Endpoint != endpoints[j].Endpoint {
			return endpoints[i].Endpoint < endpoints[j].Endpoint
		}
		return endpoints[i].Adapter.Transport.LastStreamID < endpoints[j].Adapter.Transport.LastStreamID
	})
	return ExperimentalMuxConnectionManagerSnapshot{
		Closed:            closed,
		IdleTimeout:       idleTimeout,
		MaxStreamsPerConn: maxStreamsPerConn,
		Endpoints:         endpoints,
		RetiredAdapters:   retiredAdapters,
		WatchUpdates:      watchUpdates,
		Removed:           removed,
		ClosedAdapters:    closedAdapters,
		CloseReasons:      closeReasons,
		DrainReasons:      drainReasons,
		LastUpdated:       lastUpdated,
	}
}

func (m *ExperimentalMuxConnectionManager) DiagnosisSnapshot() RPCMuxConnectionManagerDiagnosis {
	snapshot := m.Snapshot()
	return RPCMuxConnectionManagerDiagnosis{
		Enabled:           m != nil && !snapshot.Closed,
		Mode:              "experimental_mux_manager",
		IdleTimeout:       snapshot.IdleTimeout,
		MaxStreamsPerConn: snapshot.MaxStreamsPerConn,
		Endpoints:         snapshot.Endpoints,
		RetiredAdapters:   snapshot.RetiredAdapters,
		WatchUpdates:      snapshot.WatchUpdates,
		Removed:           snapshot.Removed,
		ClosedAdapters:    snapshot.ClosedAdapters,
		CloseReasons:      snapshot.CloseReasons,
		DrainReasons:      snapshot.DrainReasons,
		LastUpdated:       snapshot.LastUpdated,
	}
}

func (m *ExperimentalMuxConnectionManager) resolveAndSync(ctx context.Context) ([]string, error) {
	if m == nil {
		return nil, ErrExperimentalMuxTransportClosed
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrExperimentalMuxTransportClosed
	}
	resolver := m.resolver
	m.mu.Unlock()
	endpoints, err := resolver.Resolve(ctx)
	if err != nil {
		return nil, err
	}
	endpoints = normalizeEndpoints(endpoints)
	if len(endpoints) == 0 {
		return nil, errors.New("no endpoint to pick")
	}
	if err := m.removeMissingEndpoints(ctx, endpoints); err != nil {
		return nil, err
	}
	return endpoints, nil
}

func (m *ExperimentalMuxConnectionManager) pickEndpoint(ctx context.Context, endpoints []string) (string, error) {
	if m == nil {
		return "", ErrExperimentalMuxTransportClosed
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return "", ErrExperimentalMuxTransportClosed
	}
	balancer := m.balancer
	m.mu.Unlock()
	endpoint, err := balancer.Pick(ctx, endpoints)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(strings.TrimSpace(endpoint), "/"), nil
}

func (m *ExperimentalMuxConnectionManager) removeMissingEndpoints(ctx context.Context, endpoints []string) error {
	live := make(map[string]struct{}, len(endpoints))
	for _, endpoint := range endpoints {
		live[strings.TrimRight(strings.TrimSpace(endpoint), "/")] = struct{}{}
	}
	var removed []*muxManagedAdapter
	var retired []*muxManagedAdapter
	m.mu.Lock()
	for endpoint, adapters := range m.adapters {
		if _, ok := live[endpoint]; ok {
			continue
		}
		delete(m.adapters, endpoint)
		for _, adapter := range adapters {
			if adapter == nil {
				continue
			}
			if adapterActiveStreams(adapter) > 0 {
				adapter.retired = true
				retired = append(retired, adapter)
				m.retired[endpoint] = append(m.retired[endpoint], adapter)
				continue
			}
			removed = append(removed, adapter)
		}
	}
	m.recordClosedAdaptersLocked(removed, "resolver_update")
	if len(retired) > 0 {
		m.lastUpdated = time.Now()
	}
	m.mu.Unlock()
	var err error
	for _, adapter := range retired {
		err = errors.Join(err, m.drainManagedAdapter(ctx, adapter, "resolver_update"))
	}
	for _, adapter := range removed {
		if adapter != nil && adapter.adapter != nil {
			err = errors.Join(err, m.drainManagedAdapter(ctx, adapter, "resolver_update"))
			err = errors.Join(err, adapter.adapter.Close())
		}
	}
	return err
}

func (m *ExperimentalMuxConnectionManager) recordWatchUpdate() {
	m.mu.Lock()
	m.watchUpdates++
	m.lastUpdated = time.Now()
	m.mu.Unlock()
}

func (m *ExperimentalMuxConnectionManager) drainManagedAdapter(ctx context.Context, adapter *muxManagedAdapter, reason string) error {
	if adapter == nil || adapter.adapter == nil || adapter.adapter.transport == nil {
		return nil
	}
	if reason == "" {
		reason = "drain"
	}
	if err := adapter.adapter.transport.Drain(ctx, reason); err != nil {
		return err
	}
	m.mu.Lock()
	if m.drainReasons == nil {
		m.drainReasons = make(map[string]int64)
	}
	m.drainReasons[reason]++
	m.lastUpdated = time.Now()
	m.mu.Unlock()
	return nil
}

func (m *ExperimentalMuxConnectionManager) recordClosedAdaptersLocked(adapters []*muxManagedAdapter, reason string) {
	if len(adapters) == 0 {
		return
	}
	if reason == "" {
		reason = "closed"
	}
	if m.closeReasons == nil {
		m.closeReasons = make(map[string]int64)
	}
	if m.removed == nil {
		m.removed = make([]string, 0, len(adapters))
	}
	for _, adapter := range adapters {
		if adapter == nil {
			continue
		}
		m.closedAdapters++
		m.closeReasons[reason]++
		m.removed = append(m.removed, adapter.endpoint)
	}
	m.lastUpdated = time.Now()
}

func (m *ExperimentalMuxConnectionManager) adapter(ctx context.Context, endpoint string) (*ExperimentalMuxClientAdapter, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrExperimentalMuxTransportClosed
	}
	if existing := m.pickReusableAdapterLocked(endpoint); existing != nil {
		existing.lastUsed = time.Now()
		adapter := existing.adapter
		m.mu.Unlock()
		return adapter, nil
	}
	opts := append([]ExperimentalMuxTransportOption(nil), m.options...)
	m.mu.Unlock()
	adapter, err := DialExperimentalMuxClientAdapter(ctx, "tcp", muxEndpointAddress(endpoint), opts...)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		_ = adapter.Close()
		return nil, ErrExperimentalMuxTransportClosed
	}
	if existing := m.pickReusableAdapterLocked(endpoint); existing != nil {
		existing.lastUsed = time.Now()
		existingAdapter := existing.adapter
		m.mu.Unlock()
		_ = adapter.Close()
		return existingAdapter, nil
	}
	managed := &muxManagedAdapter{endpoint: endpoint, adapter: adapter, lastUsed: time.Now()}
	m.adapters[endpoint] = append(m.adapters[endpoint], managed)
	m.lastUpdated = managed.lastUsed
	m.mu.Unlock()
	return adapter, nil
}

func (m *ExperimentalMuxConnectionManager) touch(endpoint string) {
	m.mu.Lock()
	if adapter := m.pickReusableAdapterLocked(endpoint); adapter != nil {
		adapter.lastUsed = time.Now()
	}
	m.mu.Unlock()
}

func (m *ExperimentalMuxConnectionManager) pickReusableAdapterLocked(endpoint string) *muxManagedAdapter {
	for _, adapter := range m.adapters[endpoint] {
		if adapter == nil || adapter.adapter == nil {
			continue
		}
		if m.maxStreamsPerConn <= 0 || adapterActiveStreams(adapter) < m.maxStreamsPerConn {
			return adapter
		}
	}
	return nil
}

func (m *ExperimentalMuxConnectionManager) snapshotAdaptersLocked() []*muxManagedAdapter {
	return flattenManagedAdapters(m.adapters, m.retired)
}

func (m *ExperimentalMuxConnectionManager) snapshotEndpointSnapshotsLocked() []ExperimentalMuxEndpointSnapshot {
	count := countManagedAdapters(m.adapters) + countManagedAdapters(m.retired)
	endpoints := make([]ExperimentalMuxEndpointSnapshot, 0, count)
	endpoints = appendEndpointSnapshots(endpoints, m.adapters)
	endpoints = appendEndpointSnapshots(endpoints, m.retired)
	return endpoints
}

func appendEndpointSnapshots(endpoints []ExperimentalMuxEndpointSnapshot, adapters map[string][]*muxManagedAdapter) []ExperimentalMuxEndpointSnapshot {
	for _, adapters := range adapters {
		for _, adapter := range adapters {
			if adapter == nil || adapter.adapter == nil {
				continue
			}
			endpoints = append(endpoints, ExperimentalMuxEndpointSnapshot{
				Endpoint: adapter.endpoint,
				LastUsed: adapter.lastUsed,
				Retired:  adapter.retired,
				Adapter:  adapter.adapter.Snapshot(),
			})
		}
	}
	return endpoints
}

func flattenManagedAdapters(adapterMaps ...map[string][]*muxManagedAdapter) []*muxManagedAdapter {
	count := 0
	for _, adapters := range adapterMaps {
		count += countManagedAdapters(adapters)
	}
	flat := make([]*muxManagedAdapter, 0, count)
	for _, adapters := range adapterMaps {
		for _, endpointAdapters := range adapters {
			flat = append(flat, endpointAdapters...)
		}
	}
	return flat
}

func countManagedAdapters(adapters map[string][]*muxManagedAdapter) int {
	count := 0
	for _, endpointAdapters := range adapters {
		count += len(endpointAdapters)
	}
	return count
}

func adapterActiveStreams(adapter *muxManagedAdapter) int {
	if adapter == nil || adapter.adapter == nil {
		return 0
	}
	return adapter.adapter.Snapshot().Transport.ActiveStreams
}

func muxEndpointAddress(endpoint string) string {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return ""
	}
	parsed, err := url.Parse(endpoint)
	if err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return endpoint
}
