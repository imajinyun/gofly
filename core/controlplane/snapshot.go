// Package controlplane defines versioned snapshots for service discovery,
// configuration and governance policy distribution.
package controlplane

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/gofly/gofly/core/governance"
)

type Snapshot struct {
	Version   string                     `json:"version,omitempty"`
	Checksum  string                     `json:"checksum,omitempty"`
	Services  []ServiceSnapshot          `json:"services,omitempty"`
	Configs   map[string]json.RawMessage `json:"configs,omitempty"`
	Policies  []governance.Rule          `json:"policies,omitempty"`
	UpdatedAt time.Time                  `json:"updatedAt,omitempty"`
	Metadata  map[string]string          `json:"metadata,omitempty"`
}

type ServiceSnapshot struct {
	Name      string             `json:"name"`
	Endpoints []EndpointSnapshot `json:"endpoints,omitempty"`
	Metadata  map[string]string  `json:"metadata,omitempty"`
}

type EndpointSnapshot struct {
	Address  string            `json:"address"`
	Weight   int               `json:"weight,omitempty"`
	Zone     string            `json:"zone,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type SnapshotDiff struct {
	FromChecksum  string   `json:"fromChecksum,omitempty"`
	ToChecksum    string   `json:"toChecksum,omitempty"`
	Changed       bool     `json:"changed"`
	ChangeType    string   `json:"changeType"`
	ChangedFields []string `json:"changedFields,omitempty"`
}

type SnapshotConsumerAction struct {
	ChangeType            string   `json:"changeType"`
	Action                string   `json:"action"`
	Reason                string   `json:"reason"`
	Scopes                []string `json:"scopes,omitempty"`
	RequiresFullReconcile bool     `json:"requiresFullReconcile"`
	NextActions           []string `json:"nextActions,omitempty"`
}

func (s Snapshot) WithChecksum() Snapshot {
	s.Checksum = s.StableChecksum()
	return s
}

func DecodeSnapshotJSON(data []byte) (Snapshot, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return Snapshot{}, fmt.Errorf("controlplane snapshot JSON is empty")
	}
	var envelope struct {
		Snapshot Snapshot `json:"snapshot"`
		Data     struct {
			Snapshot Snapshot `json:"snapshot"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return Snapshot{}, fmt.Errorf("decode controlplane snapshot JSON: %w", err)
	}
	if hasSnapshotContent(envelope.Data.Snapshot) {
		return envelope.Data.Snapshot.WithChecksum(), nil
	}
	if hasSnapshotContent(envelope.Snapshot) {
		return envelope.Snapshot.WithChecksum(), nil
	}
	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("decode controlplane raw snapshot JSON: %w", err)
	}
	if !hasSnapshotContent(snapshot) {
		return Snapshot{}, fmt.Errorf("controlplane snapshot JSON does not contain a snapshot object")
	}
	return snapshot.WithChecksum(), nil
}

