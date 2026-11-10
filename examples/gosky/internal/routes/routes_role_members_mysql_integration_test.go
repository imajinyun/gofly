//go:build integration

package routes

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
)

// AUTHN-041 preserves the HTTP-to-policy/identity contracts while ownership
// moves from the browser adapter into a Gosky application service.
func TestMySQLSmartWebRoleMembersAndScopesIntegration(t *testing.T) {
	f := newRoleMenuFixture(t)
	admin := f.login(t, "manager", 0, "admin")
	reader := f.login(t, "reader", 0, "admin")
	var directory smartweb.ResponseDTO[smartweb.RoleEmployeePageVO]
	f.request(t, http.MethodPost, "/employee/query", admin, map[string]any{"pageNum": 1, "pageSize": 20}, http.StatusOK, &directory)
	var readerID, managerID uint64
	for _, user := range directory.Data.List {
		switch user.LoginName {
		case f.subject("reader"):
			readerID = user.EmployeeID
		case f.subject("manager"):
			managerID = user.EmployeeID
		}
	}
	if readerID == 0 || managerID == 0 {
		t.Fatal("active tenant directory omitted fixture identities")
	}
	result, err := f.db.ExecContext(f.ctx, "INSERT INTO users (subject, login_name, password_hash) VALUES (?, ?, ?)", f.subject("foreign"), f.subject("foreign"), integrationPasswordHash("correct-password"))
	if err != nil {
		t.Fatal(err)
	}
	foreignID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, query := range []string{"DELETE FROM tenant_users WHERE user_id = ?", "DELETE FROM users WHERE user_id = ?"} {
			if _, err := f.db.ExecContext(ctx, query, foreignID); err != nil {
				t.Errorf("cleanup foreign fixture: %v", err)
			}
		}
	})
	if _, err := f.db.ExecContext(f.ctx, "INSERT INTO tenant_users (tenant_id, user_id, member_realm) VALUES (?, ?, 'admin')", f.domains[1], foreignID); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		ids    any
		role   uint64
		token  string
		status int
	}{
		{"cross tenant batch", []any{readerID, foreignID}, 810000102, admin, http.StatusBadRequest},
		{"duplicate IDs", []uint64{readerID, readerID}, 810000102, admin, http.StatusBadRequest},
		{"empty batch", []uint64{}, 810000102, admin, http.StatusBadRequest},
		{"zero ID", []uint64{0}, 810000102, admin, http.StatusBadRequest},
		{"unknown role", []uint64{readerID}, 99999, admin, http.StatusBadRequest},
		{"unauthorized caller", []uint64{readerID}, 810000102, reader, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := f.eventCount(t)
			f.request(t, http.MethodPost, "/role/employee/batchAddRoleEmployee", tc.token, map[string]any{"roleId": tc.role, "employeeIdList": tc.ids}, tc.status, nil)
			if f.eventCount(t) != before {
				t.Fatal("rejected batch created policy events")
			}
		})
	}
	memberRequest := map[string]any{"roleId": uint64(810000102), "employeeIdList": []uint64{readerID}}
	f.request(t, http.MethodPost, "/role/employee/batchAddRoleEmployee", admin, memberRequest, http.StatusOK, nil)
	var members smartweb.ResponseDTO[smartweb.RoleEmployeePageVO]
	f.request(t, http.MethodPost, "/role/employee/queryEmployee", admin, map[string]any{"roleId": uint64(810000102), "pageNum": 1, "pageSize": 20}, http.StatusOK, &members)
	if members.Data.Total != 1 || len(members.Data.List) != 1 || members.Data.List[0].EmployeeID != readerID || !slices.Equal(members.Data.List[0].RoleIDList, []uint64{810000102}) || !slices.Equal(members.Data.List[0].RoleNameList, []string{"Writer"}) {
		t.Fatalf("writer member projection = %+v", members.Data)
	}
	f.request(t, http.MethodPost, "/role/employee/batchRemoveRoleEmployee", admin, memberRequest, http.StatusOK, nil)
	f.request(t, http.MethodPost, "/role/employee/queryEmployee", admin, map[string]any{"roleId": uint64(810000102)}, http.StatusOK, &members)
	if members.Data.Total != 0 || len(members.Data.List) != 0 {
		t.Fatalf("removed writer still listed: %+v", members.Data)
	}
	before := f.eventCount(t)
	f.request(t, http.MethodPost, "/role/employee/batchRemoveRoleEmployee", admin, map[string]any{"roleId": uint64(810000103), "employeeIdList": []uint64{managerID}}, http.StatusBadRequest, nil)
	if f.eventCount(t) != before {
		t.Fatal("final admin rejection wrote events")
	}

	f.save(t, admin, []uint64{810000011, 810000016}, http.StatusOK)
	for _, tc := range []struct {
		name       string
		view       int
		capability authz.Capability
	}{
		{"all tenant", 2, authz.CapabilityProjectReadAny},
		{"related", 1, authz.CapabilityProjectRead},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.request(t, http.MethodPost, "/role/dataScope/updateRoleDataScopeList", admin, map[string]any{"roleId": uint64(810000101), "dataScopeItemList": []map[string]int{{"dataScopeType": 1, "viewType": tc.view}}}, http.StatusOK, nil)
			got, err := f.store.ListRoleCapabilities(f.ctx, f.domains[0], authz.RoleReader)
			if err != nil {
				t.Fatal(err)
			}
			want := []authz.Capability{authz.CapabilityProjectRead, authz.CapabilityGreeterWatch}
			if tc.view == 2 {
				want = append(want, authz.CapabilityProjectReadAny)
			}
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("scope update capabilities=%v, want %v", got, want)
			}
			var selected smartweb.ResponseDTO[[]smartweb.RoleDataScopeVO]
			f.request(t, http.MethodGet, "/role/dataScope/getRoleDataScopeList/810000101", admin, nil, http.StatusOK, &selected)
			if len(selected.Data) != 1 || selected.Data[0].ViewType != tc.view {
				t.Fatalf("selected scope=%+v", selected.Data)
			}
		})
	}
	before = f.eventCount(t)
	f.request(t, http.MethodPost, "/role/dataScope/updateRoleDataScopeList", admin, map[string]any{"roleId": uint64(810000101), "dataScopeItemList": []map[string]int{{"dataScopeType": 1, "viewType": 99}}}, http.StatusBadRequest, nil)
	if f.eventCount(t) != before {
		t.Fatal("invalid scope created policy events")
	}
	events, err := f.store.ListEvents(f.ctx, f.domains[0], 100)
	if err != nil {
		t.Fatal(err)
	}
	operations := map[string]bool{}
	for _, event := range events {
		if event.RequestID == "role-menu-e2e" && event.ActorSubject == f.subject("manager") {
			operations[event.Operation] = true
		}
	}
	if !operations["add_grouping"] || !operations["remove_grouping"] {
		t.Fatalf("membership audit operations=%v", operations)
	}
	t.Logf("members=add/list/remove scope=related/all-tenant nonProjectGrant=preserved rejectedBatches=6 finalAdmin=protected events=%d", len(events))
}
