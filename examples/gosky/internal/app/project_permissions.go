package app

import (
	"context"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

type projectPermissions map[authz.Capability]bool

func (s *ProjectService) projectCapabilities(ctx context.Context) (projectPermissions, *coreerrors.Error) {
	if s == nil || s.policy == nil {
		return nil, coreerrors.New(coreerrors.CodeUnavailable, "project authorization is unavailable")
	}
	permissions := make(projectPermissions)
	for _, capability := range []authz.Capability{authz.CapabilityProjectRead, authz.CapabilityProjectReadAny,
		authz.CapabilityProjectUpdate, authz.CapabilityProjectDelete, authz.CapabilityProjectMemberManage, authz.CapabilityProjectManageAny} {
		err := s.policy.Authorize(ctx, string(capability), authz.CapabilityAction)
		if err == nil {
			permissions[capability] = true
		} else if err.Code != coreerrors.CodePermissionDenied {
			return nil, err
		}
	}
	return permissions, nil
}

// Project actions are shared by response projection and mutation enforcement.
func (p projectPermissions) allows(action, basis string) bool {
	owner := basis == ProjectAuthorizationOwner
	related := owner || basis == ProjectAuthorizationEditor || basis == ProjectAuthorizationViewer
	switch action {
	case "read":
		return p[authz.CapabilityProjectRead] && (related || p[authz.CapabilityProjectReadAny])
	case "update":
		return p[authz.CapabilityProjectUpdate] && (owner || basis == ProjectAuthorizationEditor || p[authz.CapabilityProjectManageAny])
	case "delete":
		return p[authz.CapabilityProjectDelete] && (owner || p[authz.CapabilityProjectManageAny])
	case "members.manage":
		return p[authz.CapabilityProjectMemberManage] && (owner || p[authz.CapabilityProjectManageAny])
	default:
		return false
	}
}

func (p projectPermissions) actions(basis string) []string {
	actions := make([]string, 0, 4)
	for _, action := range []string{"read", "update", "delete", "members.manage"} {
		if p.allows(action, basis) {
			actions = append(actions, action)
		}
	}
	return actions
}

func (s *ProjectService) projectRelation(ctx context.Context, project Project, subject string) (string, *coreerrors.Error) {
	if project.OwnerSubject == subject {
		return ProjectAuthorizationOwner, nil
	}
	role, err := s.repository.GetActiveMemberRole(ctx, project.ID, project.TenantID, subject)
	if err != nil {
		return "", coreerrors.Wrap(coreerrors.CodeUnavailable, "project membership is unavailable", err)
	}
	if role.Valid() {
		return string(role), nil
	}
	return ProjectAuthorizationNoGrant, nil
}

func (s *ProjectService) authorizeProjectAction(ctx context.Context, project Project, principal auth.Principal, action string) (string, *coreerrors.Error) {
	permissions, err := s.projectCapabilities(ctx)
	if err != nil {
		return ProjectAuthorizationUnavailable, err
	}
	basis, err := s.projectRelation(ctx, project, principal.Subject)
	if err != nil {
		return ProjectAuthorizationUnavailable, err
	}
	if !permissions.allows(action, basis) {
		return ProjectAuthorizationNoGrant, coreerrors.New(coreerrors.CodePermissionDenied, "permission denied")
	}
	if basis != ProjectAuthorizationOwner && (action != "update" || basis != ProjectAuthorizationEditor) {
		basis = ProjectAuthorizationTenantAdmin
	}
	return basis, nil
}
