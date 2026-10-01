package routes

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/auth"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
	"github.com/imajinyun/gofly/examples/gosky/internal/config"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

const projectAuthorizationTestSecret = "gosky-project-authorization-secret-at-least-32-bytes"

func TestRegisterRoutesProjectReadAuthorizationContract(t *testing.T) {
	tests := []struct {
		name            string
		subject         string
		tenant          string
		projectID       string
		withToken       bool
		auditErr        error
		wantStatus      int
		wantAudit       bool
		wantAuditResult string
		wantAuditBasis  string
	}{
		{name: "missing credentials", projectID: "10001", wantStatus: http.StatusUnauthorized},
		{name: "invalid numeric project identifier", subject: "owner-demo", tenant: "1001", projectID: "project-demo", withToken: true, wantStatus: http.StatusBadRequest},
		{name: "invalid numeric tenant claim", subject: "owner-demo", tenant: "tenant:demo", projectID: "10001", withToken: true, wantStatus: http.StatusForbidden},
		{name: "owner in same tenant", subject: "owner-demo", tenant: "1001", projectID: "10001", withToken: true, wantStatus: http.StatusOK, wantAudit: true, wantAuditResult: app.ProjectOperationOutcomeAllowed, wantAuditBasis: app.ProjectAuthorizationOwner},
		{name: "active member in same tenant", subject: "member-demo", tenant: "1001", projectID: "10001", withToken: true, wantStatus: http.StatusOK, wantAudit: true, wantAuditResult: app.ProjectOperationOutcomeAllowed, wantAuditBasis: app.ProjectAuthorizationMember},
		{name: "casbin reader in same tenant", subject: "reader-demo", tenant: "1001", projectID: "10001", withToken: true, wantStatus: http.StatusOK, wantAudit: true, wantAuditResult: app.ProjectOperationOutcomeAllowed, wantAuditBasis: app.ProjectAuthorizationPolicy},
		{name: "casbin writer inherits reader in same tenant", subject: "writer-demo", tenant: "1001", projectID: "10001", withToken: true, wantStatus: http.StatusOK, wantAudit: true, wantAuditResult: app.ProjectOperationOutcomeAllowed, wantAuditBasis: app.ProjectAuthorizationPolicy},
		{name: "casbin admin inherits writer and reader in same tenant", subject: "admin-demo", tenant: "1001", projectID: "10001", withToken: true, wantStatus: http.StatusOK, wantAudit: true, wantAuditResult: app.ProjectOperationOutcomeAllowed, wantAuditBasis: app.ProjectAuthorizationPolicy},
		{name: "same tenant without owner member or policy", subject: "viewer-demo", tenant: "1001", projectID: "10001", withToken: true, wantStatus: http.StatusForbidden, wantAudit: true, wantAuditResult: app.ProjectOperationOutcomeDenied, wantAuditBasis: app.ProjectAuthorizationNoGrant},
		{name: "cross tenant owner is denied", subject: "owner-demo", tenant: "2002", projectID: "10001", withToken: true, wantStatus: http.StatusForbidden, wantAudit: true, wantAuditResult: app.ProjectOperationOutcomeDenied, wantAuditBasis: app.ProjectAuthorizationCrossTenant},
		{name: "missing project", subject: "reader-demo", tenant: "1001", projectID: "10002", withToken: true, wantStatus: http.StatusNotFound, wantAudit: true, wantAuditResult: app.ProjectOperationOutcomeNotFound, wantAuditBasis: app.ProjectAuthorizationNotFound},
		{name: "audit failure fails closed", subject: "owner-demo", tenant: "1001", projectID: "10001", withToken: true, auditErr: errors.New("audit database unavailable"), wantStatus: http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := &projectAuthorizationRepository{
				projects: map[uint64]app.Project{
					10001: {ID: 10001, TenantID: 1001, OwnerSubject: "owner-demo", Name: "Gosky"},
				},
				members:  map[projectMemberKey]bool{{projectID: 10001, tenantID: 1001, subject: "member-demo"}: true},
				auditErr: tt.auditErr,
			}
			server := newProjectAuthorizationServer(t, repository)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+tt.projectID, nil)
			req.Header.Set(rest.RequestIDHeader, "request-project-contract")
			if tt.withToken {
				req.Header.Set(auth.AuthorizationHeader, auth.BearerValue(projectAuthorizationToken(t, tt.subject, tt.tenant)))
			}
			rec := httptest.NewRecorder()
			server.Handler().ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if !tt.wantAudit {
				if len(repository.operations) != 0 {
					t.Fatalf("operations = %#v, want none", repository.operations)
				}
				return
			}
			if len(repository.operations) != 1 {
				t.Fatalf("operations = %#v, want one record", repository.operations)
			}
			operation := repository.operations[0]
			if operation.Outcome != tt.wantAuditResult || operation.AuthorizationBasis != tt.wantAuditBasis {
				t.Fatalf("operation = %#v, want outcome=%q basis=%q", operation, tt.wantAuditResult, tt.wantAuditBasis)
			}
			if operation.RequestID != "request-project-contract" || operation.Operation != app.ProjectReadOperation {
				t.Fatalf("operation correlation = %#v", operation)
			}
		})
	}
}

