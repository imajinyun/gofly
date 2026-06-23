// Package rpc provides a gRPC-compatible RPC server and client with
// governance, discovery, load balancing and streaming support.
package rpc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gofly/gofly/core/governance"
	coreruntime "github.com/gofly/gofly/core/runtime"
	controladmin "github.com/gofly/gofly/ops/admin"
)

const maxDescriptorCompatibilityBytes = 1 << 20

// ServiceSnapshot captures the runtime state of a registered RPC service.
type ServiceSnapshot struct {
	Name          string                 `json:"name"`
	Version       string                 `json:"version,omitempty"`
	Metadata      map[string]string      `json:"metadata,omitempty"`
	Methods       []string               `json:"methods"`
	MethodDetails []MethodSnapshot       `json:"methodDetails,omitempty"`
	Streams       []StreamMethodSnapshot `json:"streams,omitempty"`
}

// MethodSnapshot captures the metadata for a single RPC method.
type MethodSnapshot struct {
	Name        string            `json:"name"`
	Request     string            `json:"request,omitempty"`
	Response    string            `json:"response,omitempty"`
	Timeout     string            `json:"timeout,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Middlewares int               `json:"middlewares,omitempty"`
}

// StreamMethodSnapshot captures the metadata for a streaming RPC method.
type StreamMethodSnapshot struct {
	Name        string            `json:"name"`
	Message     string            `json:"message,omitempty"`
	Timeout     string            `json:"timeout,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Middlewares int               `json:"middlewares,omitempty"`
}

type HealthSnapshot struct {
	Status   string            `json:"status"`
	State    StateSnapshot     `json:"state"`
	Services []ServiceSnapshot `json:"services"`
}

type GovernanceSnapshot struct {
	Components []governance.ComponentSnapshot `json:"components"`
}

func (s *HTTPServer) serveAdmin(w http.ResponseWriter, r *http.Request) {
	if s.opts.adminAudit != nil {
		controladmin.AuditMiddleware("rpc", s.opts.adminAudit)(http.HandlerFunc(s.serveAdminRoute)).ServeHTTP(w, r)
		return
	}
	s.serveAdminRoute(w, r)
}

func (s *HTTPServer) serveAdminRoute(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeAdmin(w, r) {
		return
	}
	if r.URL.Path == "/rpc/admin/governance" || strings.HasPrefix(r.URL.Path, "/rpc/admin/governance/") {
		s.governanceAdmin().ServeHTTP(w, r)
		return
	}
	if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/rpc/admin/descriptors/") && strings.HasSuffix(r.URL.Path, "/compatibility") {
		s.serveDescriptorCompatibility(w, r)
		return
	}
	if r.Method != http.MethodGet {
		writeRPCError(w, http.StatusMethodNotAllowed, CodeInvalidArgument, "method not allowed")
		return
	}
	switch {
	case strings.HasPrefix(r.URL.Path, "/rpc/admin/descriptors/"):
		serviceName, ok := serviceDescriptorNameFromPath(r.URL.Path)
		if !ok {
			writeRPCError(w, http.StatusBadRequest, CodeInvalidArgument, "service descriptor name is invalid")
			return
		}
		desc, ok := s.GetServiceDescriptor(serviceName)
		if !ok {
			writeRPCError(w, http.StatusNotFound, CodeNotFound, "service descriptor not found")
			return
		}
		writeAdminJSON(w, http.StatusOK, desc)
	case r.URL.Path == "/rpc/admin/state":
		writeAdminJSON(w, http.StatusOK, s.State())
	case r.URL.Path == "/rpc/admin/services":
		writeAdminJSON(w, http.StatusOK, s.ServiceSnapshots())
	case r.URL.Path == "/rpc/admin/descriptors":
		writeAdminJSON(w, http.StatusOK, s.GetServiceDescriptors())
	case r.URL.Path == "/rpc/admin/health":
		state := s.State()
		status := "ok"
		code := http.StatusOK
		if state.State == "stopping" || state.State == "stopped" {
			status = "unavailable"
			code = http.StatusServiceUnavailable
		}
		writeAdminJSON(w, code, HealthSnapshot{
			Status:   status,
			State:    state,
			Services: s.ServiceSnapshots(),
		})
	case r.URL.Path == "/rpc/admin/runtime":
		writeAdminJSON(w, http.StatusOK, s.RuntimeSnapshot(r.Context()))
	default:
		http.NotFound(w, r)
	}
}

func serviceDescriptorNameFromPath(path string) (string, bool) {
	encodedName := strings.TrimPrefix(path, "/rpc/admin/descriptors/")
	return decodeServiceDescriptorName(encodedName)
}

func serviceDescriptorCompatibilityNameFromPath(path string) (string, bool) {
	encodedName := strings.TrimPrefix(path, "/rpc/admin/descriptors/")
	encodedName = strings.TrimSuffix(encodedName, "/compatibility")
	return decodeServiceDescriptorName(encodedName)
}

func decodeServiceDescriptorName(encodedName string) (string, bool) {
	name, err := url.PathUnescape(encodedName)
	if err != nil {
		return "", false
	}
	name = strings.TrimSpace(name)
	if name == "" || strings.Contains(name, "/") {
		return "", false
	}
	return name, true
}

