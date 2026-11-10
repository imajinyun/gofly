// Package smarttablecolumn exposes the unmodified SmartAdmin per-user
// table layout contracts: /support/tableColumn/update|getColumns|delete.
package smarttablecolumn

import (
	"net/http"
	"strconv"
	"strings"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

func serviceOf(svcCtx *svc.ServiceContext) *app.TableColumnService {
	return svcCtx.CurrentTableColumnService()
}

func unavailable(ctx *rest.Context) {
	ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
}

func subject(ctx *rest.Context) (string, bool) {
	principal, ok := authn.PrincipalFromContext(ctx.Request.Context())
	if !ok || strings.TrimSpace(principal.Subject) == "" {
		ctx.JSON(http.StatusUnauthorized, smartweb.Reject[any]())
		return "", false
	}
	return principal.Subject, true
}

func parseTableID(value string) (uint64, bool) {
	id, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	return id, err == nil && id != 0
}

// UpdateHandler serves POST /support/tableColumn/update.
func UpdateHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		subjectName, ok := subject(ctx)
		if !ok {
			return
		}
		var form app.TableColumnUpdate
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "table column form is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		if err := service.Update(ctx.Request.Context(), subjectName, form); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

// GetColumnsHandler serves GET /support/tableColumn/getColumns/{tableId}.
// The unchanged client parses the data field as JSON; when no preference is
// stored the data field is an empty string (client falls back to defaults).
func GetColumnsHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		subjectName, ok := subject(ctx)
		if !ok {
			return
		}
		tableID, ok := parseTableID(ctx.PathValue("tableId"))
		if !ok {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "table id is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		config, found, err := service.GetColumns(ctx.Request.Context(), subjectName, tableID)
		if err != nil {
			ctx.Error(err)
			return
		}
		if !found {
			ctx.JSON(http.StatusOK, smartweb.OK(""))
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(config))
	}
}

// DeleteHandler serves GET /support/tableColumn/delete/{tableId}.
func DeleteHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		subjectName, ok := subject(ctx)
		if !ok {
			return
		}
		tableID, ok := parseTableID(ctx.PathValue("tableId"))
		if !ok {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "table id is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		if err := service.Delete(ctx.Request.Context(), subjectName, tableID); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}
