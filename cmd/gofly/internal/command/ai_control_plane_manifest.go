package command

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/imajinyun/gofly/core/controlplane"
)

func buildAIControlPlaneManifest() aiControlPlaneManifest {
	snapshot := defaultAIControlPlaneSnapshot().WithChecksum()
	return aiControlPlaneManifest{
		Package:          "github.com/imajinyun/gofly/core/controlplane",
		Purpose:          "versioned control-plane snapshots for AI agents to reason about runtime config, service discovery, governance policy, gateway routing, LLM and tool capabilities before acting",
		SnapshotVersion:  snapshot.Version,
		SnapshotChecksum: snapshot.Checksum,
		SchemaID:         aiControlPlaneSchemaID,
		SchemaCommand:    "gofly ai control-plane --schema jsonschema",
		SchemaChecksum:   aiControlPlaneJSONSchemaChecksum(),
		ProviderContract: []string{"Load(context.Context) (Snapshot, error)", "Watch(context.Context) (<-chan SnapshotEvent, error)", "Source() string when implemented"},
		SnapshotFields:   []string{"version", "checksum", "services", "configs", "policies", "updatedAt", "metadata"},
		EventFields:      []string{"snapshot", "source", "diff", "consumerAction", "error"},
		Capabilities: []string{
			"stable checksum independent of service ordering and updatedAt",
			"previous snapshot JSON decoding from raw snapshot, snapshot wrapper or ai control-plane envelope",
			"static provider for deterministic tests and local tools",
			"composite runtime provider for config, service discovery, governance policy and capability contributors",
			"runtime adapters for discovery snapshots, governance rule sets, raw JSON configs and capability metadata",
			"rpc policy runtime enforcement for client timeout, retry backoff with context cancellation, circuit breaker gates, balancer selection, load shedding, fallback and hedging",
			"control-plane contributor for rpc policy runtime state, cache counts and enforcement capabilities",
			"native REST admin control-plane endpoint with pluggable runtime contributors and sanitized REST runtime snapshots",
			"control-plane contributor for REST governance runtime cache counts across rate limiters, concurrency limiters and breakers",
			"generated project control-plane contributors for scaffold contract, sanitized runtime config and governance policy snapshots",
			"ai new --apply --verify runs generated project control-plane snapshot assertions when the scaffold exposes a snapshot contract test",
			"watch stream with context cancellation",
			"deduplicated snapshot events by checksum while preserving error events",
			"semantic diff classification mapped to stable consumer action policy",
			"consumer action dispatcher for runtime config planner, routing model, governance gates and capability cache refresh hooks",
		},
		ConsumerActions: controlplane.DefaultConsumerActions(),
		Determinism:     "StableChecksum canonicalizes services/endpoints/configs/metadata and excludes updatedAt so agents can detect semantic changes instead of timestamp churn",
		SecretBoundary:  "snapshots expose config metadata and raw JSON config blobs only from explicit providers; secret values must stay in environment-only resolvers and must not be copied into metadata",
		AgentGuidance: []string{
			"load one snapshot before mutating generated project configuration",
			"for generated projects, compare generated.* config blobs with scaffold artifacts and governance rules before rewriting code or policy files",
			"compare checksum before applying repeated governance or routing actions",
			"use consumerAction.action and consumerAction.scopes to narrow cache invalidation or choose full reconciliation",
			"treat SnapshotEvent.error as non-cacheable and actionable even when checksum is unchanged",
			"do not infer secret values from config metadata or provider names",
		},
		DefaultMetadata: snapshot.Metadata,
	}
}

func defaultAIControlPlaneSnapshot() controlplane.Snapshot {
	return controlplane.Snapshot{
		Version: controlplane.DefaultSnapshotVersion,
		Metadata: map[string]string{
			"config":                                "available",
			"controlplane.provider.composite":       "available",
			"discovery":                             "available",
			"governance":                            "available",
			"gateway":                               "planned",
			"rest.runtime":                          "available",
			"rest.governance.runtime":               "available",
			"llm":                                   "available",
			"tool":                                  "available",
			"generated.project.contract":            "available",
			"generated.project.verify.controlplane": "available",
		},
	}
}

func aiControlPlaneOutputContract() *aiOutputContract {
	return &aiOutputContract{
		Mode:     "single JSON envelope when --json, --output json or --format json is used; newline-delimited JSON envelopes when --watch is used with JSON output; deterministic text snapshot otherwise",
		Envelope: []string{"ok", "command", "version", "data", "error", "diagnostics", "warnings", "nextActions"},
		EventFields: []string{
			"source", "snapshot", "diff", "consumerAction", "agentGuidance", "secretBoundary", "index", "error",
			"snapshot.version", "snapshot.checksum", "snapshot.services", "snapshot.configs", "snapshot.policies", "snapshot.metadata",
			"diff.fromChecksum", "diff.toChecksum", "diff.changed", "diff.changeType", "diff.changedFields",
			"consumerAction.changeType", "consumerAction.action", "consumerAction.reason", "consumerAction.scopes", "consumerAction.requiresFullReconcile", "consumerAction.nextActions",
		},
		Semantics: map[string]string{
			"command":        "ai.control_plane",
			"watchCommand":   "ai.control_plane.event",
			"schema":         "--schema jsonschema emits the JSON Schema contract for snapshot, diff, consumerAction and watch event data",
			"watch":          "--watch emits a bounded event stream terminated by --max-events or --timeout; each JSON line is independently parseable",
			"diff":           "diff reports checksum equality for --from-checksum and semantic changedFields when both snapshots are available via --from-snapshot",
			"baseline":       "--from-snapshot accepts a raw Snapshot JSON object, a {snapshot:...} wrapper or a previous ai.control_plane envelope with data.snapshot; --from-checksum and --from-snapshot are mutually exclusive",
			"consumerAction": "consumerAction maps diff.changeType to a stable agent policy such as skip, load-baseline, refresh-routing-model, reload-governance-gates or full-reconcile",
			"determinism":    "snapshot checksum is stable across ordering and timestamp changes and excludes secret values",
			"secrets":        "control-plane output exposes capability metadata and secret boundaries only; secret values are never printed",
		},
	}
}

