package app

import (
	"context"
	"errors"
	"testing"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

func managerContext(ctx context.Context) context.Context {
	return auth.NewContext(ctx, auth.Principal{
		Subject: "unit-manager",
		Claims:  map[string]any{"tenant_id": "5001"},
	})
}

// Failure-mode derived unit tests for the dynamic RBAC domain rules
// (DYNA-001/007/008/009) and menu tree projection.
func TestBuildMenuTree(t *testing.T) {
	menus := []MenuRecord{
		{ID: 10, Name: "root"},
		{ID: 20, ParentID: 10, Name: "child", Sort: 2},
		{ID: 30, ParentID: 10, Name: "first", Sort: 1},
		{ID: 40, ParentID: 20, Name: "leaf"},
	}
	tree := BuildMenuTree(menus)
	if len(tree) != 1 || tree[0].ID != 10 {
		t.Fatalf("tree roots = %#v", tree)
	}
	root := tree[0]
	if len(root.Children) != 2 || root.Children[0].ID != 30 || root.Children[1].ID != 20 {
		t.Fatalf("children order = %#v", root.Children)
	}
	if got := root.Children[1].Children[0].ID; got != 40 {
		t.Fatalf("leaf id = %d, want 40", got)
	}

	// A cyclic parent relation without a root is unreachable and must not
	// loop forever; the primary cycle rejection lives in service validation.
	cyclic := BuildMenuTree([]MenuRecord{
		{ID: 1, ParentID: 2}, {ID: 2, ParentID: 1},
	})
	if len(cyclic) != 0 {
		t.Fatalf("unrooted cyclic tree = %#v", cyclic)
	}
}

func TestNormalizeRoleInput(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input RoleInput
		valid bool
	}{
		{"custom role", RoleInput{Code: "auditor", Name: "Auditor"}, true},
		{"underscore code", RoleInput{Code: "project_auditor", Name: "Project auditor"}, true},
		{"reserved admin", RoleInput{Code: "admin", Name: "Admin"}, false},
		{"reserved reader", RoleInput{Code: "reader", Name: "Reader"}, false},
		{"uppercase", RoleInput{Code: "Auditor", Name: "Auditor"}, false},
		{"leading digit", RoleInput{Code: "1aud", Name: "Bad"}, false},
		{"empty name", RoleInput{Code: "aud", Name: ""}, false},
		{"too long", RoleInput{Code: "a" + string(make([]byte, 65)), Name: "Long"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := normalizeRoleInput(tc.input)
			if ok != tc.valid {
				t.Fatalf("normalizeRoleInput(%q) valid=%v want %v", tc.input.Code, ok, tc.valid)
			}
		})
	}
}

type fakeRBACStore struct {
	menus       map[uint64]MenuRecord
	roles       map[uint64]RoleRecord
	codes       map[string]struct{}
	created     []RoleRecord
	grantRoleID uint64
	grantMenus  []uint64
}

func newFakeRBACStore() *fakeRBACStore {
	return &fakeRBACStore{
		menus: map[uint64]MenuRecord{
			810000001: {ID: 810000001, Name: "Access Control", Type: 1, System: true, Visible: true},
			810000002: {ID: 810000002, ParentID: 810000001, Name: "Roles", Type: 2, System: true, Visible: true},
		},
		roles: map[uint64]RoleRecord{},
		codes: map[string]struct{}{},
	}
}

