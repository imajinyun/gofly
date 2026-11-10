// Package app contains gosky application services.
package app

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

const (
	// ProjectReadResource expands read scope after the explicit read operation
	// permission has been checked.
	// It is deliberately not a REST route template.
	ProjectReadResource = string(authz.CapabilityProjectReadAny)
	// ProjectReadAction is the canonical action for deployment-owned
	// capabilities. HTTP verbs remain transport registry metadata.
	ProjectReadAction = authz.CapabilityAction
	// ProjectReadOperation identifies the durable audit operation.
	ProjectReadOperation = "project.read"
	// ProjectListOperation identifies the durable audit operation for a typed
	// tenant data-scope query.
	ProjectListOperation = "project.list"
	// ProjectMemberUpsertOperation identifies creation, reactivation, or role
	// replacement of a Project relationship.
	ProjectMemberUpsertOperation = "project.member.upsert"
	// ProjectMemberRevokeOperation identifies revocation of an active Project
	// relationship.
	ProjectMemberRevokeOperation = "project.member.revoke"
	// ProjectMemberListOperation identifies enumeration of active non-owner
	// Project collaborators. The owner is represented separately in the response
	// and is never returned as a revocable member relationship.
	ProjectMemberListOperation = "project.member.list"
	// ProjectMemberManageResource is required for every collaborator operation;
	// ownership or project.manage.any supplies its resource scope.
	ProjectMemberManageResource = string(authz.CapabilityProjectMemberManage)
	// ProjectMemberManageAction is the canonical action for the member-management
	// capability.
	ProjectMemberManageAction = authz.CapabilityAction

	ProjectOperationOutcomeAllowed  = "allowed"
	ProjectOperationOutcomeDenied   = "denied"
	ProjectOperationOutcomeNotFound = "not_found"
	ProjectOperationOutcomeError    = "error"

	ProjectAuthorizationOwner         = "owner"
	ProjectAuthorizationViewer        = "viewer"
	ProjectAuthorizationEditor        = "editor"
	ProjectAuthorizationTenantAdmin   = "tenant_admin"
	ProjectAuthorizationRelationScope = "relation_scope"
	ProjectAuthorizationNoGrant       = "no_grant"
	ProjectAuthorizationCrossTenant   = "cross_tenant"
	ProjectAuthorizationNotFound      = "not_found"
	ProjectAuthorizationUnavailable   = "authorization_unavailable"
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
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	AllowedActions []string `json:"allowedActions"`
}

// ProjectListResponse is a deterministic keyset-paginated Project collection.
// NextCursor is empty when the caller reached the end of its data scope.
type ProjectListResponse struct {
	Items      []ProjectResponse `json:"items"`
	NextCursor string            `json:"nextCursor,omitempty"`
}

// ProjectDataScope is the fully typed repository input for a Project list.
// TenantAdmin widens visibility only within TenantID; otherwise the repository
// limits rows to the owner or active viewer/editor relationship of Subject.
type ProjectDataScope struct {
	TenantID    uint64
	Subject     string
	TenantAdmin bool
	AfterID     uint64
	Limit       uint
}

// Valid reports whether a repository data scope is bounded and tenant-specific.
func (s ProjectDataScope) Valid() bool {
	return s.TenantID > 0 && ValidProjectMemberSubject(s.Subject) && s.Limit > 0 && s.Limit <= 100
}

// ProjectMemberRole is the persisted relationship role for a non-owner
// Project principal. It is deliberately distinct from tenant Casbin roles:
// the relationship determines which particular Project is in scope.
type ProjectMemberRole string

const (
	ProjectMemberRoleViewer ProjectMemberRole = "viewer"
	ProjectMemberRoleEditor ProjectMemberRole = "editor"
)

// Valid reports whether the relationship role belongs to the Project domain.
func (r ProjectMemberRole) Valid() bool {
	return r == ProjectMemberRoleViewer || r == ProjectMemberRoleEditor
}

// ProjectMemberMutation is an authorized, tenant-bound mutation of one
// non-owner Project relationship. The repository persists it atomically with
// its operation audit record.
type ProjectMemberMutation struct {
	ProjectID     uint64
	ProjectTenant uint64
	Subject       string
	Role          ProjectMemberRole
	ActorSubject  string
	Revoke        bool
}

