package auth

import (
	"context"
	"errors"
	"testing"
)

func TestPrincipalRoleChecks(t *testing.T) {
	p := Principal{Subject: "u1", Roles: []string{"admin", "ops"}}
	if !p.HasRole("admin") {
		t.Fatal("HasRole(admin) = false, want true")
	}
	if p.HasRole("guest") {
		t.Fatal("HasRole(guest) = true, want false")
	}
	if !p.HasAnyRole("guest", "ops") {
		t.Fatal("HasAnyRole(guest, ops) = false, want true")
	}
	if p.HasAnyRole("guest", "viewer") {
		t.Fatal("HasAnyRole(guest, viewer) = true, want false")
	}
	if !p.HasAllRoles("admin", "ops") {
		t.Fatal("HasAllRoles(admin, ops) = false, want true")
	}
	if p.HasAllRoles("admin", "guest") {
		t.Fatal("HasAllRoles(admin, guest) = true, want false")
	}
}

func TestPrincipalPermissionWildcards(t *testing.T) {
	p := Principal{Permissions: []string{"orders:*", "users:read"}}
	if !p.HasPermission("orders:read") {
		t.Fatal("orders:* should grant orders:read")
	}
	if !p.HasPermission("orders:write") {
		t.Fatal("orders:* should grant orders:write")
	}
	if !p.HasPermission("users:read") {
		t.Fatal("exact users:read should match")
	}
	if p.HasPermission("users:write") {
		t.Fatal("users:read should not grant users:write")
	}

	star := Principal{Permissions: []string{"*"}}
	if !star.HasPermission("anything:goes") {
		t.Fatal("* should grant anything")
	}
}

func TestRequirementSatisfied(t *testing.T) {
	p := Principal{Roles: []string{"admin"}, Permissions: []string{"orders:read"}}

	cases := []struct {
		name string
		req  Requirement
		want bool
	}{
		{"empty", Requirement{}, true},
		{"role ok", RequireRoles("admin"), true},
		{"role missing", RequireRoles("super"), false},
		{"any role ok", RequireAnyRole("super", "admin"), true},
		{"any role missing", RequireAnyRole("super", "guest"), false},
		{"perm ok", RequirePermissions("orders:read"), true},
		{"perm missing", RequirePermissions("orders:write"), false},
		{"any perm ok", RequireAnyPermission("orders:write", "orders:read"), true},
		{"combined fail", Requirement{Roles: []string{"admin"}, Permissions: []string{"orders:write"}}, false},
	}
	for _, tc := range cases {
		if got := tc.req.Satisfied(p); got != tc.want {
			t.Errorf("%s: Satisfied = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAuthorize(t *testing.T) {
	if err := Authorize(context.Background(), RequireRoles("admin")); !errors.Is(err, ErrMissingCredentials) {
		t.Fatalf("no principal err = %v, want ErrMissingCredentials", err)
	}

	ctx := NewContext(context.Background(), Principal{Roles: []string{"ops"}})
	if err := Authorize(ctx, RequireRoles("admin")); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("denied err = %v, want ErrPermissionDenied", err)
	}
	if err := Authorize(ctx, RequireRoles("ops")); err != nil {
		t.Fatalf("authorized err = %v, want nil", err)
	}
}

func TestJWTClaimsPrincipal(t *testing.T) {
	claims := JWTClaims{
		Subject: "u1",
		Extra: map[string]any{
			"roles":       []any{"admin", "ops"},
			"permissions": "orders:read orders:write",
		},
	}
	p := claims.Principal()
	if p.Subject != "u1" {
		t.Fatalf("subject = %q, want u1", p.Subject)
	}
	if !p.HasAllRoles("admin", "ops") {
		t.Fatalf("roles = %v, want admin,ops", p.Roles)
	}
	if !p.HasPermission("orders:read") || !p.HasPermission("orders:write") {
		t.Fatalf("permissions = %v missing expected", p.Permissions)
	}

	// scope fallback when permissions absent
	scoped := JWTClaims{Extra: map[string]any{"scope": "read write"}}.Principal()
	if !scoped.HasPermission("read") || !scoped.HasPermission("write") {
		t.Fatalf("scope fallback permissions = %v", scoped.Permissions)
	}

	// scp fallback
	scp := JWTClaims{Extra: map[string]any{"scp": []string{"a", "b"}}}.Principal()
	if !scp.HasPermission("a") || !scp.HasPermission("b") {
		t.Fatalf("scp fallback permissions = %v", scp.Permissions)
	}
}
