// Package authz implements gosky's tenant-scoped authorization boundary.
package authz

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/casbin/casbin/v2"
	defaultrolemanager "github.com/casbin/casbin/v2/rbac/default-role-manager"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/core/metadata"
	"github.com/imajinyun/gofly/rest"
	"github.com/imajinyun/gofly/rpc"
	"github.com/imajinyun/gofly/rpc/endpoint"
)

const (
	// DefaultModelPath is a project-owned Casbin model. It is not request input.
	DefaultModelPath = "etc/authorization/casbin_model.conf"
	// DefaultPolicyPath is a project-owned development policy source.
	DefaultPolicyPath = "etc/authorization/casbin_policy.csv"

	minimumJWTSecretBytes = 32
)

const defaultModel = `[request_definition]
r = sub, dom, obj, act

[policy_definition]
p = sub, dom, obj, act

[role_definition]
g = _, _, _

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = g(r.sub, p.sub, r.dom) && r.dom == p.dom && keyMatch2(r.obj, p.obj) && regexMatch(r.act, p.act)
`

var routeParameterPattern = regexp.MustCompile(`\{([^{}]+)\}`)

// Authorizer keeps Casbin behind gosky's transport-neutral authorization
// boundary. Callers supply only normalized identity and route information.
type Authorizer struct {
	enforcer        *casbin.SyncedEnforcer
	validator       auth.Validator
	policyAvailable atomic.Bool
	watcherMu       sync.Mutex
	watcher         *MySQLPolicyWatcher
}

// NewFileAuthorizer creates an authorizer from explicit, deployment-owned
// model and policy files. It fails closed when either source is invalid.
func NewFileAuthorizer(modelPath, policyPath string, jwtSecret []byte) (*Authorizer, error) {
	if strings.TrimSpace(modelPath) == "" {
		return nil, fmt.Errorf("casbin model path is required")
	}
	if strings.TrimSpace(policyPath) == "" {
		return nil, fmt.Errorf("casbin policy path is required")
	}
	if len(jwtSecret) < minimumJWTSecretBytes {
		return nil, fmt.Errorf("jwt secret must contain at least %d bytes", minimumJWTSecretBytes)
	}
	enforcer, err := casbin.NewSyncedEnforcer(modelPath, policyPath)
	if err != nil {
		return nil, fmt.Errorf("load casbin model and policy: %w", err)
	}
	return newAuthorizer(enforcer, jwtSecret)
}

// NewMySQLAuthorizer loads Casbin policy from gosky's MySQL policy store. It
// never falls back to the development CSV file: unavailable policy storage is
// a startup failure and later reload failures are denied as unavailable.
func NewMySQLAuthorizer(modelPath string, db *sql.DB, jwtSecret []byte) (*Authorizer, error) {
	return NewMySQLAuthorizerWithTables(modelPath, db, DefaultMySQLPolicyTables(), jwtSecret)
}

// NewMySQLAuthorizerWithTables loads Casbin policy from one validated MySQL
// rule/event table pair. The pair is deployment configuration, never request
// input, and must use the schema documented for the MySQL policy store.
func NewMySQLAuthorizerWithTables(modelPath string, db *sql.DB, tables MySQLPolicyTables, jwtSecret []byte) (*Authorizer, error) {
	if strings.TrimSpace(modelPath) == "" {
		return nil, fmt.Errorf("casbin model path is required")
	}
	if db == nil {
		return nil, fmt.Errorf("casbin policy database is required")
	}
	if len(jwtSecret) < minimumJWTSecretBytes {
		return nil, fmt.Errorf("jwt secret must contain at least %d bytes", minimumJWTSecretBytes)
	}
	adapter, err := NewMySQLAdapterWithTables(db, tables)
	if err != nil {
		return nil, err
	}
	enforcer, err := casbin.NewSyncedEnforcer(modelPath, adapter)
	if err != nil {
		return nil, fmt.Errorf("load Casbin model and MySQL policy: %w", err)
	}
	return newAuthorizer(enforcer, jwtSecret)
}