// ProjectOperation is the durable, credential-free audit record for a Project
// operation. It intentionally excludes request headers, JWTs, and responses.
type ProjectOperation struct {
	ActorTenantID      uint64
	ProjectTenantID    uint64
	ProjectID          uint64
	ActorSubject       string
	TargetSubject      string
	TargetRole         string
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
	GetActiveMemberRole(context.Context, uint64, uint64, string) (ProjectMemberRole, error)
	RecordProjectOperation(context.Context, ProjectOperation) error
}

// ProjectMemberMutationRepository owns the atomic persistence boundary for a
// Project relationship change and its allowed operation audit record. A
// successful member mutation must never be committed without its audit row.
type ProjectMemberMutationRepository interface {
	ProjectRepository
	MutateProjectMember(context.Context, ProjectMemberMutation, ProjectOperation) (bool, error)
}

// ProjectListRepository receives a validated typed data scope rather than SQL
// fragments or caller-controlled visibility flags.
type ProjectListRepository interface {
	ProjectRepository
	ListProjects(context.Context, ProjectDataScope) ([]Project, error)
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

// Get returns a Project for the admin realm after its tenant, owner/viewer/editor,
// or tenant-wide Casbin policy authorization has succeeded and the resulting
// operation has been durably recorded.
func (s *ProjectService) Get(ctx context.Context, id string, requestID string) (ProjectResponse, *coreerrors.Error) {
	return s.get(ctx, id, requestID, true)
}

// GetForFront returns a Project for the front realm only when the verified
// principal is its owner or active viewer/editor. It intentionally does not
// consult project.read.any, because that capability expands the admin data
// scope and must not grant tenant-wide front visibility.
func (s *ProjectService) GetForFront(ctx context.Context, id string, requestID string) (ProjectResponse, *coreerrors.Error) {
	return s.get(ctx, id, requestID, false)
}

func (s *ProjectService) get(ctx context.Context, id string, requestID string, allowTenantAdmin bool) (ProjectResponse, *coreerrors.Error) {
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
	var permissions projectPermissions
	var decisionErr *coreerrors.Error
	if allowTenantAdmin {
		permissions, decisionErr = s.projectCapabilities(ctx)
	}
	basis := ProjectAuthorizationNoGrant
	if decisionErr == nil {
		basis, decisionErr = s.projectRelation(ctx, project, principal.Subject)
	}
	related := basis == ProjectAuthorizationOwner || basis == ProjectAuthorizationViewer || basis == ProjectAuthorizationEditor
	allowed := related
	actions := []string{"read"}
	if allowTenantAdmin {
		allowed = permissions.allows("read", basis)
		actions = permissions.actions(basis)
	}
	if decisionErr != nil {
		operation.Outcome, operation.AuthorizationBasis = ProjectOperationOutcomeError, ProjectAuthorizationUnavailable
	} else if allowed {
		operation.Outcome, operation.AuthorizationBasis = ProjectOperationOutcomeAllowed, basis
		if !related {
			operation.AuthorizationBasis = ProjectAuthorizationTenantAdmin
		}
	} else {
		operation.Outcome, operation.AuthorizationBasis = ProjectOperationOutcomeDenied, ProjectAuthorizationNoGrant
		decisionErr = coreerrors.New(coreerrors.CodePermissionDenied, "permission denied")
	}
	if auditErr := s.record(ctx, operation); auditErr != nil {
		return ProjectResponse{}, auditErr
	}
	if decisionErr != nil {
		return ProjectResponse{}, decisionErr
	}
	response := projectResponse(project)
	response.AllowedActions = actions
	return response, nil
}

func (s *ProjectService) record(ctx context.Context, operation ProjectOperation) *coreerrors.Error {
	if err := s.repository.RecordProjectOperation(ctx, operation); err != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "project operation audit is unavailable", err)
	}
	return nil
}

// List returns Projects for the admin realm inside the verified tenant. Tenant
// admins receive the tenant-wide scope; every other principal receives only
// owner/viewer/editor rows selected by the typed repository data scope.
func (s *ProjectService) List(ctx context.Context, limitRaw, cursorRaw, requestID string) (ProjectListResponse, *coreerrors.Error) {
	return s.list(ctx, limitRaw, cursorRaw, requestID, true)
}

