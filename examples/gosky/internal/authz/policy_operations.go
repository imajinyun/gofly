package authz

import (
	"context"
	"strings"

	coreerrors "github.com/imajinyun/gofly/core/errors"
)

// AuthorizeCapability lets an application guard directory reads before mutation.
func (s *PolicyService) AuthorizeCapability(ctx context.Context, capability Capability) *coreerrors.Error {
	_, err := s.authorizeCapability(ctx, capability)
	return err
}

func (s *PolicyService) authorizeRoleDelegation(ctx context.Context, domain, role string) *coreerrors.Error {
	capabilities, err := s.store.ListRoleCapabilities(ctx, domain, role)
	if err != nil {
		return policyMutationError(err)
	}
	return s.authorizeCapabilityGrants(ctx, capabilities)
}

func (s *PolicyService) authorizeCapabilityGrants(ctx context.Context, capabilities []Capability) *coreerrors.Error {
	for _, capability := range capabilities {
		if !isManagedCapability(capability) {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "unknown role capability")
		}
		if err := s.authorizer.Authorize(ctx, string(capability), CapabilityAction); err != nil {
			return err
		}
	}
	return nil
}

// SetRoleProjectScope changes only the read scope. It never grants the read
// operation or needs the unrelated permission-editing authority.
func (s *PolicyService) SetRoleProjectScope(ctx context.Context, role string, allTenant bool, requestID string) *coreerrors.Error {
	actor, err := s.authorizeCapability(ctx, CapabilityAuthorizationRoleScopeManage)
	if err != nil {
		return err
	}
	role = strings.TrimSpace(role)
	if !validRole(role) {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "unknown tenant role")
	}
	var changed bool
	var mutationErr error
	if allTenant {
		if err := s.authorizeCapabilityGrants(ctx, []Capability{CapabilityProjectReadAny}); err != nil {
			return err
		}
		changed, mutationErr = s.store.GrantPermission(ctx, actor.withRequestID(requestID), role, actor.Domain, string(CapabilityProjectReadAny), CapabilityAction)
	} else {
		changed, mutationErr = s.store.RevokePermission(ctx, actor.withRequestID(requestID), role, actor.Domain, string(CapabilityProjectReadAny), CapabilityAction)
	}
	if mutationErr != nil {
		return policyMutationError(mutationErr)
	}
	if changed {
		if err := s.authorizer.ReloadPolicy(); err != nil {
			return coreerrors.Wrap(coreerrors.CodeUnavailable, "authorization policy is unavailable", err)
		}
	}
	return nil
}
