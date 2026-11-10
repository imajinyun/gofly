// Package smartfile exposes the unmodified SmartAdmin file contracts:
// /support/file/upload, /support/file/queryPage, /support/file/getFileUrl,
// and /support/file/downLoad.
package smartfile

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

const (
	maxUploadMemoryBytes int64 = 24 << 20 // above the 20 MiB accepted size
)

func fileService(svcCtx *svc.ServiceContext) *app.FileService {
	if svcCtx == nil {
		return nil
	}
	return svcCtx.CurrentFileService()
}

// UploadHandler serves POST /support/file/upload?folder=1 with multipart
// field "file".
func UploadHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := fileService(svcCtx)
		if service == nil {
			ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
			return
		}
		// ParseMultipartForm spools to temp files above a small memory cap.
		if err := ctx.Request.ParseMultipartForm(maxUploadMemoryBytes); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "invalid multipart upload"))
			return
		}
		folder, err := parseFolder(ctx)
		if err != nil {
			ctx.Error(err)
			return
		}
		src, fileHeader, fileHeaderErr := ctx.Request.FormFile("file")
		if fileHeaderErr != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "file field is required"))
			return
		}
		defer func() { _ = src.Close() }()
		if fileHeader.Size > app.MaxUploadBytes {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "file exceeds the size limit"))
			return
		}
		contentType := fileHeader.Header.Get("Content-Type")
		key, keyErr := storageKey(fileHeader.Filename)
		if keyErr != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, keyErr.Error()))
			return
		}
		result, uploadErr := service.Upload(ctx.Request.Context(), ctx.RequestID(), folder,
			fileHeader.Filename, contentType, key, src, fileHeader.Size)
		if uploadErr != nil {
			ctx.Error(uploadErr)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(result))
	}
}

// QueryPageHandler serves POST /support/file/queryPage.
func QueryPageHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := fileService(svcCtx)
		if service == nil {
			ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
			return
		}
		var form struct {
			FolderType      *uint8 `json:"folderType"`
			FileName        string `json:"fileName"`
			FileKey         string `json:"fileKey"`
			FileType        string `json:"fileType"`
			CreatorName     string `json:"creatorName"`
			CreateTimeBegin string `json:"createTimeBegin"`
			CreateTimeEnd   string `json:"createTimeEnd"`
			PageNum         uint64 `json:"pageNum"`
			PageSize        uint64 `json:"pageSize"`
		}
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "file query is invalid"))
			return
		}
		page, serviceErr := service.List(ctx.Request.Context(), app.FileQuery{
			FolderType:      form.FolderType,
			FileName:        form.FileName,
			FileKey:         form.FileKey,
			FileType:        form.FileType,
			CreatorName:     form.CreatorName,
			CreateTimeBegin: form.CreateTimeBegin,
			CreateTimeEnd:   form.CreateTimeEnd,
			PageNum:         form.PageNum,
			PageSize:        form.PageSize,
		})
		if serviceErr != nil {
			ctx.Error(serviceErr)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(page))
	}
}

// GetURLHandler serves GET /support/file/getFileUrl?fileKey=...
func GetURLHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := fileService(svcCtx)
		if service == nil {
			ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
			return
		}
		url, err := service.URL(ctx.Request.Context(), strings.TrimSpace(ctx.Request.URL.Query().Get("fileKey")))
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(url))
	}
}

// ViewHandler serves GET /support/file/view?fileKey=&exp=&sig=, a public
// capability-URL endpoint used by <img> tags that cannot send a bearer
// header. The HMAC signature is the authorization proof.
func ViewHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := fileService(svcCtx)
		if service == nil {
			ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
			return
		}
		key := strings.TrimSpace(ctx.Query("fileKey"))
		sig := strings.TrimSpace(ctx.Query("sig"))
		exp, parseErr := strconv.ParseInt(strings.TrimSpace(ctx.Query("exp")), 10, 64)
		if parseErr != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "invalid file url"))
			return
		}
		reader, record, err := service.VerifySigned(ctx.Request.Context(), key, exp, sig)
		if err != nil {
			ctx.Error(err)
			return
		}
		defer func() { _ = reader.Close() }()
		ctx.Response.Header().Set("Cache-Control", "private, max-age=7000")
		ctx.Response.Header().Set("Content-Type", nonEmpty(record.FileType, "application/octet-stream"))
		ctx.Response.Header().Set("Content-Length", strconv.FormatInt(record.FileSize, 10))
		if _, copyErr := io.Copy(ctx.Response, reader); copyErr != nil {
			return
		}
	}
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// DownloadHandler serves GET /support/file/downLoad?fileKey=...
func DownloadHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := fileService(svcCtx)
		if service == nil {
			ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
			return
		}
		reader, record, err := service.Download(ctx.Request.Context(), strings.TrimSpace(ctx.Request.URL.Query().Get("fileKey")))
		if err != nil {
			ctx.Error(err)
			return
		}
		defer func() { _ = reader.Close() }()
		ctx.Response.Header().Set("Content-Type", "application/octet-stream")
		ctx.Response.Header().Set("Content-Disposition",
			"attachment; filename=\""+sanitizeDownloadName(record.FileName)+"\"; fileName="+strconv.Quote(record.FileName))
		ctx.Response.Header().Set("Content-Length", strconv.FormatInt(record.FileSize, 10))
		if _, copyErr := io.Copy(ctx.Response, reader); copyErr != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "stream file failed"))
			return
		}
	}
}

func parseFolder(ctx *rest.Context) (uint8, *coreerrors.Error) {
	raw := strings.TrimSpace(ctx.Request.URL.Query().Get("folder"))
	if raw == "" {
		return 0, coreerrors.New(coreerrors.CodeInvalidArgument, "folder is required")
	}
	folder, err := strconv.ParseUint(raw, 10, 8)
	if err != nil || folder == 0 {
		return 0, coreerrors.New(coreerrors.CodeInvalidArgument, "folder is invalid")
	}
	return uint8(folder), nil
}

// storageKey derives a client-independent uuid key, preserving a sanitized
// extension from the original name.
func storageKey(fileName string) (string, *coreerrors.Error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", coreerrors.New(coreerrors.CodeInternal, "generate storage key")
	}
	key := hex.EncodeToString(nonce[:])
	ext := strings.ToLower(filepath.Ext(filepath.Base(fileName)))
	if len(ext) > 1 && len(ext) <= 10 && isSafeExt(ext[1:]) {
		key += ext
	}
	return key, nil
}

func isSafeExt(value string) bool {
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

func sanitizeDownloadName(name string) string {
	name = filepath.Base(name)
	name = strings.NewReplacer("\"", "", "\r", "", "\n", "").Replace(name)
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "download"
	}
	return name
}
