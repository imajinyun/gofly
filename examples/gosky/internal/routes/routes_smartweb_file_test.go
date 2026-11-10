package routes

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartfile"
	goskyapp "github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
	"github.com/imajinyun/gofly/examples/gosky/internal/config"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

// Failure-mode inventory:
//
//	FILE-HTTP-01 multipart upload requires an admin session (30008 otherwise)
//	FILE-HTTP-02 upload returns fileKey/fileUrl; signed view serves the bytes publicly
//	FILE-HTTP-03 a tampered/expired signed URL is rejected
//	FILE-HTTP-04 queryPage returns the SmartAdmin {list,total} envelope
func newFileTestServer(t *testing.T) (*rest.Server, *routeFileStore, *routeFileBlobs) {
	t.Helper()
	compatibilityService, err := smartweb.NewService(roleFileAuthenticator{}, &routeSmartWebRoleAuthorizer{}, nil)
	if err != nil {
		t.Fatalf("new compatibility service: %v", err)
	}
	store := newRouteFileStore()
	blobs := newRouteFileBlobs()
	fileService := goskyapp.NewFileService(store, blobs, routeFileAuthorizer{}, []byte("routes-file-test-secret"))
	serviceContext := svc.NewServiceContext(config.Config{})
	serviceContext.SetAuthenticationService(&routeAuthenticationService{})
	serviceContext.SetSmartWebService(compatibilityService)
	serviceContext.SetFileService(fileService)

	server := rest.MustNewServer(rest.Config{})
	RegisterRoutes(server, serviceContext)
	return server, store, blobs
}

type filesAuthorizer struct{}

func (filesAuthorizer) NewMySQLAuthorizerWithTables(string, interface{}, interface{}, []byte) {}
func (filesAuthorizer) Authorize(context.Context, string, string) error                       { return nil }

func TestSmartWebFileUploadAndSignedView(t *testing.T) {
	server, _, _ := newFileTestServer(t)
	adminToken := routeSmartWebRoleToken(t, authn.RealmAdmin)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("folder", "1")
	part, err := writer.CreateFormFile("file", "hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("file-bytes")); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()

	upload := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/support/file/upload?folder=1", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+adminToken)
	server.Handler().ServeHTTP(upload, request)
	if upload.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%s", upload.Code, upload.Body.String())
	}
	var uploaded struct {
		Code int `json:"code"`
		Data struct {
			FileKey string `json:"fileKey"`
			FileURL string `json:"fileUrl"`
		} `json:"data"`
	}
	if err := json.Unmarshal(upload.Body.Bytes(), &uploaded); err != nil {
		t.Fatal(err)
	}
	if uploaded.Code != 0 || !strings.HasPrefix(uploaded.Data.FileURL, "/support/file/view?") {
		t.Fatalf("upload response = %s", upload.Body.String())
	}

	// Public view: no Authorization header, signature proves access.
	view := httptest.NewRecorder()
	server.Handler().ServeHTTP(view, httptest.NewRequest(http.MethodGet, uploaded.Data.FileURL, nil))
	if view.Code != http.StatusOK || view.Body.String() != "file-bytes" {
		t.Fatalf("view status=%d body=%q", view.Code, view.Body.String())
	}

	// Tampered signature rejected.
	tampered := strings.Replace(uploaded.Data.FileURL, "sig=", "sig=AAAA", 1)
	rejected := httptest.NewRecorder()
	server.Handler().ServeHTTP(rejected, httptest.NewRequest(http.MethodGet, tampered, nil))
	if rejected.Code != http.StatusForbidden {
		t.Fatalf("tampered view status=%d, want 403", rejected.Code)
	}

	// Upload without session uses the numeric compat code.
	anon := httptest.NewRecorder()
	anonReq := httptest.NewRequest(http.MethodPost, "/support/file/upload?folder=1", strings.NewReader(""))
	server.Handler().ServeHTTP(anon, anonReq)
	if anon.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous upload status=%d, want 401", anon.Code)
	}
	var failure struct {
		Code int `json:"code"`
	}
	_ = json.Unmarshal(anon.Body.Bytes(), &failure)
	if failure.Code != smartweb.CodeNotAuthenticated {
		t.Fatalf("anonymous upload body=%s, want code %d", anon.Body.String(), smartweb.CodeNotAuthenticated)
	}
}

