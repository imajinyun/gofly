// Package app contains gosky application services.
package app

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
)

const (
	// ProjectReadResource is the deployment-owned Casbin resource used for
	// Project reads. It must match the REST route template.
	ProjectReadResource = "/api/v1/projects/:id"
	// ProjectReadAction is the Casbin action used for Project reads.
	ProjectReadAction = "GET"
	// ProjectReadOperation identifies the durable audit operation.
	ProjectReadOperation = "project.read"

	ProjectOperationOutcomeAllowed  = "allowed"
	ProjectOperationOutcomeDenied   = "denied"
	ProjectOperationOutcomeNotFound = "not_found"
	ProjectOperationOutcomeError    = "error"

	ProjectAuthorizationOwner       = "owner"
	ProjectAuthorizationMember      = "member"
	ProjectAuthorizationPolicy      = "casbin_policy"
	ProjectAuthorizationNoGrant     = "no_grant"
	ProjectAuthorizationCrossTenant = "cross_tenant"
	ProjectAuthorizationNotFound    = "not_found"
	ProjectAuthorizationUnavailable = "authorization_unavailable"
)

var (
	// ErrProjectNotFound keeps persistence absence distinct from storage faults.
	ErrProjectNotFound = errors.New("project not found")
)

// Project is the persisted authorization-relevant Project state. Tenant and
// owner values remain internal and are deliberately not exposed in responses.
type Project struct {
	ID           uint64
	TenantID     uint64
	OwnerSubject string
	Name         string
	Description  string
}

// ProjectResponse is the public Project representation returned after the
// complete tenant, ownership, membership, and policy decision succeeds.
type ProjectResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ProjectOperation is the durable, credential-free audit record for a Project
// operation. It intentionally excludes request headers, JWTs, and responses.
type ProjectOperation struct {
	ActorTenantID      uint64
	ProjectTenantID    uint64
	ProjectID          uint64
	ActorSubject       string
	Operation          string
	Outcome            string
	AuthorizationBasis string
	RequestID          string
}

// ProjectRepository provides the data boundary for Project authorization and
// its durable operation log. Implementations must distinguish not found from
// infrastructure errors and must use the supplied context for every call.
type ProjectRepository interface {
	GetProject(context.Context, uint64) (Project, error)
	IsActiveMember(context.Context, uint64, uint64, string) (bool, error)
	RecordProjectOperation(context.Context, ProjectOperation) error
}

// ProjectPolicyAuthorizer represents the Casbin part of a Project decision.
// Ownership and membership remain application-state checks outside Casbin.
type ProjectPolicyAuthorizer interface {
	Authorize(context.Context, string, string) *coreerrors.Error
}

// ProjectService combines persistent Project state with the tenant-scoped
// Casbin policy boundary. It fails closed if persistence or audit recording is
// unavailable.
type ProjectService struct {
	repository ProjectRepository
	policy     ProjectPolicyAuthorizer
}

func NewProjectService(repository ProjectRepository, policy ProjectPolicyAuthorizer) *ProjectService {
	return &ProjectService{repository: repository, policy: policy}
}

