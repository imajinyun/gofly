package authz

import (
	"net/http"
	"testing"
)

func TestDefaultCapabilityRegistry_ResolvesProtectedTransport(t *testing.T) {
	registry := DefaultCapabilityRegistry()

	tests := []struct {
		name       string
		kind       TransportKind
		identifier string
		want       Capability
	}{
		{
			name:       "project list REST route",
			kind:       TransportREST,
			identifier: HTTPTransportIdentifier(http.MethodGet, "/api/admin/projects"),
			want:       CapabilityProjectRead,
		},
		{
			name:       "project detail REST route",
			kind:       TransportREST,
			identifier: HTTPTransportIdentifier(http.MethodGet, "/api/admin/projects/{id}"),
			want:       CapabilityProjectRead,
		},
		{
			name:       "policy role list REST route",
			kind:       TransportREST,
			identifier: HTTPTransportIdentifier(http.MethodGet, "/api/admin/authorization/roles"),
			want:       CapabilityAuthorizationRoleRead,
		},
		{
			name:       "project member mutation REST route",
			kind:       TransportREST,
			identifier: HTTPTransportIdentifier(http.MethodPut, "/api/admin/projects/{id}/members"),
			want:       CapabilityProjectMemberManage,
		},
		{
			name:       "policy role mutation REST route",
			kind:       TransportREST,
			identifier: HTTPTransportIdentifier(http.MethodPost, "/api/admin/authorization/roles"),
			want:       CapabilityAuthorizationRoleMemberAdd,
		},
		{
			name:       "policy event REST route",
			kind:       TransportREST,
			identifier: HTTPTransportIdentifier(http.MethodGet, "/api/admin/authorization/policy-events"),
			want:       CapabilityAuthorizationPolicyEventRead,
		},
		{
			name:       "unary RPC method",
			kind:       TransportRPC,
			identifier: "rpc:greeter/SayHello",
			want:       CapabilityGreeterHello,
		},
		{
			name:       "mux stream",
			kind:       TransportMuxStream,
			identifier: "rpc:greeter/Watch",
			want:       CapabilityGreeterWatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := registry.Lookup(tt.kind, tt.identifier)
			if !ok {
				t.Fatalf("Lookup(%q, %q) did not resolve a capability", tt.kind, tt.identifier)
			}
			if got != tt.want {
				t.Fatalf("Lookup(%q, %q) = %q, want %q", tt.kind, tt.identifier, got, tt.want)
			}
		})
	}
}

func TestCapabilityRegistry_RejectsInvalidOrAmbiguousBindings(t *testing.T) {
	tests := []struct {
		name     string
		bindings []TransportBinding
	}{
		{
			name: "duplicate REST route after template normalization",
			bindings: []TransportBinding{
				{Kind: TransportREST, Identifier: HTTPTransportIdentifier(http.MethodGet, "/api/admin/projects/{id}"), Capability: CapabilityProjectRead},
				{Kind: TransportREST, Identifier: HTTPTransportIdentifier(http.MethodGet, "/api/admin/projects/:id"), Capability: CapabilityAuthorizationRoleManage},
			},
		},
		{
			name: "invalid capability name",
			bindings: []TransportBinding{
				{Kind: TransportRPC, Identifier: "rpc:greeter/SayHello", Capability: "Project.Read"},
			},
		},
		{
			name: "unknown transport kind",
			bindings: []TransportBinding{
				{Kind: TransportKind("websocket"), Identifier: "greeter/Watch", Capability: CapabilityGreeterWatch},
			},
		},
		{
			name: "empty RPC identifier",
			bindings: []TransportBinding{
				{Kind: TransportRPC, Capability: CapabilityGreeterHello},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewCapabilityRegistry(tt.bindings); err == nil {
				t.Fatalf("NewCapabilityRegistry(%#v) unexpectedly succeeded", tt.bindings)
			}
		})
	}
}

func TestCapabilityRegistry_MissingBindingIsNotAllowed(t *testing.T) {
	registry := DefaultCapabilityRegistry()
	if capability, ok := registry.Lookup(TransportREST, HTTPTransportIdentifier(http.MethodGet, "/api/admin/unknown")); ok {
		t.Fatalf("unknown route resolved to capability %q", capability)
	}
}