func (f *fakeRBACStore) ActiveRoleCodes(context.Context, string) (map[string]struct{}, error) {
	return f.codes, nil
}
func (f *fakeRBACStore) ListRoles(_ context.Context, _ uint64, _ bool) ([]RoleRecord, error) {
	out := make([]RoleRecord, 0, len(f.roles))
	for _, role := range f.roles {
		out = append(out, role)
	}
	return out, nil
}
func (f *fakeRBACStore) GetRole(_ context.Context, _ uint64, id uint64) (RoleRecord, bool, error) {
	r, ok := f.roles[id]
	return r, ok, nil
}
func (f *fakeRBACStore) GetRoleByCode(_ context.Context, _ uint64, code string) (RoleRecord, bool, error) {
	for _, r := range f.roles {
		if r.Code == code {
			return r, true, nil
		}
	}
	return RoleRecord{}, false, nil
}
func (f *fakeRBACStore) CreateRole(_ context.Context, tenantID uint64, input RoleInput, _ RBACActor) (RoleRecord, error) {
	if _, exists := f.codes[input.Code]; exists {
		return RoleRecord{}, ErrRBACDuplicateCode
	}
	role := RoleRecord{ID: uint64(len(f.roles) + 1), TenantID: tenantID, Code: input.Code, Name: input.Name, Remark: input.Remark, State: "active"}
	f.roles[role.ID] = role
	f.codes[role.Code] = struct{}{}
	f.created = append(f.created, role)
	return role, nil
}
func (f *fakeRBACStore) UpdateRole(context.Context, uint64, uint64, RoleUpdate, RBACActor) error {
	return nil
}
func (f *fakeRBACStore) SetRoleState(context.Context, uint64, uint64, bool, RBACActor) error {
	return nil
}
func (f *fakeRBACStore) DeleteRole(context.Context, uint64, uint64, RBACActor) error { return nil }
func (f *fakeRBACStore) ListMenus(_ context.Context, _ uint64, includeDisabled bool) ([]MenuRecord, error) {
	out := make([]MenuRecord, 0, len(f.menus))
	for _, menu := range f.menus {
		if includeDisabled || !menu.Disabled {
			out = append(out, menu)
		}
	}
	return out, nil
}
func (f *fakeRBACStore) GetMenu(_ context.Context, _ uint64, id uint64) (MenuRecord, bool, error) {
	m, ok := f.menus[id]
	return m, ok, nil
}
func (f *fakeRBACStore) CreateMenu(_ context.Context, _ uint64, input MenuInput, _ RBACActor) (MenuRecord, error) {
	id := uint64(820000000 + len(f.menus))
	menu := MenuRecord{ID: id, ParentID: input.ParentID, Name: input.Name, Type: input.Type, Sort: input.Sort, Path: input.Path, Component: input.Component, Capability: input.Capability, Visible: input.Visible}
	f.menus[id] = menu
	return menu, nil
}
func (f *fakeRBACStore) UpdateMenu(context.Context, uint64, uint64, MenuInput, RBACActor) error {
	return nil
}
func (f *fakeRBACStore) DeleteMenu(context.Context, uint64, uint64, RBACActor) error { return nil }
func (f *fakeRBACStore) RoleCapabilities(context.Context, uint64, string) ([]string, error) {
	return nil, nil
}
func (f *fakeRBACStore) ReplaceRoleGrants(_ context.Context, _ uint64, roleID uint64, menuIDs []uint64, _ RBACActor) error {
	f.grantRoleID = roleID
	f.grantMenus = append([]uint64(nil), menuIDs...)
	return nil
}
func (f *fakeRBACStore) MenusForSubject(context.Context, uint64, string) ([]MenuRecord, error) {
	return nil, nil
}
func (f *fakeRBACStore) EnsureTenantCatalog(context.Context, uint64) error           { return nil }
func (f *fakeRBACStore) EffectiveManagers(context.Context, uint64) ([]string, error) { return nil, nil }

type allowAllAuthorizer struct{ denied bool }

func (a allowAllAuthorizer) Authorize(context.Context, string, string) *coreerrors.Error {
	if a.denied {
		return coreerrors.New(coreerrors.CodePermissionDenied, "denied")
	}
	return nil
}
func (a allowAllAuthorizer) ReloadPolicy() error { return nil }

func TestCreateRoleValidation(t *testing.T) {
	service := NewRBACService(newFakeRBACStore(), allowAllAuthorizer{})
	ctx := managerContext(context.Background())

	for _, code := range []string{"admin", "writer", "reader", "Bad Code", "1bad"} {
		if _, err := service.CreateRole(ctx, RoleInput{Code: code, Name: "X"}); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("CreateRole(%q) err=%v, want invalid_argument", code, err)
		}
	}
	role, err := service.CreateRole(ctx, RoleInput{Code: "auditor", Name: "Auditor", Remark: "r"})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if role.ID == 0 || role.System {
		t.Fatalf("created role = %#v", role)
	}
}

// TestReplaceRoleGrantsResolvesFixedRoleID pins the SmartAdmin fixed role
// identifier to the tenant-local roles row. Reserved roles are seeded with
// auto-increment ids (1/2/3), while the browser contract sends 810000101-103;
// the store transaction must lock the resolved local id.
func TestReplaceRoleGrantsResolvesFixedRoleID(t *testing.T) {
	store := newFakeRBACStore()
	store.roles[3] = RoleRecord{ID: 3, TenantID: 5001, Code: authz.RoleAdmin, State: "active"}
	service := NewRBACService(store, allowAllAuthorizer{})

	if err := service.ReplaceRoleGrants(managerContext(context.Background()), FixedRoleAdminID, []uint64{810000171, 810000172}); err != nil {
		t.Fatalf("ReplaceRoleGrants: %v", err)
	}
	if store.grantRoleID != 3 {
		t.Fatalf("store role id = %d, want resolved local id 3", store.grantRoleID)
	}
	if len(store.grantMenus) != 2 || store.grantMenus[0] != 810000171 || store.grantMenus[1] != 810000172 {
		t.Fatalf("store menu ids = %v", store.grantMenus)
	}
}

