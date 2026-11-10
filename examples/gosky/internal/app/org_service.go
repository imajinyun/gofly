package app

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

// ---------- departments ----------

// DepartmentService owns tenant department use cases.
type DepartmentService struct {
	store      OrgStore
	authorizer RBACAuthorizer
}

func NewDepartmentService(authorizer RBACAuthorizer, store OrgStore) *DepartmentService {
	return &DepartmentService{store: store, authorizer: authorizer}
}

func (s *DepartmentService) List(ctx context.Context) ([]Department, *coreerrors.Error) {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySystemUserManage)
	if err != nil {
		return nil, err
	}
	departments, queryErr := s.store.ListDepartments(ctx, tenantID)
	if queryErr != nil {
		return nil, coreerrors.Wrap(coreerrors.CodeUnavailable, "rbac storage is unavailable", queryErr)
	}
	return departments, nil
}

// Tree returns the department hierarchy for tree-style selectors.
func (s *DepartmentService) Tree(ctx context.Context) ([]Department, *coreerrors.Error) {
	departments, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	return BuildDepartmentTree(departments), nil
}

func (s *DepartmentService) Create(ctx context.Context, input DepartmentInput) (Department, *coreerrors.Error) {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySystemUserManage)
	if err != nil {
		return Department{}, err
	}
	department, queryErr := s.store.CreateDepartment(ctx, tenantID, input, actorFromPrincipal(ctx, 0, ""))
	return department, orgError(queryErr)
}

func (s *DepartmentService) Update(ctx context.Context, id uint64, input DepartmentInput) *coreerrors.Error {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySystemUserManage)
	if err != nil {
		return err
	}
	return orgError(s.store.UpdateDepartment(ctx, tenantID, id, input, actorFromPrincipal(ctx, 0, "")))
}

func (s *DepartmentService) Delete(ctx context.Context, id uint64) *coreerrors.Error {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySystemUserManage)
	if err != nil {
		return err
	}
	return orgError(s.store.DeleteDepartment(ctx, tenantID, id, actorFromPrincipal(ctx, 0, "")))
}

// BuildDepartmentTree groups a flat department list into parent->children.
func BuildDepartmentTree(departments []Department) []Department {
	byParent := make(map[uint64][]Department)
	for _, d := range departments {
		byParent[d.ParentID] = append(byParent[d.ParentID], d)
	}
	var build func(uint64) []Department
	build = func(parent uint64) []Department {
		nodes := byParent[parent]
		for i := range nodes {
			nodes[i].Children = build(nodes[i].ID)
		}
		return nodes
	}
	return build(0)
}

// ---------- positions ----------

// PositionService owns tenant job-title use cases.
type PositionService struct {
	store      OrgStore
	authorizer RBACAuthorizer
}

func NewPositionService(authorizer RBACAuthorizer, store OrgStore) *PositionService {
	return &PositionService{store: store, authorizer: authorizer}
}

func (s *PositionService) List(ctx context.Context, keyword string) ([]Position, *coreerrors.Error) {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySystemUserManage)
	if err != nil {
		return nil, err
	}
	positions, queryErr := s.store.ListPositions(ctx, tenantID, keyword)
	if queryErr != nil {
		return nil, coreerrors.Wrap(coreerrors.CodeUnavailable, "rbac storage is unavailable", queryErr)
	}
	return positions, nil
}

func (s *PositionService) Create(ctx context.Context, input PositionInput) (Position, *coreerrors.Error) {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySystemUserManage)
	if err != nil {
		return Position{}, err
	}
	position, queryErr := s.store.CreatePosition(ctx, tenantID, input, actorFromPrincipal(ctx, 0, ""))
	return position, orgError(queryErr)
}

func (s *PositionService) Update(ctx context.Context, id uint64, input PositionInput) *coreerrors.Error {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySystemUserManage)
	if err != nil {
		return err
	}
	return orgError(s.store.UpdatePosition(ctx, tenantID, id, input, actorFromPrincipal(ctx, 0, "")))
}

