// Package smartposition exposes the SmartAdmin position management
// compatibility surface (/position/**).
package smartposition

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

type pageRequest struct {
	PageNum  uint64 `json:"pageNum"`
	PageSize uint64 `json:"pageSize"`
	Keywords string `json:"keywords"`
}

// QueryPageHandler serves POST /position/queryPage.
func QueryPageHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		org, ok := organization(ctx, svcCtx)
		if !ok {
			return
		}
		var req pageRequest
		if err := ctx.Bind(&req); err != nil {
			ctx.JSON(http.StatusOK, smartweb.Reject[any]())
			return
		}
		positions, err := org.ListPositions(ctx.Request.Context(), req.Keywords)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(map[string]any{
			"list":     positions,
			"total":    len(positions),
			"pageNum":  req.PageNum,
			"pageSize": req.PageSize,
		}))
	}
}

// QueryListHandler serves GET /position/queryList.
func QueryListHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		org, ok := organization(ctx, svcCtx)
		if !ok {
			return
		}
		positions, err := org.ListPositions(ctx.Request.Context(), "")
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(positions))
	}
}

// AddHandler serves POST /position/add.
func AddHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		org, ok := organization(ctx, svcCtx)
		if !ok {
			return
		}
		var input app.PositionInput
		if err := ctx.Bind(&input); err != nil {
			ctx.JSON(http.StatusOK, smartweb.Reject[any]())
			return
		}
		position, err := org.CreatePosition(ctx.Request.Context(), input)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(position.ID))
	}
}

type positionUpdateForm struct {
	PositionID uint64 `json:"positionId"`
	app.PositionInput
}

// UpdateHandler serves POST /position/update.
func UpdateHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		org, ok := organization(ctx, svcCtx)
		if !ok {
			return
		}
		var form positionUpdateForm
		if err := ctx.Bind(&form); err != nil || form.PositionID == 0 {
			ctx.JSON(http.StatusOK, smartweb.Reject[any]())
			return
		}
		if err := org.UpdatePosition(ctx.Request.Context(), form.PositionID, form.PositionInput); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK("操作成功"))
	}
}

// DeleteHandler serves GET /position/delete/{positionId}.
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
		if err := org.DeletePosition(ctx.Request.Context(), id); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK("操作成功"))
	}
}

// BatchDeleteHandler serves POST /position/batchDelete.
func BatchDeleteHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		org, ok := organization(ctx, svcCtx)
		if !ok {
			return
		}
		ids, err := smartcompat.BindUint64Array(ctx)
		if err != nil || len(ids) == 0 || len(ids) > 100 {
			ctx.JSON(http.StatusOK, smartweb.Reject[any]())
			return
		}
		for _, id := range ids {
			if err := org.DeletePosition(ctx.Request.Context(), id); err != nil {
				ctx.Error(err)
				return
			}
		}
		ctx.JSON(http.StatusOK, smartweb.OK("操作成功"))
	}
}
