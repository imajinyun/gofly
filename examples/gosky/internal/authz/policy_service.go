package authz

import (
	"context"
	"errors"
	"strings"

	"go.opentelemetry.io/otel/trace"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
)

// PolicyService exposes only the controlled role-management use cases. The
// underlying store remains unavailable to route handlers, preventing writes
// that bypass manager authorization and event auditing.
type PolicyService struct {
	store      *MySQLPolicyStore
	authorizer *Authorizer
}

func NewPolicyService(store *MySQLPolicyStore, authorizer *Authorizer) *PolicyService {
	return &PolicyService{store: store, authorizer: authorizer}
}

// AuthorizeManagement lets application use cases guard identity-directory
// reads before resolving role members. Policy mutations still recheck this
// boundary themselves before committing an audited transaction.
func (s *PolicyService) AuthorizeManagement(ctx context.Context) *coreerrors.Error {
	_, err := s.authorizeCapability(ctx, CapabilityAuthorizationRoleRead)
	return err
}

func (s *PolicyService) GrantRole(ctx context.Context, subject, role, requestID string) (bool, *coreerrors.Error) {
	actor, err := s.authorizeCapability(ctx, CapabilityAuthorizationRoleMemberAdd)
	if err != nil {
		return false, err
	}
	if err := s.authorizeRoleDelegation(ctx, actor.Domain, strings.TrimSpace(role)); err != nil {
		return false, err
	}
	changed, mutationErr := s.store.GrantRole(ctx, actor.withRequestID(requestID), strings.TrimSpace(subject), strings.TrimSpace(role), actor.Domain)
	if mutationErr != nil {
		return false, policyMutationError(mutationErr)
	}
	if changed {
		if reloadErr := s.authorizer.ReloadPolicy(); reloadErr != nil {
			return false, coreerrors.Wrap(coreerrors.CodeUnavailable, "authorization policy is unavailable", reloadErr)
		}
	}
	return changed, nil
}

func (s *PolicyService) RevokeRole(ctx context.Context, subject, role, requestID string) (bool, *coreerrors.Error) {
	actor, err := s.authorizeCapability(ctx, CapabilityAuthorizationRoleMemberRemove)
	if err != nil {
		return false, err
	}
	if err := s.authorizeRoleDelegation(ctx, actor.Domain, strings.TrimSpace(role)); err != nil {
		return false, err
	}
	changed, mutationErr := s.store.RevokeRole(ctx, actor.withRequestID(requestID), strings.TrimSpace(subject), strings.TrimSpace(role), actor.Domain)
	if mutationErr != nil {
		return false, policyMutationError(mutationErr)
	}
	if changed {
		if reloadErr := s.authorizer.ReloadPolicy(); reloadErr != nil {
			return false, coreerrors.Wrap(coreerrors.CodeUnavailable, "authorization policy is unavailable", reloadErr)
		}
	}
	return changed, nil
}

func (s *PolicyService) GrantRoles(ctx context.Context, subjects []string, role, requestID string) (bool, *coreerrors.Error) {
	actor, err := s.authorizeCapability(ctx, CapabilityAuthorizationRoleMemberAdd)
	if err != nil {
		return false, err
	}
	if err := s.authorizeRoleDelegation(ctx, actor.Domain, strings.TrimSpace(role)); err != nil {
		return false, err
	}
	changed, mutationErr := s.store.GrantRoles(ctx, actor.withRequestID(requestID), subjects, strings.TrimSpace(role), actor.Domain)
	if mutationErr != nil {
		return false, policyMutationError(mutationErr)
	}
	if changed {
		if reloadErr := s.authorizer.ReloadPolicy(); reloadErr != nil {
			return false, coreerrors.Wrap(coreerrors.CodeUnavailable, "authorization policy is unavailable", reloadErr)
		}
	}
	return changed, nil
}

func (s *PolicyService) RevokeRoles(ctx context.Context, subjects []string, role, requestID string) (bool, *coreerrors.Error) {
	actor, err := s.authorizeCapability(ctx, CapabilityAuthorizationRoleMemberRemove)
	if err != nil {
		return false, err
	}
	if err := s.authorizeRoleDelegation(ctx, actor.Domain, strings.TrimSpace(role)); err != nil {
		return false, err
	}
	changed, mutationErr := s.store.RevokeRoles(ctx, actor.withRequestID(requestID), subjects, strings.TrimSpace(role), actor.Domain)
	if mutationErr != nil {
		return false, policyMutationError(mutationErr)
	}
	if changed {
		if reloadErr := s.authorizer.ReloadPolicy(); reloadErr != nil {
			return false, coreerrors.Wrap(coreerrors.CodeUnavailable, "authorization policy is unavailable", reloadErr)
		}
	}
	return changed, nil
}

