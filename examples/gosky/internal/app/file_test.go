package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

// Failure-mode inventory:
//  FSVC-01 upload requires support.file.manage and valid folder/name/size
//  FSVC-02 uploaded metadata and blob round-trip; fileUrl is a signed /view URL
//  FSVC-03 list requires support.file.read and paginates via the shared rules
//  FSVC-04 signed URL verifies HMAC and rejects tampered key/expiry
//  FSVC-05 expired signatures are rejected
//  FSVC-06 read URLs require the read capability

type fakeFileStore struct {
	mu     sync.Mutex
	rows   map[string]FileRecord
	nextID uint64
}

func newFakeFileStore() *fakeFileStore {
	return &fakeFileStore{rows: make(map[string]FileRecord), nextID: 100}
}

func (f *fakeFileStore) key(tenantID uint64, key string) string {
	return string(rune(tenantID)) + "|" + key
}

func (f *fakeFileStore) Create(_ context.Context, tenantID uint64, record FileRecord, subject string) (FileRecord, error) {
	if subject != "user:admin" {
		return FileRecord{}, io.EOF
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	record.FileID = f.nextID
	record.TenantIDValue = tenantID
	record.CreatorID = 1001
	record.CreatorName = "Gosky Admin"
	record.CreatedAt = time.Now()
	f.rows[f.key(tenantID, record.FileKey)] = record
	return record, nil
}

func (f *fakeFileStore) List(_ context.Context, tenantID uint64, query FileQuery) (FilePage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := make([]FileRecord, 0)
	for _, record := range f.rows {
		if record.TenantIDValue == tenantID && (query.FolderType == nil || record.FolderType == *query.FolderType) {
			items = append(items, record)
		}
	}
	page := Paginate(items, query.PageNum, query.PageSize)
	return FilePage{List: append([]FileRecord(nil), page...), Total: len(items)}, nil
}

func (f *fakeFileStore) Get(_ context.Context, tenantID uint64, key string) (FileRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if record, ok := f.rows[f.key(tenantID, key)]; ok {
		return record, nil
	}
	return FileRecord{}, io.EOF
}

func (f *fakeFileStore) GetByKey(_ context.Context, key string) (FileRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, record := range f.rows {
		if record.FileKey == key {
			return record, nil
		}
	}
	return FileRecord{}, io.EOF
}

type fakeBlobStore struct{ data map[string][]byte }

func newFakeBlobStore() *fakeBlobStore { return &fakeBlobStore{data: make(map[string][]byte)} }

func (b *fakeBlobStore) Put(_ context.Context, tenantID uint64, folder uint8, key, _ string, content io.Reader, declared int64) error {
	buf := new(bytes.Buffer)
	if _, err := io.Copy(buf, content); err != nil {
		return err
	}
	if int64(buf.Len()) != declared {
		return io.ErrUnexpectedEOF
	}
	b.data[string(rune(tenantID))+string(rune(folder))+"|"+key] = append([]byte(nil), buf.Bytes()...)
	return nil
}

func (b *fakeBlobStore) Open(_ context.Context, tenantID uint64, folder uint8, key string) (io.ReadCloser, FileRecord, error) {
	data, ok := b.data[string(rune(tenantID))+string(rune(folder))+"|"+key]
	if !ok {
		return nil, FileRecord{}, io.EOF
	}
	return io.NopCloser(bytes.NewReader(data)), FileRecord{}, nil
}

func fileCtx(caps ...authz.Capability) context.Context {
	allowed := make(map[authz.Capability]bool, len(caps))
	for _, capability := range caps {
		allowed[capability] = true
	}
	return auth.NewContext(context.Background(), auth.Principal{
		Subject: "user:admin",
		Claims:  map[string]any{"tenant_id": "1001", "realm": "admin", "sid": "s1"},
	})
}

type fileCapAuthorizer struct{ allowed map[authz.Capability]bool }

func (a fileCapAuthorizer) Authorize(ctx context.Context, capability, action string) *coreerrors.Error {
	if a.allowed[authz.Capability(capability)] {
		return nil
	}
	return coreerrors.New(coreerrors.CodePermissionDenied, "denied")
}

func (a fileCapAuthorizer) ReloadPolicy() error { return nil }

func newFileService(authorizer RBACAuthorizer) *FileService {
	return NewFileService(newFakeFileStore(), newFakeBlobStore(), authorizer, []byte("unit-test-signing-secret"))
}

func TestFileServiceUploadValidation(t *testing.T) {
	service := newFileService(fileCapAuthorizer{allowed: map[authz.Capability]bool{authz.CapabilitySupportFileManage: true}})
	ctx := fileCtx(authz.CapabilitySupportFileManage)

	for _, tc := range []struct {
		name     string
		folder   uint8
		fileName string
		size     int64
	}{
		{name: "folder zero", folder: 0, fileName: "a.png", size: 10},
		{name: "folder too large", folder: 9, fileName: "a.png", size: 10},
		{name: "empty name", folder: 1, fileName: "  ", size: 10},
		{name: "path in name", folder: 1, fileName: "../x.png", size: 10},
		{name: "zero size", folder: 1, fileName: "a.png", size: 0},
		{name: "too large", folder: 1, fileName: "a.png", size: MaxUploadBytes + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.Upload(ctx, "req-1", tc.folder, tc.fileName, "image/png",
				"0000000000000000000000000000000a", strings.NewReader("0123456789"), tc.size)
			if err == nil || err.Code != coreerrors.CodeInvalidArgument {
				t.Fatalf("error = %#v, want invalid_argument", err)
			}
		})
	}
}

