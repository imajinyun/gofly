package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/discovery"
	"github.com/imajinyun/gofly/core/governance"
)

func TestSnapshotContributorFuncBoundaries(t *testing.T) {
	ctx := context.Background()
	var snapshot Snapshot
	var nilContributor SnapshotContributorFunc
	if err := nilContributor.ContributeSnapshot(ctx, &snapshot); err != nil {
		t.Fatalf("nil SnapshotContributorFunc returned error = %v, want nil", err)
	}
	boom := errors.New("boom")
	called := false
	contributor := SnapshotContributorFunc(func(gotCtx context.Context, gotSnapshot *Snapshot) error {
		if gotCtx != ctx {
			t.Fatalf("contributor context = %v, want original context", gotCtx)
		}
		if gotSnapshot != &snapshot {
			t.Fatalf("contributor snapshot pointer = %p, want %p", gotSnapshot, &snapshot)
		}
		gotSnapshot.Metadata = map[string]string{"feature": "available"}
		called = true
		return boom
	})
	if err := contributor.ContributeSnapshot(ctx, &snapshot); !errors.Is(err, boom) {
		t.Fatalf("contributor error = %v, want wrapped boom", err)
	}
	if !called || snapshot.Metadata["feature"] != "available" {
		t.Fatalf("contributor called=%t snapshot=%+v, want mutation before error", called, snapshot)
	}
}

func TestControlPlanePureOrderingAndClassification(t *testing.T) {
	rules := []governance.Rule{
		{Name: "same", Priority: 1, Service: "users", Method: "List"},
		{Name: "same", Priority: 1, Service: "orders", Method: "Update"},
		{Name: "same", Priority: 1, Service: "orders", Method: "Create"},
		{Name: "alpha", Priority: 1, Service: "users", Method: "List"},
		{Name: "low", Priority: 0, Service: "orders", Method: "List"},
		{Name: "high", Priority: 10, Service: "orders", Method: "List"},
	}
	sortGovernanceRules(rules)
	wantOrder := []string{"high/orders/List", "alpha/users/List", "same/orders/Create", "same/orders/Update", "same/users/List", "low/orders/List"}
	for i, want := range wantOrder {
		got := rules[i].Name + "/" + rules[i].Service + "/" + rules[i].Method
		if got != want {
			t.Fatalf("sorted rule %d = %q, want %q; all=%#v", i, got, want, rules)
		}
	}

	classifyCases := []struct {
		name   string
		fields []string
		want   string
	}{
		{name: "empty", fields: nil, want: "checksum-change"},
		{name: "version", fields: []string{"version"}, want: "version-change"},
		{name: "services", fields: []string{"services"}, want: "service-discovery-change"},
		{name: "configs", fields: []string{"configs"}, want: "config-change"},
		{name: "policies", fields: []string{"policies"}, want: "policy-change"},
		{name: "metadata", fields: []string{"metadata"}, want: "metadata-change"},
		{name: "unknown", fields: []string{"checksum"}, want: "checksum-change"},
		{name: "mixed", fields: []string{"version", "metadata"}, want: "mixed-change"},
	}
	for _, tt := range classifyCases {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifySnapshotChange(tt.fields); got != tt.want {
				t.Fatalf("classifySnapshotChange(%v) = %q, want %q", tt.fields, got, tt.want)
			}
		})
	}
}

