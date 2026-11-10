package smartdevtools

import (
	"net/http"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

// Code-generator compatibility stubs.
//
// The Java reference generates a Spring/MyBatis project zip from live
// database tables. gosky's generator target is Go (gofly codectl), so the
// Java-specific table configuration, preview and zip download actions do
// not apply. The read endpoints return empty, well-typed pages so the
// unmodified console page renders; the write/generate actions return an
// explicit business error instead of producing unusable Java sources.

type tableQueryForm struct {
	TableNameKeywords string `json:"tableNameKeywords"`
	PageNum           uint64 `json:"pageNum"`
	PageSize          uint64 `json:"pageSize"`
}

// CodeGeneratorTableListHandler serves POST queryTableList with an empty
// table catalog.
func CodeGeneratorTableListHandler(_ *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form tableQueryForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "查询参数错误"))
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(struct {
			List  []any `json:"list"`
			Total int   `json:"total"`
		}{List: []any{}, Total: 0}))
	}
}

// CodeGeneratorColumnsHandler serves GET getTableColumns/{table} with no
// columns.
func CodeGeneratorColumnsHandler(_ *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		ctx.JSON(http.StatusOK, smartweb.OK([]any{}))
	}
}

// CodeGeneratorConfigHandler serves GET getConfig/{table}; the generator
// stores no per-table configuration in gosky.
func CodeGeneratorConfigHandler(_ *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		ctx.JSON(http.StatusOK, smartweb.OK[any](nil))
	}
}

// CodeGeneratorUpdateConfigHandler accepts and ignores config updates.
func CodeGeneratorUpdateConfigHandler(_ *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		// Config persistence is intentionally a no-op for the Go code
		// generator compatibility stub (see package doc).
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

func codeGeneratorUnsupported(ctx *rest.Context) {
	ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument,
		"gosky 不提供 Java 代码生成，请使用 gofly codectl 生成 Go 项目代码"))
}

// CodeGeneratorPreviewHandler rejects Java preview generation.
func CodeGeneratorPreviewHandler(_ *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		codeGeneratorUnsupported(ctx)
	}
}

// CodeGeneratorDownloadHandler rejects Java zip download.
func CodeGeneratorDownloadHandler(_ *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		codeGeneratorUnsupported(ctx)
	}
}