func (s *PositionService) Delete(ctx context.Context, id uint64) *coreerrors.Error {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySystemUserManage)
	if err != nil {
		return err
	}
	return orgError(s.store.DeletePosition(ctx, tenantID, id, actorFromPrincipal(ctx, 0, "")))
}

// ---------- employees ----------

// EmployeeService coordinates the employee directory, identity records,
// and Casbin role bindings. It is the SmartAdmin console counterpart of
// UserAdminService; both share the same users/tenant_users tables.
type EmployeeService struct {
	store      OrgStore
	rbac       RBACStore
	policy     RoleBindingWriter
	authorizer RBACAuthorizer
	hasher     func(string) (string, error)
}

// RoleBindingWriter grants/revokes subject->role bindings by code.
type RoleBindingWriter interface {
	GrantRoles(ctx context.Context, subjects []string, role string, requestID string) (bool, *coreerrors.Error)
	RevokeRoles(ctx context.Context, subjects []string, role string, requestID string) (bool, *coreerrors.Error)
}

func NewEmployeeService(authorizer RBACAuthorizer, store OrgStore, rbac RBACStore, policy RoleBindingWriter) *EmployeeService {
	return &EmployeeService{store: store, rbac: rbac, policy: policy, authorizer: authorizer, hasher: authn.HashPassword}
}

func (s *EmployeeService) List(ctx context.Context, query EmployeeQuery) (EmployeePage, *coreerrors.Error) {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySystemUserManage)
	if err != nil {
		return EmployeePage{}, err
	}
	page, queryErr := s.store.ListEmployees(ctx, tenantID, query)
	if queryErr != nil {
		return EmployeePage{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "rbac storage is unavailable", queryErr)
	}
	return page, nil
}

func (s *EmployeeService) ListAll(ctx context.Context, includeDisabled bool) ([]Employee, *coreerrors.Error) {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySystemUserManage)
	if err != nil {
		return nil, err
	}
	employees, queryErr := s.store.ListAllEmployees(ctx, tenantID, includeDisabled)
	if queryErr != nil {
		return nil, coreerrors.Wrap(coreerrors.CodeUnavailable, "rbac storage is unavailable", queryErr)
	}
	return employees, nil
}

func (s *EmployeeService) ByDepartment(ctx context.Context, departmentID uint64) ([]Employee, *coreerrors.Error) {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySystemUserManage)
	if err != nil {
		return nil, err
	}
	employees, queryErr := s.store.EmployeesByDepartment(ctx, tenantID, departmentID)
	if queryErr != nil {
		return nil, coreerrors.Wrap(coreerrors.CodeUnavailable, "rbac storage is unavailable", queryErr)
	}
	return employees, nil
}

func (s *EmployeeService) Create(ctx context.Context, input EmployeeInput) (Employee, string, *coreerrors.Error) {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySystemUserManage)
	if err != nil {
		return Employee{}, "", err
	}
	if cErr := validateEmployeeInput(input, true); cErr != nil {
		return Employee{}, "", cErr
	}
	if cErr := s.validatePlacement(ctx, tenantID, input); cErr != nil {
		return Employee{}, "", cErr
	}
	password := strings.TrimSpace(input.Password)
	if password == "" {
		var genErr error
		password, genErr = randomEmployeePassword()
		if genErr != nil {
			return Employee{}, "", coreerrors.Wrap(coreerrors.CodeInternal, "generate password", genErr)
		}
	}
	passwordHash, hashErr := s.hasher(password)
	if hashErr != nil {
		return Employee{}, "", coreerrors.Wrap(coreerrors.CodeInternal, "hash password", hashErr)
	}
	input.Password = passwordHash
	subject := userSubject(strings.TrimSpace(input.LoginName))
	actor := actorFromPrincipal(ctx, 0, "")
	employee, queryErr := s.store.CreateEmployee(ctx, tenantID, input, subject, actor)
	if queryErr != nil {
		return Employee{}, "", orgError(queryErr)
	}
	if cErr := s.replaceRoleBindings(ctx, tenantID, subject, input.RoleIDs, actor); cErr != nil {
		return Employee{}, "", cErr
	}
	employee, _ = s.refetch(ctx, tenantID, subject)
	return employee, password, nil
}

