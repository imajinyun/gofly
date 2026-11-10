package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/imajinyun/gofly/core/auth"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/rest"
)

func TestRegisterRoutesProjectListDataScopeContract(t *testing.T) {
	tests := []struct {
		name       string
		subject    string
		tenant     string
		query      string
		withToken  bool
		wantStatus int
		wantIDs    []string
		wantCursor string
		wantBasis  string
		wantAudit  bool
		wantScope  *app.ProjectDataScope
	}{
		{name: "missing credentials", query: "?limit=2", wantStatus: http.StatusUnauthorized},
		{name: "owner sees owned project", subject: "owner-demo", tenant: "1001", query: "?limit=2", withToken: true, wantStatus: http.StatusOK, wantIDs: []string{"10001"}, wantBasis: app.ProjectAuthorizationRelationScope, wantAudit: true, wantScope: &app.ProjectDataScope{TenantID: 1001, Subject: "owner-demo", Limit: 2}},
		{name: "viewer sees related project", subject: "viewer-demo", tenant: "1001", query: "?limit=2", withToken: true, wantStatus: http.StatusOK, wantIDs: []string{"10002"}, wantBasis: app.ProjectAuthorizationRelationScope, wantAudit: true, wantScope: &app.ProjectDataScope{TenantID: 1001, Subject: "viewer-demo", Limit: 2}},
		{name: "reader without relation sees no project", subject: "reader-demo", tenant: "1001", query: "?limit=2", withToken: true, wantStatus: http.StatusOK, wantBasis: app.ProjectAuthorizationRelationScope, wantAudit: true, wantScope: &app.ProjectDataScope{TenantID: 1001, Subject: "reader-demo", Limit: 2}},
		{name: "admin sees tenant scope with keyset cursor", subject: "admin-demo", tenant: "1001", query: "?limit=2", withToken: true, wantStatus: http.StatusOK, wantIDs: []string{"10001", "10002"}, wantCursor: "10002", wantBasis: app.ProjectAuthorizationTenantAdmin, wantAudit: true, wantScope: &app.ProjectDataScope{TenantID: 1001, Subject: "admin-demo", TenantAdmin: true, Limit: 2}},
		{name: "invalid list limit", subject: "owner-demo", tenant: "1001", query: "?limit=0", withToken: true, wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := newProjectListRepository()
			server := newProjectAuthorizationServer(t, repository)
			req := httptest.NewRequest(http.MethodGet, "/api/admin/projects"+tt.query, nil)
			req.Header.Set(rest.RequestIDHeader, "request-project-list-contract")
			if tt.withToken {
				req.Header.Set(auth.AuthorizationHeader, auth.BearerValue(projectAuthorizationToken(t, tt.subject, tt.tenant)))
			}
			rec := httptest.NewRecorder()
			server.Handler().ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if !tt.wantAudit {
				if len(repository.operations) != 0 || repository.listScope != nil {
					t.Fatalf("unexpected persistence activity: operations=%#v scope=%#v", repository.operations, repository.listScope)
				}
				return
			}
			if len(repository.operations) != 1 || repository.operations[0].Operation != app.ProjectListOperation || repository.operations[0].Outcome != app.ProjectOperationOutcomeAllowed || repository.operations[0].AuthorizationBasis != tt.wantBasis || repository.operations[0].RequestID != "request-project-list-contract" {
				t.Fatalf("list audit operation = %#v", repository.operations)
			}
			if repository.listScope == nil || *repository.listScope != *tt.wantScope {
				t.Fatalf("list scope = %#v, want %#v", repository.listScope, tt.wantScope)
			}
			var response app.ProjectListResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode Project list response: %v", err)
			}
			gotIDs := make([]string, len(response.Items))
			for index, item := range response.Items {
				gotIDs[index] = item.ID
			}
			if !equalStrings(gotIDs, tt.wantIDs) || response.NextCursor != tt.wantCursor {
				t.Fatalf("list response = %#v, want ids=%#v cursor=%q", response, tt.wantIDs, tt.wantCursor)
			}
		})
	}
}

type projectListRepository struct {
	*projectAuthorizationRepository
	listScope *app.ProjectDataScope
}

func newProjectListRepository() *projectListRepository {
	return &projectListRepository{projectAuthorizationRepository: &projectAuthorizationRepository{
		projects: map[uint64]app.Project{
			10001: {ID: 10001, TenantID: 1001, OwnerSubject: "owner-demo", Name: "Owned"},
			10002: {ID: 10002, TenantID: 1001, OwnerSubject: "other-owner", Name: "Viewed"},
			10003: {ID: 10003, TenantID: 1001, OwnerSubject: "other-owner", Name: "Hidden"},
			20001: {ID: 20001, TenantID: 2002, OwnerSubject: "other-tenant-owner", Name: "Other tenant"},
		},
		members: map[projectMemberKey]app.ProjectMemberRole{
			{projectID: 10002, tenantID: 1001, subject: "viewer-demo"}: app.ProjectMemberRoleViewer,
		},
	}}
}

func (r *projectListRepository) ListProjects(_ context.Context, scope app.ProjectDataScope) ([]app.Project, error) {
	r.listScope = &scope
	projects := make([]app.Project, 0)
	for _, project := range r.projects {
		if project.TenantID != scope.TenantID || project.ID <= scope.AfterID {
			continue
		}
		role := r.members[projectMemberKey{projectID: project.ID, tenantID: scope.TenantID, subject: scope.Subject}]
		if !scope.TenantAdmin && project.OwnerSubject != scope.Subject && role != app.ProjectMemberRoleViewer && role != app.ProjectMemberRoleEditor {
			continue
		}
		projects = append(projects, project)
	}
	sort.Slice(projects, func(left, right int) bool { return projects[left].ID < projects[right].ID })
	if uint(len(projects)) > scope.Limit+1 {
		projects = projects[:scope.Limit+1]
	}
	return projects, nil
}

var _ app.ProjectListRepository = (*projectListRepository)(nil)

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
