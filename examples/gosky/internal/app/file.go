package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

// File folder types mirror the unchanged SmartAdmin FILE_FOLDER_TYPE_ENUM.
const (
	FileFolderCommon   uint8 = 1
	FileFolderNotice   uint8 = 2
	FileFolderHelpDoc  uint8 = 3
	FileFolderFeedback uint8 = 4
)

const (
	MaxUploadBytes    int64 = 20 << 20 // 20 MiB
	MaxFileNameLength       = 255
)

// FileRecord is the credential-free metadata projection for the file console.
type FileRecord struct {
	TenantIDValue   uint64    `json:"-"`
	FileID          uint64    `json:"fileId"`
	FolderType      uint8     `json:"folderType"`
	FileName        string    `json:"fileName"`
	FileKey         string    `json:"fileKey"`
	FileSize        int64     `json:"fileSize"`
	FileType        string    `json:"fileType"`
	CreatorID       uint64    `json:"creatorId"`
	CreatorUserType int       `json:"creatorUserType"`
	CreatorName     string    `json:"creatorName"`
	CreatedAt       time.Time `json:"createTime"`
	UpdatedAt       time.Time `json:"updateTime"`
}

// FileUpload is the post-upload response consumed by the unchanged upload
// component (it reads fileUrl/fileKey/fileName/fileSize/fileType).
type FileUpload struct {
	FileID   uint64 `json:"fileId"`
	FileName string `json:"fileName"`
	FileURL  string `json:"fileUrl"`
	FileKey  string `json:"fileKey"`
	FileSize int64  `json:"fileSize"`
	FileType string `json:"fileType"`
}

// FilePage is the SmartAdmin list response ({list,total}).
type FilePage struct {
	List  []FileRecord `json:"list"`
	Total int          `json:"total"`
}

// FileQuery carries the file console filters; dates are bound LIKE ranges.
type FileQuery struct {
	FolderType      *uint8
	FileName        string
	FileKey         string
	FileType        string
	CreatorName     string
	CreateTimeBegin string
	CreateTimeEnd   string
	PageNum         uint64
	PageSize        uint64
}

// FileStore is the metadata repository port.
type FileStore interface {
	Create(ctx context.Context, tenantID uint64, record FileRecord, uploaderSubject string) (FileRecord, error)
	List(ctx context.Context, tenantID uint64, query FileQuery) (FilePage, error)
	Get(ctx context.Context, tenantID uint64, fileKey string) (FileRecord, error)
	GetByKey(ctx context.Context, fileKey string) (FileRecord, error)
}

// FileBlobStore stores and retrieves raw bytes. The local implementation
// enforces tenant/key path safety; a future OSS driver implements the same port.
// TenantID exposes the owning tenant for signed-URL verification.
func (f FileRecord) TenantID() uint64 { return f.TenantIDValue }

type FileBlobStore interface {
	// Put writes content under tenantID/folderType/<key>. fileName and
	// contentType are metadata only and must never influence the path.
	Put(ctx context.Context, tenantID uint64, folderType uint8, key, contentType string, content io.Reader, size int64) error
	// Open returns the content and metadata for a previously stored key.
	Open(ctx context.Context, tenantID uint64, folderType uint8, key string) (io.ReadCloser, FileRecord, error)
}

// FileService is the tenant-scoped file use case.
// SignedFileURL is a time-limited capability URL for <img>/public access.
type SignedFileURL struct {
	URL       string
	ExpiresAt int64
}

type FileService struct {
	store      FileStore
	blobs      FileBlobStore
	authorizer RBACAuthorizer
	signingKey []byte
	signTTL    time.Duration
}

func NewFileService(store FileStore, blobs FileBlobStore, authorizer RBACAuthorizer, signingKey []byte) *FileService {
	return &FileService{
		store:      store,
		blobs:      blobs,
		authorizer: authorizer,
		signingKey: append([]byte(nil), signingKey...),
		signTTL:    2 * time.Hour,
	}
}

// Sign returns a capability URL for a key: /support/file/view?fileKey=&exp=&sig=
// with HMAC-SHA256 over tenant|key|exp. Used by <img> tags that cannot send
// the bearer header.
func (s *FileService) Sign(tenantID uint64, key string, now time.Time) SignedFileURL {
	expires := now.Add(s.signTTL).Unix()
	mac := hmac.New(sha256.New, s.signingKey)
	fmt.Fprintf(mac, "%d|%s|%d", tenantID, key, expires)
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return SignedFileURL{
		URL:       "/support/file/view?fileKey=" + key + "&exp=" + strconv.FormatInt(expires, 10) + "&sig=" + sig,
		ExpiresAt: expires,
	}
}

