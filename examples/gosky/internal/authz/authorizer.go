// Package authz implements gosky's tenant-scoped authorization boundary.
package authz

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/casbin/casbin/v2"

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
	enforcer  *casbin.SyncedEnforcer
	validator auth.Validator
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
	return &Authorizer{
		enforcer:  enforcer,
		validator: auth.JWTValidator(jwtSecret, auth.JWTOptions{}),
	}, nil
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

// Authorize evaluates a verified principal against a deployment-owned resource
// and action. Resource-state checks such as project ownership and membership
// are deliberately performed by their application service before or alongside
// this tenant-scoped Casbin decision.
func (a *Authorizer) Authorize(ctx context.Context, resource string, action string) *coreerrors.Error {
	return a.authorize(ctx, resource, action)
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
	principal, ok := auth.FromContext(ctx)
	if !ok || strings.TrimSpace(principal.Subject) == "" {
		return coreerrors.New(coreerrors.CodeUnauthenticated, "missing authenticated principal")
	}
	tenant := tenantFromPrincipal(principal)
	if tenant == "" {
		return coreerrors.New(coreerrors.CodePermissionDenied, "tenant claim is required")
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
