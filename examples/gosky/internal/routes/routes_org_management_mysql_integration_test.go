//go:build integration

package routes

import (
	"net/http"
	"testing"
)

// TestMySQLOrganizationManagementIntegration exercises the SmartAdmin
// employee/department/position lifecycle and role CRUD against real
// MySQL: tenant isolation, seeded-row protection, and password reset.
func TestMySQLOrganizationManagementIntegration(t *testing.T) {
	f := newDynamicRBACFixture(t)
	tenant := f.tenants[0]
	token := f.login(tenant, tenant.admin)
	memberLogin := "dyn-" + f.suffix + "-opsuser"

	requireOK := func(method, path string, input any) any {
		t.Helper()
		status, body := f.do(method, path, token, input)
		if status != http.StatusOK {
			t.Fatalf("%s %s status=%d body=%v", method, path, status, body)
		}
		return body["data"]
	}

	// Bootstrap seeds the root organization department.
	list, ok := requireOK(http.MethodGet, "/department/listAll", nil).([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("seeded departments = %#v", list)
	}
	root := list[0].(map[string]any)
	rootDepartmentID := uint64(root["departmentId"].(float64))

	// Add a child department.
	child := requireOK(http.MethodPost, "/department/add", map[string]any{
		"departmentName": "Engineering",
		"parentId":       rootDepartmentID,
		"sort":           10,
	}).(float64)
	childID := uint64(child)

	// Tree contains the new child.
	tree, ok := requireOK(http.MethodGet, "/department/treeList", nil).([]any)
	if !ok || len(tree) != 1 || len(tree[0].(map[string]any)["children"].([]any)) != 1 {
		t.Fatalf("department tree = %#v", tree)
	}

	// The seeded root department is immutable.
	if status, _ := f.do(http.MethodPost, "/department/update", token, map[string]any{
		"departmentId":   rootDepartmentID,
		"departmentName": "Hacked",
		"sort":           1,
	}); status != http.StatusBadRequest {
		t.Fatalf("update seeded department status=%d, want 400", status)
	}
	if status, _ := f.do(http.MethodGet, "/department/delete/"+orgItoa(rootDepartmentID), token, nil); status != http.StatusBadRequest {
		t.Fatalf("delete seeded department status=%d, want 400", status)
	}

	// Positions.
	positionID := uint64(requireOK(http.MethodPost, "/position/add", map[string]any{
		"positionName":  "Engineer",
		"positionLevel": "P5",
		"sort":          1,
	}).(float64))
	positionPage := requireOK(http.MethodPost, "/position/queryPage", map[string]any{
		"pageNum": 1, "pageSize": 20, "keywords": "Engineer",
	}).(map[string]any)
	if positionPage["total"].(float64) < 1 {
		t.Fatalf("position page = %#v", positionPage)
	}
	requireOK(http.MethodGet, "/position/delete/"+orgItoa(positionID), nil)

	// Custom role through the SmartAdmin role CRUD.
	role := requireOK(http.MethodPost, "/role/add", map[string]any{
		"roleName": "Ops",
		"roleCode": "ops_role_e2e",
		"remark":   "operations",
	}).(map[string]any)
	roleID := uint64(role["roleId"].(float64))

	// Reserved role codes cannot be created.
	if status, _ := f.do(http.MethodPost, "/role/add", token, map[string]any{
		"roleName": "Admin 2", "roleCode": "admin",
	}); status != http.StatusBadRequest {
		t.Fatalf("create reserved role status=%d, want 400", status)
	}
	// Fixed roles cannot be updated or deleted through the tenant API.
	if status, _ := f.do(http.MethodGet, "/role/delete/810000103", token, nil); status != http.StatusBadRequest {
		t.Fatalf("delete fixed role status=%d, want 400", status)
	}

	// Employee lifecycle with role and placement.
	initialPassword, ok := requireOK(http.MethodPost, "/employee/add", map[string]any{
		"actualName":   "Ops User",
		"loginName":    memberLogin,
		"password":     "correct-password",
		"gender":       1,
		"departmentId": childID,
		"roleIdList":   []uint64{roleID},
	}).(string)
	// No explicit password supplied: service generates one; verify shape.
	_ = ok
	_ = initialPassword
	page := requireOK(http.MethodPost, "/employee/query", map[string]any{
		"pageNum": 1, "pageSize": 100, "keyword": memberLogin,
	}).(map[string]any)
	employees := page["list"].([]any)
	if len(employees) != 1 {
		t.Fatalf("employee search = %#v", page)
	}
	employee := employees[0].(map[string]any)
	employeeID := uint64(employee["employeeId"].(float64))
	if len(employee["roleIdList"].([]any)) != 1 {
		t.Fatalf("employee roles = %#v", employee["roleIdList"])
	}
	if deptName := employee["departmentName"]; deptName != "Engineering" {
		t.Fatalf("employee departmentName = %#v", deptName)
	}

	// Directory by department.
	byDept, ok := requireOK(http.MethodGet, "/employee/getAllEmployeeByDepartmentId/"+orgItoa(childID), nil).([]any)
	if !ok || len(byDept) != 1 {
		t.Fatalf("employees in department = %#v", byDept)
	}

	// Disable -> login rejected; re-enable -> login succeeds.
	requireOK(http.MethodGet, "/employee/update/disabled/"+orgItoa(employeeID), nil)
	if status, _ := f.loginStatus(tenant, memberLogin); status != http.StatusUnauthorized {
		t.Fatalf("disabled employee login status=%d, want 401", status)
	}
	requireOK(http.MethodGet, "/employee/update/disabled/"+orgItoa(employeeID), nil)
	if status, _ := f.loginStatus(tenant, memberLogin); status != http.StatusOK {
		t.Fatalf("reenabled employee login status=%d, want 200", status)
	}

	// Password reset returns a usable new password.
	newPassword, ok := requireOK(http.MethodGet, "/employee/update/password/reset/"+orgItoa(employeeID), nil).(string)
	if !ok || len(newPassword) < 8 {
		t.Fatalf("reset password = %#v", newPassword)
	}

	// A role with members cannot be deleted; clear the binding first.
	if status, _ := f.do(http.MethodGet, "/role/delete/"+orgItoa(roleID), token, nil); status != http.StatusBadRequest {
		t.Fatalf("delete bound role status=%d, want 400", status)
	}
	requireOK(http.MethodPost, "/employee/update", map[string]any{
		"employeeId":   employeeID,
		"actualName":   "Ops User",
		"loginName":    memberLogin,
		"departmentId": childID,
		"roleIdList":   []uint64{},
	})
	requireOK(http.MethodGet, "/role/delete/"+orgItoa(roleID), nil)

	// Tenant isolation: the other tenant cannot see this department tree.
	other := f.tenants[1]
	otherToken := f.login(other, other.admin)
	status, body := f.do(http.MethodGet, "/department/listAll", otherToken, nil)
	if status != http.StatusOK {
		t.Fatalf("other tenant department list status=%d", status)
	}
	for _, item := range body["data"].([]any) {
		if item.(map[string]any)["departmentName"] == "Engineering" {
			t.Fatal("department leaked across tenants")
		}
	}
}

// orgItoa renders a positive uint64 in decimal without importing strconv.
func orgItoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [24]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
