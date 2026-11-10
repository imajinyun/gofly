package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/auth"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/rest"
)

// Failure-mode inventory:
//   - front Project reads admit only owner/viewer/editor relationships;
//   - a front token cannot inherit the admin-only tenant-wide read expansion;
//   - an admin token cannot cross the exact /api/front/ realm boundary.
func TestRegisterRoutesFrontProjectReadContract(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		subject    string
		tenant     string
		realm      authn.Realm
		withToken  bool
		wantAudit  bool
		wantStatus int
		wantIDs    []string
		wantBasis  string
	}{
		{name: "missing credentials", path: "/api/front/projects/10001", wantStatus: http.StatusUnauthorized},
		{name: "front owner reads detail", path: "/api/front/projects/10001", subject: "owner-demo", tenant: "1001", realm: authn.RealmFront, withToken: true, wantAudit: true, wantStatus: http.StatusOK, wantBasis: app.ProjectAuthorizationOwner},
		{name: "front active viewer reads detail", path: "/api/front/projects/10001", subject: "viewer-member-demo", tenant: "1001", realm: authn.RealmFront, withToken: true, wantAudit: true, wantStatus: http.StatusOK, wantBasis: app.ProjectAuthorizationViewer},
		{name: "front casbin admin without relation is denied", path: "/api/front/projects/10001", subject: "admin-demo", tenant: "1001", realm: authn.RealmFront, withToken: true, wantAudit: true, wantStatus: http.StatusForbidden, wantBasis: app.ProjectAuthorizationNoGrant},
		{name: "front cross tenant owner is denied", path: "/api/front/projects/10001", subject: "owner-demo", tenant: "2002", realm: authn.RealmFront, withToken: true, wantAudit: true, wantStatus: http.StatusForbidden, wantBasis: app.ProjectAuthorizationCrossTenant},
		{name: "admin token is rejected by front interceptor", path: "/api/front/projects/10001", subject: "owner-demo", tenant: "1001", realm: authn.RealmAdmin, withToken: true, wantStatus: http.StatusUnauthorized},
		{name: "front owner lists relation scope", path: "/api/front/projects?limit=10", subject: "owner-demo", tenant: "1001", realm: authn.RealmFront, withToken: true, wantAudit: true, wantStatus: http.StatusOK, wantIDs: []string{"10001"}, wantBasis: app.ProjectAuthorizationRelationScope},
		{name: "front casbin admin list remains relation scoped", path: "/api/front/projects?limit=10", subject: "admin-demo", tenant: "1001", realm: authn.RealmFront, withToken: true, wantAudit: true, wantStatus: http.StatusOK, wantIDs: []string{}, wantBasis: app.ProjectAuthorizationRelationScope},
		{name: "front member mutation route is absent", method: http.MethodPut, path: "/api/front/projects/10001/members", subject: "owner-demo", tenant: "1001", realm: authn.RealmFront, withToken: true, wantStatus: http.StatusNotFound},
		{name: "front policy route is absent", path: "/api/front/authorization/roles", subject: "owner-demo", tenant: "1001", realm: authn.RealmFront, withToken: true, wantStatus: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := newProjectListRepository()
			repository.projects[10001] = app.Project{ID: 10001, TenantID: 1001, OwnerSubject: "owner-demo", Name: "Owned"}
			repository.members[projectMemberKey{projectID: 10001, tenantID: 1001, subject: "viewer-member-demo"}] = app.ProjectMemberRoleViewer
			server := newProjectAuthorizationServer(t, repository)
			method := tt.method
			if method == "" {
				method = http.MethodGet
			}
			req := httptest.NewRequest(method, tt.path, nil)
			req.Header.Set(rest.RequestIDHeader, "request-front-project-contract")
			if tt.withToken {
				req.Header.Set(auth.AuthorizationHeader, auth.BearerValue(projectAuthorizationRealmToken(t, tt.subject, tt.tenant, tt.realm)))
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
			if operation.AuthorizationBasis != tt.wantBasis {
				t.Fatalf("authorization basis = %q, want %q", operation.AuthorizationBasis, tt.wantBasis)
			}
			if tt.wantStatus == http.StatusOK && tt.wantIDs != nil {
				var response app.ProjectListResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
					t.Fatalf("decode front Project list: %v", err)
				}
				gotIDs := make([]string, len(response.Items))
				for index, project := range response.Items {
					gotIDs[index] = project.ID
				}
				if !equalStrings(gotIDs, tt.wantIDs) {
					t.Fatalf("front Project list IDs = %#v, want %#v", gotIDs, tt.wantIDs)
				}
			}
		})
	}
}

func projectAuthorizationRealmToken(t *testing.T, subject, tenant string, realm authn.Realm) string {
	t.Helper()
	token, err := auth.SignJWT(auth.JWTClaims{
		Subject:   subject,
		Issuer:    "gosky",
		Audience:  "gosky-web",
		IssuedAt:  time.Now().Add(-time.Minute).Unix(),
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
		Extra: map[string]any{
			"tenant_id": tenant,
			"realm":     string(realm),
			"sid":       "front-project-route-session",
		},
	}, []byte(projectAuthorizationTestSecret))
	if err != nil {
		t.Fatalf("sign %s project authorization token: %v", realm, err)
	}
	return token
}
