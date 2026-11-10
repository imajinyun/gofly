// Package authn provides gosky's business-identity boundary for admin and
// front HTTP routes. It deliberately does not authorize a capability: callers
// must run the deployment-owned capability registry after this authenticated
// principal has been established.
package authn

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/imajinyun/gofly/core/auth"
)

// Realm identifies a mutually exclusive gosky web identity domain.
type Realm string

const (
	RealmAdmin Realm = "admin"
	RealmFront Realm = "front"
)

// RequestPrincipal is the typed, verified identity available to gosky business
// handlers. None of its fields originate from a request header or body.
type RequestPrincipal struct {
	Subject   string
	TenantID  uint64
	Realm     Realm
	SessionID string
}

type principalContextKey struct{}

// PrincipalFromContext returns the principal established by one of gosky's
// realm interceptors. It never interprets arbitrary auth claims at a handler.
func PrincipalFromContext(ctx context.Context) (RequestPrincipal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(RequestPrincipal)
	return principal, ok
}

// PrincipalValidator provides the persisted session/account/membership check
// that completes a signed Access JWT authentication decision.
type PrincipalValidator interface {
	ValidatePrincipal(context.Context, RequestPrincipal) error
}

// Interceptor enriches a verified JWT identity with a realm-specific request
// principal and, when configured, rejects revoked sessions and disabled
// account or tenant-membership rows.
type Interceptor struct {
	realm              Realm
	validator          auth.Validator
	principalValidator PrincipalValidator
}

// NewAdminInterceptor creates the identity gate for `/api/admin/**` routes.
func NewAdminInterceptor(validator auth.Validator, principalValidator ...PrincipalValidator) Interceptor {
	return newInterceptor(RealmAdmin, validator, principalValidator...)
}

// NewFrontInterceptor creates the identity gate for `/api/front/**` routes.
func NewFrontInterceptor(validator auth.Validator, principalValidator ...PrincipalValidator) Interceptor {
	return newInterceptor(RealmFront, validator, principalValidator...)
}

func newInterceptor(realm Realm, validator auth.Validator, principalValidators ...PrincipalValidator) Interceptor {
	interceptor := Interceptor{realm: realm, validator: validator}
	if len(principalValidators) > 0 {
		interceptor.principalValidator = principalValidators[0]
	}
	return interceptor
}

// Validator adapts the realm interceptor to gofly's route-level authentication
// option. It fails closed when the JWT, identity claims, or realm are invalid.
func (i Interceptor) Validator() auth.Validator {
	return func(ctx context.Context, token string) (context.Context, error) {
		if i.validator == nil || !validRealm(i.realm) {
			return ctx, auth.ErrInvalidCredentials
		}
		authenticatedCtx, err := i.validator(ctx, token)
		if err != nil {
			return ctx, err
		}
		principal, ok := auth.FromContext(authenticatedCtx)
		if !ok {
			return ctx, auth.ErrInvalidCredentials
		}
		requestPrincipal, err := principalFromAuth(principal)
		if err != nil || requestPrincipal.Realm != i.realm {
			return ctx, auth.ErrInvalidCredentials
		}
		if i.principalValidator != nil {
			if err := i.principalValidator.ValidatePrincipal(authenticatedCtx, requestPrincipal); err != nil {
				return ctx, auth.ErrInvalidCredentials
			}
		}
		return context.WithValue(authenticatedCtx, principalContextKey{}, requestPrincipal), nil
	}
}

func principalFromAuth(principal auth.Principal) (RequestPrincipal, error) {
	subject := strings.TrimSpace(principal.Subject)
	if subject == "" || len([]rune(subject)) > 255 {
		return RequestPrincipal{}, errors.New("principal subject is invalid")
	}
	tenantID, ok := unsignedClaim(principal.Claims["tenant_id"])
	if !ok || tenantID == 0 {
		return RequestPrincipal{}, errors.New("principal tenant is invalid")
	}
	realmValue, ok := principal.Claims["realm"].(string)
	realm := Realm(strings.TrimSpace(realmValue))
	if !ok || !validRealm(realm) {
		return RequestPrincipal{}, errors.New("principal realm is invalid")
	}
	sessionID, ok := principal.Claims["sid"].(string)
	sessionID = strings.TrimSpace(sessionID)
	if !ok || sessionID == "" || len([]rune(sessionID)) > 255 {
		return RequestPrincipal{}, errors.New("principal session is invalid")
	}
	return RequestPrincipal{Subject: subject, TenantID: tenantID, Realm: realm, SessionID: sessionID}, nil
}

func unsignedClaim(value any) (uint64, bool) {
	switch typed := value.(type) {
	case string:
		parsed, err := strconv.ParseUint(strings.TrimSpace(typed), 10, 64)
		return parsed, err == nil
	case float64:
		if typed < 0 || typed != float64(uint64(typed)) {
			return 0, false
		}
		return uint64(typed), true
	case uint64:
		return typed, true
	case jsonNumber:
		parsed, err := strconv.ParseUint(string(typed), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

type jsonNumber string

func validRealm(realm Realm) bool {
	return realm == RealmAdmin || realm == RealmFront
}

// RouteDomain determines which business interceptor owns a registered route.
// Only the first segment beneath `/api` is authoritative; substring matching
// would turn unrelated paths into an authentication boundary.
type RouteDomain string

const (
	RouteDomainAdmin  RouteDomain = "admin"
	RouteDomainFront  RouteDomain = "front"
	RouteDomainPublic RouteDomain = "public"
)

// ClassifyBusinessRouteDomain accepts only explicit gosky business prefixes.
// Infrastructure control-plane paths such as `/admin/control-plane` are not
// part of this namespace and therefore return ok=false.
func ClassifyBusinessRouteDomain(path string) (RouteDomain, bool) {
	segments := strings.Split(strings.TrimSpace(path), "/")
	if len(segments) < 4 || segments[0] != "" || segments[1] != "api" || segments[2] == "" {
		return "", false
	}
	switch RouteDomain(segments[2]) {
	case RouteDomainAdmin, RouteDomainFront, RouteDomainPublic:
		return RouteDomain(segments[2]), true
	default:
		return "", false
	}
}