// Get returns a Project only after its tenant, owner/member, or Casbin policy
// authorization has succeeded and the resulting operation has been durably
// recorded.
func (s *ProjectService) Get(ctx context.Context, id string, requestID string) (ProjectResponse, *coreerrors.Error) {
	projectID, ok := parseUnsignedIdentifier(id)
	if !ok {
		return ProjectResponse{}, coreerrors.New(coreerrors.CodeInvalidArgument, "project id is invalid")
	}
	principal, ok := auth.FromContext(ctx)
	if !ok || strings.TrimSpace(principal.Subject) == "" || len([]rune(strings.TrimSpace(principal.Subject))) > 255 {
		return ProjectResponse{}, coreerrors.New(coreerrors.CodeUnauthenticated, "missing authenticated principal")
	}
	actorTenantID, ok := tenantFromPrincipal(principal)
	if !ok {
		return ProjectResponse{}, coreerrors.New(coreerrors.CodePermissionDenied, "tenant claim is required")
	}
	if s == nil || s.repository == nil {
		return ProjectResponse{}, coreerrors.New(coreerrors.CodeUnavailable, "project persistence is unavailable")
	}

	project, err := s.repository.GetProject(ctx, projectID)
	if err != nil {
		if errors.Is(err, ErrProjectNotFound) {
			operation := newProjectOperation(actorTenantID, 0, projectID, principal.Subject, requestID, ProjectOperationOutcomeNotFound, ProjectAuthorizationNotFound)
			if auditErr := s.record(ctx, operation); auditErr != nil {
				return ProjectResponse{}, auditErr
			}
			return ProjectResponse{}, coreerrors.New(coreerrors.CodeNotFound, "project not found")
		}
		return ProjectResponse{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "project persistence is unavailable", err)
	}

	operation := newProjectOperation(actorTenantID, project.TenantID, project.ID, principal.Subject, requestID, "", "")
	if actorTenantID != project.TenantID {
		operation.Outcome = ProjectOperationOutcomeDenied
		operation.AuthorizationBasis = ProjectAuthorizationCrossTenant
		if auditErr := s.record(ctx, operation); auditErr != nil {
			return ProjectResponse{}, auditErr
		}
		return ProjectResponse{}, coreerrors.New(coreerrors.CodePermissionDenied, "permission denied")
	}
	if principal.Subject == project.OwnerSubject {
		operation.Outcome = ProjectOperationOutcomeAllowed
		operation.AuthorizationBasis = ProjectAuthorizationOwner
		if auditErr := s.record(ctx, operation); auditErr != nil {
			return ProjectResponse{}, auditErr
		}
		return projectResponse(project), nil
	}
	member, err := s.repository.IsActiveMember(ctx, project.ID, project.TenantID, principal.Subject)
	if err != nil {
		return ProjectResponse{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "project membership is unavailable", err)
	}
	if member {
		operation.Outcome = ProjectOperationOutcomeAllowed
		operation.AuthorizationBasis = ProjectAuthorizationMember
		if auditErr := s.record(ctx, operation); auditErr != nil {
			return ProjectResponse{}, auditErr
		}
		return projectResponse(project), nil
	}
	if s.policy != nil {
		if policyErr := s.policy.Authorize(ctx, ProjectReadResource, ProjectReadAction); policyErr == nil {
			operation.Outcome = ProjectOperationOutcomeAllowed
			operation.AuthorizationBasis = ProjectAuthorizationPolicy
			if auditErr := s.record(ctx, operation); auditErr != nil {
				return ProjectResponse{}, auditErr
			}
			return projectResponse(project), nil
		} else if policyErr.Code != coreerrors.CodePermissionDenied {
			operation.Outcome = ProjectOperationOutcomeError
			operation.AuthorizationBasis = ProjectAuthorizationUnavailable
			if auditErr := s.record(ctx, operation); auditErr != nil {
				return ProjectResponse{}, auditErr
			}
			return ProjectResponse{}, policyErr
		}
	}
	operation.Outcome = ProjectOperationOutcomeDenied
	operation.AuthorizationBasis = ProjectAuthorizationNoGrant
	if auditErr := s.record(ctx, operation); auditErr != nil {
		return ProjectResponse{}, auditErr
	}
	return ProjectResponse{}, coreerrors.New(coreerrors.CodePermissionDenied, "permission denied")
}

func (s *ProjectService) record(ctx context.Context, operation ProjectOperation) *coreerrors.Error {
	if err := s.repository.RecordProjectOperation(ctx, operation); err != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "project operation audit is unavailable", err)
	}
	return nil
}

func projectResponse(project Project) ProjectResponse {
	return ProjectResponse{ID: strconv.FormatUint(project.ID, 10), Name: project.Name, Description: project.Description}
}

func newProjectOperation(actorTenantID uint64, projectTenantID uint64, projectID uint64, actorSubject string, requestID string, outcome string, basis string) ProjectOperation {
	return ProjectOperation{
		ActorTenantID:      actorTenantID,
		ProjectTenantID:    projectTenantID,
		ProjectID:          projectID,
		ActorSubject:       boundedValue(actorSubject, 255),
		Operation:          ProjectReadOperation,
		Outcome:            outcome,
		AuthorizationBasis: basis,
		RequestID:          boundedValue(requestID, 128),
	}
}

func tenantFromPrincipal(principal auth.Principal) (uint64, bool) {
	if principal.Claims == nil {
		return 0, false
	}
	tenantID, ok := principal.Claims["tenant_id"].(string)
	if !ok {
		return 0, false
	}
	return parseUnsignedIdentifier(tenantID)
}

func parseUnsignedIdentifier(value string) (uint64, bool) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 20 {
		return 0, false
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 {
		return 0, false
	}
	return parsed, true
}

func boundedValue(value string, maximum int) string {
	value = strings.TrimSpace(value)
	if maximum <= 0 || len([]rune(value)) <= maximum {
		return value
	}
	return string([]rune(value)[:maximum])
}