func newAuthorizer(enforcer *casbin.SyncedEnforcer, jwtSecret []byte) (*Authorizer, error) {
	// One edge is a direct user-to-role assignment. Legacy role-to-role links
	// cannot contribute another hop, including after policy reloads.
	enforcer.SetRoleManager(defaultrolemanager.NewRoleManager(1))
	if err := enforcer.BuildRoleLinks(); err != nil {
		return nil, fmt.Errorf("build explicit role assignments: %w", err)
	}
	authorizer := &Authorizer{
		enforcer:  enforcer,
		validator: auth.JWTValidator(jwtSecret, auth.JWTOptions{}),
	}
	authorizer.policyAvailable.Store(true)
	return authorizer, nil
}

// ReloadPolicy reloads the Adapter state. Once a reload fails the authorizer
// fails closed until a subsequent successful reload restores availability.
func (a *Authorizer) ReloadPolicy() error {
	if a == nil || a.enforcer == nil {
		return fmt.Errorf("authorization is unavailable")
	}
	if err := a.enforcer.LoadPolicy(); err != nil {
		a.policyAvailable.Store(false)
		return fmt.Errorf("reload Casbin policy: %w", err)
	}
	a.policyAvailable.Store(true)
	return nil
}

// StartMySQLPolicyWatcher starts durable cross-instance policy refresh. The
// caller owns parent cancellation or must call Close.
func (a *Authorizer) StartMySQLPolicyWatcher(ctx context.Context, interval time.Duration) error {
	if a == nil || a.enforcer == nil {
		return fmt.Errorf("authorization is unavailable")
	}
	adapter, ok := a.enforcer.GetAdapter().(*MySQLAdapter)
	if !ok || adapter == nil {
		return fmt.Errorf("MySQL policy watcher requires a MySQL policy adapter")
	}
	watcher, err := NewMySQLPolicyWatcherWithTables(adapter.db, adapter.tables, interval, a.ReloadPolicy)
	if err != nil {
		return err
	}
	if err := watcher.Start(ctx); err != nil {
		return err
	}
	a.watcherMu.Lock()
	previous := a.watcher
	a.watcher = watcher
	a.watcherMu.Unlock()
	if previous != nil {
		previous.Close()
	}
	return nil
}

// Close releases the optional policy watcher. File-backed authorizers have no
// background resources and Close is therefore safe for all construction modes.
func (a *Authorizer) Close() {
	if a == nil {
		return
	}
	a.watcherMu.Lock()
	watcher := a.watcher
	a.watcher = nil
	a.watcherMu.Unlock()
	if watcher != nil {
		watcher.Close()
	}
}

// JWTValidator verifies the bearer token before authorization runs.
func (a *Authorizer) JWTValidator() auth.Validator {
	if a == nil || a.validator == nil {
		return func(ctx context.Context, _ string) (context.Context, error) {
			return ctx, auth.ErrInvalidCredentials
		}
	}
	return a.validator
}

// Middleware enforces a Casbin policy after a route's JWT middleware has
// populated auth.Principal. It only accepts subject and tenant values from the
// verified JWT context; caller headers never select a policy domain.
func (a *Authorizer) Middleware() rest.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			method, resource, ok := routeFromContext(r)
			if !ok {
				rest.WriteError(w, coreerrors.New(coreerrors.CodePermissionDenied, "route authorization metadata is required"))
				return
			}
			if err := a.authorize(r.Context(), resource, method); err != nil {
				rest.WriteError(w, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RegisteredHTTPMiddleware enforces the capability mapped from the matched
// server route template. It never sends a raw HTTP method or path to Casbin.
// A missing registry entry is a deployment error and fails closed before the
// business handler is called.
func (a *Authorizer) RegisteredHTTPMiddleware(registry *CapabilityRegistry) rest.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			method, resource, ok := routeFromContext(r)
			if !ok {
				rest.WriteError(w, coreerrors.New(coreerrors.CodeUnavailable, "route authorization metadata is required"))
				return
			}
			capability, authorizationErr := registeredCapability(registry, TransportREST, HTTPTransportIdentifier(method, resource))
			if authorizationErr != nil {
				rest.WriteError(w, authorizationErr)
				return
			}
			if err := a.authorize(r.Context(), string(capability), CapabilityAction); err != nil {
				rest.WriteError(w, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RPCMiddleware authenticates an RPC bearer token and enforces the Casbin
// policy for one deployment-owned RPC resource. RPC metadata is untrusted
// transport input: only the verified JWT principal selects the policy domain.
func (a *Authorizer) RPCMiddleware(resource string) endpoint.Middleware {
	resource = strings.TrimSpace(resource)
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			authenticated, err := a.authenticateAndAuthorize(ctx, resource, "CALL")
			if err != nil {
				return nil, err
			}
			return next(authenticated, req)
		}
	}
}

// RegisteredRPCMiddleware authenticates an RPC request and resolves its
// deployment-owned transport name through the capability registry before
// evaluating Casbin. Callers cannot grant raw RPC method names through tenant
// policy.
func (a *Authorizer) RegisteredRPCMiddleware(registry *CapabilityRegistry, transportIdentifier string) endpoint.Middleware {
	transportIdentifier = strings.TrimSpace(transportIdentifier)
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			capability, authorizationErr := registeredCapability(registry, TransportRPC, transportIdentifier)
			if authorizationErr != nil {
				return nil, authorizationErr
			}
			authenticated, err := a.authenticateAndAuthorize(ctx, string(capability), CapabilityAction)
			if err != nil {
				return nil, err
			}
			return next(authenticated, req)
		}
	}
}