func (s *EmployeeService) Update(ctx context.Context, input EmployeeInput) *coreerrors.Error {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySystemUserManage)
	if err != nil {
		return err
	}
	if input.EmployeeID == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "user id is invalid")
	}
	if cErr := validateEmployeeInput(input, false); cErr != nil {
		return cErr
	}
	if cErr := s.validatePlacement(ctx, tenantID, input); cErr != nil {
		return cErr
	}
	actor := actorFromPrincipal(ctx, 0, "")
	if queryErr := s.store.UpdateEmployee(ctx, tenantID, input, actor); queryErr != nil {
		return orgError(queryErr)
	}
	subject, subjectErr := s.subjectByUserID(ctx, tenantID, input.EmployeeID)
	if subjectErr != nil {
		return subjectErr
	}
	return s.replaceRoleBindings(ctx, tenantID, subject, input.RoleIDs, actor)
}

func (s *EmployeeService) SetDisabled(ctx context.Context, userID uint64, disabled bool) *coreerrors.Error {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySystemUserManage)
	if err != nil {
		return err
	}
	if userID == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "user id is invalid")
	}
	return orgError(s.store.SetEmployeeDisabled(ctx, tenantID, userID, disabled, actorFromPrincipal(ctx, 0, "")))
}

func (s *EmployeeService) Delete(ctx context.Context, ids []uint64) *coreerrors.Error {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySystemUserManage)
	if err != nil {
		return err
	}
	if len(ids) == 0 || len(ids) > 100 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "user ids are invalid")
	}
	return orgError(s.store.DeleteEmployees(ctx, tenantID, ids, actorFromPrincipal(ctx, 0, "")))
}

func (s *EmployeeService) MoveDepartment(ctx context.Context, ids []uint64, departmentID uint64) *coreerrors.Error {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySystemUserManage)
	if err != nil {
		return err
	}
	return orgError(s.store.MoveEmployeesDepartment(ctx, tenantID, ids, departmentID, actorFromPrincipal(ctx, 0, "")))
}

// ResetPassword generates a new random password when newPassword is empty;
// otherwise newPassword is used. The generated/used password is returned.
func (s *EmployeeService) ResetPassword(ctx context.Context, userID uint64, newPassword string) (string, *coreerrors.Error) {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySystemUserManage)
	if err != nil {
		return "", err
	}
	if userID == 0 {
		return "", coreerrors.New(coreerrors.CodeInvalidArgument, "user id is invalid")
	}
	password := strings.TrimSpace(newPassword)
	if password == "" {
		var genErr error
		password, genErr = randomEmployeePassword()
		if genErr != nil {
			return "", coreerrors.Wrap(coreerrors.CodeInternal, "generate password", genErr)
		}
	} else if len(password) < minAdminPasswordLength {
		return "", coreerrors.New(coreerrors.CodeInvalidArgument, "password must contain at least 8 characters")
	}
	passwordHash, hashErr := s.hasher(password)
	if hashErr != nil {
		return "", coreerrors.Wrap(coreerrors.CodeInternal, "hash password", hashErr)
	}
	if queryErr := s.store.ResetEmployeePassword(ctx, tenantID, userID, passwordHash, actorFromPrincipal(ctx, 0, "")); queryErr != nil {
		return "", orgError(queryErr)
	}
	return password, nil
}

func (s *EmployeeService) UpdateCenter(ctx context.Context, userID uint64, input EmployeeCenterInput) *coreerrors.Error {
	principal, ok := auth.FromContext(ctx)
	if !ok {
		return coreerrors.New(coreerrors.CodeUnauthenticated, "missing authenticated principal")
	}
	tenantID, ok := tenantFromPrincipal(principal)
	if !ok || userID == 0 {
		return coreerrors.New(coreerrors.CodeUnauthenticated, "missing authenticated principal")
	}
	if strings.TrimSpace(input.ActualName) == "" || len(input.Phone) > 20 || len(input.Email) > 100 || input.Gender < 0 || input.Gender > 2 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "profile is invalid")
	}
	return orgError(s.store.UpdateEmployeeCenter(ctx, tenantID, userID, input, actorFromPrincipal(ctx, 0, "")))
}

