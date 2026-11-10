package authz

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
)

// The prewritten failure inventory is in docs/authorization/explicit-admin-permissions.md.
// Legacy links are injected as deployment data to exercise the real Casbin boundary.
func TestExplicitRoleAuthorization(t *testing.T) {
	dir := t.TempDir()
	policy := `p, reader, 1001, project.read, EXECUTE
p, writer, 1001, project.update, EXECUTE
p, admin, 1001, authorization.role.manage, EXECUTE
g, writer, reader, 1001
g, admin, writer, 1001
g, alice, writer, 1001
g, bob, admin, 1001
g, carol, reader, 1001
g, carol, writer, 1001
`
	path := filepath.Join(dir, "policy.csv")
	if err := os.WriteFile(path, []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := NewFileAuthorizer("../../etc/authorization/casbin_model.conf", path, []byte(testJWTSecret))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	for _, tc := range []struct {
		name, subject, domain, capability string
		allowed                           bool
	}{
		{"direct writer grant", "alice", "1001", "project.update", true},
		{"writer cannot inherit reader", "alice", "1001", "project.read", false},
		{"admin cannot inherit writer", "bob", "1001", "project.update", false},
		{"admin direct grant", "bob", "1001", "authorization.role.manage", true},
		{"explicit multiple roles read", "carol", "1001", "project.read", true},
		{"explicit multiple roles update", "carol", "1001", "project.update", true},
		{"different tenant", "carol", "2002", "project.read", false},
		{"role name is not an identity", "admin", "1001", "authorization.role.manage", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := auth.NewContext(t.Context(), auth.Principal{Subject: tc.subject, Claims: map[string]any{"tenant_id": tc.domain, "realm": "admin"}})
			decision := a.Authorize(ctx, tc.capability, CapabilityAction)
			if tc.allowed && decision != nil || !tc.allowed && (decision == nil || decision.Code != coreerrors.CodePermissionDenied) {
				t.Fatalf("subject=%s domain=%s capability=%s allowed=%t decision=%v", tc.subject, tc.domain, tc.capability, tc.allowed, decision)
			}
		})
	}
	if err := os.WriteFile(filepath.Join(t.ArtifactDir(), "policy.csv"), []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
}
