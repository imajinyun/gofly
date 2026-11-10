// Package smarthelpdoc exposes the unmodified SmartAdmin help-document
// contracts: the tenant catalog CRUD, the management document CRUD, and the
// authenticated user-side browsing with per-user view records.
package smarthelpdoc

import (
	"net"
	"net/http"
	"strconv"
	"strings"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartcompat"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

const maxClientIPLength = 45

func serviceOf(svcCtx *svc.ServiceContext) *app.HelpDocService {
	return svcCtx.CurrentHelpDocService()
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

func parseID(value string) (uint64, bool) {
	id, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	return id, err == nil && id != 0
}

// clientIP honors a single-hop reverse proxy and falls back to the remote
// address port strip. Bounded to the VARCHAR(45) column at the service edge.
func clientIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		if comma := strings.IndexByte(forwarded, ','); comma >= 0 {
			forwarded = forwarded[:comma]
		}
		if ip := strings.TrimSpace(forwarded); ip != "" {
			return truncateIP(ip)
		}
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		return truncateIP(r.RemoteAddr)
	}
	return truncateIP(host)
}

func truncateIP(value string) string {
	if len(value) > maxClientIPLength {
		return value[:maxClientIPLength]
	}
	return value
}

// ---------------- catalogs ----------------

type catalogForm struct {
	ID       uint64 `json:"helpDocCatalogId"`
	Name     string `json:"name"`
	ParentID uint64 `json:"parentId"`
	Sort     int    `json:"sort"`
}

func (form catalogForm) toInput() app.HelpDocCatalogInput {
	return app.HelpDocCatalogInput{ID: form.ID, Name: form.Name, ParentID: form.ParentID, Sort: form.Sort}
}

// GetAllCatalogsHandler serves GET /support/helpDoc/helpDocCatalog/getAll.
func GetAllCatalogsHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		if _, ok := subject(ctx); !ok {
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		catalogs, err := service.ListCatalogs(ctx.Request.Context())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(catalogs))
	}
}

// AddCatalogHandler serves POST /support/helpDoc/helpDocCatalog/add.
func AddCatalogHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form catalogForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "catalog form is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		if err := service.AddCatalog(ctx.Request.Context(), form.toInput()); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

// UpdateCatalogHandler serves POST /support/helpDoc/helpDocCatalog/update.
func UpdateCatalogHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form catalogForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "catalog form is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		if err := service.UpdateCatalog(ctx.Request.Context(), form.toInput()); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

// DeleteCatalogHandler serves GET /support/helpDoc/helpDocCatalog/delete/{id}.
func DeleteCatalogHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		id, ok := parseID(ctx.PathValue("helpDocCatalogId"))
		if !ok {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "catalog id is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		if err := service.DeleteCatalog(ctx.Request.Context(), id); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

// ---------------- management documents ----------------

type docRelationForm struct {
	RelationName string `json:"relationName"`
	RelationID   uint64 `json:"relationId"`
}

type docForm struct {
	ID           uint64               `json:"helpDocId"`
	Title        string               `json:"title"`
	CatalogID    uint64               `json:"helpDocCatalogId"`
	ContentText  string               `json:"contentText"`
	ContentHTML  string               `json:"contentHtml"`
	Attachment   smartcompat.FileKeys `json:"attachment"`
	Sort         int                  `json:"sort"`
	Author       string               `json:"author"`
	RelationList []docRelationForm    `json:"relationList"`
}

func (form docForm) toInput() app.HelpDocInput {
	relations := make([]app.HelpDocRelation, 0, len(form.RelationList))
	for _, relation := range form.RelationList {
		relations = append(relations, app.HelpDocRelation{RelationName: relation.RelationName, RelationID: relation.RelationID})
	}
	return app.HelpDocInput{
		ID:             form.ID,
		Title:          form.Title,
		CatalogID:      form.CatalogID,
		ContentText:    form.ContentText,
		ContentHTML:    form.ContentHTML,
		AttachmentKeys: form.Attachment.Keys,
		Sort:           form.Sort,
		Author:         form.Author,
		Relations:      relations,
	}
}

type docQueryForm struct {
	CatalogID       *uint64 `json:"helpDocCatalogId"`
	Keywords        string  `json:"keywords"`
	CreateTimeBegin string  `json:"createTimeBegin"`
	CreateTimeEnd   string  `json:"createTimeEnd"`
	PageNum         uint64  `json:"pageNum"`
	PageSize        uint64  `json:"pageSize"`
}

// QueryHandler serves POST /support/helpDoc/query.
func QueryHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form docQueryForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "help doc query is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		page, err := service.ListDocs(ctx.Request.Context(), app.HelpDocQuery{
			CatalogID:       form.CatalogID,
			Keywords:        form.Keywords,
			CreateTimeBegin: form.CreateTimeBegin,
			CreateTimeEnd:   form.CreateTimeEnd,
			PageNum:         form.PageNum,
			PageSize:        form.PageSize,
		})
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(page))
	}
}