func (s Snapshot) StableChecksum() string {
	canonical := canonicalSnapshot{
		Version:  s.Version,
		Services: cloneServiceSnapshots(s.Services),
		Configs:  cloneRawMessages(s.Configs),
		Policies: cloneGovernanceRules(s.Policies),
		Metadata: cloneStringMap(s.Metadata),
	}
	sortServiceSnapshots(canonical.Services)
	sortGovernanceRules(canonical.Policies)
	data, err := json.Marshal(canonical)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func DiffSnapshots(from, to Snapshot) SnapshotDiff {
	from = from.WithChecksum()
	to = to.WithChecksum()
	diff := SnapshotDiff{
		FromChecksum: from.Checksum,
		ToChecksum:   to.Checksum,
		Changed:      from.Checksum != to.Checksum,
		ChangeType:   "none",
	}
	if !diff.Changed {
		return diff
	}
	fromCanonical := from.canonical()
	toCanonical := to.canonical()
	if fromCanonical.Version != toCanonical.Version {
		diff.ChangedFields = append(diff.ChangedFields, "version")
	}
	if !reflect.DeepEqual(fromCanonical.Services, toCanonical.Services) {
		diff.ChangedFields = append(diff.ChangedFields, "services")
	}
	if !reflect.DeepEqual(fromCanonical.Configs, toCanonical.Configs) {
		diff.ChangedFields = append(diff.ChangedFields, "configs")
	}
	if !reflect.DeepEqual(fromCanonical.Policies, toCanonical.Policies) {
		diff.ChangedFields = append(diff.ChangedFields, "policies")
	}
	if !reflect.DeepEqual(fromCanonical.Metadata, toCanonical.Metadata) {
		diff.ChangedFields = append(diff.ChangedFields, "metadata")
	}
	diff.ChangeType = classifySnapshotChange(diff.ChangedFields)
	return diff
}

func DiffSnapshotChecksum(fromChecksum string, to Snapshot) SnapshotDiff {
	to = to.WithChecksum()
	diff := SnapshotDiff{
		FromChecksum: fromChecksum,
		ToChecksum:   to.Checksum,
		Changed:      fromChecksum != "" && fromChecksum != to.Checksum,
		ChangeType:   "none",
	}
	if fromChecksum == "" {
		diff.Changed = true
		diff.ChangeType = "initial-snapshot"
		return diff
	}
	if diff.Changed {
		diff.ChangeType = "checksum-mismatch"
	}
	return diff
}

func ConsumerActionForDiff(diff SnapshotDiff) SnapshotConsumerAction {
	switch diff.ChangeType {
	case "none":
		return SnapshotConsumerAction{
			ChangeType: "none",
			Action:     "skip",
			Reason:     "snapshot checksum is unchanged; repeated governance or routing work can be skipped",
			Scopes:     []string{"cache"},
			NextActions: []string{
				"reuse previously computed plan when the caller trust boundary is unchanged",
				"do not re-run expensive reconciliation unless forced by the user",
			},
		}
	case "initial-snapshot":
		return SnapshotConsumerAction{
			ChangeType:            "initial-snapshot",
			Action:                "load-baseline",
			Reason:                "no previous checksum was supplied; consumers should establish a baseline before mutating state",
			Scopes:                []string{"config", "service-discovery", "policy", "metadata"},
			RequiresFullReconcile: true,
			NextActions: []string{
				"persist snapshot checksum before applying generated project changes",
				"run full control-plane reconciliation if this is the first observation in a session",
			},
		}
	case "checksum-mismatch":
		return SnapshotConsumerAction{
			ChangeType:            "checksum-mismatch",
			Action:                "inspect-snapshot",
			Reason:                "only a previous checksum is available, so changed fields cannot be inferred safely",
			Scopes:                []string{"unknown"},
			RequiresFullReconcile: true,
			NextActions: []string{
				"load the previous snapshot if available to compute semantic changedFields",
				"fall back to full reconciliation when previous snapshot data is unavailable",
			},
		}
	case "config-change":
		return SnapshotConsumerAction{
			ChangeType: "config-change",
			Action:     "refresh-config-planner",
			Reason:     "configuration inputs changed without requiring route-only or policy-only invalidation",
			Scopes:     []string{"config"},
			NextActions: []string{
				"reload configuration-derived defaults before planning or applying scaffold changes",
				"preserve existing routing and policy decisions unless their checksum also changed",
			},
		}
	case "service-discovery-change":
		return SnapshotConsumerAction{
			ChangeType: "service-discovery-change",
			Action:     "refresh-routing-model",
			Reason:     "service endpoints changed; consumers should refresh endpoint and gateway reasoning",
			Scopes:     []string{"service-discovery", "routing"},
			NextActions: []string{
				"re-evaluate generated gateway routes and service client endpoint assumptions",
				"avoid rewriting unrelated config or policy files",
			},
		}
	case "policy-change":
		return SnapshotConsumerAction{
			ChangeType: "policy-change",
			Action:     "reload-governance-gates",
			Reason:     "governance policy changed; risk gates must be reloaded before further mutations",
			Scopes:     []string{"policy", "governance"},
			NextActions: []string{
				"reload governance policy and risk-level decisions",
				"re-check pending mutations against the updated policy before applying",
			},
		}
	case "metadata-change":
		return SnapshotConsumerAction{
			ChangeType: "metadata-change",
			Action:     "refresh-capability-cache",
			Reason:     "capability metadata changed; consumers should refresh cached availability hints",
			Scopes:     []string{"metadata", "capabilities"},
			NextActions: []string{
				"refresh capability and availability hints used by agents",
				"avoid full reconciliation unless metadata controls a requested operation",
			},
		}
	case "version-change", "mixed-change", "checksum-change":
		return SnapshotConsumerAction{
			ChangeType:            diff.ChangeType,
			Action:                "full-reconcile",
			Reason:                "multiple or structural control-plane dimensions changed; narrow invalidation is unsafe",
			Scopes:                []string{"config", "service-discovery", "policy", "metadata"},
			RequiresFullReconcile: true,
			NextActions: []string{
				"reload the full snapshot before mutating generated project state",
				"compare the new checksum after reconciliation to avoid repeated work",
			},
		}
	default:
		return SnapshotConsumerAction{
			ChangeType:            diff.ChangeType,
			Action:                "full-reconcile",
			Reason:                "unknown change type; safest consumer behavior is full reconciliation",
			Scopes:                []string{"unknown"},
			RequiresFullReconcile: true,
			NextActions: []string{
				"treat unknown change types as full-reconcile until the manifest is upgraded",
			},
		}
	}
}

func DefaultConsumerActions() []SnapshotConsumerAction {
	changeTypes := []string{"none", "initial-snapshot", "checksum-mismatch", "config-change", "service-discovery-change", "policy-change", "metadata-change", "version-change", "mixed-change", "checksum-change"}
	actions := make([]SnapshotConsumerAction, 0, len(changeTypes))
	for _, changeType := range changeTypes {
		actions = append(actions, ConsumerActionForDiff(SnapshotDiff{ChangeType: changeType}))
	}
	return actions
}

func (s Snapshot) canonical() canonicalSnapshot {
	canonical := canonicalSnapshot{
		Version:  s.Version,
		Services: cloneServiceSnapshots(s.Services),
		Configs:  cloneRawMessages(s.Configs),
		Policies: cloneGovernanceRules(s.Policies),
		Metadata: cloneStringMap(s.Metadata),
	}
	sortServiceSnapshots(canonical.Services)
	sortGovernanceRules(canonical.Policies)
	return canonical
}

func hasSnapshotContent(snapshot Snapshot) bool {
	return snapshot.Version != "" || snapshot.Checksum != "" || len(snapshot.Services) > 0 || len(snapshot.Configs) > 0 || len(snapshot.Policies) > 0 || len(snapshot.Metadata) > 0 || !snapshot.UpdatedAt.IsZero()
}

func classifySnapshotChange(fields []string) string {
	if len(fields) == 0 {
		return "checksum-change"
	}
	if len(fields) > 1 {
		return "mixed-change"
	}
	switch fields[0] {
	case "version":
		return "version-change"
	case "services":
		return "service-discovery-change"
	case "configs":
		return "config-change"
	case "policies":
		return "policy-change"
	case "metadata":
		return "metadata-change"
	default:
		return "checksum-change"
	}
}

type canonicalSnapshot struct {
	Version  string                     `json:"version,omitempty"`
	Services []ServiceSnapshot          `json:"services,omitempty"`
	Configs  map[string]json.RawMessage `json:"configs,omitempty"`
	Policies []governance.Rule          `json:"policies,omitempty"`
	Metadata map[string]string          `json:"metadata,omitempty"`
}

func cloneServiceSnapshots(in []ServiceSnapshot) []ServiceSnapshot {
	if len(in) == 0 {
		return nil
	}
	out := make([]ServiceSnapshot, len(in))
	for i, service := range in {
		out[i] = ServiceSnapshot{
			Name:      service.Name,
			Endpoints: cloneEndpointSnapshots(service.Endpoints),
			Metadata:  cloneStringMap(service.Metadata),
		}
	}
	return out
}

func cloneEndpointSnapshots(in []EndpointSnapshot) []EndpointSnapshot {
	if len(in) == 0 {
		return nil
	}
	out := make([]EndpointSnapshot, len(in))
	for i, endpoint := range in {
		out[i] = EndpointSnapshot{
			Address:  endpoint.Address,
			Weight:   endpoint.Weight,
			Zone:     endpoint.Zone,
			Metadata: cloneStringMap(endpoint.Metadata),
		}
	}
	return out
}

func cloneRawMessages(in map[string]json.RawMessage) map[string]json.RawMessage {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage, len(in))
	for key, value := range in {
		out[key] = append(json.RawMessage(nil), value...)
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
