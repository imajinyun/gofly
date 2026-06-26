package command

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

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