func TestControlPlaneProviderSourceAndWatchBoundaries(t *testing.T) {
	if got := (CompositeProvider{}).Source(); got != "runtime" {
		t.Fatalf("CompositeProvider default source = %q, want runtime", got)
	}
	if got := (StaticProvider{}).Source(); got != "static" {
		t.Fatalf("StaticProvider default source = %q, want static", got)
	}
	var nilCtx context.Context
	if _, err := (CompositeProvider{}).Watch(nilCtx); err == nil || err.Error() != "controlplane watch context is nil" {
		t.Fatalf("CompositeProvider Watch(nil) error = %v, want nil context error", err)
	}
	if _, err := (StaticProvider{}).Watch(nilCtx); err == nil || err.Error() != "controlplane watch context is nil" {
		t.Fatalf("StaticProvider Watch(nil) error = %v, want nil context error", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	events, err := (CompositeProvider{WatchInterval: time.Millisecond}).Watch(ctx)
	if err != nil {
		t.Fatalf("CompositeProvider canceled Watch error = %v", err)
	}
	select {
	case event, ok := <-events:
		if ok {
			if event.Error == "" {
				t.Fatalf("CompositeProvider canceled watch event = %#v, want cancellation error", event)
			}
			select {
			case _, ok := <-events:
				if ok {
					t.Fatal("CompositeProvider canceled watch stayed open after cancellation event")
				}
			case <-time.After(time.Second):
				t.Fatal("CompositeProvider canceled watch did not close after cancellation event")
			}
		}
	case <-time.After(time.Second):
		t.Fatal("CompositeProvider canceled watch did not close")
	}
}

func TestControlPlaneProviderLoadBoundaries(t *testing.T) {
	var nilCtx context.Context
	snapshot, err := (CompositeProvider{Contributors: []SnapshotContributor{nil}}).Load(nilCtx)
	if err != nil {
		t.Fatalf("CompositeProvider Load(nil) error = %v", err)
	}
	if snapshot.Version != DefaultSnapshotVersion || snapshot.Checksum == "" {
		t.Fatalf("CompositeProvider default snapshot = %+v, want default version and checksum", snapshot)
	}
	boom := errors.New("boom")
	_, err = (CompositeProvider{Contributors: []SnapshotContributor{SnapshotContributorFunc(func(context.Context, *Snapshot) error {
		return boom
	})}}).Load(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("CompositeProvider contributor error = %v, want boom", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (StaticProvider{}).Load(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("StaticProvider canceled Load error = %v, want context.Canceled", err)
	}
	staticSnapshot, err := (StaticProvider{Snapshot: Snapshot{Version: "v1"}}).Load(nilCtx)
	if err != nil || staticSnapshot.Version != "v1" || staticSnapshot.Checksum == "" {
		t.Fatalf("StaticProvider Load(nil) = %+v/%v, want v1 checksum", staticSnapshot, err)
	}
}

func TestSnapshotStableChecksumIgnoresOrderingAndTimestamp(t *testing.T) {
	left := Snapshot{
		Version: "v1",
		Services: []ServiceSnapshot{
			{Name: "orders", Endpoints: []EndpointSnapshot{{Address: "10.0.0.2"}, {Address: "10.0.0.1"}}},
			{Name: "users", Endpoints: []EndpointSnapshot{{Address: "10.0.1.1"}}},
		},
		Configs:   map[string]json.RawMessage{"app": json.RawMessage(`{"timeout":"1s"}`)},
		UpdatedAt: time.Unix(1, 0),
	}
	right := Snapshot{
		Version: "v1",
		Services: []ServiceSnapshot{
			{Name: "users", Endpoints: []EndpointSnapshot{{Address: "10.0.1.1"}}},
			{Name: "orders", Endpoints: []EndpointSnapshot{{Address: "10.0.0.1"}, {Address: "10.0.0.2"}}},
		},
		Configs:   map[string]json.RawMessage{"app": json.RawMessage(`{"timeout":"1s"}`)},
		UpdatedAt: time.Unix(2, 0),
	}
	if left.StableChecksum() == "" || left.StableChecksum() != right.StableChecksum() {
		t.Fatalf("checksums = %q/%q, want stable equal non-empty", left.StableChecksum(), right.StableChecksum())
	}
}

func TestDiffSnapshotsClassifiesSemanticChanges(t *testing.T) {
	base := Snapshot{Version: "v1", Services: []ServiceSnapshot{{Name: "orders", Endpoints: []EndpointSnapshot{{Address: "10.0.0.1"}}}}}
	same := Snapshot{Version: "v1", Services: []ServiceSnapshot{{Name: "orders", Endpoints: []EndpointSnapshot{{Address: "10.0.0.1"}}}}, UpdatedAt: time.Unix(99, 0)}
	if diff := DiffSnapshots(base, same); diff.Changed || diff.ChangeType != "none" || len(diff.ChangedFields) != 0 {
		t.Fatalf("same semantic diff = %+v, want unchanged", diff)
	}

	serviceChanged := Snapshot{Version: "v1", Services: []ServiceSnapshot{{Name: "orders", Endpoints: []EndpointSnapshot{{Address: "10.0.0.2"}}}}}
	diff := DiffSnapshots(base, serviceChanged)
	if !diff.Changed || diff.ChangeType != "service-discovery-change" || len(diff.ChangedFields) != 1 || diff.ChangedFields[0] != "services" {
		t.Fatalf("service diff = %+v, want service-discovery-change", diff)
	}

	mixed := Snapshot{Version: "v2", Metadata: map[string]string{"gateway": "available"}}
	diff = DiffSnapshots(base, mixed)
	if !diff.Changed || diff.ChangeType != "mixed-change" || len(diff.ChangedFields) < 2 {
		t.Fatalf("mixed diff = %+v, want mixed semantic fields", diff)
	}
}

func TestDiffSnapshotChecksumClassifiesUnknownPreviousSnapshot(t *testing.T) {
	snapshot := Snapshot{Version: "v1"}.WithChecksum()
	if diff := DiffSnapshotChecksum(snapshot.Checksum, snapshot); diff.Changed || diff.ChangeType != "none" {
		t.Fatalf("matching checksum diff = %+v, want unchanged", diff)
	}
	if diff := DiffSnapshotChecksum("old", snapshot); !diff.Changed || diff.ChangeType != "checksum-mismatch" || diff.ToChecksum != snapshot.Checksum {
		t.Fatalf("mismatching checksum diff = %+v, want checksum-mismatch", diff)
	}
	if diff := DiffSnapshotChecksum("", snapshot); !diff.Changed || diff.ChangeType != "initial-snapshot" {
		t.Fatalf("empty checksum diff = %+v, want initial-snapshot", diff)
	}
}

func TestDecodeSnapshotJSONAcceptsRawWrapperAndEnvelope(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "raw snapshot", data: `{"version":"v1","metadata":{"llm":"available"}}`},
		{name: "snapshot wrapper", data: `{"snapshot":{"version":"v1","metadata":{"llm":"available"}}}`},
		{name: "ai control-plane envelope", data: `{"ok":true,"command":"ai.control_plane","data":{"snapshot":{"version":"v1","metadata":{"llm":"available"}}}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot, err := DecodeSnapshotJSON([]byte(tt.data))
			if err != nil {
				t.Fatalf("DecodeSnapshotJSON: %v", err)
			}
			if snapshot.Version != "v1" || snapshot.Metadata["llm"] != "available" || snapshot.Checksum == "" {
				t.Fatalf("snapshot = %+v, want version, metadata and checksum", snapshot)
			}
		})
	}
}

func TestDecodeSnapshotJSONRejectsMissingSnapshot(t *testing.T) {
	if _, err := DecodeSnapshotJSON([]byte(`{"ok":true,"data":{}}`)); err == nil {
		t.Fatal("DecodeSnapshotJSON missing snapshot error = nil, want error")
	}
}

func TestConsumerActionForDiffMapsChangeTypes(t *testing.T) {
	tests := []struct {
		name              string
		changeType        string
		wantAction        string
		wantScope         string
		wantFullReconcile bool
	}{
		{name: "unchanged skips", changeType: "none", wantAction: "skip", wantScope: "cache"},
		{name: "initial loads baseline", changeType: "initial-snapshot", wantAction: "load-baseline", wantScope: "config", wantFullReconcile: true},
		{name: "checksum mismatch inspects", changeType: "checksum-mismatch", wantAction: "inspect-snapshot", wantScope: "unknown", wantFullReconcile: true},
		{name: "config refreshes planner", changeType: "config-change", wantAction: "refresh-config-planner", wantScope: "config"},
		{name: "service refreshes routing", changeType: "service-discovery-change", wantAction: "refresh-routing-model", wantScope: "routing"},
		{name: "policy reloads gates", changeType: "policy-change", wantAction: "reload-governance-gates", wantScope: "governance"},
		{name: "metadata refreshes capabilities", changeType: "metadata-change", wantAction: "refresh-capability-cache", wantScope: "capabilities"},
		{name: "mixed reconciles", changeType: "mixed-change", wantAction: "full-reconcile", wantScope: "policy", wantFullReconcile: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action := ConsumerActionForDiff(SnapshotDiff{ChangeType: tt.changeType})
			if action.ChangeType != tt.changeType || action.Action != tt.wantAction || action.RequiresFullReconcile != tt.wantFullReconcile {
				t.Fatalf("action = %+v, want changeType=%q action=%q fullReconcile=%t", action, tt.changeType, tt.wantAction, tt.wantFullReconcile)
			}
			if !containsString(action.Scopes, tt.wantScope) {
				t.Fatalf("action scopes = %+v, want %q", action.Scopes, tt.wantScope)
			}
			if action.Reason == "" || len(action.NextActions) == 0 {
				t.Fatalf("action = %+v, want reason and next actions", action)
			}
		})
	}
}

func TestDefaultConsumerActionsCoversManifestChangeTypes(t *testing.T) {
	actions := DefaultConsumerActions()
	if len(actions) != 10 {
		t.Fatalf("default consumer actions = %d, want 10", len(actions))
	}
	seen := map[string]bool{}
	for _, action := range actions {
		if action.ChangeType == "" || action.Action == "" {
			t.Fatalf("default action = %+v, want change type and action", action)
		}
		seen[action.ChangeType] = true
	}
	for _, want := range []string{"none", "initial-snapshot", "checksum-mismatch", "config-change", "service-discovery-change", "policy-change", "metadata-change", "version-change", "mixed-change", "checksum-change"} {
		if !seen[want] {
			t.Fatalf("default consumer actions missing %q: %+v", want, actions)
		}
	}
}

func TestStaticProviderLoadAndWatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	provider := StaticProvider{Name: "unit", Snapshot: Snapshot{Version: "v1", Services: []ServiceSnapshot{{Name: "orders"}}}}
	snapshot, err := provider.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if snapshot.Checksum == "" || provider.Source() != "unit" {
		t.Fatalf("snapshot/source = %#v/%q, want checksum and source", snapshot, provider.Source())
	}
	events, err := provider.Watch(ctx)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	event := <-events
	if event.Source != "unit" || event.Snapshot.Checksum == "" {
		t.Fatalf("event = %#v, want source and checksum", event)
	}
	cancel()
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("watch channel stayed open after cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("watch channel did not close after cancel")
	}
}

func TestCompositeProviderAggregatesRuntimeSources(t *testing.T) {
	ctx := context.Background()
	registry := discovery.NewMemoryRegistry()
	if _, err := registry.Register(ctx, discovery.Instance{ID: "orders-a", Service: "orders", Endpoint: "10.0.0.2:9000", Weight: 20, Zone: "az-b", Status: discovery.StatusHealthy, Version: "v1", Tags: map[string]string{"tier": "api"}}); err != nil {
		t.Fatalf("register orders-a: %v", err)
	}
	if _, err := registry.Register(ctx, discovery.Instance{ID: "orders-b", Service: "orders", Endpoint: "10.0.0.1:9000", Weight: 10, Zone: "az-a", Status: discovery.StatusHealthy, Version: "v1", Metadata: map[string]string{"owner": "payments"}}); err != nil {
		t.Fatalf("register orders-b: %v", err)
	}
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "orders-timeout",
		Priority:  10,
		Transport: "rpc",
		Service:   "orders",
		Policy: governance.Policy{
			Timeout: 2 * time.Second,
			Metadata: map[string]string{
				"owner": "platform",
			},
		},
	})
	configs := map[string]json.RawMessage{"runtime": json.RawMessage(`{"mode":"test"}`)}
	provider := CompositeProvider{
		Name:    "runtime-unit",
		Version: DefaultSnapshotVersion,
		Contributors: []SnapshotContributor{
			DiscoveryContributor{Registry: registry},
			GovernanceContributor{Rules: rules},
			ConfigContributor{Configs: configs},
			MetadataContributor{Metadata: map[string]string{"rest.security_headers": "available"}},
		},
	}

	snapshot, err := provider.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if provider.Source() != "runtime-unit" || snapshot.Version != DefaultSnapshotVersion || snapshot.Checksum == "" {
		t.Fatalf("provider/source snapshot = %#v source=%q, want runtime source and checksum", snapshot, provider.Source())
	}
	if len(snapshot.Services) != 1 || snapshot.Services[0].Name != "orders" || len(snapshot.Services[0].Endpoints) != 2 {
		t.Fatalf("services = %#v, want orders with two endpoints", snapshot.Services)
	}
	if snapshot.Services[0].Endpoints[0].Address != "10.0.0.1:9000" || snapshot.Services[0].Endpoints[0].Metadata["meta.owner"] != "payments" {
		t.Fatalf("first endpoint = %#v, want sorted endpoint with metadata", snapshot.Services[0].Endpoints[0])
	}
	if got := string(snapshot.Configs["runtime"]); got != `{"mode":"test"}` {
		t.Fatalf("runtime config = %s, want copied config", got)
	}
	if len(snapshot.Policies) != 1 || snapshot.Policies[0].Name != "orders-timeout" || snapshot.Metadata["rest.security_headers"] != "available" {
		t.Fatalf("policies/metadata = %#v/%#v, want governance policy and capability metadata", snapshot.Policies, snapshot.Metadata)
	}
	configs["runtime"][0] = '{'
	if got := string(snapshot.Configs["runtime"]); got != `{"mode":"test"}` {
		t.Fatalf("snapshot config mutated through source map: %s", got)
	}
}

func TestCompositeProviderSemanticRuntimeChanges(t *testing.T) {
	ctx := context.Background()
	rules := governance.NewRuleSet(governance.Rule{Name: "orders-timeout", Service: "orders", Policy: governance.Policy{Timeout: time.Second}})
	policyProvider := CompositeProvider{Contributors: []SnapshotContributor{GovernanceContributor{Rules: rules}}}
	basePolicySnapshot, err := policyProvider.Load(ctx)
	if err != nil {
		t.Fatalf("load base policy snapshot: %v", err)
	}
	rules.Replace(governance.Rule{Name: "orders-timeout", Service: "orders", Policy: governance.Policy{Timeout: 3 * time.Second}})
	changedPolicySnapshot, err := policyProvider.Load(ctx)
	if err != nil {
		t.Fatalf("load changed policy snapshot: %v", err)
	}
	if diff := DiffSnapshots(basePolicySnapshot, changedPolicySnapshot); !diff.Changed || diff.ChangeType != "policy-change" || !containsString(diff.ChangedFields, "policies") {
		t.Fatalf("policy diff = %+v, want policy-change", diff)
	}

	registry := discovery.NewMemoryRegistry()
	if _, err := registry.Register(ctx, discovery.Instance{ID: "orders-a", Service: "orders", Endpoint: "10.0.0.1:9000"}); err != nil {
		t.Fatalf("register initial discovery instance: %v", err)
	}
	discoveryProvider := CompositeProvider{Contributors: []SnapshotContributor{DiscoveryContributor{Registry: registry}}}
	baseDiscoverySnapshot, err := discoveryProvider.Load(ctx)
	if err != nil {
		t.Fatalf("load base discovery snapshot: %v", err)
	}
	if _, err := registry.Register(ctx, discovery.Instance{ID: "orders-b", Service: "orders", Endpoint: "10.0.0.2:9000"}); err != nil {
		t.Fatalf("register changed discovery instance: %v", err)
	}
	changedDiscoverySnapshot, err := discoveryProvider.Load(ctx)
	if err != nil {
		t.Fatalf("load changed discovery snapshot: %v", err)
	}
	if diff := DiffSnapshots(baseDiscoverySnapshot, changedDiscoverySnapshot); !diff.Changed || diff.ChangeType != "service-discovery-change" || !containsString(diff.ChangedFields, "services") {
		t.Fatalf("discovery diff = %+v, want service-discovery-change", diff)
	}
}

func TestCompositeProviderWatchAndContributorErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider := CompositeProvider{
		Name:          "runtime-watch",
		WatchInterval: time.Hour,
		Contributors:  []SnapshotContributor{MetadataContributor{Metadata: map[string]string{"llm": "available"}}},
	}
	events, err := provider.Watch(ctx)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	event := <-events
	if event.Source != "runtime-watch" || event.Error != "" || event.Snapshot.Metadata["llm"] != "available" || event.Snapshot.Checksum == "" {
		t.Fatalf("event = %#v, want initial runtime snapshot", event)
	}
	cancel()
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("composite watch channel stayed open after cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("composite watch channel did not close after cancel")
	}

	badProvider := CompositeProvider{Contributors: []SnapshotContributor{ConfigContributor{Configs: map[string]json.RawMessage{"broken": json.RawMessage(`{`)}}}}
	if _, err := badProvider.Load(context.Background()); err == nil {
		t.Fatal("Load invalid config error = nil, want error")
	}
}

func TestDispatchSnapshotConsumerActionInvokesRuntimeHooks(t *testing.T) {
	snapshot := Snapshot{Version: DefaultSnapshotVersion, Metadata: map[string]string{"capability": "available"}}
	var invoked []string
	record := func(name string) SnapshotConsumerHook {
		return func(ctx context.Context, action SnapshotConsumerAction, got Snapshot) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if action.Action == "" || got.Version != DefaultSnapshotVersion {
				t.Fatalf("hook %s action/snapshot = %+v/%+v, want populated action and snapshot", name, action, got)
			}
			invoked = append(invoked, name)
			return nil
		}
	}
	hooks := SnapshotConsumerHooks{
		LoadBaseline:           record("load-baseline"),
		InspectSnapshot:        record("inspect-snapshot"),
		RefreshConfigPlanner:   record("refresh-config-planner"),
		RefreshRoutingModel:    record("refresh-routing-model"),
		ReloadGovernanceGates:  record("reload-governance-gates"),
		RefreshCapabilityCache: record("refresh-capability-cache"),
		FullReconcile:          record("full-reconcile"),
	}
	tests := []struct {
		name          string
		changeType    string
		wantAction    string
		wantHook      string
		wantReconcile bool
	}{
		{name: "initial snapshot loads baseline", changeType: "initial-snapshot", wantAction: "load-baseline", wantHook: "load-baseline", wantReconcile: true},
		{name: "checksum mismatch inspects snapshot", changeType: "checksum-mismatch", wantAction: "inspect-snapshot", wantHook: "inspect-snapshot", wantReconcile: true},
		{name: "config change refreshes planner", changeType: "config-change", wantAction: "refresh-config-planner", wantHook: "refresh-config-planner"},
		{name: "service change refreshes routing", changeType: "service-discovery-change", wantAction: "refresh-routing-model", wantHook: "refresh-routing-model"},
		{name: "policy change reloads governance", changeType: "policy-change", wantAction: "reload-governance-gates", wantHook: "reload-governance-gates"},
		{name: "metadata change refreshes capability cache", changeType: "metadata-change", wantAction: "refresh-capability-cache", wantHook: "refresh-capability-cache"},
		{name: "mixed change reconciles fully", changeType: "mixed-change", wantAction: "full-reconcile", wantHook: "full-reconcile", wantReconcile: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := len(invoked)
			dispatch, err := DispatchSnapshotDiff(context.Background(), SnapshotDiff{ChangeType: tt.changeType}, snapshot, hooks)
			if err != nil {
				t.Fatalf("DispatchSnapshotDiff: %v", err)
			}
			if dispatch.ChangeType != tt.changeType || dispatch.Action != tt.wantAction || dispatch.RequiresFullReconcile != tt.wantReconcile {
				t.Fatalf("dispatch = %+v, want changeType=%q action=%q fullReconcile=%t", dispatch, tt.changeType, tt.wantAction, tt.wantReconcile)
			}
			if len(dispatch.Invoked) != 1 || dispatch.Invoked[0] != tt.wantHook || invoked[len(invoked)-1] != tt.wantHook || len(invoked) != before+1 {
				t.Fatalf("dispatch/invoked = %+v/%+v, want hook %q", dispatch, invoked, tt.wantHook)
			}
		})
	}
}

func TestDispatchSnapshotConsumerActionSkipErrorsAndContext(t *testing.T) {
	snapshot := Snapshot{Version: DefaultSnapshotVersion}
	skip, err := DispatchSnapshotConsumerAction(context.Background(), ConsumerActionForDiff(SnapshotDiff{ChangeType: "none"}), snapshot, SnapshotConsumerHooks{})
	if err != nil {
		t.Fatalf("skip DispatchSnapshotConsumerAction: %v", err)
	}
	if !skip.Skipped || len(skip.Invoked) != 0 || skip.Action != "skip" {
		t.Fatalf("skip dispatch = %+v, want skipped without hooks", skip)
	}

	if _, err := DispatchSnapshotConsumerAction(context.Background(), ConsumerActionForDiff(SnapshotDiff{ChangeType: "policy-change"}), snapshot, SnapshotConsumerHooks{}); err == nil {
		t.Fatal("missing policy hook error = nil, want error")
	}
	if _, err := DispatchSnapshotConsumerAction(context.Background(), SnapshotConsumerAction{ChangeType: "custom", Action: "custom-action"}, snapshot, SnapshotConsumerHooks{}); err == nil {
		t.Fatal("unsupported custom action error = nil, want error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := DispatchSnapshotConsumerAction(ctx, ConsumerActionForDiff(SnapshotDiff{ChangeType: "metadata-change"}), snapshot, SnapshotConsumerHooks{RefreshCapabilityCache: func(context.Context, SnapshotConsumerAction, Snapshot) error { return nil }}); err == nil {
		t.Fatal("canceled context dispatch error = nil, want error")
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestDeduplicateSnapshotEvents(t *testing.T) {
	ctx := context.Background()
	in := make(chan SnapshotEvent, 4)
	in <- SnapshotEvent{Snapshot: Snapshot{Version: "v1"}, Source: "test"}
	in <- SnapshotEvent{Snapshot: Snapshot{Version: "v1"}, Source: "test"}
	in <- SnapshotEvent{Error: "backend unavailable", Source: "test"}
	in <- SnapshotEvent{Snapshot: Snapshot{Version: "v2"}, Source: "test"}
	close(in)

	var got []SnapshotEvent
	for event := range DeduplicateSnapshotEvents(ctx, in) {
		got = append(got, event)
	}
	if len(got) != 3 {
		t.Fatalf("deduplicated events = %#v, want first snapshot, error, changed snapshot", got)
	}
	if got[0].Snapshot.Version != "v1" || got[1].Error == "" || got[2].Snapshot.Version != "v2" {
		t.Fatalf("events = %#v, want v1/error/v2", got)
	}
}

func TestDeduplicateSnapshotEventsNilInputCloses(t *testing.T) {
	events := DeduplicateSnapshotEvents(context.Background(), nil)
	select {
	case event, ok := <-events:
		if ok {
			t.Fatalf("nil input event = %#v, want closed channel", event)
		}
	case <-time.After(time.Second):
		t.Fatal("nil input deduplicator did not close")
	}
}