// MuxStreamMiddleware authenticates the bearer token carried in the
// experimental mux routing metadata and enforces a Casbin STREAM policy before
// the stream business handler receives its first message.
func (a *Authorizer) MuxStreamMiddleware(resource string) rpc.ExperimentalMuxStreamMiddleware {
	resource = strings.TrimSpace(resource)
	return func(next rpc.ExperimentalMuxStreamHandler) rpc.ExperimentalMuxStreamHandler {
		return func(ctx context.Context, stream *rpc.ExperimentalMuxStream) error {
			authenticated, err := a.authenticateAndAuthorize(ctx, resource, "STREAM")
			if err != nil {
				return err
			}
			return next(authenticated, stream)
		}
	}
}

// RegisteredMuxStreamMiddleware resolves a mux stream name through the
// capability registry and enforces that mapped capability before the handler
// reads its first business message.
func (a *Authorizer) RegisteredMuxStreamMiddleware(registry *CapabilityRegistry, transportIdentifier string) rpc.ExperimentalMuxStreamMiddleware {
	transportIdentifier = strings.TrimSpace(transportIdentifier)
	return func(next rpc.ExperimentalMuxStreamHandler) rpc.ExperimentalMuxStreamHandler {
		return func(ctx context.Context, stream *rpc.ExperimentalMuxStream) error {
			capability, authorizationErr := registeredCapability(registry, TransportMuxStream, transportIdentifier)
			if authorizationErr != nil {
				return authorizationErr
			}
			authenticated, err := a.authenticateAndAuthorize(ctx, string(capability), CapabilityAction)
			if err != nil {
				return err
			}
			return next(authenticated, stream)
		}
	}
}

// ReceiveRegisteredMuxMessage receives one transport message, then
// re-evaluates the stream's fixed capability before exposing that message to
// its business handler. The stream context must have already passed
// RegisteredMuxStreamMiddleware, so its principal comes only from the verified
// JWT rather than from a client message. A denial is returned before the
// handler can consume or respond to the message; the mux adapter then closes
// the stream with the corresponding RPC error code.
//
// ExperimentalMuxStream is intentionally a concrete transport type, so gosky
// cannot substitute a transparent Receive wrapper at middleware registration.
// Long-lived gosky handlers must call this method for every business receive.
func (a *Authorizer) ReceiveRegisteredMuxMessage(
	ctx context.Context,
	stream *rpc.ExperimentalMuxStream,
	registry *CapabilityRegistry,
	transportIdentifier string,
) (rpc.Message, error) {
	capability, authorizationErr := registeredCapability(registry, TransportMuxStream, transportIdentifier)
	if authorizationErr != nil {
		return rpc.Message{}, authorizationErr
	}
	message, receiveErr := stream.Receive(ctx)
	if receiveErr != nil {
		return rpc.Message{}, receiveErr
	}
	if authorizationErr := a.authorize(ctx, string(capability), CapabilityAction); authorizationErr != nil {
		return rpc.Message{}, authorizationErr
	}
	return message, nil
}

