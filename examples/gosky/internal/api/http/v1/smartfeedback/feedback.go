// Package smartfeedback exposes the unmodified SmartAdmin feedback
// contracts: /support/feedback/add (side-help modal, any employee) and
// /support/feedback/query (support console).
package smartfeedback

import (
	"net/http"
	"strings"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartcompat"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

func serviceOf(svcCtx *svc.ServiceContext) *app.FeedbackService {
	return svcCtx.CurrentFeedbackService()
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

type feedbackAddForm struct {
	Content     string               `json:"feedbackContent"`
	Attachments smartcompat.FileKeys `json:"feedbackAttachment"`
}

type feedbackQueryForm struct {
	SearchWord string `json:"searchWord"`
	StartDate  string `json:"startDate"`
	EndDate    string `json:"endDate"`
	PageNum    uint64 `json:"pageNum"`
	PageSize   uint64 `json:"pageSize"`
}

// AddHandler serves POST /support/feedback/add.
func AddHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		subjectName, ok := subject(ctx)
		if !ok {
			return
		}
		var form feedbackAddForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "feedback form is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		err := service.Add(ctx.Request.Context(), subjectName, app.FeedbackInput{
			Content:        form.Content,
			AttachmentKeys: form.Attachments.Keys,
		})
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

// QueryHandler serves POST /support/feedback/query.
func QueryHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form feedbackQueryForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "feedback query is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		page, err := service.List(ctx.Request.Context(), app.FeedbackQuery{
			SearchWord: form.SearchWord,
			StartDate:  form.StartDate,
			EndDate:    form.EndDate,
			PageNum:    form.PageNum,
			PageSize:   form.PageSize,
		})
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(page))
	}
}
