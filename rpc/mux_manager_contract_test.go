package rpc

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestExperimentalMuxConnectionManagerNormalizesDefensiveOptions(t *testing.T) {
	if _, err := NewExperimentalMuxConnectionManager(nil); err == nil {
		t.Fatal("nil resolver should be rejected")
	}
	transportOptions := []ExperimentalMuxTransportOption{
		WithExperimentalMuxReceiveQueueSize(7),
	}
	manager, err := NewExperimentalMuxConnectionManager(
		ResolverFunc(func(context.Context) ([]string, error) {
			return []string{" tcp://one/ ", "tcp://one", "tcp://two/"}, nil
		}),
		nil,
		WithExperimentalMuxConnectionManagerBalancer(nil),
		WithExperimentalMuxConnectionManagerTransportOptions(transportOptions...),
		WithExperimentalMuxConnectionManagerOpenRetryReasons(),
		func(manager *ExperimentalMuxConnectionManager) {
			manager.balancer = nil
			manager.idleTimeout = -time.Second
			manager.maxConnsPerEndpoint = -1
			manager.maxIdleConnsPerEndpoint = -1
			manager.healthFailureThreshold = 0
			manager.healthEjectionDuration = -time.Second
			manager.healthBackoffMultiplier = 0
			manager.healthMaxCooldown = -time.Second
			manager.maxOpenRetries = -1
		},
	)
	if err != nil {
		t.Fatalf("NewExperimentalMuxConnectionManager: %v", err)
	}
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	transportOptions[0] = nil
	if len(manager.options) != 1 || manager.options[0] == nil {
		t.Fatalf("transport options = %#v, want defensive copy", manager.options)
	}
	if manager.candidate != nil {
		t.Fatalf("candidate = %#v, want transport options to clear candidate mode", manager.candidate)
	}
	snapshot := manager.Snapshot()
	if snapshot.IdleTimeout != 0 ||
		snapshot.MaxConnsPerEndpoint != 0 ||
		snapshot.MaxIdleConnsPerEndpoint != 0 ||
		snapshot.HealthFailureThreshold != 1 ||
		snapshot.HealthEjectionDuration != 0 ||
		snapshot.HealthBackoffMultiplier != 1 ||
		snapshot.HealthMaxCooldown != 0 ||
		snapshot.MaxOpenRetries != 0 {
		t.Fatalf("normalized snapshot = %+v", snapshot)
	}
	if want := []string{"dial_failure", "pool_exhausted"}; !reflect.DeepEqual(snapshot.OpenRetryReasons, want) {
		t.Fatalf("open retry reasons = %v, want %v", snapshot.OpenRetryReasons, want)
	}
	if err := manager.SyncResolver(context.Background()); err != nil {
		t.Fatalf("SyncResolver: %v", err)
	}
	endpoint, err := manager.pickEndpoint(context.Background(), []string{" tcp://one/ "})
	if err != nil {
		t.Fatalf("pickEndpoint: %v", err)
	}
	if endpoint != "tcp://one" {
		t.Fatalf("pickEndpoint = %q, want tcp://one", endpoint)
	}
}

