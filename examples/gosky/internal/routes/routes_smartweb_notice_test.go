package routes

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/config"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

// Failure-mode inventory:
//
//	OA-N01 unauthenticated and front-realm requests are rejected
//	OA-N02 capability denial surfaces as 403
//	OA-N03 type management round-trips the exact Java path contracts
//	OA-N04 admin notice query returns the fake-backed page
//	OA-N05 employee board resolves the viewer and returns visible rows
//	OA-N06 first view records IP/UA and returns the reloaded detail

type routeNoticeStore struct {
	types  []app.NoticeType
	viewer app.IdentityScope
}

func (s *routeNoticeStore) ListTypes(_ context.Context) ([]app.NoticeType, error) {
	return s.types, nil
}

func (s *routeNoticeStore) AddType(_ context.Context, name string) (uint64, error) {
	id := uint64(len(s.types) + 1)
	s.types = append(s.types, app.NoticeType{ID: id, Name: name})
	return id, nil
}

func (s *routeNoticeStore) UpdateType(_ context.Context, id uint64, name string) (bool, error) {
	for i := range s.types {
		if s.types[i].ID == id {
			s.types[i].Name = name
			return true, nil
		}
	}
	return false, nil
}

func (s *routeNoticeStore) DeleteType(_ context.Context, id uint64) (bool, error) {
	for i := range s.types {
		if s.types[i].ID == id {
			s.types = append(s.types[:i], s.types[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

func (s *routeNoticeStore) TypeInUse(_ context.Context, _ uint64) (bool, error) {
	return false, nil
}

func (s *routeNoticeStore) List(_ context.Context, _ app.NoticeQuery) (app.NoticePage, error) {
	return app.NoticePage{List: []app.Notice{{ID: 1, Title: "Admin row"}}, Total: 1}, nil
}

func (s *routeNoticeStore) Get(_ context.Context, _ uint64) (app.NoticeDetail, error) {
	return app.NoticeDetail{
		Notice: app.Notice{ID: 9, Title: "Published notice", AllVisibleFlag: true,
			PublishFlag: true, PublishTime: time.Now().Add(-time.Minute)},
	}, nil
}

func (s *routeNoticeStore) Insert(_ context.Context, _ app.NoticeInput, _ time.Time) (uint64, error) {
	return 1, nil
}

func (s *routeNoticeStore) Update(_ context.Context, _ app.NoticeInput, _ time.Time) (bool, error) {
	return true, nil
}

func (s *routeNoticeStore) SoftDelete(_ context.Context, _ uint64) error { return nil }

func (s *routeNoticeStore) ResolveViewer(_ context.Context, _ uint64, _ string) (app.IdentityScope, error) {
	return s.viewer, nil
}

func (s *routeNoticeStore) ListVisible(_ context.Context, _ app.IdentityScope, _ app.EmployeeNoticeQuery) (app.NoticePage, error) {
	return app.NoticePage{List: []app.Notice{{ID: 9, Title: "Published notice"}}, Total: 1}, nil
}

func (s *routeNoticeStore) RecordView(_ context.Context, _, _ uint64, _, _ string) (bool, int, int, error) {
	return true, 1, 1, nil
}

func (s *routeNoticeStore) ListViewRecords(_ context.Context, _ app.ViewRecordQuery) (app.NoticeViewRecordPage, error) {
	return app.NoticeViewRecordPage{List: []app.NoticeViewRecord{}, Total: 0}, nil
}

type routeNoticeAuthorizer struct{ denied bool }

func (a routeNoticeAuthorizer) Authorize(_ context.Context, _, _ string) *coreerrors.Error {
	if a.denied {
		return coreerrors.New(coreerrors.CodePermissionDenied, "denied")
	}
	return nil
}

func (routeNoticeAuthorizer) ReloadPolicy() error { return nil }

func newNoticeRouteServer(t *testing.T, denied bool) (*rest.Server, *routeNoticeStore) {
	t.Helper()
	policy := &routeSmartWebRoleAuthorizer{}
	compatibilityService, err := smartweb.NewService(&routeSmartWebAuthenticator{}, policy, nil, &routeSmartWebRoleManager{})
	if err != nil {
		t.Fatalf("new smart-web compatibility service: %v", err)
	}
	store := &routeNoticeStore{viewer: app.IdentityScope{UserID: 1001, DepartmentID: 8, DisplayName: "Admin"}}
	serviceContext := svc.NewServiceContext(config.Config{})
	serviceContext.SetAuthenticationService(&routeAuthenticationService{})
	serviceContext.SetSmartWebService(compatibilityService)
	authorizer := routeNoticeAuthorizer{denied: denied}
	serviceContext.SetNoticeTypeService(app.NewNoticeTypeService(store, authorizer))
	serviceContext.SetNoticeService(app.NewNoticeService(store, authorizer))
	server := rest.MustNewServer(rest.Config{})
	RegisterRoutes(server, serviceContext)
	return server, store
}

func TestRegisterRoutesSmartWebNoticeContract(t *testing.T) {
	server, store := newNoticeRouteServer(t, false)
	adminToken := routeSmartWebRoleToken(t, authn.RealmAdmin)
	frontToken := routeSmartWebRoleToken(t, authn.RealmFront)

	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		var reader io.Reader = http.NoBody
		if body != "" {
			reader = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, reader)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set(auth.AuthorizationHeader, auth.BearerValue(token))
		}
		rec := httptest.NewRecorder()
		server.Handler().ServeHTTP(rec, req)
		return rec
	}

	t.Run("OA-N01 gates unauthenticated and front requests", func(t *testing.T) {
		if rec := request(http.MethodGet, "/oa/noticeType/getAll", "", ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous status = %d, want 401", rec.Code)
		}
		if rec := request(http.MethodPost, "/oa/notice/query", `{}`, frontToken); rec.Code != http.StatusUnauthorized {
			t.Fatalf("front status = %d, want 401", rec.Code)
		}
	})

	t.Run("OA-N02 capability denial returns 403", func(t *testing.T) {
		deniedServer, _ := newNoticeRouteServer(t, true)
		req := httptest.NewRequest(http.MethodGet, "/oa/noticeType/getAll", nil)
		req.Header.Set(auth.AuthorizationHeader, auth.BearerValue(adminToken))
		rec := httptest.NewRecorder()
		deniedServer.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("denied status = %d, want 403; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("OA-N03 type management paths", func(t *testing.T) {
		if rec := request(http.MethodGet, "/oa/noticeType/add/Notice", "", adminToken); rec.Code != http.StatusOK {
			t.Fatalf("add type status = %d body=%s", rec.Code, rec.Body.String())
		}
		if rec := request(http.MethodGet, "/oa/noticeType/update/1/Renamed", "", adminToken); rec.Code != http.StatusOK {
			t.Fatalf("update type status = %d body=%s", rec.Code, rec.Body.String())
		}
		rec := request(http.MethodGet, "/oa/noticeType/getAll", "", adminToken)
		if rec.Code != http.StatusOK {
			t.Fatalf("list types status = %d body=%s", rec.Code, rec.Body.String())
		}
		var response smartweb.ResponseDTO[[]app.NoticeType]
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode types: %v", err)
		}
		if !response.OK || len(response.Data) != 1 || response.Data[0].Name != "Renamed" {
			t.Fatalf("types = %#v", response.Data)
		}
		if rec := request(http.MethodGet, "/oa/noticeType/delete/1", "", adminToken); rec.Code != http.StatusOK {
			t.Fatalf("delete type status = %d body=%s", rec.Code, rec.Body.String())
		}
		if len(store.types) != 0 {
			t.Fatalf("types remaining = %d", len(store.types))
		}
	})

	t.Run("OA-N04 admin query", func(t *testing.T) {
		rec := request(http.MethodPost, "/oa/notice/query", `{"pageNum":1,"pageSize":10}`, adminToken)
		if rec.Code != http.StatusOK {
			t.Fatalf("query status = %d body=%s", rec.Code, rec.Body.String())
		}
		var response smartweb.ResponseDTO[app.NoticePage]
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode page: %v", err)
		}
		if !response.OK || response.Data.Total != 1 || response.Data.List[0].Title != "Admin row" {
			t.Fatalf("page = %#v", response.Data)
		}
	})

	t.Run("OA-N05 employee board", func(t *testing.T) {
		rec := request(http.MethodPost, "/oa/notice/employee/query", `{"pageNum":1,"pageSize":10}`, adminToken)
		if rec.Code != http.StatusOK {
			t.Fatalf("employee query status = %d body=%s", rec.Code, rec.Body.String())
		}
		var response smartweb.ResponseDTO[app.NoticePage]
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode board: %v", err)
		}
		if !response.OK || response.Data.Total != 1 || response.Data.List[0].ID != 9 {
			t.Fatalf("board = %#v", response.Data)
		}
	})

	t.Run("OA-N06 first view", func(t *testing.T) {
		rec := request(http.MethodGet, "/oa/notice/employee/view/9", "", adminToken)
		if rec.Code != http.StatusOK {
			t.Fatalf("view status = %d body=%s", rec.Code, rec.Body.String())
		}
		var response smartweb.ResponseDTO[app.NoticeDetail]
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode detail: %v", err)
		}
		if !response.OK || response.Data.ID != 9 {
			t.Fatalf("detail = %#v", response.Data)
		}
	})
}