func (s *EmployeeService) UpdateAvatar(ctx context.Context, userID uint64, avatar string) *coreerrors.Error {
	principal, ok := auth.FromContext(ctx)
	if !ok {
		return coreerrors.New(coreerrors.CodeUnauthenticated, "missing authenticated principal")
	}
	tenantID, ok := tenantFromPrincipal(principal)
	if !ok || userID == 0 {
		return coreerrors.New(coreerrors.CodeUnauthenticated, "missing authenticated principal")
	}
	if len(avatar) > 255 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "avatar reference is too long")
	}
	return orgError(s.store.UpdateEmployeeAvatar(ctx, tenantID, userID, avatar, actorFromPrincipal(ctx, 0, "")))
}

func (s *EmployeeService) validatePlacement(ctx context.Context, tenantID uint64, input EmployeeInput) *coreerrors.Error {
	if input.DepartmentID == 0 {
		// Default to the tenant's root department when the console
		// submits no explicit placement.
		departments, err := s.store.ListDepartments(ctx, tenantID)
		if err != nil {
			return orgError(err)
		}
		for _, d := range departments {
			if d.ParentID == 0 {
				input.DepartmentID = d.ID
				break
			}
		}
		if input.DepartmentID == 0 {
			return coreerrors.New(coreerrors.CodeFailedPrecondition, "tenant has no department")
		}
	}
	if input.PositionID != 0 {
		positions, err := s.store.ListPositions(ctx, tenantID, "")
		if err != nil {
			return orgError(err)
		}
		valid := false
		for _, p := range positions {
			if p.ID == input.PositionID {
				valid = true
				break
			}
		}
		if !valid {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "position is invalid")
		}
	}
	return nil
}

// replaceRoleBindings reconciles one subject's fixed/custom role set with
// the requested SmartAdmin role IDs. Roles absent from the request are
// revoked; roles the subject did not already hold are granted.
func (s *EmployeeService) replaceRoleBindings(ctx context.Context, tenantID uint64, subject string, roleIDs []uint64, actor RBACActor) *coreerrors.Error {
	want := make(map[string]struct{})
	for _, id := range roleIDs {
		if code, ok := FixedRoleCode(id); ok {
			want[code] = struct{}{}
			continue
		}
		role, found, err := s.rbac.GetRole(ctx, tenantID, id)
		if err != nil {
			return coreerrors.Wrap(coreerrors.CodeUnavailable, "rbac storage is unavailable", err)
		}
		if !found || role.System || role.State != "active" {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "unknown gosky role")
		}
		want[role.Code] = struct{}{}
	}
	for _, code := range []string{authz.RoleReader, authz.RoleWriter, authz.RoleAdmin} {
		// Fixed role membership is compared through the current bindings.
		has := s.subjectHasRole(ctx, tenantID, subject, code)
		_, requested := want[code]
		if requested && !has {
			if _, err := s.policy.GrantRoles(ctx, []string{subject}, code, actor.RequestID); err != nil {
				return policyMutationError(err)
			}
		}
		if !requested && has {
			if _, err := s.policy.RevokeRoles(ctx, []string{subject}, code, actor.RequestID); err != nil {
				return policyMutationError(err)
			}
		}
		delete(want, code)
	}
	roles, err := s.rbac.ListRoles(ctx, tenantID, true)
	if err != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "rbac storage is unavailable", err)
	}
	for _, role := range roles {
		_, requested := want[role.Code]
		has := s.subjectHasRole(ctx, tenantID, subject, role.Code)
		if requested && !has {
			if _, err := s.policy.GrantRoles(ctx, []string{subject}, role.Code, actor.RequestID); err != nil {
				return policyMutationError(err)
			}
		}
		if !requested && has {
			if _, err := s.policy.RevokeRoles(ctx, []string{subject}, role.Code, actor.RequestID); err != nil {
				return policyMutationError(err)
			}
		}
	}
	return nil
}

