package routes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/imajinyun/gofly/core/auth"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/rest"
)

// Failure-mode inventory:
//   - only the owner or a tenant admin can enumerate active collaborators;
//   - a related editor and a cross-tenant owner receive 403 without member data;
//   - the response keeps the owner separate, never as a revocable member row;
//   - a list-store failure returns 503 rather than a partial relationship list.
func TestRegisterRoutesProjectMemberListContract(t *testing.T) {
	tests := []struct {
		name        string
		subject     string
		tenant      string
		withToken   bool
		listErr     error
		wantStatus  int
		wantOwner   string
		wantMember  []app.ProjectMemberResponse
		wantAudit   bool
		wantOutcome string
		wantBasis   string
	}{
		{name: "missing credentials", wantStatus: http.StatusUnauthorized},
		{name: "owner lists active collaborators", subject: "owner-demo", tenant: "1001", withToken: true, wantStatus: http.StatusOK, wantOwner: "owner-demo", wantMember: []app.ProjectMemberResponse{{Subject: "editor-demo", Role: app.ProjectMemberRoleEditor}, {Subject: "viewer-demo", Role: app.ProjectMemberRoleViewer}}, wantAudit: true, wantOutcome: app.ProjectOperationOutcomeAllowed, wantBasis: app.ProjectAuthorizationOwner},
		{name: "tenant admin lists active collaborators", subject: "admin-demo", tenant: "1001", withToken: true, wantStatus: http.StatusOK, wantOwner: "owner-demo", wantMember: []app.ProjectMemberResponse{{Subject: "editor-demo", Role: app.ProjectMemberRoleEditor}, {Subject: "viewer-demo", Role: app.ProjectMemberRoleViewer}}, wantAudit: true, wantOutcome: app.ProjectOperationOutcomeAllowed, wantBasis: app.ProjectAuthorizationTenantAdmin},
		{name: "related editor cannot enumerate collaborators", subject: "editor-demo", tenant: "1001", withToken: true, wantStatus: http.StatusForbidden, wantAudit: true, wantOutcome: app.ProjectOperationOutcomeDenied, wantBasis: app.ProjectAuthorizationNoGrant},
		{name: "cross tenant owner cannot enumerate collaborators", subject: "owner-demo", tenant: "2002", withToken: true, wantStatus: http.StatusForbidden, wantAudit: true, wantOutcome: app.ProjectOperationOutcomeDenied, wantBasis: app.ProjectAuthorizationCrossTenant},
		{name: "membership store failure fails closed", subject: "owner-demo", tenant: "1001", withToken: true, listErr: errors.New("member list unavailable"), wantStatus: http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := newProjectMemberListRepository(tt.listErr)
			server := newProjectAuthorizationServer(t, repository)
			req := httptest.NewRequest(http.MethodGet, "/api/admin/projects/10001/members", nil)
			req.Header.Set(rest.RequestIDHeader, "request-project-member-list")
			if tt.withToken {
				req.Header.Set(auth.AuthorizationHeader, auth.BearerValue(projectAuthorizationToken(t, tt.subject, tt.tenant)))
			}
			rec := httptest.NewRecorder()
			server.Handler().ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantStatus == http.StatusOK {
				var response app.ProjectMemberListResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
					t.Fatalf("decode member list response: %v", err)
				}
				if response.OwnerSubject != tt.wantOwner || !equalProjectMemberResponses(response.Items, tt.wantMember) {
					t.Fatalf("member list response = %#v, want owner=%q members=%#v", response, tt.wantOwner, tt.wantMember)
				}
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
			if operation.Operation != app.ProjectMemberListOperation || operation.Outcome != tt.wantOutcome || operation.AuthorizationBasis != tt.wantBasis || operation.RequestID != "request-project-member-list" {
				t.Fatalf("member-list operation = %#v", operation)
			}
		})
	}
}

type projectMemberListRepository struct {
	*projectAuthorizationRepository
	listErr error
}

func newProjectMemberListRepository(listErr error) *projectMemberListRepository {
	return &projectMemberListRepository{
		projectAuthorizationRepository: &projectAuthorizationRepository{
			projects: map[uint64]app.Project{
				10001: {ID: 10001, TenantID: 1001, OwnerSubject: "owner-demo", Name: "Gosky"},
			},
			members: map[projectMemberKey]app.ProjectMemberRole{
				{projectID: 10001, tenantID: 1001, subject: "editor-demo"}: app.ProjectMemberRoleEditor,
				{projectID: 10001, tenantID: 1001, subject: "viewer-demo"}: app.ProjectMemberRoleViewer,
			},
		},
		listErr: listErr,
	}
}

func (r *projectMemberListRepository) ListActiveMembers(_ context.Context, projectID, tenantID uint64, limit uint) ([]app.ProjectMemberResponse, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	items := []app.ProjectMemberResponse{
		{Subject: "editor-demo", Role: app.ProjectMemberRoleEditor},
		{Subject: "viewer-demo", Role: app.ProjectMemberRoleViewer},
	}
	if projectID != 10001 || tenantID != 1001 || limit < uint(len(items)) {
		return nil, errors.New("unexpected member-list scope")
	}
	return items, nil
}

var _ app.ProjectMemberListRepository = (*projectMemberListRepository)(nil)

func equalProjectMemberResponses(got, want []app.ProjectMemberResponse) bool {
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