// Authorize evaluates a verified principal against a deployment-owned resource
// and action. Resource-state checks such as project ownership and membership
// are deliberately performed by their application service before or alongside
// this tenant-scoped Casbin decision.
func (a *Authorizer) Authorize(ctx context.Context, resource string, action string) *coreerrors.Error {
	return a.authorize(ctx, resource, action)
}

func registeredCapability(registry *CapabilityRegistry, kind TransportKind, transportIdentifier string) (Capability, *coreerrors.Error) {
	capability, ok := registry.Lookup(kind, transportIdentifier)
	if !ok {
		return "", coreerrors.New(coreerrors.CodeUnavailable, "authorization capability mapping is unavailable")
	}
	return capability, nil
}

func (a *Authorizer) authenticateAndAuthorize(ctx context.Context, resource, action string) (context.Context, *coreerrors.Error) {
	if strings.TrimSpace(resource) == "" {
		return ctx, coreerrors.New(coreerrors.CodeUnavailable, "rpc authorization resource is required")
	}
	token, ok := rpcBearerFromContext(ctx)
	if !ok {
		return ctx, coreerrors.New(coreerrors.CodeUnauthenticated, "missing credentials")
	}
	authenticated, err := a.JWTValidator()(ctx, token)
	if err != nil {
		return ctx, coreerrors.New(coreerrors.CodeUnauthenticated, "invalid credentials")
	}
	if err := a.authorize(authenticated, resource, action); err != nil {
		return authenticated, err
	}
	return authenticated, nil
}

func (a *Authorizer) authorize(ctx context.Context, resource, action string) *coreerrors.Error {
	if a == nil || a.enforcer == nil {
		return coreerrors.New(coreerrors.CodeUnavailable, "authorization is unavailable")
	}
	if !a.policyAvailable.Load() {
		return coreerrors.New(coreerrors.CodeUnavailable, "authorization policy is unavailable")
	}
	a.watcherMu.Lock()
	watcher := a.watcher
	a.watcherMu.Unlock()
	if watcher != nil && watcher.Err() != nil {
		return coreerrors.New(coreerrors.CodeUnavailable, "authorization policy is unavailable")
	}
	principal, ok := auth.FromContext(ctx)
	if !ok || strings.TrimSpace(principal.Subject) == "" {
		return coreerrors.New(coreerrors.CodeUnauthenticated, "missing authenticated principal")
	}
	tenant := tenantFromPrincipal(principal)
	if tenant == "" {
		return coreerrors.New(coreerrors.CodePermissionDenied, "tenant claim is required")
	}
	if validRole(principal.Subject) {
		return coreerrors.New(coreerrors.CodePermissionDenied, "role names cannot identify users")
	}
	allowed, err := a.enforcer.Enforce(principal.Subject, tenant, resource, action)
	if err != nil {
		slog.ErrorContext(ctx, "authorization decision failed", "action", action, "resource", resource, "error", err)
		return coreerrors.New(coreerrors.CodeUnavailable, "authorization decision is unavailable")
	}
	slog.InfoContext(ctx, "authorization decision", "action", action, "resource", resource, "allowed", allowed)
	if !allowed {
		return coreerrors.New(coreerrors.CodePermissionDenied, "permission denied")
	}
	return nil
}

func rpcBearerFromContext(ctx context.Context) (string, bool) {
	md, _ := metadata.FromContext(ctx)
	for _, key := range []string{auth.MetadataKey, auth.AuthorizationHeader} {
		if token, ok := auth.ExtractBearer(md.Get(key)); ok {
			return token, true
		}
	}
	return "", false
}

func tenantFromPrincipal(principal auth.Principal) string {
	if principal.Claims == nil {
		return ""
	}
	tenant, ok := principal.Claims["tenant_id"].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(tenant)
}

func routeFromContext(r *http.Request) (method, resource string, ok bool) {
	pattern := strings.TrimSpace(rest.RoutePatternFromContext(r.Context()))
	method, resource, ok = strings.Cut(pattern, " ")
	method = strings.TrimSpace(method)
	resource = routeParameterPattern.ReplaceAllString(strings.TrimSpace(resource), `:$1`)
	return method, resource, ok && method != "" && resource != ""
}