func TestFileServiceUploadRequiresManageCapability(t *testing.T) {
	service := newFileService(fileCapAuthorizer{allowed: map[authz.Capability]bool{authz.CapabilitySupportFileRead: true}})
	_, err := service.Upload(fileCtx(), "req-1", FileFolderCommon, "a.png", "image/png",
		"0000000000000000000000000000000a", strings.NewReader("0123456789"), 10)
	if err == nil || err.Code != coreerrors.CodePermissionDenied {
		t.Fatalf("upload without manage capability error = %#v", err)
	}
}

func TestFileServiceUploadAndSignedView(t *testing.T) {
	service := newFileService(fileCapAuthorizer{allowed: map[authz.Capability]bool{
		authz.CapabilitySupportFileManage: true,
		authz.CapabilitySupportFileRead:   true,
	}})
	ctx := fileCtx(authz.CapabilitySupportFileManage, authz.CapabilitySupportFileRead)
	content := "signed-view-content"
	upload, err := service.Upload(ctx, "req-1", FileFolderFeedback, "feedback.txt", "text/plain",
		"0000000000000000000000000000000b", strings.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if upload.FileKey != "0000000000000000000000000000000b" || upload.FileSize != int64(len(content)) ||
		!strings.HasPrefix(upload.FileURL, "/support/file/view?fileKey=") {
		t.Fatalf("upload projection = %#v", upload)
	}

	// getFileUrl returns the same signed shape and requires read capability.
	signed, err := service.URL(ctx, upload.FileKey)
	if err != nil || !strings.Contains(signed, "exp=") || !strings.Contains(signed, "sig=") {
		t.Fatalf("signed url = %q, %v", signed, err)
	}

	query := strings.SplitN(strings.TrimPrefix(signed, "/support/file/view?"), "&", 0)
	_ = query
	parsed := parseViewQuery(t, signed)
	reader, record, verifyErr := service.VerifySigned(ctx, parsed.key, parsed.exp, parsed.sig)
	if verifyErr != nil {
		t.Fatalf("verify signed: %v", verifyErr)
	}
	defer reader.Close()
	data, _ := io.ReadAll(reader)
	if string(data) != content || record.FileName != "feedback.txt" {
		t.Fatalf("signed view data=%q record=%#v", data, record)
	}

	// Tampering with the key invalidates the signature.
	if _, _, err := service.VerifySigned(ctx, "0000000000000000000000000000000c", parsed.exp, parsed.sig); err == nil {
		t.Fatal("tampered key accepted")
	}
	// Expired signatures are rejected.
	expired := time.Now().Add(-time.Minute).Unix()
	signature := signForTest(t, service, record.TenantID(), upload.FileKey, expired)
	if _, _, err := service.VerifySigned(ctx, upload.FileKey, expired, signature); err == nil {
		t.Fatal("expired signature accepted")
	}
}

func TestFileServiceListRequiresReadAndPaginates(t *testing.T) {
	allowed := fileCapAuthorizer{allowed: map[authz.Capability]bool{
		authz.CapabilitySupportFileManage: true,
		authz.CapabilitySupportFileRead:   true,
	}}
	service := newFileService(allowed)
	ctx := fileCtx(authz.CapabilitySupportFileManage, authz.CapabilitySupportFileRead)
	for i := 0; i < 3; i++ {
		key := strings.Repeat("a", 31) + string(rune('a'+i))
		if _, err := service.Upload(ctx, "req", FileFolderCommon, "f.txt", "text/plain", key, strings.NewReader("x"), 1); err != nil {
			t.Fatalf("upload %d: %v", i, err)
		}
	}
	page, err := service.List(ctx, FileQuery{PageNum: 1, PageSize: 2})
	if err != nil || page.Total != 3 || len(page.List) != 2 {
		t.Fatalf("first page = %#v, %v", page, err)
	}
	page, err = service.List(ctx, FileQuery{PageNum: 2, PageSize: 2})
	if err != nil || len(page.List) != 1 {
		t.Fatalf("second page = %#v, %v", page, err)
	}

	readDenied := newFileService(fileCapAuthorizer{allowed: map[authz.Capability]bool{}})
	if _, err := readDenied.List(fileCtx(), FileQuery{PageNum: 1, PageSize: 10}); err == nil {
		t.Fatal("list without read capability accepted")
	}
}

type viewQuery struct {
	key string
	exp int64
	sig string
}

func parseViewQuery(t *testing.T, raw string) viewQuery {
	t.Helper()
	query := strings.TrimPrefix(raw, "/support/file/view?")
	values := make(map[string]string)
	for _, part := range strings.Split(query, "&") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			t.Fatalf("bad query %q", query)
		}
		values[key] = value
	}
	var exp int64
	if _, err := parseTestInt(values["exp"], &exp); err != nil {
		t.Fatalf("parse exp: %v", err)
	}
	return viewQuery{key: values["fileKey"], exp: exp, sig: values["sig"]}
}

func parseTestInt(value string, target *int64) (int, error) {
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, io.EOF
		}
		*target = *target*10 + int64(r-'0')
	}
	return len(value), nil
}

func signForTest(t *testing.T, service *FileService, tenantID uint64, key string, expires int64) string {
	t.Helper()
	// Reuse the service's HMAC by asking for a URL near the desired expiry is
	// not possible, so compute with the same documented scheme directly.
	return computeTestSignature(service.signingKey, tenantID, key, expires)
}

func computeTestSignature(key []byte, tenantID uint64, fileKey string, expires int64) string {
	mac := hmac.New(sha256.New, key)
	fmt.Fprintf(mac, "%d|%s|%d", tenantID, fileKey, expires)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
