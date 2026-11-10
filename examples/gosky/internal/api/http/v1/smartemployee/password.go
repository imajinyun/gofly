package smartemployee

import (
	"encoding/json"
	"net/http"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

type changeOwnPasswordRequest struct {
	OldPassword string `json:"oldPassword"`
	NewPassword string `json:"newPassword"`
}

// ChangeOwnPasswordHandler serves POST /employee/update/password.
// The unchanged client sends postEncryptRequest: {"encryptData": "<SM4>"},
// where the plaintext JSON contains oldPassword/newPassword. Unlike an
// administrator reset, the caller must prove the current password.
func ChangeOwnPasswordHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		principal, ok := authn.PrincipalFromContext(ctx.Request.Context())
		if !ok {
			ctx.JSON(http.StatusUnauthorized, smartweb.Reject[any]())
			return
		}
		oldPlain, newPlain, err := decodePasswordBody(ctx)
		if err != nil {
			ctx.Error(err)
			return
		}
		service := svcCtx.CurrentOwnPasswordService()
		if service == nil {
			ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
			return
		}
		if serviceErr := service.Change(ctx.Request.Context(), principal, oldPlain, newPlain); serviceErr != nil {
			ctx.Error(serviceErr)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

// decodePasswordBody supports both the SM4-encrypted envelope and a plain
// JSON body (tests, local tools). Encrypted is the browser contract.
func decodePasswordBody(ctx *rest.Context) (string, string, *coreerrors.Error) {
	var encrypted struct {
		EncryptData string `json:"encryptData"`
	}
	if err := ctx.Bind(&encrypted); err != nil {
		return "", "", coreerrors.New(coreerrors.CodeInvalidArgument, "password body is invalid")
	}
	bodyJSON := encrypted.EncryptData
	if bodyJSON != "" {
		decrypted, decryptErr := smartweb.DecryptEnvelope(bodyJSON)
		if decryptErr != nil {
			return "", "", coreerrors.New(coreerrors.CodeInvalidArgument, "encrypted password cannot be read")
		}
		bodyJSON = decrypted
	}
	var form changeOwnPasswordRequest
	if err := json.Unmarshal([]byte(bodyJSON), &form); err != nil {
		return "", "", coreerrors.New(coreerrors.CodeInvalidArgument, "password fields are required")
	}
	return form.OldPassword, form.NewPassword, nil
}
