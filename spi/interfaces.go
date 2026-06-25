// Package spi defines gofly's stable service provider interfaces.
//
// The interfaces in this package are the compatibility boundary for external
// extensions. They are intentionally small, transport-neutral where practical,
// and backed by compile-time contract tests against the runtime packages. New
// methods must not be added to an existing stable interface without a new
// versioned interface name.
package spi

import (
	"context"
	"net/http"
	"time"

	"github.com/imajinyun/gofly/core/controlplane"
	"github.com/imajinyun/gofly/core/discovery"
	"github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/rest"
	"github.com/imajinyun/gofly/rpc/endpoint"
)

// ProtocolVersion is the current stable SPI contract version.
const ProtocolVersion = "gofly.spi.v1"

// ConfigSource loads a typed configuration value.
//
// Implementations must honor context cancellation before blocking on external
// I/O and should return immutable values or defensive copies when the loaded
// value contains maps, slices, or pointers.
type ConfigSource[T any] interface {
	Load(context.Context) (T, error)
}

// ConfigWatcher is an optional extension for ConfigSource implementations that
// can push or poll configuration changes.
//
// Watch must stop promptly when ctx is canceled. The callback must never be
// called concurrently by the same watcher unless the implementation documents
// that stronger behavior in its own package.
type ConfigWatcher[T any] interface {
	ConfigSource[T]
	Watch(context.Context, func(T)) error
}

// DiscoveryProvider is the stable discovery SPI for service registration,
// deregistration, resolution, and watching.
//
// Implementations must normalize instances according to the discovery package
// contract and must close watch channels when ctx is canceled.
type DiscoveryProvider interface {
	discovery.Registrar
	discovery.Resolver
}

// GovernanceProvider loads governance rules used by REST, RPC, Gateway, and MQ
// runtimes.
//
// Implementations should return defensive copies of rule slices because callers
// may sort or normalize rules before installing them.
type GovernanceProvider interface {
	Load(context.Context) ([]governance.Rule, error)
}

// GovernanceSaver is an optional extension for GovernanceProvider
// implementations that support persisting runtime rule updates.
type GovernanceSaver interface {
	Save(context.Context, []governance.Rule, time.Duration) error
}

// ControlPlaneContributor contributes runtime state to a control-plane snapshot.
//
// Contributors must only mutate the provided snapshot, must preserve existing
// fields unless intentionally appending, and must be deterministic for the same
// runtime state so control-plane checksums remain stable.
type ControlPlaneContributor interface {
	ContributeSnapshot(context.Context, *controlplane.Snapshot) error
}

// HTTPMiddleware wraps a net/http handler.
//
// The interface is deliberately based on net/http instead of a specific router
// so a single middleware can be adapted to gofly REST, Gateway, or plain
// net/http services.
type HTTPMiddleware interface {
	WrapHTTP(http.Handler) http.Handler
}

// HTTPMiddlewareFunc adapts a function to HTTPMiddleware.
type HTTPMiddlewareFunc func(http.Handler) http.Handler

// WrapHTTP implements HTTPMiddleware.
func (f HTTPMiddlewareFunc) WrapHTTP(next http.Handler) http.Handler {
	if f == nil {
		return next
	}
	return f(next)
}

// RESTMiddleware adapts a stable HTTPMiddleware to the rest.Middleware type.
func RESTMiddleware(mw HTTPMiddleware) rest.Middleware {
	return func(next http.Handler) http.Handler {
		if mw == nil {
			return next
		}
		return mw.WrapHTTP(next)
	}
}

// RPCInterceptor wraps a transport-neutral RPC endpoint.
//
// Interceptors must preserve ctx, return the downstream response/error unless
// intentionally enforcing a policy, and must not retain request values beyond
// the call unless they make defensive copies.
type RPCInterceptor interface {
	WrapRPC(endpoint.Endpoint) endpoint.Endpoint
}

// RPCInterceptorFunc adapts a function to RPCInterceptor.
type RPCInterceptorFunc func(endpoint.Endpoint) endpoint.Endpoint

// WrapRPC implements RPCInterceptor.
func (f RPCInterceptorFunc) WrapRPC(next endpoint.Endpoint) endpoint.Endpoint {
	if f == nil {
		return next
	}
	return f(next)
}

// EndpointMiddleware adapts a stable RPCInterceptor to endpoint.Middleware.
func EndpointMiddleware(interceptor RPCInterceptor) endpoint.Middleware {
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		if interceptor == nil {
			return next
		}
		return interceptor.WrapRPC(next)
	}
}

// GeneratorPlugin is the stable in-process generator plugin SPI.
//
// External executable plugins should use the JSON protocol described by
// GeneratorRequest and GeneratorResponse. In-process plugins should implement
// this interface and must only declare relative output paths.
type GeneratorPlugin interface {
	Name() string
	Manifest() GeneratorManifest
	Generate(context.Context, GeneratorRequest) (GeneratorResponse, error)
}

// GeneratorManifest declares a generator plugin's compatibility and security
// posture. Hosts must reject plugins that do not include ProtocolVersion in
// CompatibleVersions.
type GeneratorManifest struct {
	Name               string   `json:"name"`
	Version            string   `json:"version"`
	CompatibleVersions []string `json:"compatibleVersions"`
	Capabilities       []string `json:"capabilities,omitempty"`
	Permissions        []string `json:"permissions,omitempty"`
	RequiresDryRun     bool     `json:"requiresDryRun,omitempty"`
}

// GeneratorRequest is the JSON-safe request shared by generator plugins.
type GeneratorRequest struct {
	ProtocolVersion string            `json:"protocolVersion"`
	Command         string            `json:"command"`
	Service         string            `json:"service"`
	Module          string            `json:"module"`
	Style           string            `json:"style"`
	Dir             string            `json:"dir"`
	Input           map[string]string `json:"input,omitempty"`
	IDL             []byte            `json:"idl,omitempty"`
	IDLFormat       string            `json:"idlFormat,omitempty"`
}

// GeneratorFile declares one file a generator plugin wants the host to write.
// Path must be relative to the target project and must not contain symlink or
// parent-directory traversal segments.
type GeneratorFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// GeneratorPatch declares an append or anchored insertion into an existing
// target file. Path follows the same containment rules as GeneratorFile.Path.
type GeneratorPatch struct {
	Path        string `json:"path"`
	Patch       string `json:"patch"`
	InsertAfter string `json:"insertAfter,omitempty"`
}

// GeneratorResponse is the JSON-safe response shared by generator plugins.
type GeneratorResponse struct {
	ProtocolVersion string           `json:"protocolVersion,omitempty"`
	Files           []GeneratorFile  `json:"files,omitempty"`
	Patches         []GeneratorPatch `json:"patches,omitempty"`
	Message         string           `json:"message,omitempty"`
	Error           string           `json:"error,omitempty"`
}
