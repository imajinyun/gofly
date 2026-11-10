package routes

import (
	"bytes"
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

func TestRegisterRoutesProjectMemberMutationContract(t *testing.T) {
	tests := []struct {
		name             string
		method           string
		subject          string
		tenant           string
		withToken        bool
		request          projectMemberMutationRequest
		mutationErr      error
		wantStatus       int
		wantChanged      bool
		wantMember       app.ProjectMemberRole
		wantMemberAbsent bool
		wantAudit        bool
		wantOutcome      string
		wantBasis        string
		wantOperation    string
	}{
		{
			name:          "missing credentials",
			method:        http.MethodPut,
			request:       projectMemberMutationRequest{Subject: "new-viewer", Role: app.ProjectMemberRoleViewer},
			wantStatus:    http.StatusUnauthorized,
			wantOperation: app.ProjectMemberUpsertOperation,
		},
		{
			name:          "owner grants viewer",
			method:        http.MethodPut,
			subject:       "owner-demo",
			tenant:        "1001",
			withToken:     true,
			request:       projectMemberMutationRequest{Subject: "new-viewer", Role: app.ProjectMemberRoleViewer},
			wantStatus:    http.StatusOK,
			wantChanged:   true,
			wantMember:    app.ProjectMemberRoleViewer,
			wantAudit:     true,
			wantOutcome:   app.ProjectOperationOutcomeAllowed,
			wantBasis:     app.ProjectAuthorizationOwner,
			wantOperation: app.ProjectMemberUpsertOperation,
		},
		{
			name:          "tenant admin grants editor",
			method:        http.MethodPut,
			subject:       "admin-demo",
			tenant:        "1001",
			withToken:     true,
			request:       projectMemberMutationRequest{Subject: "new-editor", Role: app.ProjectMemberRoleEditor},
			wantStatus:    http.StatusOK,
			wantChanged:   true,
			wantMember:    app.ProjectMemberRoleEditor,
			wantAudit:     true,
			wantOutcome:   app.ProjectOperationOutcomeAllowed,
			wantBasis:     app.ProjectAuthorizationTenantAdmin,
			wantOperation: app.ProjectMemberUpsertOperation,
		},
		{
			name:          "reader cannot grant member",
			method:        http.MethodPut,
			subject:       "reader-demo",
			tenant:        "1001",
			withToken:     true,
			request:       projectMemberMutationRequest{Subject: "new-viewer", Role: app.ProjectMemberRoleViewer},
			wantStatus:    http.StatusForbidden,
			wantAudit:     true,
			wantOutcome:   app.ProjectOperationOutcomeDenied,
			wantBasis:     app.ProjectAuthorizationNoGrant,
			wantOperation: app.ProjectMemberUpsertOperation,
		},
		{
			name:          "cross tenant owner cannot grant member",
			method:        http.MethodPut,
			subject:       "owner-demo",
			tenant:        "2002",
			withToken:     true,
			request:       projectMemberMutationRequest{Subject: "new-viewer", Role: app.ProjectMemberRoleViewer},
			wantStatus:    http.StatusForbidden,
			wantAudit:     true,
			wantOutcome:   app.ProjectOperationOutcomeDenied,
			wantBasis:     app.ProjectAuthorizationCrossTenant,
			wantOperation: app.ProjectMemberUpsertOperation,
		},
		{
			name:          "invalid role is rejected before persistence",
			method:        http.MethodPut,
			subject:       "owner-demo",
			tenant:        "1001",
			withToken:     true,
			request:       projectMemberMutationRequest{Subject: "new-viewer", Role: app.ProjectMemberRole("admin")},
			wantStatus:    http.StatusBadRequest,
			wantOperation: app.ProjectMemberUpsertOperation,
		},
		{
			name:             "owner revokes active editor",
			method:           http.MethodDelete,
			subject:          "owner-demo",
			tenant:           "1001",
			withToken:        true,
			request:          projectMemberMutationRequest{Subject: "existing-editor", Role: app.ProjectMemberRoleEditor},
			wantStatus:       http.StatusOK,
			wantChanged:      true,
			wantMemberAbsent: true,
			wantAudit:        true,
			wantOutcome:      app.ProjectOperationOutcomeAllowed,
			wantBasis:        app.ProjectAuthorizationOwner,
			wantOperation:    app.ProjectMemberRevokeOperation,
		},
		{
			name:          "transaction failure leaves relation and audit untouched",
			method:        http.MethodPut,
			subject:       "owner-demo",
			tenant:        "1001",
			withToken:     true,
			request:       projectMemberMutationRequest{Subject: "new-viewer", Role: app.ProjectMemberRoleViewer},
			mutationErr:   errors.New("audit insert failed"),
			wantStatus:    http.StatusServiceUnavailable,
			wantOperation: app.ProjectMemberUpsertOperation,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := newProjectMemberMutationRepository(tt.mutationErr)
			server := newProjectAuthorizationServer(t, repository)
			body, err := json.Marshal(tt.request)
			if err != nil {
				t.Fatalf("marshal member mutation request: %v", err)
			}
			req := httptest.NewRequest(tt.method, "/api/admin/projects/10001/members", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(rest.RequestIDHeader, "request-project-member-contract")
			if tt.withToken {
				req.Header.Set(auth.AuthorizationHeader, auth.BearerValue(projectAuthorizationToken(t, tt.subject, tt.tenant)))
			}
			rec := httptest.NewRecorder()
			server.Handler().ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}

			key := projectMemberKey{projectID: 10001, tenantID: 1001, subject: tt.request.Subject}
			if tt.wantMemberAbsent {
				if _, exists := repository.members[key]; exists {
					t.Fatalf("member %q still exists after revocation", tt.request.Subject)
				}
			} else if tt.wantMember != "" && repository.members[key] != tt.wantMember {
				t.Fatalf("member role = %q, want %q", repository.members[key], tt.wantMember)
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
			if operation.Outcome != tt.wantOutcome || operation.AuthorizationBasis != tt.wantBasis || operation.Operation != tt.wantOperation {
				t.Fatalf("operation = %#v, want outcome=%q basis=%q operation=%q", operation, tt.wantOutcome, tt.wantBasis, tt.wantOperation)
			}
			if operation.TargetSubject != tt.request.Subject || operation.TargetRole != string(tt.request.Role) || operation.RequestID != "request-project-member-contract" {
				t.Fatalf("member operation target/correlation = %#v", operation)
			}
			var response struct {
				Changed bool `json:"changed"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode member mutation response: %v", err)
			}
			if response.Changed != tt.wantChanged {
				t.Fatalf("changed = %t, want %t", response.Changed, tt.wantChanged)
			}
		})
	}
}

type projectMemberMutationRequest struct {
	Subject string                `json:"subject"`
	Role    app.ProjectMemberRole `json:"role"`
}

type projectMemberMutationRepository struct {
	*projectAuthorizationRepository
	mutationErr error
}

func newProjectMemberMutationRepository(mutationErr error) *projectMemberMutationRepository {
	return &projectMemberMutationRepository{
		projectAuthorizationRepository: &projectAuthorizationRepository{
			projects: map[uint64]app.Project{
				10001: {ID: 10001, TenantID: 1001, OwnerSubject: "owner-demo", Name: "Gosky"},
			},
			members: map[projectMemberKey]app.ProjectMemberRole{
				{projectID: 10001, tenantID: 1001, subject: "existing-editor"}: app.ProjectMemberRoleEditor,
			},
		},
		mutationErr: mutationErr,
	}
}

func (r *projectMemberMutationRepository) MutateProjectMember(_ context.Context, mutation app.ProjectMemberMutation, operation app.ProjectOperation) (bool, error) {
	if r.mutationErr != nil {
		return false, r.mutationErr
	}
	key := projectMemberKey{projectID: mutation.ProjectID, tenantID: mutation.ProjectTenant, subject: mutation.Subject}
	if mutation.Revoke {
		role, exists := r.members[key]
		if !exists || role != mutation.Role {
			r.operations = append(r.operations, operation)
			return false, nil
		}
		delete(r.members, key)
		r.operations = append(r.operations, operation)
		return true, nil
	}
	previous, exists := r.members[key]
	r.members[key] = mutation.Role
	r.operations = append(r.operations, operation)
	return !exists || previous != mutation.Role, nil
}

var _ app.ProjectMemberMutationRepository = (*projectMemberMutationRepository)(nil)
