// Package smartmessage exposes the unmodified SmartAdmin in-app message
// contracts: the personal inbox (/support/message/**) and the admin console
// (/message/query|sendMessages|delete).
package smartmessage

import (
	"bytes"
	"encoding/json"
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

func serviceOf(svcCtx *svc.ServiceContext) *app.MessageService {
	return svcCtx.CurrentMessageService()
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

func parseMessageID(value string) (uint64, bool) {
	id, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	return id, err == nil && id != 0
}

// compatFlag accepts both JSON booleans (personal inbox/header) and the
// 0/1 numbers emitted by SmartEnumSelect FLAG_NUMBER_ENUM (admin console).
type compatFlag struct {
	Value bool
	Set   bool
}

func (f *compatFlag) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	f.Set = true
	switch trimmed[0] {
	case 't', 'f':
		return json.Unmarshal(trimmed, &f.Value)
	default:
		var number int
		if err := json.Unmarshal(trimmed, &number); err != nil {
			return err
		}
		if number != 0 && number != 1 {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "readFlag must be 0 or 1")
		}
		f.Value = number == 1
		return nil
	}
}

type messageQueryForm struct {
	SearchWord  string      `json:"searchWord"`
	MessageType *int        `json:"messageType"`
	ReadFlag    *compatFlag `json:"readFlag"`
	StartDate   string      `json:"startDate"`
	EndDate     string      `json:"endDate"`
	PageNum     uint64      `json:"pageNum"`
	PageSize    uint64      `json:"pageSize"`
}

func (form messageQueryForm) toQuery() app.MessageQuery {
	query := app.MessageQuery{
		SearchWord:  form.SearchWord,
		MessageType: form.MessageType,
		StartDate:   form.StartDate,
		EndDate:     form.EndDate,
		PageNum:     form.PageNum,
		PageSize:    form.PageSize,
	}
	if form.ReadFlag != nil && form.ReadFlag.Set {
		read := form.ReadFlag.Value
		query.ReadFlag = &read
	}
	return query
}

// QueryMyMessageHandler serves POST /support/message/queryMyMessage.
func QueryMyMessageHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		subjectName, ok := subject(ctx)
		if !ok {
			return
		}
		var form messageQueryForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "message query is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		page, err := service.ListInbox(ctx.Request.Context(), subjectName, form.toQuery())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(page))
	}
}

// UnreadCountHandler serves GET /support/message/getUnreadCount.
func UnreadCountHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		subjectName, ok := subject(ctx)
		if !ok {
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		count, err := service.CountUnread(ctx.Request.Context(), subjectName)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(count))
	}
}

// ReadHandler serves GET /support/message/read/{messageId}.
func ReadHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		subjectName, ok := subject(ctx)
		if !ok {
			return
		}
		messageID, ok := parseMessageID(ctx.PathValue("messageId"))
		if !ok {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "message id is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		if err := service.MarkRead(ctx.Request.Context(), subjectName, messageID); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

// QueryHandler serves POST /message/query (admin console).
func QueryHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form messageQueryForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "message query is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		page, err := service.ListAdmin(ctx.Request.Context(), form.toQuery())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(page))
	}
}

// SendHandler serves POST /message/sendMessages (JSON array, one item per
// receiver); every item must share title/content/messageType.
func SendHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		subjectName, ok := subject(ctx)
		if !ok {
			return
		}
		inputs, err := smartcompat.BindJSONSlice[app.MessageInput](ctx)
		if err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "message batch is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		if err := service.Send(ctx.Request.Context(), subjectName, inputs); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

// DeleteHandler serves GET /message/delete/{messageId}.
func DeleteHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		messageID, ok := parseMessageID(ctx.PathValue("messageId"))
		if !ok {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "message id is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		if err := service.Delete(ctx.Request.Context(), messageID); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}
