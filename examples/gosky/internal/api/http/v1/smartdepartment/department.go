// Package smartdepartment exposes the SmartAdmin department management
// compatibility surface (/department/**) backed by gosky's tenant
// organization services.
package smartdepartment

import (
	"net/http"
	"strconv"
	"strings"

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

// ListAllHandler serves GET /department/listAll.
func ListAllHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		org, ok := organization(ctx, svcCtx)
		if !ok {
			return
		}
		departments, err := org.ListDepartments(ctx.Request.Context())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(departments))
	}
}

// TreeHandler serves GET /department/treeList.
func TreeHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		org, ok := organization(ctx, svcCtx)
		if !ok {
			return
		}
		tree, err := org.DepartmentTree(ctx.Request.Context())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(tree))
	}
}

// AddHandler serves POST /department/add.
func AddHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		org, ok := organization(ctx, svcCtx)
		if !ok {
			return
		}
		var input app.DepartmentInput
		if err := ctx.Bind(&input); err != nil {
			ctx.JSON(http.StatusOK, smartweb.Reject[any]())
			return
		}
		department, err := org.CreateDepartment(ctx.Request.Context(), input)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(department.ID))
	}
}

// departmentUpdateForm carries the department id plus mutable fields.
type departmentUpdateForm struct {
	DepartmentID uint64 `json:"departmentId"`
	app.DepartmentInput
}

// UpdateHandler serves POST /department/update.
func UpdateHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		org, ok := organization(ctx, svcCtx)
		if !ok {
			return
		}
		var form departmentUpdateForm
		if err := ctx.Bind(&form); err != nil || form.DepartmentID == 0 {
			ctx.JSON(http.StatusOK, smartweb.Reject[any]())
			return
		}
		if err := org.UpdateDepartment(ctx.Request.Context(), form.DepartmentID, form.DepartmentInput); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK("操作成功"))
	}
}

// DeleteHandler serves GET /department/delete/{departmentId}.
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
		if err := org.DeleteDepartment(ctx.Request.Context(), id); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK("操作成功"))
	}
}
