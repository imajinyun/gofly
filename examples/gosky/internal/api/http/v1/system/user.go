package system

import (
	"net/http"
	"strconv"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

type userDTO struct {
	ID          uint64 `json:"id"`
	Subject     string `json:"subject"`
	LoginName   string `json:"loginName"`
	DisplayName string `json:"displayName"`
	AccountType string `json:"accountType"`
	UserState   string `json:"userState"`
	MemberRealm string `json:"memberRealm"`
	MemberState string `json:"memberState"`
}

type userPageResponse struct {
	List  []userDTO `json:"list"`
	Total int       `json:"total"`
}

type createUserRequest struct {
	LoginName   string `json:"loginName" validate:"required,min=3,max=255"`
	DisplayName string `json:"displayName" validate:"max=255"`
	Password    string `json:"password" validate:"required,min=8,max=255"`
	MemberRealm string `json:"memberRealm" validate:"omitempty,oneof=admin front both"`
}

type userStateRequest struct {
	Enabled bool `json:"enabled"`
}

type resetPasswordRequest struct {
	Password string `json:"password" validate:"required,min=8,max=255"`
}

func toUserDTO(user app.UserRecord) userDTO {
	return userDTO{
		ID:          user.ID,
		Subject:     user.Subject,
		LoginName:   user.LoginName,
		DisplayName: user.DisplayName,
		AccountType: user.AccountType,
		UserState:   user.UserState,
		MemberRealm: user.MemberRealm,
		MemberState: user.MemberState,
	}
}

func ListUsersHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentUserAdminService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "user administration is unavailable"))
			return
		}
		pageNum := parseUintQuery(ctx, "pageNum", 1)
		pageSize := parseUintQuery(ctx, "pageSize", 20)
		page, err := service.ListUsers(ctx.Request.Context(), pageNum, pageSize, ctx.Request.URL.Query().Get("keywords"))
		if err != nil {
			ctx.Error(err)
			return
		}
		items := make([]userDTO, 0, len(page.Users))
		for _, user := range page.Users {
			items = append(items, toUserDTO(user))
		}
		ctx.JSON(http.StatusOK, userPageResponse{List: items, Total: page.Total})
	}
}

func CreateUserHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentUserAdminService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "user administration is unavailable"))
			return
		}
		var request createUserRequest
		if err := ctx.Bind(&request); err != nil {
			ctx.Error(err)
			return
		}
		user, err := service.CreateUser(ctx.Request.Context(), app.CreateUserInput{
			LoginName:   request.LoginName,
			DisplayName: request.DisplayName,
			Password:    request.Password,
			MemberRealm: request.MemberRealm,
		})
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusCreated, toUserDTO(user))
	}
}

func SetUserStateHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentUserAdminService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "user administration is unavailable"))
			return
		}
		userID, ok := pathID(ctx)
		if !ok {
			return
		}
		var request userStateRequest
		if err := ctx.Bind(&request); err != nil {
			ctx.Error(err)
			return
		}
		if err := service.SetMembershipEnabled(ctx.Request.Context(), userID, request.Enabled); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, map[string]bool{"changed": true})
	}
}

func ResetPasswordHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentUserAdminService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "user administration is unavailable"))
			return
		}
		userID, ok := pathID(ctx)
		if !ok {
			return
		}
		var request resetPasswordRequest
		if err := ctx.Bind(&request); err != nil {
			ctx.Error(err)
			return
		}
		if err := service.ResetPassword(ctx.Request.Context(), userID, request.Password); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, map[string]bool{"changed": true})
	}
}

func parseUintQuery(ctx *rest.Context, name string, fallback uint64) uint64 {
	raw := ctx.Request.URL.Query().Get(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return fallback
	}
	return value
}