// ListForFront returns only Projects related to the verified front principal.
// It deliberately keeps TenantAdmin false even if the principal also has an
// admin-realm project.read.any policy binding.
func (s *ProjectService) ListForFront(ctx context.Context, limitRaw, cursorRaw, requestID string) (ProjectListResponse, *coreerrors.Error) {
	return s.list(ctx, limitRaw, cursorRaw, requestID, false)
}

func (s *ProjectService) list(ctx context.Context, limitRaw, cursorRaw, requestID string, allowTenantAdmin bool) (ProjectListResponse, *coreerrors.Error) {
	limit, ok := parseProjectListLimit(limitRaw)
	if !ok {
		return ProjectListResponse{}, coreerrors.New(coreerrors.CodeInvalidArgument, "project list limit must be between 1 and 100")
	}
	afterID, ok := parseProjectListCursor(cursorRaw)
	if !ok {
		return ProjectListResponse{}, coreerrors.New(coreerrors.CodeInvalidArgument, "project list cursor is invalid")
	}
	principal, ok := auth.FromContext(ctx)
	if !ok || strings.TrimSpace(principal.Subject) == "" || len([]rune(strings.TrimSpace(principal.Subject))) > 255 {
		return ProjectListResponse{}, coreerrors.New(coreerrors.CodeUnauthenticated, "missing authenticated principal")
	}
	actorTenantID, ok := tenantFromPrincipal(principal)
	if !ok {
		return ProjectListResponse{}, coreerrors.New(coreerrors.CodePermissionDenied, "tenant claim is required")
	}
	if s == nil || s.repository == nil {
		return ProjectListResponse{}, coreerrors.New(coreerrors.CodeUnavailable, "project persistence is unavailable")
	}

	scope := ProjectDataScope{TenantID: actorTenantID, Subject: strings.TrimSpace(principal.Subject), AfterID: afterID, Limit: limit}
	basis := ProjectAuthorizationRelationScope
	var permissions projectPermissions
	if allowTenantAdmin {
		var policyErr *coreerrors.Error
		permissions, policyErr = s.projectCapabilities(ctx)
		if policyErr == nil && !permissions[authz.CapabilityProjectRead] {
			policyErr = coreerrors.New(coreerrors.CodePermissionDenied, "permission denied")
		}
		if policyErr != nil {
			outcome, denialBasis := ProjectOperationOutcomeDenied, ProjectAuthorizationNoGrant
			if policyErr.Code != coreerrors.CodePermissionDenied {
				outcome, denialBasis = ProjectOperationOutcomeError, ProjectAuthorizationUnavailable
			}
			operation := newProjectOperation(actorTenantID, actorTenantID, 0, principal.Subject, requestID, outcome, denialBasis)
			operation.Operation = ProjectListOperation
			if auditErr := s.record(ctx, operation); auditErr != nil {
				return ProjectListResponse{}, auditErr
			}
			return ProjectListResponse{}, policyErr
		}
		scope.TenantAdmin = permissions[authz.CapabilityProjectReadAny]
		if scope.TenantAdmin {
			basis = ProjectAuthorizationTenantAdmin
		}
	}
	listRepository, ok := s.repository.(ProjectListRepository)
	if !ok {
		return ProjectListResponse{}, coreerrors.New(coreerrors.CodeUnavailable, "project list persistence is unavailable")
	}
	projects, err := listRepository.ListProjects(ctx, scope)
	if err != nil {
		return ProjectListResponse{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "project list persistence is unavailable", err)
	}
	response := ProjectListResponse{Items: make([]ProjectResponse, 0, min(len(projects), int(limit)))}
	if len(projects) > int(limit) {
		response.NextCursor = strconv.FormatUint(projects[limit-1].ID, 10)
		projects = projects[:limit]
	}
	for _, project := range projects {
		relation, relationErr := s.projectRelation(ctx, project, principal.Subject)
		if relationErr != nil {
			return ProjectListResponse{}, relationErr
		}
		item := projectResponse(project)
		if allowTenantAdmin {
			if !permissions.allows("read", relation) {
				continue
			}
			item.AllowedActions = permissions.actions(relation)
		} else {
			if relation == ProjectAuthorizationNoGrant {
				continue
			}
			item.AllowedActions = []string{"read"}
		}
		response.Items = append(response.Items, item)
	}
	operation := newProjectOperation(actorTenantID, actorTenantID, 0, principal.Subject, requestID, ProjectOperationOutcomeAllowed, basis)
	operation.Operation = ProjectListOperation
	if auditErr := s.record(ctx, operation); auditErr != nil {
		return ProjectListResponse{}, auditErr
	}
	return response, nil
}