// GetDetailHandler serves GET /support/helpDoc/getDetail/{id}.
func GetDetailHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		id, ok := parseID(ctx.PathValue("helpDocId"))
		if !ok {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "help doc id is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		detail, err := service.GetDetail(ctx.Request.Context(), id)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(detail))
	}
}

// AddHandler serves POST /support/helpDoc/add.
func AddHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form docForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "help doc form is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		if err := service.AddDoc(ctx.Request.Context(), form.toInput()); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

// UpdateHandler serves POST /support/helpDoc/update.
func UpdateHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form docForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "help doc form is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		if err := service.UpdateDoc(ctx.Request.Context(), form.toInput()); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

// DeleteHandler serves GET /support/helpDoc/delete/{id}.
func DeleteHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		id, ok := parseID(ctx.PathValue("helpDocId"))
		if !ok {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "help doc id is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		if err := service.DeleteDoc(ctx.Request.Context(), id); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

// RelationHandler serves GET /support/helpDoc/queryHelpDocByRelationId/{id}.
func RelationHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		if _, ok := subject(ctx); !ok {
			return
		}
		relationID, ok := parseIDAllowZero(ctx.PathValue("relationId"))
		if !ok {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "relation id is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		docs, err := service.ListDocsByRelation(ctx.Request.Context(), relationID)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(docs))
	}
}

// ---------------- user-side browsing ----------------

// AllHandler serves GET /support/helpDoc/user/queryAllHelpDocList.
func AllHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		if _, ok := subject(ctx); !ok {
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		docs, err := service.ListAllDocs(ctx.Request.Context())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(docs))
	}
}

// ViewHandler serves GET /support/helpDoc/user/view/{id}.
func ViewHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		subjectName, ok := subject(ctx)
		if !ok {
			return
		}
		id, ok := parseID(ctx.PathValue("helpDocId"))
		if !ok {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "help doc id is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		detail, err := service.View(ctx.Request.Context(), id, subjectName,
			clientIP(ctx.Request), strings.TrimSpace(ctx.Request.UserAgent()))
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(detail))
	}
}

type viewRecordQueryForm struct {
	HelpDocID uint64  `json:"helpDocId"`
	UserID    *uint64 `json:"userId"`
	Keywords  string  `json:"keywords"`
	PageNum   uint64  `json:"pageNum"`
	PageSize  uint64  `json:"pageSize"`
}

// ViewRecordHandler serves POST /support/helpDoc/user/queryViewRecord.
func ViewRecordHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		if _, ok := subject(ctx); !ok {
			return
		}
		var form viewRecordQueryForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "view record query is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		page, err := service.ListViewRecords(ctx.Request.Context(), app.HelpDocViewRecordQuery{
			HelpDocID: form.HelpDocID,
			UserID:    form.UserID,
			Keywords:  form.Keywords,
			PageNum:   form.PageNum,
			PageSize:  form.PageSize,
		})
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(page))
	}
}

func parseIDAllowZero(value string) (uint64, bool) {
	id, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	return id, err == nil
}
