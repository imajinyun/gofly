// Package smartemployee exposes the SmartAdmin employee directory
// compatibility surface (/employee/**): paginated directory, lifecycle,
// department assignment, and administrator password operations.
package smartemployee

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartcompat"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

func organization(ctx *rest.Context, svcCtx *svc.ServiceContext) (smartweb.Organization, bool) {
	compat := svcCtx.CurrentSmartWebService()
	if compat == nil {
		ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
		return nil, false
	}
	org := compat.CurrentOrganization()
	if org == nil {
		ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
		return nil, false
	}
	return org, true
}

type employeeQueryForm struct {
	PageNum      uint64 `json:"pageNum"`
	PageSize     uint64 `json:"pageSize"`
	Keyword      string `json:"keyword"`
	DepartmentID uint64 `json:"departmentId"`
	DisabledFlag *bool  `json:"disabledFlag"`
}

// QueryHandler serves POST /employee/query with pagination.
func QueryHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		org, ok := organization(ctx, svcCtx)
		if !ok {
			return
		}
		var form employeeQueryForm
		if err := ctx.Bind(&form); err != nil {
			ctx.JSON(http.StatusOK, smartweb.Reject[any]())
			return
		}
		page, err := org.ListEmployees(ctx.Request.Context(), app.EmployeeQuery{
			PageNum:      form.PageNum,
			PageSize:     form.PageSize,
			Keyword:      form.Keyword,
			DepartmentID: form.DepartmentID,
			Disabled:     form.DisabledFlag,
		})
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(page))
	}
}

// QueryAllHandler serves POST /employee/queryAll. The reference also
// accepts a GET with disabledFlag query; the browser uses POST.
func QueryAllHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		org, ok := organization(ctx, svcCtx)
		if !ok {
			return
		}
		includeDisabled := true
		if raw := strings.TrimSpace(ctx.Request.URL.Query().Get("disabledFlag")); raw != "" {
			includeDisabled = raw == "true"
		}
		employees, err := org.ListAllEmployees(ctx.Request.Context(), includeDisabled)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(employees))
	}
}

// AddHandler serves POST /employee/add. Returns the generated initial
// password for one-time display.
func AddHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		org, ok := organization(ctx, svcCtx)
		if !ok {
			return
		}
		var input app.EmployeeInput
		if err := ctx.Bind(&input); err != nil {
			ctx.JSON(http.StatusOK, smartweb.Reject[any]())
			return
		}
		_, password, err := org.CreateEmployee(ctx.Request.Context(), input)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(password))
	}
}

// UpdateHandler serves POST /employee/update.
func UpdateHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		org, ok := organization(ctx, svcCtx)
		if !ok {
			return
		}
		var input app.EmployeeInput
		if err := ctx.Bind(&input); err != nil || input.EmployeeID == 0 {
			ctx.JSON(http.StatusOK, smartweb.Reject[any]())
			return
		}
		if err := org.UpdateEmployee(ctx.Request.Context(), input); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK("操作成功"))
	}
}

// DeleteHandler serves GET /employee/delete/{employeeId}.
func DeleteHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		org, ok := organization(ctx, svcCtx)
		if !ok {
			return
		}
		id, err := strconv.ParseUint(strings.TrimSpace(ctx.PathValue("id")), 10, 64)
		if err != nil || id == 0 {
			ctx.JSON(http.StatusOK, smartweb.Reject[any]())
			return
		}
		if err := org.DeleteEmployees(ctx.Request.Context(), []uint64{id}); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK("操作成功"))
	}
}

// BatchDeleteHandler serves POST /employee/update/batch/delete.
func BatchDeleteHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		org, ok := organization(ctx, svcCtx)
		if !ok {
			return
		}
		ids, err := smartcompat.BindUint64Array(ctx)
		if err != nil || len(ids) == 0 {
			ctx.JSON(http.StatusOK, smartweb.Reject[any]())
			return
		}
		if err := org.DeleteEmployees(ctx.Request.Context(), ids); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK("操作成功"))
	}
}

type batchDepartmentForm struct {
	EmployeeIDList []uint64 `json:"employeeIdList"`
	DepartmentID   uint64   `json:"departmentId"`
}