// UpsertMember grants or replaces one non-owner Project relationship. The
// caller needs project.member.manage plus ownership or project.manage.any.
func (s *ProjectService) UpsertMember(ctx context.Context, id, subject, role, requestID string) (ProjectMemberMutationResponse, *coreerrors.Error) {
	return s.mutateMember(ctx, id, subject, role, requestID, false)
}

// RevokeMember revokes one matching active non-owner Project relationship.
// Its request role is intentional: it prevents a stale client from revoking a
// relationship after it was replaced with a different role.
func (s *ProjectService) RevokeMember(ctx context.Context, id, subject, role, requestID string) (ProjectMemberMutationResponse, *coreerrors.Error) {
	return s.mutateMember(ctx, id, subject, role, requestID, true)
}

// ProjectMemberMutationResponse is returned after the member mutation and its
// allowed audit event commit together. Changed is false for idempotent writes
// or a revoke that no longer matches an active relationship.
type ProjectMemberMutationResponse struct {
	Changed bool `json:"changed"`
}

// ProjectMemberResponse is an active non-owner Project relationship. A
// member's role remains local to one Project and must not be confused with a
// tenant-wide Casbin role.
type ProjectMemberResponse struct {
	Subject string            `json:"subject"`
	Role    ProjectMemberRole `json:"role"`
}

// ProjectMemberListResponse separates the immutable Project owner from
// revocable active collaborator relationships.
type ProjectMemberListResponse struct {
	OwnerSubject string                  `json:"ownerSubject"`
	Items        []ProjectMemberResponse `json:"items"`
}

// ProjectMemberListRepository is the optional repository capability needed to
// enumerate collaborators after application-layer authorization has completed.
// The typed arguments ensure the repository cannot widen tenant scope.
type ProjectMemberListRepository interface {
	ProjectRepository
	ListActiveMembers(context.Context, uint64, uint64, uint) ([]ProjectMemberResponse, error)
}

