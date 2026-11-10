// Package smartencrypt exposes the unmodified SmartAdmin API-encryption
// demo contracts (support/apiEncrypt). Requests arrive as
// {"encryptData": base64(SM4-hex)} and encrypted responses carry the
// ciphertext in data with dataType=10, exactly like the web demo.
package smartencrypt

import (
	"encoding/json"
	"net/http"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/apiencrypt"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

// encryptForm mirrors AdminApiEncryptController.JweForm.
type encryptForm struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func (form encryptForm) valid() *coreerrors.Error {
	if form.Name == "" {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "姓名不能为空")
	}
	if form.Age < 1 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "age 必须大于等于 1")
	}
	return nil
}

type encryptedRequest struct {
	EncryptData string `json:"encryptData"`
}

func writeEncrypted(ctx *rest.Context, payload any) {
	plaintext, err := json.Marshal(payload)
	if err != nil {
		ctx.Error(coreerrors.New(coreerrors.CodeInternal, "响应序列化失败"))
		return
	}
	ciphertext, err := apiencrypt.Encrypt(string(plaintext))
	if err != nil {
		ctx.Error(coreerrors.New(coreerrors.CodeInternal, "响应加密失败"))
		return
	}
	ctx.JSON(http.StatusOK, smartweb.ResponseDTO[string]{
		Code:     0,
		Msg:      "操作成功",
		OK:       true,
		Data:     ciphertext,
		DataType: smartweb.DataTypeEncrypt,
	})
}

func decryptBody(ctx *rest.Context, target any) bool {
	var wrapper encryptedRequest
	if err := ctx.Bind(&wrapper); err != nil {
		ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "加密请求格式错误"))
		return false
	}
	if wrapper.EncryptData == "" {
		// The Java advice passes the raw body through when encryptData is
		// absent; the demo always sends it, so reject here.
		ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "encryptData 不能为空"))
		return false
	}
	plaintext, err := apiencrypt.Decrypt(wrapper.EncryptData)
	if err != nil {
		ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "请求解密失败"))
		return false
	}
	if err := json.Unmarshal([]byte(plaintext), target); err != nil {
		ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "解密后的参数格式错误"))
		return false
	}
	return true
}

// RequestEncryptHandler serves POST testRequestEncrypt: encrypted
// request, plaintext response.
func RequestEncryptHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form encryptForm
		if !decryptBody(ctx, &form) {
			return
		}
		if err := form.valid(); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(form))
	}
}

// ResponseEncryptHandler serves POST testResponseEncrypt: plaintext
// request, encrypted response.
func ResponseEncryptHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form encryptForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "参数格式错误"))
			return
		}
		if err := form.valid(); err != nil {
			ctx.Error(err)
			return
		}
		writeEncrypted(ctx, form)
	}
}

// BothHandler serves POST testDecryptAndEncrypt: encrypted request and
// encrypted response.
func BothHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form encryptForm
		if !decryptBody(ctx, &form) {
			return
		}
		if err := form.valid(); err != nil {
			ctx.Error(err)
			return
		}
		writeEncrypted(ctx, form)
	}
}

// ArrayHandler serves POST testArray: encrypted array request and
// encrypted array response.
func ArrayHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var forms []encryptForm
		if !decryptBody(ctx, &forms) {
			return
		}
		for _, form := range forms {
			if err := form.valid(); err != nil {
				ctx.Error(err)
				return
			}
		}
		writeEncrypted(ctx, forms)
	}
}
