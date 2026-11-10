package smartprotect

import (
	"net/http"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartcompat"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

// ---------------- login logs ----------------

type loginLogQueryForm struct {
	UserID    *uint64 `json:"userId"`
	UserType  *int    `json:"userType"`
	UserName  string  `json:"userName"`
	IP        string  `json:"ip"`
	StartDate string  `json:"startDate"`
	EndDate   string  `json:"endDate"`
	PageNum   uint64  `json:"pageNum"`
	PageSize  uint64  `json:"pageSize"`
}

func (form loginLogQueryForm) toQuery() app.LoginLogQuery {
	return app.LoginLogQuery{
		UserID:    form.UserID,
		UserType:  form.UserType,
		UserName:  form.UserName,
		IP:        form.IP,
		StartDate: form.StartDate,
		EndDate:   form.EndDate,
		PageNum:   form.PageNum,
		PageSize:  form.PageSize,
	}
}

// QueryLogsHandler serves POST /support/loginLog/page/query.
func QueryLogsHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form loginLogQueryForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "login log query is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		page, err := service.ListLogs(ctx.Request.Context(), form.toQuery())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(page))
	}
}

// QueryOwnLogsHandler serves POST /support/loginLog/page/query/login.
func QueryOwnLogsHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		p, ok := principal(ctx)
		if !ok {
			ctx.JSON(http.StatusUnauthorized, smartweb.Reject[any]())
			return
		}
		var form loginLogQueryForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "login log query is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		page, err := service.ListOwnLogs(ctx.Request.Context(), p.Subject, form.toQuery())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(page))
	}
}

// ---------------- fail lock state ----------------

type loginFailQueryForm struct {
	LoginName          string `json:"loginName"`
	LockFlag           *bool  `json:"lockFlag"`
	LoginLockBeginTime string `json:"loginLockBeginTimeBegin"`
	LoginLockEndTime   string `json:"loginLockBeginTimeEnd"`
	PageNum            uint64 `json:"pageNum"`
	PageSize           uint64 `json:"pageSize"`
}

// QueryFailsHandler serves POST /support/protect/loginFail/queryPage.
func QueryFailsHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		p, ok := principal(ctx)
		if !ok {
			ctx.JSON(http.StatusUnauthorized, smartweb.Reject[any]())
			return
		}
		var form loginFailQueryForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "login fail query is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		page, err := service.ListFails(ctx.Request.Context(), p.TenantID, app.LoginFailQuery{
			LoginName:          form.LoginName,
			LockFlag:           form.LockFlag,
			LockBeginTimeBegin: form.LoginLockBeginTime,
			LockBeginTimeEnd:   form.LoginLockEndTime,
			PageNum:            form.PageNum,
			PageSize:           form.PageSize,
		})
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(page))
	}
}

// DeleteFailsHandler serves POST /support/protect/loginFail/batchDelete.
func DeleteFailsHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		ids, err := smartcompat.BindUint64Array(ctx)
		if err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "id list is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		if err := service.DeleteFails(ctx.Request.Context(), ids); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

// ---------------- level-3 protect config ----------------

type level3ConfigForm struct {
	LoginFailMaxTimes                   *int   `json:"loginFailMaxTimes"`
	LoginFailLockMinutes                *int   `json:"loginFailLockMinutes"`
	LoginActiveTimeoutMinutes           *int   `json:"loginActiveTimeoutMinutes"`
	TwoFactorLoginEnabled               *bool  `json:"twoFactorLoginEnabled"`
	PasswordComplexityEnabled           *bool  `json:"passwordComplexityEnabled"`
	RegularChangePasswordMonths         *int   `json:"regularChangePasswordMonths"`
	RegularChangePasswordNotAllowRepeat *int   `json:"regularChangePasswordNotAllowRepeatTimes"`
	FileDetectFlag                      *bool  `json:"fileDetectFlag"`
	MaxUploadFileSizeMB                 *int64 `json:"maxUploadFileSizeMb"`
}

func (form level3ConfigForm) toConfig() app.Level3ProtectConfig {
	return app.Level3ProtectConfig{
		LoginFailMaxTimes:                   form.LoginFailMaxTimes,
		LoginFailLockMinutes:                form.LoginFailLockMinutes,
		LoginActiveTimeoutMinutes:           form.LoginActiveTimeoutMinutes,
		TwoFactorLoginEnabled:               form.TwoFactorLoginEnabled,
		PasswordComplexityEnabled:           form.PasswordComplexityEnabled,
		RegularChangePasswordMonths:         form.RegularChangePasswordMonths,
		RegularChangePasswordNotAllowRepeat: form.RegularChangePasswordNotAllowRepeat,
		FileDetectFlag:                      form.FileDetectFlag,
		MaxUploadFileSizeMB:                 form.MaxUploadFileSizeMB,
	}
}

// GetConfigHandler serves GET /support/protect/level3protect/getConfig.
func GetConfigHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		raw, err := service.GetProtectConfig(ctx.Request.Context())
		if err != nil {
			ctx.Error(err)
			return
		}
		// The unchanged client JSON.parses the data string; an empty string
		// is its "not configured" sentinel.
		ctx.JSON(http.StatusOK, smartweb.OK(raw))
	}
}

// UpdateConfigHandler serves POST /support/protect/level3protect/updateConfig.
func UpdateConfigHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form level3ConfigForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "protect config is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		if err := service.UpdateProtectConfig(ctx.Request.Context(), form.toConfig()); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}