func TestExperimentalMuxConnectionManagerLifecycleErrors(t *testing.T) {
	var nilManager *ExperimentalMuxConnectionManager
	if snapshot := nilManager.Snapshot(); !reflect.DeepEqual(snapshot, ExperimentalMuxConnectionManagerSnapshot{}) {
		t.Fatalf("nil Snapshot = %+v, want zero value", snapshot)
	}
	nilManager.startJanitor()
	nilManager.stopJanitor()
	if err := nilManager.Drain(context.Background(), "shutdown"); err != nil {
		t.Fatalf("nil Drain = %v, want nil", err)
	}
	if err := nilManager.CloseIdle(context.Background()); err != nil {
		t.Fatalf("nil CloseIdle = %v, want nil", err)
	}
	if err := nilManager.Close(); err != nil {
		t.Fatalf("nil Close = %v, want nil", err)
	}
	if err := nilManager.Watch(context.Background()); !errors.Is(err, ErrExperimentalMuxTransportClosed) {
		t.Fatalf("nil Watch = %v, want closed", err)
	}
	if err := nilManager.SyncResolver(context.Background()); !errors.Is(err, ErrExperimentalMuxTransportClosed) {
		t.Fatalf("nil SyncResolver = %v, want closed", err)
	}
	if _, err := nilManager.pickEndpoint(context.Background(), []string{"tcp://one"}); !errors.Is(err, ErrExperimentalMuxTransportClosed) {
		t.Fatalf("nil pickEndpoint = %v, want closed", err)
	}

	manager, err := NewExperimentalMuxConnectionManager(
		ResolverFunc(func(ctx context.Context) ([]string, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return []string{"tcp://one"}, nil
		}),
	)
	if err != nil {
		t.Fatalf("NewExperimentalMuxConnectionManager: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for name, call := range map[string]func() error{
		"drain":      func() error { return manager.Drain(canceled, "shutdown") },
		"close idle": func() error { return manager.CloseIdle(canceled) },
		"sync":       func() error { return manager.SyncResolver(canceled) },
		"watch":      func() error { return manager.Watch(canceled) },
	} {
		t.Run(name+" canceled", func(t *testing.T) {
			if err := call(); !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want context canceled", err)
			}
		})
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	for name, call := range map[string]func() error{
		"close idle": func() error { return manager.CloseIdle(context.Background()) },
		"sync":       func() error { return manager.SyncResolver(context.Background()) },
		"watch":      func() error { return manager.Watch(context.Background()) },
	} {
		t.Run(name+" closed", func(t *testing.T) {
			if err := call(); !errors.Is(err, ErrExperimentalMuxTransportClosed) {
				t.Fatalf("error = %v, want closed", err)
			}
		})
	}
	if _, err := manager.pickEndpoint(context.Background(), []string{"tcp://one"}); !errors.Is(err, ErrExperimentalMuxTransportClosed) {
		t.Fatalf("pickEndpoint after Close = %v, want closed", err)
	}
}

func TestExperimentalMuxConnectionManagerResolverFailures(t *testing.T) {
	resolveErr := errors.New("registry unavailable")
	manager, err := NewExperimentalMuxConnectionManager(
		ResolverFunc(func(context.Context) ([]string, error) { return nil, resolveErr }),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if err := manager.SyncResolver(context.Background()); !errors.Is(err, resolveErr) {
		t.Fatalf("resolver error = %v, want %v", err, resolveErr)
	}

	empty, err := NewExperimentalMuxConnectionManager(
		ResolverFunc(func(context.Context) ([]string, error) { return []string{" ", "/"}, nil }),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer empty.Close()
	if err := empty.SyncResolver(context.Background()); err == nil || err.Error() != "no endpoint to pick" {
		t.Fatalf("empty resolver error = %v, want no endpoint to pick", err)
	}
}

func TestExperimentalMuxServerAdapterDrainBoundaries(t *testing.T) {
	var nilAdapter *ExperimentalMuxServerAdapter
	if err := nilAdapter.Drain(context.Background(), "shutdown"); !errors.Is(err, ErrExperimentalMuxTransportClosed) {
		t.Fatalf("nil server Drain = %v, want closed", err)
	}
	if err := nilAdapter.waitCandidateDrain(context.Background(), "shutdown"); err != nil {
		t.Fatalf("nil server waitCandidateDrain = %v, want nil", err)
	}

	empty := &ExperimentalMuxServerAdapter{}
	if err := empty.Drain(context.Background(), "shutdown"); !errors.Is(err, ErrExperimentalMuxTransportClosed) {
		t.Fatalf("empty server Drain = %v, want closed", err)
	}
	if err := empty.waitCandidateDrain(context.Background(), "shutdown"); err != nil {
		t.Fatalf("empty server waitCandidateDrain = %v, want nil", err)
	}
}

func TestExperimentalMuxConnectionManagerWatchContracts(t *testing.T) {
	t.Run("resolver without watch", func(t *testing.T) {
		manager, err := NewExperimentalMuxConnectionManager(
			ResolverFunc(func(context.Context) ([]string, error) { return []string{"tcp://one"}, nil }),
		)
		if err != nil {
			t.Fatal(err)
		}
		defer manager.Close()
		if err := manager.Watch(context.Background()); err == nil || err.Error() != "mux connection manager resolver does not support watch" {
			t.Fatalf("Watch = %v, want unsupported error", err)
		}
	})

	t.Run("watch setup error", func(t *testing.T) {
		wantErr := errors.New("watch unavailable")
		manager, err := NewExperimentalMuxConnectionManager(hardeningWatchResolver{watchErr: wantErr})
		if err != nil {
			t.Fatal(err)
		}
		defer manager.Close()
		if err := manager.Watch(context.Background()); !errors.Is(err, wantErr) {
			t.Fatalf("Watch = %v, want %v", err, wantErr)
		}
	})

	t.Run("empty updates ignored before channel close", func(t *testing.T) {
		updates := make(chan []string, 2)
		updates <- []string{" ", "/"}
		close(updates)
		manager, err := NewExperimentalMuxConnectionManager(hardeningWatchResolver{updates: updates})
		if err != nil {
			t.Fatal(err)
		}
		defer manager.Close()
		if err := manager.Watch(context.Background()); err != nil {
			t.Fatalf("Watch: %v", err)
		}
		if snapshot := manager.Snapshot(); snapshot.WatchUpdates != 0 {
			t.Fatalf("watch updates = %d, want zero", snapshot.WatchUpdates)
		}
	})
}

func TestExperimentalMuxConnectionManagerDrainAndSnapshots(t *testing.T) {
	manager, err := NewExperimentalMuxConnectionManager(
		ResolverFunc(func(context.Context) ([]string, error) { return []string{"tcp://one"}, nil }),
	)
	if err != nil {
		t.Fatal(err)
	}
	active := &muxManagedAdapter{
		endpoint:     "tcp://one",
		connectionID: "mux-1",
		adapter:      &ExperimentalMuxClientAdapter{},
		lastUsed:     time.Unix(10, 0),
	}
	retired := &muxManagedAdapter{
		endpoint:     "tcp://two",
		connectionID: "mux-2",
		adapter:      &ExperimentalMuxClientAdapter{},
		lastUsed:     time.Unix(20, 0),
		retired:      true,
	}
	manager.adapters = map[string][]*muxManagedAdapter{
		"tcp://one": {nil, active},
	}
	manager.retired = map[string][]*muxManagedAdapter{
		"tcp://two": {retired, {endpoint: "tcp://ignored"}},
	}

	manager.mu.Lock()
	adapters := manager.snapshotAdaptersLocked()
	endpoints := manager.snapshotEndpointSnapshotsLocked()
	manager.mu.Unlock()
	if len(adapters) != 4 {
		t.Fatalf("adapter snapshot count = %d, want 4 including nil entries", len(adapters))
	}
	if len(endpoints) != 2 {
		t.Fatalf("endpoint snapshots = %+v, want active and retired adapters", endpoints)
	}
	if !endpoints[1].Retired && !endpoints[0].Retired {
		t.Fatalf("endpoint snapshots = %+v, want retired evidence", endpoints)
	}
	if err := manager.Drain(context.Background(), "shutdown"); !errors.Is(err, ErrExperimentalMuxTransportClosed) {
		t.Fatalf("Drain = %v, want joined closed error", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	snapshot := manager.Snapshot()
	if !snapshot.Closed || snapshot.ClosedAdapters != 3 || snapshot.CloseReasons["manager_closed"] != 3 {
		t.Fatalf("closed snapshot = %+v, want three concrete managed entries recorded", snapshot)
	}
}

func TestMuxManagerDiagnosisHelpers(t *testing.T) {
	unavailable := NewError(CodeUnavailable, "unavailable")
	deadline := NewError(CodeDeadlineExceeded, "deadline")
	invalid := NewError(CodeInvalidArgument, "invalid")

	for _, test := range []struct {
		name     string
		fallback string
		health   string
		err      error
		want     string
	}{
		{name: "health wins", fallback: "fallback", health: "ejected", err: invalid, want: "ejected"},
		{name: "fallback wins", fallback: "fallback", err: invalid, want: "fallback"},
		{name: "error code", err: unavailable, want: string(CodeUnavailable)},
		{name: "nil error code", want: string(CodeOK)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := muxOpenRetryReason(test.fallback, test.health, test.err); got != test.want {
				t.Fatalf("muxOpenRetryReason = %q, want %q", got, test.want)
			}
		})
	}

	for _, test := range []struct {
		name   string
		reason string
		err    error
		want   bool
	}{
		{name: "pool exhaustion", reason: "pool_exhausted", want: true},
		{name: "dial failure", reason: "dial_failure", want: true},
		{name: "unhealthy", reason: "unhealthy", want: true},
		{name: "unavailable code", err: unavailable, want: true},
		{name: "deadline code", err: deadline, want: true},
		{name: "non health error", err: invalid, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := muxEndpointHealthFailure(test.reason, test.err); got != test.want {
				t.Fatalf("muxEndpointHealthFailure = %v, want %v", got, test.want)
			}
		})
	}

	if got := muxRetryReasons(nil); got != nil {
		t.Fatalf("muxRetryReasons(nil) = %v, want nil", got)
	}
	if got := muxRetryReasons(muxRetryReasonSet(" z ", "", "a", "z")); !reflect.DeepEqual(got, []string{"a", "z"}) {
		t.Fatalf("retry reasons = %v, want sorted unique values", got)
	}
	if got := filterMuxEndpoints([]string{" tcp://one/ ", "tcp://one", "tcp://two/"}, []string{" tcp://one/ ", ""}); !reflect.DeepEqual(got, []string{"tcp://two"}) {
		t.Fatalf("filtered endpoints = %v, want tcp://two", got)
	}
	for endpoint, want := range map[string]string{
		"":                                 "",
		" https://example.com:8443/path/ ": "example.com:8443",
		"tcp://127.0.0.1:9000/":            "127.0.0.1:9000",
		"127.0.0.1:9000/":                  "127.0.0.1:9000",
		":":                                ":",
	} {
		if got := muxEndpointAddress(endpoint); got != want {
			t.Fatalf("muxEndpointAddress(%q) = %q, want %q", endpoint, got, want)
		}
	}

	if got := adapterActiveStreams(nil); got != 0 {
		t.Fatalf("nil adapter active streams = %d, want zero", got)
	}
	empty := &muxManagedAdapter{adapter: &ExperimentalMuxClientAdapter{}}
	if got := adapterActiveStreams(empty); got != 0 {
		t.Fatalf("empty adapter active streams = %d, want zero", got)
	}
	if !adapterUnhealthy(empty) {
		t.Fatal("empty adapter should report closed transport")
	}
	if adapterUnhealthy(nil) {
		t.Fatal("nil adapter should not report unhealthy")
	}
}

func TestExperimentalMuxConnectionManagerHealthAndRetryAccounting(t *testing.T) {
	manager := &ExperimentalMuxConnectionManager{
		healthFailureThreshold:  1,
		healthEjectionDuration:  10 * time.Second,
		healthBackoffMultiplier: 0,
		healthMaxCooldown:       15 * time.Second,
	}
	now := time.Unix(100, 0)
	unavailable := NewError(CodeUnavailable, "downstream unavailable")

	manager.recordEndpointFailureAtLocked("", "", unavailable, now)
	manager.recordEndpointFailureAtLocked("tcp://ignored", "", NewError(CodeInvalidArgument, "invalid"), now)
	if len(manager.health) != 0 {
		t.Fatalf("ignored health failures = %+v, want none", manager.health)
	}
	manager.recordEndpointFailureAtLocked(" tcp://one/ ", "", unavailable, now)
	state := manager.health["tcp://one"]
	if state == nil ||
		state.failures != 1 ||
		state.reason != "failure" ||
		state.lastError != unavailable.Error() ||
		state.cooldown != 10*time.Second ||
		!state.cooldownUntil.Equal(now.Add(10*time.Second)) {
		t.Fatalf("health state = %+v, want normalized ejection evidence", state)
	}
	if manager.endpointHealthReason("") != "" || manager.endpointHealthReason("tcp://missing") != "" {
		t.Fatal("empty or missing endpoint should not have a health reason")
	}
	if got := manager.endpointHealthReason(" tcp://one/ "); got != "failure" {
		t.Fatalf("endpoint health reason = %q, want failure", got)
	}

	manager.healthEjectionDuration = 20 * time.Second
	if got := manager.nextHealthCooldown(time.Second); got != 15*time.Second {
		t.Fatalf("capped health cooldown = %v, want 15s", got)
	}
	manager.healthEjectionDuration = 10 * time.Second
	if got := manager.nextHealthCooldown(time.Second); got != 10*time.Second {
		t.Fatalf("minimum health cooldown = %v, want 10s", got)
	}
	manager.healthEjectionDuration = 0
	if got := manager.nextHealthCooldown(time.Second); got != 0 {
		t.Fatalf("disabled health cooldown = %v, want zero", got)
	}
	manager.healthEjectionDuration = 10 * time.Second

	manager.health["clean"] = &muxEndpointHealth{}
	manager.health["nil"] = nil
	health := manager.snapshotHealthLocked(now.Add(time.Second))
	if len(health) != 1 || health[0].Endpoint != "tcp://one" || !health[0].Ejected {
		t.Fatalf("health snapshot = %+v, want only active ejection", health)
	}
	health = manager.snapshotHealthLocked(now.Add(11 * time.Second))
	if len(health) != 1 || health[0].Ejected {
		t.Fatalf("expired health snapshot = %+v, want visible but not ejected", health)
	}

	manager.recordEndpointSuccess("")
	manager.recordEndpointSuccess("tcp://missing")
	manager.recordEndpointSuccess(" tcp://one/ ")
	if state.failures != 0 || state.reason != "" || state.lastError != "" || manager.endpointRecoveries != 1 {
		t.Fatalf("health recovery = state=%+v recoveries=%d", state, manager.endpointRecoveries)
	}

	manager.retryReasons = nil
	manager.recordOpenRetry("", "tcp://two", "")
	if manager.openRetries != 0 {
		t.Fatalf("invalid retry count = %d, want zero", manager.openRetries)
	}
	manager.recordOpenRetry(" tcp://one/ ", " tcp://two/ ", "")
	if manager.openRetries != 1 ||
		manager.lastRetriedFrom != "tcp://one" ||
		manager.lastRetriedTo != "tcp://two" ||
		manager.retryReasons["open_before"] != 1 {
		t.Fatalf("retry accounting = %+v", manager.Snapshot())
	}

	if manager.openRetryAllowed("", unavailable) {
		t.Fatal("retry should be disabled when maxOpenRetries is zero")
	}
	manager.maxOpenRetries = 1
	if manager.openRetryAllowed("", NewError(CodeInvalidArgument, "invalid")) {
		t.Fatal("non-retryable code should not retry")
	}
	manager.openRetryReasons = nil
	if manager.openRetryAllowed("", unavailable) {
		t.Fatal("empty retry reason set should not retry")
	}
	manager.openRetryReasons = muxRetryReasonSet("open_before")
	if !manager.openRetryAllowed("", unavailable) {
		t.Fatal("default open_before reason should retry when configured")
	}
	if manager.openRetryAllowed("dial_failure", unavailable) {
		t.Fatal("unconfigured retry reason should not retry")
	}
}

func TestExperimentalMuxConnectionManagerIdleBookkeeping(t *testing.T) {
	manager := &ExperimentalMuxConnectionManager{
		adapters:     make(map[string][]*muxManagedAdapter),
		retired:      make(map[string][]*muxManagedAdapter),
		closeReasons: nil,
		drainReasons: nil,
	}
	now := time.Unix(200, 0)
	unhealthy := &muxManagedAdapter{
		endpoint: "tcp://unhealthy",
		adapter:  &ExperimentalMuxClientAdapter{},
	}
	manager.adapters["tcp://unhealthy"] = []*muxManagedAdapter{nil, unhealthy}
	manager.retired["tcp://retired"] = []*muxManagedAdapter{
		nil,
		{endpoint: "tcp://retired", adapter: &ExperimentalMuxClientAdapter{}},
	}

	if err := manager.closeIdle(context.Background(), now, true); err != nil {
		t.Fatalf("closeIdle: %v", err)
	}
	snapshot := manager.Snapshot()
	if snapshot.JanitorRuns != 1 ||
		snapshot.UnhealthyAdapters != 2 ||
		snapshot.ClosedAdapters != 2 ||
		snapshot.CloseReasons["unhealthy"] != 2 ||
		len(snapshot.Endpoints) != 0 {
		t.Fatalf("idle bookkeeping snapshot = %+v", snapshot)
	}

	manager.recordClosedAdaptersLocked(nil)
	manager.recordClosedAdaptersLocked([]muxManagedClose{
		{},
		{adapter: &muxManagedAdapter{endpoint: "tcp://default"}},
	})
	manager.recordClosedAdaptersForReasonLocked(nil, "")
	manager.recordClosedAdaptersForReasonLocked(
		[]*muxManagedAdapter{nil, {endpoint: "tcp://closed"}},
		"",
	)
	snapshot = manager.Snapshot()
	if snapshot.CloseReasons["closed"] != 2 {
		t.Fatalf("default close reasons = %+v, want two", snapshot.CloseReasons)
	}

	if err := manager.drainManagedAdapter(context.Background(), nil, ""); err != nil {
		t.Fatalf("drain nil managed adapter = %v, want nil", err)
	}
	if err := manager.drainManagedAdapter(context.Background(), &muxManagedAdapter{}, ""); err != nil {
		t.Fatalf("drain empty managed adapter = %v, want nil", err)
	}
	if err := closeManagedAdapters([]muxManagedClose{{}, {adapter: &muxManagedAdapter{}}}); err != nil {
		t.Fatalf("close empty managed adapters = %v, want nil", err)
	}
}

func TestExperimentalMuxConnectionManagerUsesInjectedClock(t *testing.T) {
	manager, err := NewExperimentalMuxConnectionManager(
		ResolverFunc(func(context.Context) ([]string, error) { return []string{"tcp://one"}, nil }),
		nil,
	)
	if err != nil {
		t.Fatalf("NewExperimentalMuxConnectionManager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	// The bookkeeping timestamps are stamped from the injected clock, so
	// lastUpdated becomes deterministic for observers and tests.
	stamped := time.Unix(1700000789, 0).UTC()
	manager.nowFunc = func() time.Time { return stamped }
	manager.recordWatchUpdate()
	if snapshot := manager.Snapshot(); !snapshot.LastUpdated.Equal(stamped) {
		t.Fatalf("manager lastUpdated = %v, want injected %v", snapshot.LastUpdated, stamped)
	}

	// The nil-manager clock falls back to time.Now without panicking.
	if (*ExperimentalMuxConnectionManager)(nil).now().IsZero() {
		t.Fatal("nil manager clock returned zero time")
	}
}
