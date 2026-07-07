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
	mu                      sync.Mutex
	resolver                Resolver
	balancer                Balancer
	idleTimeout             time.Duration
	maxStreamsPerConn       int
	maxConnsPerEndpoint     int
	maxIdleConnsPerEndpoint int
	janitorInterval         time.Duration
	options                 []ExperimentalMuxTransportOption
	adapters                map[string][]*muxManagedAdapter
	retired                 map[string][]*muxManagedAdapter
	closed                  bool
	watchUpdates            int64
	removed                 []string
	closedAdapters          int64
	unhealthyAdapters       int64
	poolExhaustions         int64
	janitorRuns             int64
	closeReasons            map[string]int64
	drainReasons            map[string]int64
	lastUpdated             time.Time
	janitorCancel           context.CancelFunc
	janitorDone             chan struct{}
}

type muxManagedAdapter struct {
	endpoint string
	adapter  *ExperimentalMuxClientAdapter
	lastUsed time.Time
	retired  bool
}

type muxManagedClose struct {
	adapter *muxManagedAdapter
	reason  string
	drain   bool
}

type ExperimentalMuxConnectionManagerOption func(*ExperimentalMuxConnectionManager)

type ExperimentalMuxConnectionManagerSnapshot struct {
	Closed                  bool                              `json:"closed"`
	IdleTimeout             time.Duration                     `json:"idleTimeout,omitempty"`
	MaxStreamsPerConn       int                               `json:"maxStreamsPerConn,omitempty"`
	MaxConnsPerEndpoint     int                               `json:"maxConnsPerEndpoint,omitempty"`
	MaxIdleConnsPerEndpoint int                               `json:"maxIdleConnsPerEndpoint,omitempty"`
	JanitorInterval         time.Duration                     `json:"janitorInterval,omitempty"`
	Endpoints               []ExperimentalMuxEndpointSnapshot `json:"endpoints,omitempty"`
	RetiredAdapters         int                               `json:"retiredAdapters,omitempty"`
	WatchUpdates            int64                             `json:"watchUpdates,omitempty"`
	Removed                 []string                          `json:"removed,omitempty"`
	ClosedAdapters          int64                             `json:"closedAdapters,omitempty"`
	UnhealthyAdapters       int64                             `json:"unhealthyAdapters,omitempty"`
	PoolExhaustions         int64                             `json:"poolExhaustions,omitempty"`
	JanitorRuns             int64                             `json:"janitorRuns,omitempty"`
	CloseReasons            map[string]int64                  `json:"closeReasons,omitempty"`
	DrainReasons            map[string]int64                  `json:"drainReasons,omitempty"`
	LastUpdated             time.Time                         `json:"lastUpdated,omitempty"`
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
	if m.maxConnsPerEndpoint < 0 {
		m.maxConnsPerEndpoint = 0
	}
	if m.maxIdleConnsPerEndpoint < 0 {
		m.maxIdleConnsPerEndpoint = 0
	}
	if m.janitorInterval > 0 {
		m.startJanitor()
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

func WithExperimentalMuxConnectionManagerMaxConnsPerEndpoint(max int) ExperimentalMuxConnectionManagerOption {
	return func(m *ExperimentalMuxConnectionManager) {
		if max > 0 {
			m.maxConnsPerEndpoint = max
		}
	}
}

func WithExperimentalMuxConnectionManagerMaxIdleConnsPerEndpoint(max int) ExperimentalMuxConnectionManagerOption {
	return func(m *ExperimentalMuxConnectionManager) {
		if max > 0 {
			m.maxIdleConnsPerEndpoint = max
		}
	}
}

func WithExperimentalMuxConnectionManagerJanitorInterval(interval time.Duration) ExperimentalMuxConnectionManagerOption {
	return func(m *ExperimentalMuxConnectionManager) {
		if interval > 0 {
			m.janitorInterval = interval
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
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.closeIdle(ctx, time.Now(), false)
}

func (m *ExperimentalMuxConnectionManager) closeIdle(ctx context.Context, now time.Time, janitor bool) error {
	var closing []muxManagedClose
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrExperimentalMuxTransportClosed
	}
	if janitor {
		m.janitorRuns++
		m.lastUpdated = now
	}
	for endpoint, adapters := range m.adapters {
		kept := adapters[:0]
		idleIndexes := make([]int, 0, len(adapters))
		for _, adapter := range adapters {
			if adapter == nil {
				continue
			}
			if adapterUnhealthy(adapter) {
				m.unhealthyAdapters++
				closing = append(closing, muxManagedClose{adapter: adapter, reason: "unhealthy"})
				continue
			}
			if adapterActiveStreams(adapter) > 0 {
				kept = append(kept, adapter)
				continue
			}
			if m.idleTimeout > 0 && now.Sub(adapter.lastUsed) >= m.idleTimeout {
				closing = append(closing, muxManagedClose{adapter: adapter, reason: "idle", drain: true})
				continue
			}
			idleIndexes = append(idleIndexes, len(kept))
			kept = append(kept, adapter)
		}
		if m.maxIdleConnsPerEndpoint > 0 && len(idleIndexes) > m.maxIdleConnsPerEndpoint {
			excess := len(idleIndexes) - m.maxIdleConnsPerEndpoint
			for i := 0; i < excess; i++ {
				index := idleIndexes[i]
				if kept[index] != nil {
					closing = append(closing, muxManagedClose{adapter: kept[index], reason: "max_idle", drain: true})
					kept[index] = nil
				}
			}
			compacted := kept[:0]
			for _, adapter := range kept {
				if adapter != nil {
					compacted = append(compacted, adapter)
				}
			}
			kept = compacted
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
			if adapterUnhealthy(adapter) {
				m.unhealthyAdapters++
				closing = append(closing, muxManagedClose{adapter: adapter, reason: "unhealthy", drain: false})
				continue
			}
			if adapterActiveStreams(adapter) > 0 {
				kept = append(kept, adapter)
				continue
			}
			closing = append(closing, muxManagedClose{adapter: adapter, reason: "idle", drain: false})
		}
		if len(kept) == 0 {
			delete(m.retired, endpoint)
		} else {
			m.retired[endpoint] = kept
		}
	}
	m.recordClosedAdaptersLocked(closing)
	m.mu.Unlock()
	var err error
	for _, closeItem := range closing {
		if closeItem.adapter == nil || closeItem.adapter.adapter == nil {
			continue
		}
		if closeItem.drain {
			err = errors.Join(err, m.drainManagedAdapter(ctx, closeItem.adapter, closeItem.reason))
		}
		err = errors.Join(err, closeItem.adapter.adapter.Close())
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
	m.stopJanitor()
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
	m.recordClosedAdaptersForReasonLocked(closed, "manager_closed")
	m.mu.Unlock()
	var err error
	for _, adapter := range closed {
		if adapter != nil && adapter.adapter != nil {
			err = errors.Join(err, adapter.adapter.Close())
		}
	}
	return err
}

func (m *ExperimentalMuxConnectionManager) startJanitor() {
	if m == nil || m.janitorInterval <= 0 {
		return
	}
	m.mu.Lock()
	if m.closed || m.janitorCancel != nil {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	interval := m.janitorInterval
	m.janitorCancel = cancel
	m.janitorDone = done
	m.mu.Unlock()
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				_ = m.closeIdle(ctx, now, true)
			}
		}
	}()
}

func (m *ExperimentalMuxConnectionManager) stopJanitor() {
	if m == nil {
		return
	}
	m.mu.Lock()
	cancel := m.janitorCancel
	done := m.janitorDone
	m.janitorCancel = nil
	m.janitorDone = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
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
	unhealthyAdapters := m.unhealthyAdapters
	poolExhaustions := m.poolExhaustions
	janitorRuns := m.janitorRuns
	maxConnsPerEndpoint := m.maxConnsPerEndpoint
	maxIdleConnsPerEndpoint := m.maxIdleConnsPerEndpoint
	janitorInterval := m.janitorInterval
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
		Closed:                  closed,
		IdleTimeout:             idleTimeout,
		MaxStreamsPerConn:       maxStreamsPerConn,
		MaxConnsPerEndpoint:     maxConnsPerEndpoint,
		MaxIdleConnsPerEndpoint: maxIdleConnsPerEndpoint,
		JanitorInterval:         janitorInterval,
		Endpoints:               endpoints,
		RetiredAdapters:         retiredAdapters,
		WatchUpdates:            watchUpdates,
		Removed:                 removed,
		ClosedAdapters:          closedAdapters,
		UnhealthyAdapters:       unhealthyAdapters,
		PoolExhaustions:         poolExhaustions,
		JanitorRuns:             janitorRuns,
		CloseReasons:            closeReasons,
		DrainReasons:            drainReasons,
		LastUpdated:             lastUpdated,
	}
}

func (m *ExperimentalMuxConnectionManager) DiagnosisSnapshot() RPCMuxConnectionManagerDiagnosis {
	snapshot := m.Snapshot()
	return RPCMuxConnectionManagerDiagnosis{
		Enabled:                 m != nil && !snapshot.Closed,
		Mode:                    "experimental_mux_manager",
		IdleTimeout:             snapshot.IdleTimeout,
		MaxStreamsPerConn:       snapshot.MaxStreamsPerConn,
		MaxConnsPerEndpoint:     snapshot.MaxConnsPerEndpoint,
		MaxIdleConnsPerEndpoint: snapshot.MaxIdleConnsPerEndpoint,
		JanitorInterval:         snapshot.JanitorInterval,
		Endpoints:               snapshot.Endpoints,
		RetiredAdapters:         snapshot.RetiredAdapters,
		WatchUpdates:            snapshot.WatchUpdates,
		Removed:                 snapshot.Removed,
		ClosedAdapters:          snapshot.ClosedAdapters,
		UnhealthyAdapters:       snapshot.UnhealthyAdapters,
		PoolExhaustions:         snapshot.PoolExhaustions,
		JanitorRuns:             snapshot.JanitorRuns,
		CloseReasons:            snapshot.CloseReasons,
		DrainReasons:            snapshot.DrainReasons,
		LastUpdated:             snapshot.LastUpdated,
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
	m.recordClosedAdaptersForReasonLocked(removed, "resolver_update")
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

func (m *ExperimentalMuxConnectionManager) recordClosedAdaptersLocked(closing []muxManagedClose) {
	if len(closing) == 0 {
		return
	}
	if m.closeReasons == nil {
		m.closeReasons = make(map[string]int64)
	}
	if m.removed == nil {
		m.removed = make([]string, 0, len(closing))
	}
	for _, closeItem := range closing {
		if closeItem.adapter == nil {
			continue
		}
		reason := closeItem.reason
		if reason == "" {
			reason = "closed"
		}
		m.closedAdapters++
		m.closeReasons[reason]++
		m.removed = append(m.removed, closeItem.adapter.endpoint)
	}
	m.lastUpdated = time.Now()
}

func (m *ExperimentalMuxConnectionManager) recordClosedAdaptersForReasonLocked(adapters []*muxManagedAdapter, reason string) {
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
	unhealthy := m.collectUnhealthyEndpointLocked(endpoint)
	m.recordClosedAdaptersLocked(unhealthy)
	if existing := m.pickReusableAdapterLocked(endpoint); existing != nil {
		existing.lastUsed = time.Now()
		adapter := existing.adapter
		m.mu.Unlock()
		_ = closeManagedAdapters(unhealthy)
		return adapter, nil
	}
	if m.maxConnsPerEndpoint > 0 && len(m.adapters[endpoint]) >= m.maxConnsPerEndpoint {
		m.poolExhaustions++
		m.lastUpdated = time.Now()
		m.mu.Unlock()
		_ = closeManagedAdapters(unhealthy)
		return nil, NewError(CodeUnavailable, "mux connection pool exhausted")
	}
	opts := append([]ExperimentalMuxTransportOption(nil), m.options...)
	m.mu.Unlock()
	_ = closeManagedAdapters(unhealthy)
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
	unhealthy = m.collectUnhealthyEndpointLocked(endpoint)
	m.recordClosedAdaptersLocked(unhealthy)
	if existing := m.pickReusableAdapterLocked(endpoint); existing != nil {
		existing.lastUsed = time.Now()
		existingAdapter := existing.adapter
		m.mu.Unlock()
		_ = adapter.Close()
		_ = closeManagedAdapters(unhealthy)
		return existingAdapter, nil
	}
	if m.maxConnsPerEndpoint > 0 && len(m.adapters[endpoint]) >= m.maxConnsPerEndpoint {
		m.poolExhaustions++
		m.lastUpdated = time.Now()
		m.mu.Unlock()
		_ = adapter.Close()
		_ = closeManagedAdapters(unhealthy)
		return nil, NewError(CodeUnavailable, "mux connection pool exhausted")
	}
	managed := &muxManagedAdapter{endpoint: endpoint, adapter: adapter, lastUsed: time.Now()}
	m.adapters[endpoint] = append(m.adapters[endpoint], managed)
	m.lastUpdated = managed.lastUsed
	m.mu.Unlock()
	_ = closeManagedAdapters(unhealthy)
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
		if adapterUnhealthy(adapter) {
			continue
		}
		if m.maxStreamsPerConn <= 0 || adapterActiveStreams(adapter) < m.maxStreamsPerConn {
			return adapter
		}
	}
	return nil
}

func (m *ExperimentalMuxConnectionManager) collectUnhealthyEndpointLocked(endpoint string) []muxManagedClose {
	adapters := m.adapters[endpoint]
	if len(adapters) == 0 {
		return nil
	}
	kept := adapters[:0]
	closing := make([]muxManagedClose, 0)
	for _, adapter := range adapters {
		if adapter == nil {
			continue
		}
		if adapterUnhealthy(adapter) {
			m.unhealthyAdapters++
			closing = append(closing, muxManagedClose{adapter: adapter, reason: "unhealthy"})
			continue
		}
		kept = append(kept, adapter)
	}
	if len(kept) == 0 {
		delete(m.adapters, endpoint)
	} else {
		m.adapters[endpoint] = kept
	}
	return closing
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

func closeManagedAdapters(closing []muxManagedClose) error {
	var err error
	for _, closeItem := range closing {
		if closeItem.adapter == nil || closeItem.adapter.adapter == nil {
			continue
		}
		err = errors.Join(err, closeItem.adapter.adapter.Close())
	}
	return err
}

func adapterActiveStreams(adapter *muxManagedAdapter) int {
	if adapter == nil || adapter.adapter == nil {
		return 0
	}
	return adapter.adapter.Snapshot().Transport.ActiveStreams
}

func adapterUnhealthy(adapter *muxManagedAdapter) bool {
	if adapter == nil || adapter.adapter == nil {
		return false
	}
	snapshot := adapter.adapter.Snapshot().Transport
	return snapshot.Closed || snapshot.Liveness == "closed"
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