func (s *EmployeeService) subjectHasRole(ctx context.Context, tenantID uint64, subject, code string) bool {
	codes, err := s.store.SubjectRoles(ctx, tenantID, subject)
	if err != nil {
		return false
	}
	for _, current := range codes {
		if current == code {
			return true
		}
	}
	return false
}

func (s *EmployeeService) subjectByUserID(ctx context.Context, tenantID, userID uint64) (string, *coreerrors.Error) {
	page, err := s.store.ListEmployees(ctx, tenantID, EmployeeQuery{PageNum: 1, PageSize: 100, IncludeDeleted: true})
	if err != nil {
		return "", coreerrors.Wrap(coreerrors.CodeUnavailable, "rbac storage is unavailable", err)
	}
	for _, e := range page.List {
		if e.UserID == userID {
			return e.Subject, nil
		}
	}
	return "", coreerrors.New(coreerrors.CodeNotFound, "rbac object not found")
}

func (s *EmployeeService) refetch(ctx context.Context, tenantID uint64, subject string) (Employee, error) {
	employees, err := s.store.ListAllEmployees(ctx, tenantID, true)
	if err != nil {
		return Employee{}, err
	}
	for _, e := range employees {
		if e.Subject == subject {
			return e, nil
		}
	}
	return Employee{}, errors.New("created employee not found")
}

func validateEmployeeInput(input EmployeeInput, create bool) *coreerrors.Error {
	name := strings.TrimSpace(input.LoginName)
	if create {
		if !adminLoginNamePattern.MatchString(name) || len(name) > 255 {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "user definition is invalid")
		}
		if strings.TrimSpace(input.ActualName) == "" || len([]rune(input.ActualName)) > 255 {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "actual name is required")
		}
		if pw := strings.TrimSpace(input.Password); pw != "" && len([]rune(pw)) < minAdminPasswordLength {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "password must contain at least 8 characters")
		}
	}
	if len(input.Phone) > 20 || len(input.Email) > 100 || input.Gender < 0 || input.Gender > 2 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "profile is invalid")
	}
	return nil
}

const employeePasswordAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func randomEmployeePassword() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = employeePasswordAlphabet[int(buf[i])%len(employeePasswordAlphabet)]
	}
	return "Gosky" + string(buf) + "1", nil
}

func orgError(err error) *coreerrors.Error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrOrgNotFound):
		return coreerrors.New(coreerrors.CodeNotFound, "rbac object not found")
	case errors.Is(err, ErrOrgDuplicate):
		return coreerrors.New(coreerrors.CodeAlreadyExists, "rbac object already exists")
	case errors.Is(err, ErrOrgReserved):
		return coreerrors.New(coreerrors.CodeFailedPrecondition, "seeded rbac object is read-only")
	case errors.Is(err, ErrOrgInvalidRelation), errors.Is(err, ErrOrgInvalidInput):
		return coreerrors.New(coreerrors.CodeInvalidArgument, "rbac object is invalid")
	case errors.Is(err, ErrOrgHasMembers), errors.Is(err, ErrOrgHasChildren):
		return coreerrors.New(coreerrors.CodeFailedPrecondition, "rbac object still in use")
	default:
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "rbac storage is unavailable", err)
	}
}

func policyMutationError(err error) *coreerrors.Error {
	switch {
	case errors.Is(err, authz.ErrLastTenantAdmin):
		return coreerrors.New(coreerrors.CodeFailedPrecondition, "cannot remove the final tenant administrator")
	case errors.Is(err, authz.ErrInvalidPolicyRule):
		return coreerrors.New(coreerrors.CodeInvalidArgument, "policy role request is invalid")
	default:
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "authorization policy is unavailable", err)
	}
}