func (s *HTTPServer) serveDescriptorCompatibility(w http.ResponseWriter, r *http.Request) {
	if r.Body == nil {
		writeRPCError(w, http.StatusBadRequest, CodeInvalidArgument, "descriptor compatibility payload is required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxDescriptorCompatibilityBytes)
	defer r.Body.Close()

	serviceName, ok := serviceDescriptorCompatibilityNameFromPath(r.URL.Path)
	if !ok {
		writeRPCError(w, http.StatusBadRequest, CodeInvalidArgument, "service descriptor name is invalid")
		return
	}
	base, ok := s.GetServiceDescriptor(serviceName)
	if !ok {
		writeRPCError(w, http.StatusNotFound, CodeNotFound, "service descriptor not found")
		return
	}
	var target Descriptor
	if err := json.NewDecoder(r.Body).Decode(&target); err != nil {
		writeRPCError(w, http.StatusBadRequest, CodeInvalidArgument, "descriptor compatibility payload is invalid")
		return
	}
	if err := target.Validate(); err != nil {
		writeRPCError(w, http.StatusBadRequest, CodeInvalidArgument, "descriptor compatibility payload is invalid: "+err.Error())
		return
	}
	report := CompareDescriptors(base, target)
	status := http.StatusOK
	if report.HasBreaking() {
		status = http.StatusConflict
	}
	writeAdminJSON(w, status, report)
}

func (s *HTTPServer) Governance() GovernanceSnapshot {
	return GovernanceSnapshot{Components: s.opts.governance.Snapshots()}
}

func (s *HTTPServer) RuntimeSnapshot(ctx context.Context) coreruntime.Snapshot {
	registry := coreruntime.NewRegistry()
	registry.Register("rpc.http.server", "server", func(context.Context) coreruntime.ComponentSnapshot {
		state := s.State()
		return coreruntime.ComponentSnapshot{
			Name:   "rpc.http.server",
			Kind:   "server",
			Owner:  "rpc",
			Target: state.Address,
			Status: state.State,
			Middleware: &coreruntime.MiddlewareSnapshot{
				Unary:  middlewareCountLayers("server_middleware", len(s.opts.middlewares)),
				Stream: middlewareCountLayers("server_stream_middleware", len(s.opts.streamMiddlewares)),
			},
			Governance: s.RuntimeCacheSnapshot(),
			Details: map[string]any{
				"services":     len(s.ServiceSnapshots()),
				"stateSince":   state.Since,
				"adminEnabled": true,
			},
		}
	}, coreruntime.WithOwner("rpc"))
	return registry.Snapshot(ctx)
}

func (s *HTTPServer) RuntimeCacheSnapshot() RPCPolicyRuntimeCacheSnapshot {
	if s == nil || s.runtime == nil {
		return RPCPolicyRuntimeCacheSnapshot{}
	}
	s.runtime.mu.Lock()
	defer s.runtime.mu.Unlock()
	return RPCPolicyRuntimeCacheSnapshot{
		RateLimiters:        len(s.runtime.rateLimits),
		ConcurrencyLimiters: len(s.runtime.concurrency),
		Breakers:            len(s.runtime.breakers),
		Balancers:           len(s.runtime.balancers),
	}
}

func (s *HTTPServer) governanceAdmin() *governance.Admin {
	return governance.NewAdmin(
		s.opts.rules,
		s.opts.governance,
		governance.WithAdminManager(s.opts.manager),
		governance.WithAdminPathPrefix("/rpc/admin/governance"),
		governance.WithAdminDefaultRequest(governance.Request{Transport: governance.TransportRPC}),
	)
}

func (s *HTTPServer) authorizeAdmin(w http.ResponseWriter, r *http.Request) bool {
	return controladmin.AuthorizeBearerOrLocal(w, r, s.opts.adminToken, rpcAdminErrorWriter)
}

func rpcAdminErrorWriter(w http.ResponseWriter, status int, message string) {
	code := CodePermissionDenied
	if status == http.StatusUnauthorized {
		code = CodeUnauthenticated
	}
	writeRPCError(w, status, code, message)
}

func (s *HTTPServer) ServiceSnapshots() []ServiceSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	services := make([]ServiceSnapshot, 0, len(s.services))
	for _, service := range s.services {
		methods := make([]string, 0, len(service.Methods))
		methodDetails := make([]MethodSnapshot, 0, len(service.Methods))
		for _, method := range service.Methods {
			methods = append(methods, method.Name)
			methodDetails = append(methodDetails, MethodSnapshot{
				Name:        method.Name,
				Request:     descriptorTypeName(method.Request, method.Metadata, "request"),
				Response:    descriptorTypeName(method.Response, method.Metadata, "response"),
				Timeout:     durationString(method.Timeout),
				Metadata:    cloneStringMap(method.Metadata),
				Middlewares: len(method.Middlewares),
			})
		}
		sort.Strings(methods)
		sort.Slice(methodDetails, func(i, j int) bool { return methodDetails[i].Name < methodDetails[j].Name })
		streams := make([]StreamMethodSnapshot, 0, len(service.Streams))
		for _, stream := range service.Streams {
			streams = append(streams, StreamMethodSnapshot{
				Name:        stream.Name,
				Message:     descriptorTypeName(stream.Message, stream.Metadata, "message"),
				Timeout:     durationString(stream.Timeout),
				Metadata:    cloneStringMap(stream.Metadata),
				Middlewares: len(stream.Middlewares),
			})
		}
		sort.Slice(streams, func(i, j int) bool { return streams[i].Name < streams[j].Name })
		services = append(services, ServiceSnapshot{
			Name:          service.Name,
			Version:       service.Version,
			Metadata:      cloneStringMap(service.Metadata),
			Methods:       methods,
			MethodDetails: methodDetails,
			Streams:       streams,
		})
	}
	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })
	return services
}

func durationString(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return d.String()
}

func writeAdminJSON(w http.ResponseWriter, status int, v any) {
	controladmin.WriteJSON(w, status, v)
}
