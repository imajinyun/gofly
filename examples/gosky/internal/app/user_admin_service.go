package app

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

const (
	minAdminPasswordLength = 8
	maxAdminPasswordLength = 255
	userSubjectPrefix      = "user:"
)

var adminLoginNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,254}$`)

// UserAdminService owns tenant user administration: bounded directory reads,
// user creation with Argon2id hashing, membership disable/enable, and
// password reset with session revocation.
type UserAdminService struct {
	users  UserAdminStore
	rbac   RBACStore
	policy RBACAuthorizer
}

func NewUserAdminService(users UserAdminStore, rbac RBACStore, policy RBACAuthorizer) *UserAdminService {
	return &UserAdminService{users: users, rbac: rbac, policy: policy}
}

func (s *UserAdminService) actor(ctx context.Context, tenantID uint64) RBACActor {
	return actorFromPrincipal(ctx, tenantID, "")
}

func (s *UserAdminService) authorize(ctx context.Context) (uint64, *coreerrors.Error) {
	if s == nil || s.users == nil || s.rbac == nil {
		return 0, coreerrors.New(coreerrors.CodeUnavailable, "user administration is unavailable")
	}
	if s.policy != nil {
		if err := s.policy.Authorize(ctx, string(authz.CapabilitySystemUserManage), authz.CapabilityAction); err != nil {
			return 0, err
		}
	}
	principal, ok := auth.FromContext(ctx)
	if !ok || strings.TrimSpace(principal.Subject) == "" {
		return 0, coreerrors.New(coreerrors.CodeUnauthenticated, "missing authenticated principal")
	}
	tenantID, valid := tenantFromPrincipal(principal)
	if !valid {
		return 0, coreerrors.New(coreerrors.CodePermissionDenied, "tenant claim is required")
	}
	return tenantID, nil
}

// ListUsers returns one bounded page of same-tenant users.
func (s *UserAdminService) ListUsers(ctx context.Context, pageNum, pageSize uint64, keywords string) (UserAdminPage, *coreerrors.Error) {
	tenantID, err := s.authorize(ctx)
	if err != nil {
		return UserAdminPage{}, err
	}
	page, queryErr := s.users.ListUsers(ctx, tenantID, pageNum, pageSize, strings.TrimSpace(keywords))
	if queryErr != nil {
		return UserAdminPage{}, rbacStoreError(queryErr)
	}
	return page, nil
}

// CreateUser validates input, hashes the initial password, and inserts the
// user plus its tenant membership in one transaction.
func (s *UserAdminService) CreateUser(ctx context.Context, input CreateUserInput) (UserRecord, *coreerrors.Error) {
	tenantID, err := s.authorize(ctx)
	if err != nil {
		return UserRecord{}, err
	}
	input, ok := normalizeCreateUserInput(input)
	if !ok {
		return UserRecord{}, coreerrors.New(coreerrors.CodeInvalidArgument, "user definition is invalid")
	}
	passwordHash, hashErr := authn.HashPassword(input.Password)
	if hashErr != nil {
		return UserRecord{}, coreerrors.New(coreerrors.CodeInvalidArgument, "password is invalid")
	}
	subject := userSubject(input.LoginName)
	user, queryErr := s.users.CreateUser(ctx, tenantID, input, subject, passwordHash, s.actor(ctx, tenantID))
	if queryErr != nil {
		return UserRecord{}, rbacStoreError(queryErr)
	}
	return user, nil
}

// SetMembershipEnabled toggles the tenant membership. Disabling revokes the
// membership scope's refresh sessions and is refused for the last tenant
// administrator.
func (s *UserAdminService) SetMembershipEnabled(ctx context.Context, userID uint64, enabled bool) *coreerrors.Error {
	tenantID, err := s.authorize(ctx)
	if err != nil {
		return err
	}
	if userID == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "user id is invalid")
	}
	if !enabled {
		if err := s.guardLastAdministrator(ctx, tenantID, userID); err != nil {
			return err
		}
	}
	if err := s.users.SetMembershipState(ctx, tenantID, userID, !enabled, s.actor(ctx, tenantID)); err != nil {
		return rbacStoreError(err)
	}
	return nil
}

// ResetPassword replaces the password hash and revokes the user's sessions in
// the same transaction.
func (s *UserAdminService) ResetPassword(ctx context.Context, userID uint64, password string) *coreerrors.Error {
	tenantID, err := s.authorize(ctx)
	if err != nil {
		return err
	}
	password = strings.TrimSpace(password)
	if len([]rune(password)) < minAdminPasswordLength || len([]rune(password)) > maxAdminPasswordLength {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "password must contain at least 8 characters")
	}
	passwordHash, hashErr := authn.HashPassword(password)
	if hashErr != nil {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "password is invalid")
	}
	if err := s.users.ResetPassword(ctx, tenantID, userID, passwordHash, s.actor(ctx, tenantID)); err != nil {
		return rbacStoreError(err)
	}
	return nil
}

// guardLastAdministrator refuses disabling the only identity that retains the
// complete management capability set in the tenant.
func (s *UserAdminService) guardLastAdministrator(ctx context.Context, tenantID, userID uint64) *coreerrors.Error {
	target, found, err := s.users.GetUser(ctx, tenantID, userID)
	if err != nil {
		return rbacStoreError(err)
	}
	if !found {
		return coreerrors.New(coreerrors.CodeNotFound, "user not found")
	}
	managers, queryErr := s.rbac.EffectiveManagers(ctx, tenantID)
	if queryErr != nil {
		return rbacStoreError(queryErr)
	}
	if !slices.Contains(managers, target.Subject) {
		return nil
	}
	if len(managers) == 1 {
		return coreerrors.New(coreerrors.CodeFailedPrecondition, "cannot disable the final tenant administrator")
	}
	return nil
}

func normalizeCreateUserInput(input CreateUserInput) (CreateUserInput, bool) {
	input.LoginName = strings.TrimSpace(input.LoginName)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.MemberRealm = strings.TrimSpace(input.MemberRealm)
	if !adminLoginNamePattern.MatchString(input.LoginName) {
		return CreateUserInput{}, false
	}
	if len([]rune(input.DisplayName)) > 255 {
		return CreateUserInput{}, false
	}
	if input.MemberRealm == "" {
		input.MemberRealm = "both"
	}
	if !slices.Contains([]string{"admin", "front", "both"}, input.MemberRealm) {
		return CreateUserInput{}, false
	}
	if len([]rune(input.Password)) < minAdminPasswordLength || len([]rune(input.Password)) > maxAdminPasswordLength {
		return CreateUserInput{}, false
	}
	return input, true
}

func userSubject(loginName string) string {
	return fmt.Sprintf("%s%s", userSubjectPrefix, loginName)
}
