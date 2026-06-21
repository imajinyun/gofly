package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gofly/gofly/core/discovery"
	"github.com/gofly/gofly/core/governance"
)

const DefaultSnapshotVersion = "gofly-control-plane.v1"

type Provider interface {
	Load(context.Context) (Snapshot, error)
	Watch(context.Context) (<-chan SnapshotEvent, error)
}

type ProviderSource interface {
	Source() string
}

type SnapshotContributor interface {
	ContributeSnapshot(context.Context, *Snapshot) error
}

type SnapshotContributorFunc func(context.Context, *Snapshot) error

func (f SnapshotContributorFunc) ContributeSnapshot(ctx context.Context, snapshot *Snapshot) error {
	if f == nil {
		return nil
	}
	return f(ctx, snapshot)
}

type CompositeProvider struct {
	Version       string
	Name          string
	Contributors  []SnapshotContributor
	WatchInterval time.Duration
}

func (p CompositeProvider) Source() string {
	if strings.TrimSpace(p.Name) != "" {
		return p.Name
	}
	return "runtime"
}

func (p CompositeProvider) Load(ctx context.Context) (Snapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	version := strings.TrimSpace(p.Version)
	if version == "" {
		version = DefaultSnapshotVersion
	}
	snapshot := Snapshot{Version: version, UpdatedAt: time.Now().UTC()}
	for _, contributor := range p.Contributors {
		if contributor == nil {
			continue
		}
		if err := contributor.ContributeSnapshot(ctx, &snapshot); err != nil {
			return Snapshot{}, err
		}
	}
	return snapshot.WithChecksum(), nil
}

func (p CompositeProvider) Watch(ctx context.Context) (<-chan SnapshotEvent, error) {
	if ctx == nil {
		return nil, errors.New("controlplane watch context is nil")
	}
	interval := p.WatchInterval
	if interval <= 0 {
		interval = time.Second
	}
	out := make(chan SnapshotEvent, 1)
	go func() {
		defer close(out)
		emit := func() bool {
			snapshot, err := p.Load(ctx)
			event := SnapshotEvent{Snapshot: snapshot, Source: p.Source()}
			if err != nil {
				event = SnapshotEvent{Source: p.Source(), Error: err.Error()}
			}
			select {
			case out <- event:
				return true
			case <-ctx.Done():
				return false
			}
		}
		if !emit() {
			return
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !emit() {
					return
				}
			}
		}
	}()
	return out, nil
}

type MetadataContributor struct {
	Metadata map[string]string
}

func (c MetadataContributor) ContributeSnapshot(_ context.Context, snapshot *Snapshot) error {
	if snapshot == nil || len(c.Metadata) == 0 {
		return nil
	}
	if snapshot.Metadata == nil {
		snapshot.Metadata = make(map[string]string, len(c.Metadata))
	}
	for key, value := range c.Metadata {
		if strings.TrimSpace(key) == "" {
			continue
		}
		snapshot.Metadata[key] = value
	}
	return nil
}

type ConfigContributor struct {
	Configs map[string]json.RawMessage
}

func (c ConfigContributor) ContributeSnapshot(_ context.Context, snapshot *Snapshot) error {
	if snapshot == nil || len(c.Configs) == 0 {
		return nil
	}
	if snapshot.Configs == nil {
		snapshot.Configs = make(map[string]json.RawMessage, len(c.Configs))
	}
	for key, value := range c.Configs {
		if strings.TrimSpace(key) == "" {
			continue
		}
		if !json.Valid(value) {
			return fmt.Errorf("controlplane config %q is not valid JSON", key)
		}
		snapshot.Configs[key] = append(json.RawMessage(nil), value...)
	}
	return nil
}

type GovernanceRuleSource interface {
	Snapshot() []governance.Rule
}

type GovernanceContributor struct {
	Rules GovernanceRuleSource
}

func (c GovernanceContributor) ContributeSnapshot(_ context.Context, snapshot *Snapshot) error {
	if snapshot == nil || c.Rules == nil {
		return nil
	}
	rules := cloneGovernanceRules(c.Rules.Snapshot())
	if len(rules) == 0 {
		return nil
	}
	snapshot.Policies = append(snapshot.Policies, rules...)
	sortGovernanceRules(snapshot.Policies)
	return nil
}

type DiscoverySnapshotSource interface {
	Snapshot() map[string][]discovery.Instance
}