func (s *PolicyService) ListRoles(ctx context.Context) ([]RoleBinding, *coreerrors.Error) {
	actor, err := s.authorizeCapability(ctx, CapabilityAuthorizationRoleRead)
	if err != nil {
		return nil, err
	}
	bindings, listErr := s.store.ListRoleBindings(ctx, actor.Domain)
	if listErr != nil {
		return nil, coreerrors.Wrap(coreerrors.CodeUnavailable, "policy storage is unavailable", listErr)
	}
	return bindings, nil
}

// RoleCapabilities returns only direct, canonical capability policies for one
// fixed role in the verified tenant. Role inheritance is disabled; these are the exact grants used at runtime.
func (s *PolicyService) RoleCapabilities(ctx context.Context, role string) ([]Capability, *coreerrors.Error) {
	actor, err := s.authorizeCapability(ctx, CapabilityAuthorizationRoleRead)
	if err != nil {
		return nil, err
	}
	capabilities, listErr := s.store.ListRoleCapabilities(ctx, actor.Domain, strings.TrimSpace(role))
	if listErr != nil {
		if errors.Is(listErr, ErrInvalidPolicyRule) {
			return nil, coreerrors.New(coreerrors.CodeInvalidArgument, "policy role request is invalid")
		}
		return nil, coreerrors.Wrap(coreerrors.CodeUnavailable, "policy storage is unavailable", listErr)
	}
	return capabilities, nil
}

// ReplaceRoleCapabilities atomically replaces just the fixed, code-owned
// capability set for one role. It does not permit transport names, custom
// actions, or arbitrary Casbin fields to cross the HTTP boundary.
func (s *PolicyService) ReplaceRoleCapabilities(ctx context.Context, role string, capabilities []Capability, requestID string) *coreerrors.Error {
	actor, err := s.authorizeCapability(ctx, CapabilityAuthorizationRoleManage)
	if err != nil {
		return err
	}
	changed, mutationErr := s.store.ReplaceRoleCapabilities(ctx, actor.withRequestID(requestID), strings.TrimSpace(role), actor.Domain, capabilities)
	if mutationErr != nil {
		return policyMutationError(mutationErr)
	}
	if changed {
		if reloadErr := s.authorizer.ReloadPolicy(); reloadErr != nil {
			return coreerrors.Wrap(coreerrors.CodeUnavailable, "authorization policy is unavailable", reloadErr)
		}
	}
	return nil
}

func (s *PolicyService) ListEvents(ctx context.Context, limit int) ([]PolicyEvent, *coreerrors.Error) {
	actor, err := s.authorizeCapability(ctx, CapabilityAuthorizationPolicyEventRead)
	if err != nil {
		return nil, err
	}
	events, listErr := s.store.ListEvents(ctx, actor.Domain, limit)
	if listErr != nil {
		if errors.Is(listErr, ErrInvalidPolicyRule) {
			return nil, coreerrors.New(coreerrors.CodeInvalidArgument, "event limit must be between 1 and 100")
		}
		return nil, coreerrors.Wrap(coreerrors.CodeUnavailable, "policy storage is unavailable", listErr)
	}
	return events, nil
}

func (s *PolicyService) authorizeCapability(ctx context.Context, capability Capability) (policyActor, *coreerrors.Error) {
	if s == nil || s.store == nil || s.authorizer == nil {
		return policyActor{}, coreerrors.New(coreerrors.CodeUnavailable, "policy management is unavailable")
	}
	principal, ok := auth.FromContext(ctx)
	if !ok || strings.TrimSpace(principal.Subject) == "" {
		return policyActor{}, coreerrors.New(coreerrors.CodeUnauthenticated, "missing authenticated principal")
	}
	domain := tenantFromPrincipal(principal)
	if domain == "" {
		return policyActor{}, coreerrors.New(coreerrors.CodePermissionDenied, "tenant claim is required")
	}
	if err := s.authorizer.Authorize(ctx, string(capability), CapabilityAction); err != nil {
		return policyActor{}, err
	}
	traceID := ""
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		traceID = spanContext.TraceID().String()
	}
	return policyActor{Subject: principal.Subject, Domain: domain, TraceID: traceID}, nil
}

type policyActor PolicyActor

func (a policyActor) withRequestID(requestID string) PolicyActor {
	return PolicyActor{Subject: a.Subject, Domain: a.Domain, RequestID: strings.TrimSpace(requestID), TraceID: a.TraceID}
}

func policyMutationError(err error) *coreerrors.Error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrInvalidPolicyRule) || errors.Is(err, ErrPolicyDomainMismatch) {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "policy role request is invalid")
	}
	if errors.Is(err, ErrLastTenantAdmin) {
		return coreerrors.New(coreerrors.CodeFailedPrecondition, "cannot remove the final tenant administrator")
	}
	return coreerrors.Wrap(coreerrors.CodeUnavailable, "policy storage is unavailable", err)
}