func TestSmartWebFileQueryPage(t *testing.T) {
	server, _, _ := newFileTestServer(t)
	token := routeSmartWebRoleToken(t, authn.RealmAdmin)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/support/file/queryPage", strings.NewReader(`{"pageNum":1,"pageSize":10}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("queryPage status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"list"`) || !strings.Contains(recorder.Body.String(), `"total"`) {
		t.Fatalf("queryPage body=%s, want list/total envelope", recorder.Body.String())
	}
}

// In-memory fakes implementing app.FileStore / app.FileBlobStore.

type routeFileStore struct {
	mu     sync.Mutex
	rows   map[string]goskyapp.FileRecord
	nextID uint64
}

func newRouteFileStore() *routeFileStore {
	return &routeFileStore{rows: make(map[string]goskyapp.FileRecord), nextID: 1000}
}

func (s *routeFileStore) Create(_ context.Context, tenantID uint64, record goskyapp.FileRecord, _ string) (goskyapp.FileRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	record.FileID = s.nextID
	record.TenantIDValue = tenantID
	s.rows[record.FileKey] = record
	return record, nil
}

func (s *routeFileStore) List(_ context.Context, tenantID uint64, query goskyapp.FileQuery) (goskyapp.FilePage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]goskyapp.FileRecord, 0)
	for _, record := range s.rows {
		if record.TenantIDValue == tenantID {
			items = append(items, record)
		}
	}
	return goskyapp.FilePage{List: items, Total: len(items)}, nil
}

func (s *routeFileStore) Get(_ context.Context, tenantID uint64, key string) (goskyapp.FileRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record, ok := s.rows[key]; ok && record.TenantIDValue == tenantID {
		return record, nil
	}
	return goskyapp.FileRecord{}, io.EOF
}

func (s *routeFileStore) GetByKey(_ context.Context, key string) (goskyapp.FileRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record, ok := s.rows[key]; ok {
		return record, nil
	}
	return goskyapp.FileRecord{}, io.EOF
}

type routeFileBlobs struct{ data sync.Map }

func newRouteFileBlobs() *routeFileBlobs { return &routeFileBlobs{} }

func (b *routeFileBlobs) Put(_ context.Context, tenantID uint64, folder uint8, key, _ string, content io.Reader, declared int64) error {
	buf := new(bytes.Buffer)
	if _, err := io.Copy(buf, content); err != nil {
		return err
	}
	if int64(buf.Len()) != declared {
		return io.ErrUnexpectedEOF
	}
	b.data.Store(fmt.Sprintf("%d/%d/%s", tenantID, folder, key), buf.Bytes())
	return nil
}

func (b *routeFileBlobs) Open(_ context.Context, tenantID uint64, folder uint8, key string) (io.ReadCloser, goskyapp.FileRecord, error) {
	value, ok := b.data.Load(fmt.Sprintf("%d/%d/%s", tenantID, folder, key))
	if !ok {
		return nil, goskyapp.FileRecord{}, io.EOF
	}
	return io.NopCloser(bytes.NewReader(value.([]byte))), goskyapp.FileRecord{}, nil
}

type routeFileAuthorizer struct{}

func (routeFileAuthorizer) Authorize(context.Context, string, string) *coreerrors.Error { return nil }
func (routeFileAuthorizer) ReloadPolicy() error                                         { return nil }

var (
	_ goskyapp.FileStore     = (*routeFileStore)(nil)
	_ goskyapp.FileBlobStore = (*routeFileBlobs)(nil)
	_                        = hmac.New
	_                        = sha256.New
	_                        = base64.RawURLEncoding
	_                        = smartfile.UploadHandler
	_                        = authz.CapabilitySupportFileRead
	_                        = time.Second
)

// roleFileAuthenticator returns admin/project capabilities so the smartweb
// service navigation and role projections work in this test server.
type roleFileAuthenticator struct{}

func (roleFileAuthenticator) LoginSmartWeb(context.Context, authn.SmartWebLoginInput) (authn.SmartWebLoginResult, error) {
	return authn.SmartWebLoginResult{}, authn.ErrAuthenticationRejected
}
func (roleFileAuthenticator) SmartWebLoginInfo(context.Context, authn.RequestPrincipal) (authn.SmartWebIdentity, error) {
	return authn.SmartWebIdentity{UserID: 1, TenantID: 1001, Subject: "admin-demo", LoginName: "admin"}, nil
}
func (roleFileAuthenticator) Logout(context.Context, authn.LogoutInput) error { return nil }
