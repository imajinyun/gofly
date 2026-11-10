package app

import (
	"context"
	"errors"
	"strings"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
)

var (
	ErrProjectAccessChanged = errors.New("project authorization state changed")
	ErrProjectConflict      = errors.New("project name already exists")
)

type ProjectMutation struct {
	ProjectID     uint64
	ProjectTenant uint64
	ActorSubject  string
	Name          string
	Description   string
	Delete        bool
}

type ProjectMutationResponse struct {
	Changed bool `json:"changed"`
}

// ProjectMutationRepository commits a write and its audit, rechecking mutable
// project ownership/state and editor membership under the transaction lock.
type ProjectMutationRepository interface {
	MutateProject(context.Context, ProjectMutation, ProjectOperation) (bool, error)
}

func (s *ProjectService) Update(ctx context.Context, id, name, description, requestID string) (ProjectMutationResponse, *coreerrors.Error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 255 || len([]rune(description)) > 10000 {
		return ProjectMutationResponse{}, coreerrors.New(coreerrors.CodeInvalidArgument, "project name or description is invalid")
	}
	return s.mutateProject(ctx, id, name, description, requestID, false)
}

func (s *ProjectService) Delete(ctx context.Context, id, requestID string) (ProjectMutationResponse, *coreerrors.Error) {
	return s.mutateProject(ctx, id, "", "", requestID, true)
}

func (s *ProjectService) mutateProject(ctx context.Context, id, name, description, requestID string, remove bool) (ProjectMutationResponse, *coreerrors.Error) {
	projectID, ok := parseUnsignedIdentifier(id)
	if !ok {
		return ProjectMutationResponse{}, coreerrors.New(coreerrors.CodeInvalidArgument, "project id is invalid")
	}
	principal, ok := auth.FromContext(ctx)
	if !ok || !ValidProjectMemberSubject(principal.Subject) {
		return ProjectMutationResponse{}, coreerrors.New(coreerrors.CodeUnauthenticated, "missing authenticated principal")
	}
	tenantID, ok := tenantFromPrincipal(principal)
	if !ok {
		return ProjectMutationResponse{}, coreerrors.New(coreerrors.CodePermissionDenied, "tenant claim is required")
	}
	if s == nil || s.repository == nil {
		return ProjectMutationResponse{}, coreerrors.New(coreerrors.CodeUnavailable, "project persistence is unavailable")
	}
	action := "update"
	if remove {
		action = "delete"
	}
	operation := newProjectOperation(tenantID, 0, projectID, principal.Subject, requestID, ProjectOperationOutcomeDenied, ProjectAuthorizationNoGrant)
	operation.Operation = "project." + action
	project, err := s.repository.GetProject(ctx, projectID)
	if err != nil {
		if !errors.Is(err, ErrProjectNotFound) {
			return ProjectMutationResponse{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "project persistence is unavailable", err)
		}
		operation.Outcome, operation.AuthorizationBasis = ProjectOperationOutcomeNotFound, ProjectAuthorizationNotFound
		if auditErr := s.record(ctx, operation); auditErr != nil {
			return ProjectMutationResponse{}, auditErr
		}
		return ProjectMutationResponse{}, coreerrors.New(coreerrors.CodeNotFound, "project not found")
	}
	operation.ProjectTenantID = project.TenantID
	if tenantID != project.TenantID {
		operation.AuthorizationBasis = ProjectAuthorizationCrossTenant
		if auditErr := s.record(ctx, operation); auditErr != nil {
			return ProjectMutationResponse{}, auditErr
		}
		return ProjectMutationResponse{}, coreerrors.New(coreerrors.CodePermissionDenied, "permission denied")
	}
	basis, decision := s.authorizeProjectAction(ctx, project, principal, action)
	operation.AuthorizationBasis = basis
	if decision != nil {
		if decision.Code != coreerrors.CodePermissionDenied {
			operation.Outcome = ProjectOperationOutcomeError
		}
		if auditErr := s.record(ctx, operation); auditErr != nil {
			return ProjectMutationResponse{}, auditErr
		}
		return ProjectMutationResponse{}, decision
	}
	repository, ok := s.repository.(ProjectMutationRepository)
	if !ok {
		return ProjectMutationResponse{}, coreerrors.New(coreerrors.CodeUnavailable, "project mutation persistence is unavailable")
	}
	operation.Outcome = ProjectOperationOutcomeAllowed
	changed, err := repository.MutateProject(ctx, ProjectMutation{ProjectID: projectID, ProjectTenant: tenantID, ActorSubject: principal.Subject, Name: name, Description: description, Delete: remove}, operation)
	if err != nil {
		code := coreerrors.CodeUnavailable
		operation.Outcome = ProjectOperationOutcomeDenied
		switch {
		case errors.Is(err, ErrProjectAccessChanged):
			code = coreerrors.CodePermissionDenied
		case errors.Is(err, ErrProjectNotFound):
			code = coreerrors.CodeNotFound
			operation.Outcome = ProjectOperationOutcomeNotFound
		case errors.Is(err, ErrProjectConflict):
			code = coreerrors.CodeAlreadyExists
		default:
			return ProjectMutationResponse{}, coreerrors.Wrap(code, "project mutation or audit is unavailable", err)
		}
		if auditErr := s.record(ctx, operation); auditErr != nil {
			return ProjectMutationResponse{}, auditErr
		}
		return ProjectMutationResponse{}, coreerrors.New(code, "project mutation rejected")
	}
	return ProjectMutationResponse{Changed: changed}, nil
}