func newProjectAuthorizationServer(t *testing.T, repository app.ProjectRepository) *rest.Server {
	t.Helper()
	authorizer := projectAuthorizationAuthorizer(t)
	serviceContext := svc.NewServiceContext(config.Config{})
	serviceContext.SetAuthorizer(authorizer)
	serviceContext.SetProjectService(app.NewProjectService(repository, authorizer))
	server := rest.MustNewServer(rest.Config{})
	RegisterRoutes(server, serviceContext)
	return server
}

func projectAuthorizationAuthorizer(t *testing.T) *authz.Authorizer {
	t.Helper()
	dir := t.TempDir()
	modelPath := filepath.Join(dir, "casbin_model.conf")
	policyPath := filepath.Join(dir, "casbin_policy.csv")
	const model = `[request_definition]
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
	const policy = `p, reader, 1001, /api/v1/projects/:id, GET
g, writer, reader, 1001
g, admin, writer, 1001
g, reader-demo, reader, 1001
g, writer-demo, writer, 1001
g, admin-demo, admin, 1001
`
	if err := os.WriteFile(modelPath, []byte(model), 0o600); err != nil {
		t.Fatalf("write casbin model: %v", err)
	}
	if err := os.WriteFile(policyPath, []byte(policy), 0o600); err != nil {
		t.Fatalf("write casbin policy: %v", err)
	}
	authorizer, err := authz.NewFileAuthorizer(modelPath, policyPath, []byte(projectAuthorizationTestSecret))
	if err != nil {
		t.Fatalf("new authorizer: %v", err)
	}
	return authorizer
}

func projectAuthorizationToken(t *testing.T, subject string, tenant string) string {
	t.Helper()
	token, err := auth.SignJWT(auth.JWTClaims{
		Subject:   subject,
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
		Extra:     map[string]any{"tenant_id": tenant},
	}, []byte(projectAuthorizationTestSecret))
	if err != nil {
		t.Fatalf("sign project authorization token: %v", err)
	}
	return token
}

type projectAuthorizationRepository struct {
	projects   map[uint64]app.Project
	members    map[projectMemberKey]bool
	operations []app.ProjectOperation
	auditErr   error
}

func (r *projectAuthorizationRepository) GetProject(_ context.Context, id uint64) (app.Project, error) {
	project, ok := r.projects[id]
	if !ok {
		return app.Project{}, app.ErrProjectNotFound
	}
	return project, nil
}

func (r *projectAuthorizationRepository) IsActiveMember(_ context.Context, projectID uint64, tenantID uint64, subject string) (bool, error) {
	return r.members[projectMemberKey{projectID: projectID, tenantID: tenantID, subject: subject}], nil
}

type projectMemberKey struct {
	projectID uint64
	tenantID  uint64
	subject   string
}

func (r *projectAuthorizationRepository) RecordProjectOperation(_ context.Context, operation app.ProjectOperation) error {
	if r.auditErr != nil {
		return r.auditErr
	}
	r.operations = append(r.operations, operation)
	return nil
}

var _ app.ProjectRepository = (*projectAuthorizationRepository)(nil)