func TestMenuInputValidation(t *testing.T) {
	store := newFakeRBACStore()
	service := NewRBACService(store, allowAllAuthorizer{})
	ctx := managerContext(context.Background())

	// Function point requires a registered capability.
	if _, err := service.CreateMenu(ctx, MenuInput{ParentID: 810000002, Name: "Hack", Type: 3}); err == nil || err.Code != coreerrors.CodeInvalidArgument {
		t.Fatalf("point without capability err=%v", err)
	}
	if _, err := service.CreateMenu(ctx, MenuInput{ParentID: 810000002, Name: "Hack", Type: 3, Capability: "system.exec"}); err == nil {
		t.Fatal("unregistered capability accepted")
	}
	// Catalogs and pages cannot carry capabilities.
	if _, err := service.CreateMenu(ctx, MenuInput{ParentID: 810000001, Name: "P", Type: 2, Capability: string(authz.CapabilityProjectRead)}); err == nil {
		t.Fatal("page with capability accepted")
	}
	// Unknown and point parents are rejected.
	if _, err := service.CreateMenu(ctx, MenuInput{ParentID: 999999, Name: "X", Type: 2}); err == nil {
		t.Fatal("unknown parent accepted")
	}
	// A valid page under the seeded catalog is created.
	menu, err := service.CreateMenu(ctx, MenuInput{ParentID: 810000001, Name: "Ops", Type: 2, Path: "/system/ops", Component: "system/ops/index.vue", Visible: true})
	if err != nil {
		t.Fatalf("create page: %v", err)
	}
	if menu.ID < 820000000 {
		t.Fatalf("custom menu id=%d", menu.ID)
	}
	// Seeded rows cannot be updated or deleted.
	store.menus[menu.ID] = MenuRecord{} // no-op guard for coverage
	if err := service.UpdateMenu(ctx, 810000002, MenuInput{ParentID: 810000001, Name: "Hijack", Type: 2}); err == nil || err.Code != coreerrors.CodeFailedPrecondition {
		t.Fatalf("system menu update err=%v, want failed_precondition", err)
	}
}

func TestFixedRoleCodeMapping(t *testing.T) {
	cases := map[uint64]string{
		FixedRoleReaderID: authz.RoleReader,
		FixedRoleWriterID: authz.RoleWriter,
		FixedRoleAdminID:  authz.RoleAdmin,
	}
	for id, want := range cases {
		got, ok := FixedRoleCode(id)
		if !ok || got != want {
			t.Fatalf("FixedRoleCode(%d)=(%q,%v), want %q", id, got, ok, want)
		}
	}
	if _, ok := FixedRoleCode(42); ok {
		t.Fatal("custom role id resolved as fixed")
	}
}

func TestRBACStoreErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		code coreerrors.Code
	}{
		{ErrRBACRoleNotFound, coreerrors.CodeNotFound},
		{ErrRBACDuplicateCode, coreerrors.CodeAlreadyExists},
		{ErrRBACRoleInUse, coreerrors.CodeFailedPrecondition},
		{ErrRBACReservedRole, coreerrors.CodeFailedPrecondition},
		{ErrRBACInvalidRelation, coreerrors.CodeInvalidArgument},
		{authz.ErrLastTenantAdmin, coreerrors.CodeFailedPrecondition},
	}
	for _, tc := range cases {
		mapped := rbacStoreError(tc.err)
		if mapped == nil || mapped.Code != tc.code {
			t.Fatalf("rbacStoreError(%v)=%v, want %s", tc.err, mapped, tc.code)
		}
	}
	if err := rbacStoreError(nil); err != nil {
		t.Fatalf("nil error mapped to %v", err)
	}
	if err := rbacStoreError(errors.New("boom")); err == nil || err.Code != coreerrors.CodeUnavailable {
		t.Fatalf("unknown error mapped to %v", err)
	}
}

func TestBuildDepartmentTree(t *testing.T) {
	departments := []Department{
		{ID: 1, Name: "Root", ParentID: 0},
		{ID: 2, Name: "Engineering", ParentID: 1, Sort: 2},
		{ID: 3, Name: "Platform", ParentID: 2},
		{ID: 4, Name: "Sales", ParentID: 1, Sort: 1},
	}
	tree := BuildDepartmentTree(departments)
	if len(tree) != 1 || tree[0].ID != 1 || len(tree[0].Children) != 2 {
		t.Fatalf("tree = %#v", tree)
	}
}

func TestRandomEmployeePassword(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 20; i++ {
		password, err := randomEmployeePassword()
		if err != nil || len(password) < 8 {
			t.Fatalf("password=%q err=%v", password, err)
		}
		seen[password] = struct{}{}
	}
	if len(seen) < 15 {
		t.Fatalf("password generator repeated: %d unique of 20", len(seen))
	}
}