func buildAIControlPlaneJSONSchema() map[string]any {
	schema := buildAIControlPlaneJSONSchemaData()
	schema["xSchemaChecksum"] = stableJSONChecksum(schema)
	return schema
}

func aiControlPlaneJSONSchemaChecksum() string {
	return stableJSONChecksum(buildAIControlPlaneJSONSchemaData())
}

func stableJSONChecksum(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}

func buildAIControlPlaneJSONSchemaData() map[string]any {
	stringArraySchema := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	stringMapSchema := map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}
	rawConfigMapSchema := map[string]any{"type": "object", "additionalProperties": true}
	endpointSchema := map[string]any{
		"type":                 "object",
		"required":             []string{"address"},
		"additionalProperties": false,
		"properties": map[string]any{
			"address":  map[string]any{"type": "string"},
			"weight":   map[string]any{"type": "integer"},
			"zone":     map[string]any{"type": "string"},
			"metadata": stringMapSchema,
		},
	}
	serviceSchema := map[string]any{
		"type":                 "object",
		"required":             []string{"name"},
		"additionalProperties": false,
		"properties": map[string]any{
			"name":      map[string]any{"type": "string"},
			"endpoints": map[string]any{"type": "array", "items": endpointSchema},
			"metadata":  stringMapSchema,
		},
	}
	policySchema := map[string]any{"type": "object", "additionalProperties": true}
	snapshotSchema := map[string]any{
		"type":                 "object",
		"required":             []string{"version", "checksum"},
		"additionalProperties": false,
		"properties": map[string]any{
			"version":   map[string]any{"type": "string"},
			"checksum":  map[string]any{"type": "string"},
			"services":  map[string]any{"type": "array", "items": serviceSchema},
			"configs":   rawConfigMapSchema,
			"policies":  map[string]any{"type": "array", "items": policySchema},
			"updatedAt": map[string]any{"type": "string", "format": "date-time"},
			"metadata":  stringMapSchema,
		},
	}
	diffSchema := map[string]any{
		"type":                 "object",
		"required":             []string{"changed", "changeType"},
		"additionalProperties": false,
		"properties": map[string]any{
			"fromChecksum":  map[string]any{"type": "string"},
			"toChecksum":    map[string]any{"type": "string"},
			"changed":       map[string]any{"type": "boolean"},
			"changeType":    map[string]any{"type": "string", "enum": []string{"none", "initial-snapshot", "checksum-mismatch", "version-change", "service-discovery-change", "config-change", "policy-change", "metadata-change", "mixed-change", "checksum-change"}},
			"changedFields": stringArraySchema,
		},
	}
	consumerActionSchema := map[string]any{
		"type":                 "object",
		"required":             []string{"changeType", "action", "reason", "requiresFullReconcile"},
		"additionalProperties": false,
		"properties": map[string]any{
			"changeType":            map[string]any{"type": "string"},
			"action":                map[string]any{"type": "string", "enum": []string{"skip", "load-baseline", "inspect-snapshot", "refresh-config-planner", "refresh-routing-model", "reload-governance-gates", "refresh-capability-cache", "full-reconcile"}},
			"reason":                map[string]any{"type": "string"},
			"scopes":                stringArraySchema,
			"requiresFullReconcile": map[string]any{"type": "boolean"},
			"nextActions":           stringArraySchema,
		},
	}
	snapshotResultSchema := map[string]any{
		"type":                 "object",
		"required":             []string{"source", "snapshot", "diff", "consumerAction", "agentGuidance", "secretBoundary"},
		"additionalProperties": false,
		"properties": map[string]any{
			"source":         map[string]any{"type": "string"},
			"snapshot":       snapshotSchema,
			"diff":           diffSchema,
			"consumerAction": consumerActionSchema,
			"agentGuidance":  stringArraySchema,
			"secretBoundary": map[string]any{"type": "string"},
		},
	}
	watchEventSchema := map[string]any{
		"type":                 "object",
		"required":             []string{"index", "diff", "consumerAction"},
		"additionalProperties": false,
		"properties": map[string]any{
			"index":          map[string]any{"type": "integer", "minimum": 0},
			"source":         map[string]any{"type": "string"},
			"snapshot":       snapshotSchema,
			"diff":           diffSchema,
			"consumerAction": consumerActionSchema,
			"error":          map[string]any{"type": "string"},
			"secretBoundary": map[string]any{"type": "string"},
		},
	}
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  aiControlPlaneSchemaID,
		"title":                "gofly AI control-plane contract",
		"type":                 "object",
		"required":             []string{"snapshot", "diff", "consumerAction", "snapshotResult", "watchEvent"},
		"additionalProperties": false,
		"properties": map[string]any{
			"snapshot":       snapshotSchema,
			"diff":           diffSchema,
			"consumerAction": consumerActionSchema,
			"snapshotResult": snapshotResultSchema,
			"watchEvent":     watchEventSchema,
		},
	}
}