type DiscoveryContributor struct {
	Registry DiscoverySnapshotSource
}

func (c DiscoveryContributor) ContributeSnapshot(_ context.Context, snapshot *Snapshot) error {
	if snapshot == nil || c.Registry == nil {
		return nil
	}
	services := serviceSnapshotsFromDiscovery(c.Registry.Snapshot())
	if len(services) == 0 {
		return nil
	}
	snapshot.Services = append(snapshot.Services, services...)
	sortServiceSnapshots(snapshot.Services)
	return nil
}

type SnapshotConsumerHook func(context.Context, SnapshotConsumerAction, Snapshot) error

type SnapshotConsumerHooks struct {
	LoadBaseline           SnapshotConsumerHook
	InspectSnapshot        SnapshotConsumerHook
	RefreshConfigPlanner   SnapshotConsumerHook
	RefreshRoutingModel    SnapshotConsumerHook
	ReloadGovernanceGates  SnapshotConsumerHook
	RefreshCapabilityCache SnapshotConsumerHook
	FullReconcile          SnapshotConsumerHook
}

type SnapshotConsumerDispatch struct {
	ChangeType            string   `json:"changeType"`
	Action                string   `json:"action"`
	Invoked               []string `json:"invoked,omitempty"`
	Skipped               bool     `json:"skipped"`
	RequiresFullReconcile bool     `json:"requiresFullReconcile"`
}

func DispatchSnapshotConsumerAction(ctx context.Context, action SnapshotConsumerAction, snapshot Snapshot, hooks SnapshotConsumerHooks) (SnapshotConsumerDispatch, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return SnapshotConsumerDispatch{}, err
	}
	result := SnapshotConsumerDispatch{
		ChangeType:            action.ChangeType,
		Action:                action.Action,
		RequiresFullReconcile: action.RequiresFullReconcile,
	}
	switch action.Action {
	case "skip":
		result.Skipped = true
		return result, nil
	case "load-baseline":
		return dispatchSnapshotConsumerHook(ctx, result, "load-baseline", hooks.LoadBaseline, action, snapshot)
	case "inspect-snapshot":
		return dispatchSnapshotConsumerHook(ctx, result, "inspect-snapshot", hooks.InspectSnapshot, action, snapshot)
	case "refresh-config-planner":
		return dispatchSnapshotConsumerHook(ctx, result, "refresh-config-planner", hooks.RefreshConfigPlanner, action, snapshot)
	case "refresh-routing-model":
		return dispatchSnapshotConsumerHook(ctx, result, "refresh-routing-model", hooks.RefreshRoutingModel, action, snapshot)
	case "reload-governance-gates":
		return dispatchSnapshotConsumerHook(ctx, result, "reload-governance-gates", hooks.ReloadGovernanceGates, action, snapshot)
	case "refresh-capability-cache":
		return dispatchSnapshotConsumerHook(ctx, result, "refresh-capability-cache", hooks.RefreshCapabilityCache, action, snapshot)
	case "full-reconcile":
		return dispatchSnapshotConsumerHook(ctx, result, "full-reconcile", hooks.FullReconcile, action, snapshot)
	default:
		return result, fmt.Errorf("controlplane consumer action %q is not supported", action.Action)
	}
}

func DispatchSnapshotDiff(ctx context.Context, diff SnapshotDiff, snapshot Snapshot, hooks SnapshotConsumerHooks) (SnapshotConsumerDispatch, error) {
	return DispatchSnapshotConsumerAction(ctx, ConsumerActionForDiff(diff), snapshot, hooks)
}

func dispatchSnapshotConsumerHook(ctx context.Context, result SnapshotConsumerDispatch, name string, hook SnapshotConsumerHook, action SnapshotConsumerAction, snapshot Snapshot) (SnapshotConsumerDispatch, error) {
	if hook == nil {
		return result, fmt.Errorf("controlplane consumer action %q requires a %s hook", result.Action, name)
	}
	if err := hook(ctx, action, snapshot); err != nil {
		return result, err
	}
	result.Invoked = append(result.Invoked, name)
	return result, nil
}

type SnapshotEvent struct {
	Snapshot Snapshot `json:"snapshot,omitempty"`
	Source   string   `json:"source,omitempty"`
	Error    string   `json:"error,omitempty"`
}

type StaticProvider struct {
	Snapshot Snapshot
	Name     string
}

func (p StaticProvider) Source() string {
	if strings.TrimSpace(p.Name) != "" {
		return p.Name
	}
	return "static"
}