// BatchDepartmentHandler serves POST /employee/update/batch/department.
func BatchDepartmentHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		org, ok := organization(ctx, svcCtx)
		if !ok {
			return
		}
		var form batchDepartmentForm
		if err := ctx.Bind(&form); err != nil || form.DepartmentID == 0 {
			ctx.JSON(http.StatusOK, smartweb.Reject[any]())
			return
		}
		if err := org.MoveEmployeesDepartment(ctx.Request.Context(), form.EmployeeIDList, form.DepartmentID); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK("操作成功"))
	}
}

// DisabledHandler serves GET /employee/update/disabled/{employeeId} and
// toggles the membership state.
func DisabledHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		org, ok := organization(ctx, svcCtx)
		if !ok {
			return
		}
		id, err := strconv.ParseUint(strings.TrimSpace(ctx.PathValue("id")), 10, 64)
		if err != nil || id == 0 {
			ctx.JSON(http.StatusOK, smartweb.Reject[any]())
			return
		}
		employees, queryErr := org.ListAllEmployees(ctx.Request.Context(), true)
		if queryErr != nil {
			ctx.Error(queryErr)
			return
		}
		target := false
		found := false
		for _, employee := range employees {
			if employee.UserID == id {
				target = !employee.Disabled
				found = true
				break
			}
		}
		if !found {
			ctx.JSON(http.StatusOK, smartweb.Reject[any]())
			return
		}
		if err := org.SetEmployeeDisabled(ctx.Request.Context(), id, target); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK("操作成功"))
	}
}

// ResetPasswordHandler serves GET
// /employee/update/password/reset/{employeeId}; a random password is
// returned for one-time display.
func ResetPasswordHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		org, ok := organization(ctx, svcCtx)
		if !ok {
			return
		}
		id, err := strconv.ParseUint(strings.TrimSpace(ctx.PathValue("id")), 10, 64)
		if err != nil || id == 0 {
			ctx.JSON(http.StatusOK, smartweb.Reject[any]())
			return
		}
		password, err := org.ResetEmployeePassword(ctx.Request.Context(), id, "")
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(password))
	}
}

// PasswordComplexityHandler serves GET
// /employee/getPasswordComplexityEnabled. Gosky always enforces its own
// minimum length; the UI flag is disabled.
func PasswordComplexityHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		if svcCtx.CurrentSmartWebService() == nil {
			ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(false))
	}
}

// ByDepartmentHandler serves GET
// /employee/getAllEmployeeByDepartmentId/{departmentId}.
func ByDepartmentHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		org, ok := organization(ctx, svcCtx)
		if !ok {
			return
		}
		id, err := strconv.ParseUint(strings.TrimSpace(ctx.PathValue("id")), 10, 64)
		if err != nil || id == 0 {
			ctx.JSON(http.StatusOK, smartweb.Reject[any]())
			return
		}
		employees, queryErr := org.EmployeesByDepartment(ctx.Request.Context(), id)
		if queryErr != nil {
			ctx.Error(queryErr)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(employees))
	}
}

type centerForm struct {
	EmployeeID uint64 `json:"employeeId"`
	app.EmployeeCenterInput
}

// UpdateCenterHandler serves POST /employee/update/center.
func UpdateCenterHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		org, ok := organization(ctx, svcCtx)
		if !ok {
			return
		}
		var form centerForm
		if err := ctx.Bind(&form); err != nil || form.EmployeeID == 0 {
			ctx.JSON(http.StatusOK, smartweb.Reject[any]())
			return
		}
		if err := org.UpdateEmployeeCenter(ctx.Request.Context(), form.EmployeeID, form.EmployeeCenterInput); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK("操作成功"))
	}
}

type avatarForm struct {
	EmployeeID uint64 `json:"employeeId"`
	Avatar     string `json:"avatar"`
}

// UpdateAvatarHandler serves POST /employee/update/avatar.
func UpdateAvatarHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		org, ok := organization(ctx, svcCtx)
		if !ok {
			return
		}
		var form avatarForm
		if err := ctx.Bind(&form); err != nil || form.EmployeeID == 0 {
			ctx.JSON(http.StatusOK, smartweb.Reject[any]())
			return
		}
		if err := org.UpdateEmployeeAvatar(ctx.Request.Context(), form.EmployeeID, form.Avatar); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK("操作成功"))
	}
}