// ListMembers returns active collaborators only to the Project owner or a
// tenant administrator with project.member.manage. It intentionally does not
// use membership as a read grant: knowing one Project is not authority to
// enumerate its collaborators.
func (s *ProjectService) ListMembers(ctx context.Context, id, limitRaw, requestID string) (ProjectMemberListResponse, *coreerrors.Error) {
	projectID, ok := parseUnsignedIdentifier(id)
	if !ok {
		return ProjectMemberListResponse{}, coreerrors.New(coreerrors.CodeInvalidArgument, "project id is invalid")
	}
	limit, ok := parseProjectListLimit(limitRaw)
	if !ok {
		return ProjectMemberListResponse{}, coreerrors.New(coreerrors.CodeInvalidArgument, "project member list limit must be between 1 and 100")
	}
	principal, ok := auth.FromContext(ctx)
	if !ok || strings.TrimSpace(principal.Subject) == "" || len([]rune(strings.TrimSpace(principal.Subject))) > 255 {
		return ProjectMemberListResponse{}, coreerrors.New(coreerrors.CodeUnauthenticated, "missing authenticated principal")
	}
	actorTenantID, ok := tenantFromPrincipal(principal)
	if !ok {
		return ProjectMemberListResponse{}, coreerrors.New(coreerrors.CodePermissionDenied, "tenant claim is required")
	}
	if s == nil || s.repository == nil {
		return ProjectMemberListResponse{}, coreerrors.New(coreerrors.CodeUnavailable, "project persistence is unavailable")
	}

	project, err := s.repository.GetProject(ctx, projectID)
	if err != nil {
		if errors.Is(err, ErrProjectNotFound) {
			operation := newProjectOperation(actorTenantID, 0, projectID, principal.Subject, requestID, ProjectOperationOutcomeNotFound, ProjectAuthorizationNotFound)
			operation.Operation = ProjectMemberListOperation
			if auditErr := s.record(ctx, operation); auditErr != nil {
				return ProjectMemberListResponse{}, auditErr
			}
			return ProjectMemberListResponse{}, coreerrors.New(coreerrors.CodeNotFound, "project not found")
		}
		return ProjectMemberListResponse{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "project persistence is unavailable", err)
	}

	operation := newProjectOperation(actorTenantID, project.TenantID, project.ID, principal.Subject, requestID, "", "")
	operation.Operation = ProjectMemberListOperation
	if actorTenantID != project.TenantID {
		operation.Outcome = ProjectOperationOutcomeDenied
		operation.AuthorizationBasis = ProjectAuthorizationCrossTenant
		if auditErr := s.record(ctx, operation); auditErr != nil {
			return ProjectMemberListResponse{}, auditErr
		}
		return ProjectMemberListResponse{}, coreerrors.New(coreerrors.CodePermissionDenied, "permission denied")
	}
	basis, authorizationErr := s.authorizeProjectAction(ctx, project, principal, "members.manage")
	if authorizationErr != nil {
		operation.AuthorizationBasis = basis
		operation.Outcome = ProjectOperationOutcomeDenied
		if authorizationErr.Code != coreerrors.CodePermissionDenied {
			operation.Outcome = ProjectOperationOutcomeError
		}
		if auditErr := s.record(ctx, operation); auditErr != nil {
			return ProjectMemberListResponse{}, auditErr
		}
		return ProjectMemberListResponse{}, authorizationErr
	}
	operation.AuthorizationBasis = basis

	if operation.AuthorizationBasis == "" {
		operation.Outcome = ProjectOperationOutcomeDenied
		operation.AuthorizationBasis = ProjectAuthorizationNoGrant
		if auditErr := s.record(ctx, operation); auditErr != nil {
			return ProjectMemberListResponse{}, auditErr
		}
		return ProjectMemberListResponse{}, coreerrors.New(coreerrors.CodePermissionDenied, "permission denied")
	}

	listRepository, ok := s.repository.(ProjectMemberListRepository)
	if !ok {
		return ProjectMemberListResponse{}, coreerrors.New(coreerrors.CodeUnavailable, "project member list persistence is unavailable")
	}
	items, err := listRepository.ListActiveMembers(ctx, project.ID, project.TenantID, limit)
	if err != nil {
		return ProjectMemberListResponse{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "project member list persistence is unavailable", err)
	}
	operation.Outcome = ProjectOperationOutcomeAllowed
	if auditErr := s.record(ctx, operation); auditErr != nil {
		return ProjectMemberListResponse{}, auditErr
	}
	return ProjectMemberListResponse{OwnerSubject: project.OwnerSubject, Items: items}, nil
}