func (p StaticProvider) Load(ctx context.Context) (Snapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	return p.Snapshot.WithChecksum(), nil
}

func (p StaticProvider) Watch(ctx context.Context) (<-chan SnapshotEvent, error) {
	if ctx == nil {
		return nil, errors.New("controlplane watch context is nil")
	}
	out := make(chan SnapshotEvent, 1)
	go func() {
		defer close(out)
		snapshot := p.Snapshot.WithChecksum()
		select {
		case out <- SnapshotEvent{Snapshot: snapshot, Source: p.Source()}:
		case <-ctx.Done():
			return
		}
		<-ctx.Done()
	}()
	return out, nil
}

func DeduplicateSnapshotEvents(ctx context.Context, in <-chan SnapshotEvent) <-chan SnapshotEvent {
	if ctx == nil {
		ctx = context.Background()
	}
	out := make(chan SnapshotEvent)
	go func() {
		defer close(out)
		lastChecksum := ""
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-in:
				if !ok {
					return
				}
				if event.Error != "" {
					select {
					case out <- event:
					case <-ctx.Done():
					}
					continue
				}
				event.Snapshot = event.Snapshot.WithChecksum()
				if event.Snapshot.Checksum == lastChecksum {
					continue
				}
				lastChecksum = event.Snapshot.Checksum
				select {
				case out <- event:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

func serviceSnapshotsFromDiscovery(in map[string][]discovery.Instance) []ServiceSnapshot {
	if len(in) == 0 {
		return nil
	}
	services := make([]ServiceSnapshot, 0, len(in))
	for serviceName, instances := range in {
		serviceName = strings.TrimSpace(serviceName)
		if serviceName == "" || len(instances) == 0 {
			continue
		}
		service := ServiceSnapshot{Name: serviceName}
		for _, instance := range instances {
			if strings.TrimSpace(instance.Endpoint) == "" {
				continue
			}
			metadata := discoveryEndpointMetadata(instance)
			service.Endpoints = append(service.Endpoints, EndpointSnapshot{
				Address:  instance.Endpoint,
				Weight:   instance.Weight,
				Zone:     instance.Zone,
				Metadata: metadata,
			})
		}
		if len(service.Endpoints) == 0 {
			continue
		}
		sortEndpointSnapshots(service.Endpoints)
		service.Metadata = map[string]string{"source": "discovery"}
		services = append(services, service)
	}
	sortServiceSnapshots(services)
	return services
}

func discoveryEndpointMetadata(instance discovery.Instance) map[string]string {
	metadata := make(map[string]string)
	if instance.ID != "" {
		metadata["id"] = instance.ID
	}
	if instance.Service != "" {
		metadata["service"] = instance.Service
	}
	if instance.Status != "" {
		metadata["status"] = instance.Status
	}
	if instance.Version != "" {
		metadata["version"] = instance.Version
	}
	for key, value := range instance.Tags {
		if strings.TrimSpace(key) != "" {
			metadata["tag."+key] = value
		}
	}
	for key, value := range instance.Metadata {
		if strings.TrimSpace(key) != "" {
			metadata["meta."+key] = value
		}
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func sortServiceSnapshots(services []ServiceSnapshot) {
	sort.Slice(services, func(i, j int) bool {
		return services[i].Name < services[j].Name
	})
	for i := range services {
		sortEndpointSnapshots(services[i].Endpoints)
	}
}

func sortEndpointSnapshots(endpoints []EndpointSnapshot) {
	sort.Slice(endpoints, func(i, j int) bool {
		if endpoints[i].Address != endpoints[j].Address {
			return endpoints[i].Address < endpoints[j].Address
		}
		return endpoints[i].Zone < endpoints[j].Zone
	})
}

func cloneGovernanceRules(in []governance.Rule) []governance.Rule {
	if len(in) == 0 {
		return nil
	}
	data, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	out := make([]governance.Rule, 0, len(in))
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	sortGovernanceRules(out)
	return out
}

func sortGovernanceRules(rules []governance.Rule) {
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].Priority != rules[j].Priority {
			return rules[i].Priority > rules[j].Priority
		}
		if rules[i].Name != rules[j].Name {
			return rules[i].Name < rules[j].Name
		}
		if rules[i].Service != rules[j].Service {
			return rules[i].Service < rules[j].Service
		}
		return rules[i].Method < rules[j].Method
	})
}
