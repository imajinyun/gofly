package authz

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

const (
	// CapabilityAction is the single Casbin action used for deployment-owned
	// capabilities. Transport verbs and RPC invocation kinds remain registry
	// metadata rather than tenant-editable policy values.
	CapabilityAction = "EXECUTE"

	CapabilityProjectRead                   Capability = "project.read"
	CapabilityProjectReadAny                Capability = "project.read.any"
	CapabilityProjectUpdate                 Capability = "project.update"
	CapabilityProjectDelete                 Capability = "project.delete"
	CapabilityProjectManageAny              Capability = "project.manage.any"
	CapabilityProjectMemberManage           Capability = "project.member.manage"
	CapabilityAuthorizationRoleRead         Capability = "authorization.role.read"
	CapabilityAuthorizationRoleManage       Capability = "authorization.role.manage"
	CapabilityAuthorizationRoleScopeManage  Capability = "authorization.role.scope.manage"
	CapabilityAuthorizationRoleMemberAdd    Capability = "authorization.role.member.add"
	CapabilityAuthorizationRoleMemberRemove Capability = "authorization.role.member.remove"
	CapabilityAuthorizationPolicyEventRead  Capability = "authorization.policy.events.read"
	CapabilitySystemUserManage              Capability = "system.user.manage"
	CapabilitySystemMenuManage              Capability = "system.menu.manage"
	CapabilitySupportFileRead               Capability = "support.file.read"
	CapabilitySupportFileManage             Capability = "support.file.manage"
	CapabilitySupportDictRead               Capability = "support.dict.read"
	CapabilitySupportDictManage             Capability = "support.dict.manage"
	CapabilitySupportConfigRead             Capability = "support.config.read"
	CapabilitySupportConfigManage           Capability = "support.config.manage"
	CapabilitySupportSerialRead             Capability = "support.serialNumber.read"
	CapabilitySupportSerialGenerate         Capability = "support.serialNumber.generate"
	CapabilitySupportMessageRead            Capability = "support.message.read"
	CapabilitySupportMessageSend            Capability = "support.message.send"
	CapabilitySupportFeedbackRead           Capability = "support.feedback.read"
	CapabilitySupportHelpDocRead            Capability = "support.helpDoc.read"
	CapabilitySupportHelpDocManage          Capability = "support.helpDoc.manage"
	CapabilitySupportLoginLogRead           Capability = "support.loginLog.read"
	CapabilitySupportLoginLogManage         Capability = "support.loginLog.manage"
	CapabilitySupportOperateLogRead         Capability = "support.operateLog.read"
	CapabilitySupportCacheRead              Capability = "support.cache.read"
	CapabilitySupportCacheManage            Capability = "support.cache.manage"
	CapabilitySupportReloadRead             Capability = "support.reload.read"
	CapabilitySupportReloadManage           Capability = "support.reload.manage"
	CapabilitySupportJobRead                Capability = "support.job.read"
	CapabilitySupportJobManage              Capability = "support.job.manage"
	CapabilitySupportJobExecute             Capability = "support.job.execute"
	CapabilitySupportDevToolsRead           Capability = "support.devTools.read"
	CapabilitySupportChangeLogRead          Capability = "support.changeLog.read"
	CapabilitySupportChangeLogManage        Capability = "support.changeLog.manage"
	CapabilityOANoticeRead                  Capability = "oa.notice.read"
	CapabilityOANoticeManage                Capability = "oa.notice.manage"
	CapabilityGreeterHello                  Capability = "greeter.hello"
	CapabilityGreeterWatch                  Capability = "greeter.watch"
)

var capabilityNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:\.[a-z][a-z0-9]*)+$`)

// ManagedCapabilities is the explicit, deployment-owned policy vocabulary.
func ManagedCapabilities() []Capability {
	return []Capability{CapabilityProjectRead, CapabilityProjectReadAny,
		CapabilityProjectMemberManage, CapabilityAuthorizationRoleManage,
		CapabilityGreeterHello, CapabilityGreeterWatch, CapabilityProjectUpdate,
		CapabilityProjectDelete, CapabilityProjectManageAny, CapabilityAuthorizationRoleRead,
		CapabilityAuthorizationRoleScopeManage, CapabilityAuthorizationRoleMemberAdd,
		CapabilityAuthorizationRoleMemberRemove, CapabilityAuthorizationPolicyEventRead,
		CapabilitySystemUserManage, CapabilitySystemMenuManage,
		CapabilitySupportFileRead, CapabilitySupportFileManage,
		CapabilitySupportDictRead, CapabilitySupportDictManage,
		CapabilitySupportConfigRead, CapabilitySupportConfigManage,
		CapabilitySupportSerialRead, CapabilitySupportSerialGenerate,
		CapabilitySupportMessageRead, CapabilitySupportMessageSend,
		CapabilitySupportFeedbackRead,
		CapabilitySupportHelpDocRead, CapabilitySupportHelpDocManage,
		CapabilitySupportLoginLogRead, CapabilitySupportLoginLogManage,
		CapabilitySupportOperateLogRead,
		CapabilitySupportCacheRead, CapabilitySupportCacheManage,
		CapabilitySupportReloadRead, CapabilitySupportReloadManage,
		CapabilitySupportJobRead, CapabilitySupportJobManage, CapabilitySupportJobExecute,
		CapabilitySupportDevToolsRead,
		CapabilitySupportChangeLogRead, CapabilitySupportChangeLogManage,
		CapabilityOANoticeRead, CapabilityOANoticeManage}
}

// DefaultRoleCapabilities returns complete direct grants, never inherited ones.
func DefaultRoleCapabilities(role string) []Capability {
	switch role {
	case RoleReader:
		return []Capability{CapabilityProjectRead, CapabilityGreeterHello, CapabilityGreeterWatch}
	case RoleWriter:
		return []Capability{CapabilityProjectRead, CapabilityProjectUpdate, CapabilityProjectMemberManage, CapabilityGreeterHello, CapabilityGreeterWatch}
	case RoleAdmin:
		return ManagedCapabilities()
	default:
		return nil
	}
}

// Capability is a deployment-owned authorization operation. Tenant policy
// binds fixed roles to these values; it never accepts a capability from an
// HTTP request, RPC message, or policy-management API body.
type Capability string

// TransportKind distinguishes the entry point that maps to a capability.
// A separate stream kind prevents accidentally treating a unary RPC grant as
// permission to open a long-lived stream.
type TransportKind string

const (
	TransportREST      TransportKind = "rest"
	TransportRPC       TransportKind = "rpc"
	TransportMuxStream TransportKind = "mux_stream"
)

// TransportBinding maps one stable transport identifier to one canonical
// capability. Bindings are constructed only by application code at startup.
type TransportBinding struct {
	Kind       TransportKind
	Identifier string
	Capability Capability
}

type transportKey struct {
	kind       TransportKind
	identifier string
}

// CapabilityRegistry is an immutable lookup table for gosky's protected
// transport surface. Its fields remain private so callers cannot mutate a
// running service's capability mapping.
type CapabilityRegistry struct {
	bindings map[transportKey]Capability
}

// NewCapabilityRegistry validates a complete deployment-owned binding set
// before routes and RPC methods can use it. It rejects ambiguous normalized
// identifiers so a transport surface cannot acquire two capabilities.
func NewCapabilityRegistry(bindings []TransportBinding) (*CapabilityRegistry, error) {
	if len(bindings) == 0 {
		return nil, fmt.Errorf("capability registry must contain at least one binding")
	}
	registry := &CapabilityRegistry{bindings: make(map[transportKey]Capability, len(bindings))}
	for _, binding := range bindings {
		identifier, err := normalizeTransportIdentifier(binding.Kind, binding.Identifier)
		if err != nil {
			return nil, err
		}
		if !capabilityNamePattern.MatchString(string(binding.Capability)) {
			return nil, fmt.Errorf("invalid authorization capability %q", binding.Capability)
		}
		key := transportKey{kind: binding.Kind, identifier: identifier}
		if _, exists := registry.bindings[key]; exists {
			return nil, fmt.Errorf("duplicate authorization transport binding %q/%q", binding.Kind, identifier)
		}
		registry.bindings[key] = binding.Capability
	}
	return registry, nil
}

// DefaultCapabilityRegistry returns gosky's complete current protected
// transport mapping. The set is code-owned so tenants cannot change the
// application surface through Casbin policy mutation.
func DefaultCapabilityRegistry() *CapabilityRegistry {
	registry, err := NewCapabilityRegistry([]TransportBinding{
		{Kind: TransportREST, Identifier: HTTPTransportIdentifier(http.MethodPut, "/api/admin/projects/:id"), Capability: CapabilityProjectUpdate},
		{Kind: TransportREST, Identifier: HTTPTransportIdentifier(http.MethodDelete, "/api/admin/projects/:id"), Capability: CapabilityProjectDelete},
		{Kind: TransportREST, Identifier: HTTPTransportIdentifier(http.MethodGet, "/api/admin/projects"), Capability: CapabilityProjectRead},
		{Kind: TransportREST, Identifier: HTTPTransportIdentifier(http.MethodGet, "/api/admin/projects/:id"), Capability: CapabilityProjectRead},
		{Kind: TransportREST, Identifier: HTTPTransportIdentifier(http.MethodGet, "/api/admin/projects/:id/members"), Capability: CapabilityProjectMemberManage},
		{Kind: TransportREST, Identifier: HTTPTransportIdentifier(http.MethodPut, "/api/admin/projects/:id/members"), Capability: CapabilityProjectMemberManage},
		{Kind: TransportREST, Identifier: HTTPTransportIdentifier(http.MethodDelete, "/api/admin/projects/:id/members"), Capability: CapabilityProjectMemberManage},
		{Kind: TransportREST, Identifier: HTTPTransportIdentifier(http.MethodGet, "/api/admin/authorization/roles"), Capability: CapabilityAuthorizationRoleRead},
		{Kind: TransportREST, Identifier: HTTPTransportIdentifier(http.MethodPost, "/api/admin/authorization/roles"), Capability: CapabilityAuthorizationRoleMemberAdd},
		{Kind: TransportREST, Identifier: HTTPTransportIdentifier(http.MethodDelete, "/api/admin/authorization/roles"), Capability: CapabilityAuthorizationRoleMemberRemove},
		{Kind: TransportREST, Identifier: HTTPTransportIdentifier(http.MethodGet, "/api/admin/authorization/policy-events"), Capability: CapabilityAuthorizationPolicyEventRead},
		{Kind: TransportREST, Identifier: HTTPTransportIdentifier(http.MethodGet, "/api/admin/system/users"), Capability: CapabilitySystemUserManage},
		{Kind: TransportREST, Identifier: HTTPTransportIdentifier(http.MethodPost, "/api/admin/system/users"), Capability: CapabilitySystemUserManage},
		{Kind: TransportREST, Identifier: HTTPTransportIdentifier(http.MethodPatch, "/api/admin/system/users/:id/state"), Capability: CapabilitySystemUserManage},
		{Kind: TransportREST, Identifier: HTTPTransportIdentifier(http.MethodPost, "/api/admin/system/users/:id/reset-password"), Capability: CapabilitySystemUserManage},
		{Kind: TransportREST, Identifier: HTTPTransportIdentifier(http.MethodGet, "/api/admin/system/roles"), Capability: CapabilityAuthorizationRoleRead},
		{Kind: TransportREST, Identifier: HTTPTransportIdentifier(http.MethodPost, "/api/admin/system/roles"), Capability: CapabilityAuthorizationRoleManage},
		{Kind: TransportREST, Identifier: HTTPTransportIdentifier(http.MethodPatch, "/api/admin/system/roles/:id"), Capability: CapabilityAuthorizationRoleManage},
		{Kind: TransportREST, Identifier: HTTPTransportIdentifier(http.MethodDelete, "/api/admin/system/roles/:id"), Capability: CapabilityAuthorizationRoleManage},
		{Kind: TransportREST, Identifier: HTTPTransportIdentifier(http.MethodGet, "/api/admin/system/roles/:id/menus"), Capability: CapabilityAuthorizationRoleRead},
		{Kind: TransportREST, Identifier: HTTPTransportIdentifier(http.MethodPut, "/api/admin/system/roles/:id/menus"), Capability: CapabilityAuthorizationRoleManage},
		{Kind: TransportREST, Identifier: HTTPTransportIdentifier(http.MethodGet, "/api/admin/system/menus"), Capability: CapabilityAuthorizationRoleRead},
		{Kind: TransportREST, Identifier: HTTPTransportIdentifier(http.MethodPost, "/api/admin/system/menus"), Capability: CapabilitySystemMenuManage},
		{Kind: TransportREST, Identifier: HTTPTransportIdentifier(http.MethodPut, "/api/admin/system/menus/:id"), Capability: CapabilitySystemMenuManage},
		{Kind: TransportREST, Identifier: HTTPTransportIdentifier(http.MethodDelete, "/api/admin/system/menus/:id"), Capability: CapabilitySystemMenuManage},
		{Kind: TransportRPC, Identifier: "rpc:greeter/SayHello", Capability: CapabilityGreeterHello},
		{Kind: TransportMuxStream, Identifier: "rpc:greeter/Watch", Capability: CapabilityGreeterWatch},
	})
	if err != nil {
		panic(fmt.Sprintf("invalid default authorization capability registry: %v", err))
	}
	return registry
}

// Lookup returns the canonical capability for one normalized transport entry.
// Callers must deny requests with no mapping rather than falling back to the
// caller-provided identifier.
func (r *CapabilityRegistry) Lookup(kind TransportKind, identifier string) (Capability, bool) {
	if r == nil {
		return "", false
	}
	normalized, err := normalizeTransportIdentifier(kind, identifier)
	if err != nil {
		return "", false
	}
	capability, ok := r.bindings[transportKey{kind: kind, identifier: normalized}]
	return capability, ok
}

// HTTPTransportIdentifier produces the canonical REST lookup key. It is used
// for registered templates, not untrusted request paths.
func HTTPTransportIdentifier(method, path string) string {
	return strings.ToUpper(strings.TrimSpace(method)) + " " + normalizeHTTPPathTemplate(path)
}

func normalizeTransportIdentifier(kind TransportKind, identifier string) (string, error) {
	identifier = strings.TrimSpace(identifier)
	switch kind {
	case TransportREST:
		method, path, ok := strings.Cut(identifier, " ")
		if !ok || strings.TrimSpace(method) == "" || strings.TrimSpace(path) == "" || strings.ContainsAny(strings.TrimSpace(path), " \t\r\n") {
			return "", fmt.Errorf("invalid REST authorization transport identifier %q", identifier)
		}
		method = strings.ToUpper(strings.TrimSpace(method))
		if method != strings.TrimSpace(method) || !isHTTPMethodToken(method) {
			return "", fmt.Errorf("invalid REST authorization method %q", method)
		}
		path = normalizeHTTPPathTemplate(path)
		if !strings.HasPrefix(path, "/") || path == "/" {
			return "", fmt.Errorf("invalid REST authorization path %q", path)
		}
		return method + " " + path, nil
	case TransportRPC, TransportMuxStream:
		if !validRPCTransportIdentifier(identifier) {
			return "", fmt.Errorf("invalid %s authorization transport identifier %q", kind, identifier)
		}
		return identifier, nil
	default:
		return "", fmt.Errorf("unsupported authorization transport kind %q", kind)
	}
}

func normalizeHTTPPathTemplate(path string) string {
	path = strings.TrimSpace(path)
	return routeParameterPattern.ReplaceAllString(path, `:$1`)
}

func isHTTPMethodToken(method string) bool {
	for _, character := range method {
		if character < 'A' || character > 'Z' {
			return false
		}
	}
	return true
}

func validRPCTransportIdentifier(identifier string) bool {
	if !strings.HasPrefix(identifier, "rpc:") || strings.ContainsAny(identifier, " \t\r\n") {
		return false
	}
	serviceAndMethod := strings.TrimPrefix(identifier, "rpc:")
	service, method, ok := strings.Cut(serviceAndMethod, "/")
	return ok && service != "" && method != "" && !strings.Contains(service, "/") && !strings.Contains(method, "/")
}