func (s *ProjectService) mutateMember(ctx context.Context, id, subject, role, requestID string, revoke bool) (ProjectMemberMutationResponse, *coreerrors.Error) {
	projectID, ok := parseUnsignedIdentifier(id)
	if !ok {
		return ProjectMemberMutationResponse{}, coreerrors.New(coreerrors.CodeInvalidArgument, "project id is invalid")
	}
	targetSubject := strings.TrimSpace(subject)
	targetRole := ProjectMemberRole(strings.TrimSpace(role))
	if !ValidProjectMemberSubject(targetSubject) || !targetRole.Valid() {
		return ProjectMemberMutationResponse{}, coreerrors.New(coreerrors.CodeInvalidArgument, "project member request is invalid")
	}
	principal, ok := auth.FromContext(ctx)
	if !ok || strings.TrimSpace(principal.Subject) == "" || len([]rune(strings.TrimSpace(principal.Subject))) > 255 {
		return ProjectMemberMutationResponse{}, coreerrors.New(coreerrors.CodeUnauthenticated, "missing authenticated principal")
	}
	actorTenantID, ok := tenantFromPrincipal(principal)
	if !ok {
		return ProjectMemberMutationResponse{}, coreerrors.New(coreerrors.CodePermissionDenied, "tenant claim is required")
	}
	if s == nil || s.repository == nil {
		return ProjectMemberMutationResponse{}, coreerrors.New(coreerrors.CodeUnavailable, "project persistence is unavailable")
	}

	operationName := ProjectMemberUpsertOperation
	if revoke {
		operationName = ProjectMemberRevokeOperation
	}
	project, err := s.repository.GetProject(ctx, projectID)
	if err != nil {
		if errors.Is(err, ErrProjectNotFound) {
			operation := newProjectMemberOperation(actorTenantID, 0, projectID, principal.Subject, targetSubject, targetRole, requestID, operationName, ProjectOperationOutcomeNotFound, ProjectAuthorizationNotFound)
			if auditErr := s.record(ctx, operation); auditErr != nil {
				return ProjectMemberMutationResponse{}, auditErr
			}
			return ProjectMemberMutationResponse{}, coreerrors.New(coreerrors.CodeNotFound, "project not found")
		}
		return ProjectMemberMutationResponse{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "project persistence is unavailable", err)
	}

	operation := newProjectMemberOperation(actorTenantID, project.TenantID, project.ID, principal.Subject, targetSubject, targetRole, requestID, operationName, "", "")
	if actorTenantID != project.TenantID {
		operation.Outcome = ProjectOperationOutcomeDenied
		operation.AuthorizationBasis = ProjectAuthorizationCrossTenant
		if auditErr := s.record(ctx, operation); auditErr != nil {
			return ProjectMemberMutationResponse{}, auditErr
		}
		return ProjectMemberMutationResponse{}, coreerrors.New(coreerrors.CodePermissionDenied, "permission denied")
	}
	if targetSubject == project.OwnerSubject {
		return ProjectMemberMutationResponse{}, coreerrors.New(coreerrors.CodeInvalidArgument, "project owner relationship is immutable")
	}
	basis, authorizationErr := s.authorizeProjectAction(ctx, project, principal, "members.manage")
	if authorizationErr != nil {
		operation.AuthorizationBasis = basis
		operation.Outcome = ProjectOperationOutcomeDenied
		if authorizationErr.Code != coreerrors.CodePermissionDenied {
			operation.Outcome = ProjectOperationOutcomeError
		}
		if auditErr := s.record(ctx, operation); auditErr != nil {
			return ProjectMemberMutationResponse{}, auditErr
		}
		return ProjectMemberMutationResponse{}, authorizationErr
	}
	operation.AuthorizationBasis = basis

	if operation.AuthorizationBasis == "" {
		operation.Outcome = ProjectOperationOutcomeDenied
		operation.AuthorizationBasis = ProjectAuthorizationNoGrant
		if auditErr := s.record(ctx, operation); auditErr != nil {
			return ProjectMemberMutationResponse{}, auditErr
		}
		return ProjectMemberMutationResponse{}, coreerrors.New(coreerrors.CodePermissionDenied, "permission denied")
	}

	mutationRepository, ok := s.repository.(ProjectMemberMutationRepository)
	if !ok {
		return ProjectMemberMutationResponse{}, coreerrors.New(coreerrors.CodeUnavailable, "project member persistence is unavailable")
	}
	operation.Outcome = ProjectOperationOutcomeAllowed
	changed, err := mutationRepository.MutateProjectMember(ctx, ProjectMemberMutation{
		ProjectID:     project.ID,
		ProjectTenant: project.TenantID,
		Subject:       targetSubject,
		Role:          targetRole,
		ActorSubject:  principal.Subject,
		Revoke:        revoke,
	}, operation)
	if err != nil {
		return ProjectMemberMutationResponse{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "project member mutation audit is unavailable", err)
	}
	return ProjectMemberMutationResponse{Changed: changed}, nil
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

func newProjectMemberOperation(actorTenantID uint64, projectTenantID uint64, projectID uint64, actorSubject, targetSubject string, targetRole ProjectMemberRole, requestID, operationName, outcome, basis string) ProjectOperation {
	operation := newProjectOperation(actorTenantID, projectTenantID, projectID, actorSubject, requestID, outcome, basis)
	operation.Operation = operationName
	operation.TargetSubject = boundedValue(targetSubject, 255)
	operation.TargetRole = boundedValue(string(targetRole), 64)
	return operation
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

func parseProjectListLimit(value string) (uint, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 20, true
	}
	parsed, err := strconv.ParseUint(value, 10, 8)
	if err != nil || parsed == 0 || parsed > 100 {
		return 0, false
	}
	return uint(parsed), true
}

func parseProjectListCursor(value string) (uint64, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, true
	}
	return parseUnsignedIdentifier(value)
}

func boundedValue(value string, maximum int) string {
	value = strings.TrimSpace(value)
	if maximum <= 0 || len([]rune(value)) <= maximum {
		return value
	}
	return string([]rune(value)[:maximum])
}

// ValidProjectMemberSubject validates subjects shared by services and repositories.
func ValidProjectMemberSubject(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len([]rune(value)) <= 255
}
