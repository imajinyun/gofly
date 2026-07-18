package rpc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

const maxRPCMuxOTelLogProfileBytes = 16 << 10

// RPCMuxOTelLogSinkSchemaProvider optionally describes the JSON profile
// accepted by an RPCMuxOTelLogSinkProvider.
type RPCMuxOTelLogSinkSchemaProvider interface {
	RPCMuxOTelLogProfileSchema() json.RawMessage
}

// RPCMuxOTelLogSinkSnapshot describes one registered mux OTel log sink without
// exposing provider instances or profile values.
type RPCMuxOTelLogSinkSnapshot struct {
	Name               string          `json:"name"`
	ProfileValidation  bool            `json:"profileValidation"`
	ProfileSchema      json.RawMessage `json:"profileSchema,omitempty"`
	ClientExport       bool            `json:"clientExport"`
	ServerExport       bool            `json:"serverExport"`
	DeliveryGovernance bool            `json:"deliveryGovernance"`
}

// RPCMuxOTelLogSinkRegistrySnapshot is a deterministic, machine-readable view
// of the registered sink extension points.
type RPCMuxOTelLogSinkRegistrySnapshot struct {
	Sinks        []RPCMuxOTelLogSinkSnapshot `json:"sinks"`
	Capabilities []string                    `json:"capabilities"`
}

// DecodeRPCMuxOTelLogProfile decodes one JSON object into a sink-owned typed
// profile. Unknown fields and trailing values are rejected.
func DecodeRPCMuxOTelLogProfile(profile string, target any) error {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return fmt.Errorf("profile is required")
	}
	if len(profile) > maxRPCMuxOTelLogProfileBytes {
		return fmt.Errorf("profile exceeds %d bytes", maxRPCMuxOTelLogProfileBytes)
	}
	if target == nil {
		return fmt.Errorf("profile target is required")
	}
	decoder := json.NewDecoder(bytes.NewBufferString(profile))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode profile: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode profile: trailing JSON value")
		}
		return fmt.Errorf("decode profile: %w", err)
	}
	return nil
}

// RPCMuxOTelLogSinkRegistry returns a deterministic snapshot suitable for
// admin and control-plane introspection.
func RPCMuxOTelLogSinkRegistry() RPCMuxOTelLogSinkRegistrySnapshot {
	rpcMuxOTelLogSinks.RLock()
	providers := make(map[string]RPCMuxOTelLogSinkProvider, len(rpcMuxOTelLogSinks.items))
	for name, provider := range rpcMuxOTelLogSinks.items {
		providers[name] = provider
	}
	rpcMuxOTelLogSinks.RUnlock()

	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	sort.Strings(names)

	snapshot := RPCMuxOTelLogSinkRegistrySnapshot{
		Sinks: make([]RPCMuxOTelLogSinkSnapshot, 0, len(names)),
		Capabilities: []string{
			"client_export",
			"server_export",
			"profile_validation",
			"typed_profile_schema",
			"bounded_delivery",
			"timeout_isolation",
			"panic_isolation",
		},
	}
	for _, name := range names {
		provider := providers[name]
		snapshot.Sinks = append(snapshot.Sinks, RPCMuxOTelLogSinkSnapshot{
			Name:               name,
			ProfileValidation:  true,
			ProfileSchema:      rpcMuxOTelLogProfileSchema(provider),
			ClientExport:       true,
			ServerExport:       true,
			DeliveryGovernance: true,
		})
	}
	return snapshot
}

func rpcMuxOTelLogProfileSchema(provider RPCMuxOTelLogSinkProvider) (schema json.RawMessage) {
	schemaProvider, ok := provider.(RPCMuxOTelLogSinkSchemaProvider)
	if !ok || isNilRPCMuxOTelLogSinkProvider(schemaProvider) {
		return nil
	}
	defer func() {
		if recover() != nil {
			schema = nil
		}
	}()
	raw := schemaProvider.RPCMuxOTelLogProfileSchema()
	if !json.Valid(raw) {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}
