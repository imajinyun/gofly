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
	mu             sync.Mutex
	resolver       Resolver
	balancer       Balancer
	idleTimeout    time.Duration
	options        []ExperimentalMuxTransportOption
	adapters       map[string]*muxManagedAdapter
	closed         bool
	watchUpdates   int64
	removed        []string
	closedAdapters int64
	closeReasons   map[string]int64
	drainReasons   map[string]int64
	lastUpdated    time.Time
}

type muxManagedAdapter struct {
	endpoint string
	adapter  *ExperimentalMuxClientAdapter
	lastUsed time.Time
}

type ExperimentalMuxConnectionManagerOption func(*ExperimentalMuxConnectionManager)

type ExperimentalMuxConnectionManagerSnapshot struct {
	Closed         bool                              `json:"closed"`
	IdleTimeout    time.Duration                     `json:"idleTimeout,omitempty"`
	Endpoints      []ExperimentalMuxEndpointSnapshot `json:"endpoints,omitempty"`
	WatchUpdates   int64                             `json:"watchUpdates,omitempty"`
	Removed        []string                          `json:"removed,omitempty"`
	ClosedAdapters int64                             `json:"closedAdapters,omitempty"`
	CloseReasons   map[string]int64                  `json:"closeReasons,omitempty"`
	DrainReasons   map[string]int64                  `json:"drainReasons,omitempty"`
	LastUpdated    time.Time                         `json:"lastUpdated,omitempty"`
}

type ExperimentalMuxEndpointSnapshot struct {
	Endpoint string                         `json:"endpoint"`
	LastUsed time.Time                      `json:"lastUsed,omitempty"`
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
		adapters:     make(map[string]*muxManagedAdapter),
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
	for endpoint, adapter := range m.adapters {
		if adapter == nil || now.Sub(adapter.lastUsed) < m.idleTimeout {
			continue
		}
		delete(m.adapters, endpoint)
		idle = append(idle, adapter)
	}
	m.recordClosedAdaptersLocked(idle, "idle")
	m.mu.Unlock()
	var err error
	for _, adapter := range idle {
		err = errors.Join(err, m.drainManagedAdapter(ctx, adapter, "idle"))
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
	adapters := make([]*muxManagedAdapter, 0, len(m.adapters))
	for _, adapter := range m.adapters {
		adapters = append(adapters, adapter)
	}
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
	m.adapters = nil
	closed := make([]*muxManagedAdapter, 0, len(adapters))
	for _, adapter := range adapters {
		closed = append(closed, adapter)
	}
	m.recordClosedAdaptersLocked(closed, "manager_closed")
	m.mu.Unlock()
	var err error
	for _, adapter := range adapters {
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
	watchUpdates := m.watchUpdates
	removed := append([]string(nil), m.removed...)
	closedAdapters := m.closedAdapters
	closeReasons := cloneStringInt64Map(m.closeReasons)
	drainReasons := cloneStringInt64Map(m.drainReasons)
	lastUpdated := m.lastUpdated
	endpoints := make([]ExperimentalMuxEndpointSnapshot, 0, len(m.adapters))
	for _, adapter := range m.adapters {
		if adapter == nil || adapter.adapter == nil {
			continue
		}
		endpoints = append(endpoints, ExperimentalMuxEndpointSnapshot{
			Endpoint: adapter.endpoint,
			LastUsed: adapter.lastUsed,
			Adapter:  adapter.adapter.Snapshot(),
		})
	}
	m.mu.Unlock()
	sort.Slice(endpoints, func(i, j int) bool { return endpoints[i].Endpoint < endpoints[j].Endpoint })
	return ExperimentalMuxConnectionManagerSnapshot{
		Closed:         closed,
		IdleTimeout:    idleTimeout,
		Endpoints:      endpoints,
		WatchUpdates:   watchUpdates,
		Removed:        removed,
		ClosedAdapters: closedAdapters,
		CloseReasons:   closeReasons,
		DrainReasons:   drainReasons,
		LastUpdated:    lastUpdated,
	}
}

func (m *ExperimentalMuxConnectionManager) DiagnosisSnapshot() RPCMuxConnectionManagerDiagnosis {
	snapshot := m.Snapshot()
	return RPCMuxConnectionManagerDiagnosis{
		Enabled:        m != nil && !snapshot.Closed,
		Mode:           "experimental_mux_manager",
		IdleTimeout:    snapshot.IdleTimeout,
		Endpoints:      snapshot.Endpoints,
		WatchUpdates:   snapshot.WatchUpdates,
		Removed:        snapshot.Removed,
		ClosedAdapters: snapshot.ClosedAdapters,
		CloseReasons:   snapshot.CloseReasons,
		DrainReasons:   snapshot.DrainReasons,
		LastUpdated:    snapshot.LastUpdated,
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
	m.mu.Lock()
	for endpoint, adapter := range m.adapters {
		if _, ok := live[endpoint]; ok {
			continue
		}
		delete(m.adapters, endpoint)
		removed = append(removed, adapter)
	}
	m.recordClosedAdaptersLocked(removed, "resolver_update")
	m.mu.Unlock()
	var err error
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
	if existing := m.adapters[endpoint]; existing != nil && existing.adapter != nil {
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
	if existing := m.adapters[endpoint]; existing != nil && existing.adapter != nil {
		existing.lastUsed = time.Now()
		existingAdapter := existing.adapter
		m.mu.Unlock()
		_ = adapter.Close()
		return existingAdapter, nil
	}
	m.adapters[endpoint] = &muxManagedAdapter{endpoint: endpoint, adapter: adapter, lastUsed: time.Now()}
	m.mu.Unlock()
	return adapter, nil
}

func (m *ExperimentalMuxConnectionManager) touch(endpoint string) {
	m.mu.Lock()
	if adapter := m.adapters[endpoint]; adapter != nil {
		adapter.lastUsed = time.Now()
	}
	m.mu.Unlock()
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