// VerifySigned validates a capability URL without an authenticated principal
// and opens the referenced blob across tenants by key lookup.
func (s *FileService) VerifySigned(ctx context.Context, key string, expires int64, sig string) (io.ReadCloser, FileRecord, *coreerrors.Error) {
	if len(s.signingKey) == 0 {
		return nil, FileRecord{}, coreerrors.New(coreerrors.CodeFailedPrecondition, "signed file urls are disabled")
	}
	if time.Now().Unix() > expires {
		return nil, FileRecord{}, coreerrors.New(coreerrors.CodePermissionDenied, "file url expired")
	}
	mac := hmac.New(sha256.New, s.signingKey)
	provided, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return nil, FileRecord{}, coreerrors.New(coreerrors.CodePermissionDenied, "invalid file signature")
	}
	_ = provided
	record, storeErr := s.store.GetByKey(ctx, key)
	if storeErr != nil {
		return nil, FileRecord{}, coreerrors.Wrap(coreerrors.CodeNotFound, "file not found", storeErr)
	}
	fmt.Fprintf(mac, "%d|%s|%d", record.TenantID(), key, expires)
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return nil, FileRecord{}, coreerrors.New(coreerrors.CodePermissionDenied, "invalid file signature")
	}
	reader, blobRecord, openErr := s.blobs.Open(ctx, record.TenantID(), record.FolderType, record.FileKey)
	if openErr != nil {
		return nil, FileRecord{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "open file", openErr)
	}
	_ = blobRecord
	return reader, record, nil
}

// Upload validates the upload, stores bytes, and records metadata in one use
// case. folder is the numeric SmartAdmin folder type.
func (s *FileService) Upload(ctx context.Context, requestID string, folder uint8, fileName, contentType, key string, content io.Reader, size int64) (FileUpload, *coreerrors.Error) {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportFileManage)
	if err != nil {
		return FileUpload{}, err
	}
	if err := validateFolder(folder); err != nil {
		return FileUpload{}, err
	}
	fileName = strings.TrimSpace(fileName)
	if fileName == "" || len([]rune(fileName)) > MaxFileNameLength || strings.ContainsAny(fileName, "/\\\x00") {
		return FileUpload{}, coreerrors.New(coreerrors.CodeInvalidArgument, "file name is invalid")
	}
	if size <= 0 || size > MaxUploadBytes {
		return FileUpload{}, coreerrors.New(coreerrors.CodeInvalidArgument, "file size is invalid")
	}
	contentType = normalizeContentType(contentType)
	if err := s.blobs.Put(ctx, tenantID, folder, key, contentType, io.LimitReader(content, MaxUploadBytes+1), size); err != nil {
		return FileUpload{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "store file", err)
	}
	actor := actorFromPrincipal(ctx, tenantID, requestID)
	record := FileRecord{
		FolderType: folder,
		FileName:   fileName,
		FileKey:    key,
		FileSize:   size,
		FileType:   contentType,
	}
	stored, storeErr := s.store.Create(ctx, tenantID, record, actor.Subject)
	if storeErr != nil {
		return FileUpload{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "record file metadata", storeErr)
	}
	return FileUpload{
		FileID:   stored.FileID,
		FileName: stored.FileName,
		FileURL:  s.Sign(tenantID, stored.FileKey, time.Now()).URL,
		FileKey:  stored.FileKey,
		FileSize: stored.FileSize,
		FileType: stored.FileType,
	}, nil
}

// List returns the paged file console view for the verified tenant.
func (s *FileService) List(ctx context.Context, query FileQuery) (FilePage, *coreerrors.Error) {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportFileRead)
	if err != nil {
		return FilePage{}, err
	}
	if query.FolderType != nil {
		if verr := validateFolder(*query.FolderType); verr != nil {
			return FilePage{}, verr
		}
	}
	query.PageNum, query.PageSize = NormalizePage(query.PageNum, query.PageSize)
	page, storeErr := s.store.List(ctx, tenantID, query)
	if storeErr != nil {
		return FilePage{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "query files", storeErr)
	}
	return page, nil
}

// URL returns a signed browser URL for one tenant-owned key.
func (s *FileService) URL(ctx context.Context, fileKey string) (string, *coreerrors.Error) {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportFileRead)
	if err != nil {
		return "", err
	}
	record, storeErr := s.store.Get(ctx, tenantID, fileKey)
	if storeErr != nil {
		return "", coreerrors.Wrap(coreerrors.CodeNotFound, "file not found", storeErr)
	}
	return s.Sign(tenantID, record.FileKey, time.Now()).URL, nil
}

// Download opens the stored bytes for one tenant-owned key.
func (s *FileService) Download(ctx context.Context, fileKey string) (io.ReadCloser, FileRecord, *coreerrors.Error) {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportFileRead)
	if err != nil {
		return nil, FileRecord{}, err
	}
	record, storeErr := s.store.Get(ctx, tenantID, fileKey)
	if storeErr != nil {
		return nil, FileRecord{}, coreerrors.Wrap(coreerrors.CodeNotFound, "file not found", storeErr)
	}
	reader, _, openErr := s.blobs.Open(ctx, tenantID, record.FolderType, record.FileKey)
	if openErr != nil {
		return nil, FileRecord{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "open file", openErr)
	}
	return reader, record, nil
}

func validateFolder(folder uint8) *coreerrors.Error {
	if folder < FileFolderCommon || folder > FileFolderFeedback {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "file folder type is invalid")
	}
	return nil
}

func normalizeContentType(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" || len(value) > 128 || strings.ContainsAny(value, "\r\n") {
		return "application/octet-stream"
	}
	return value
}
