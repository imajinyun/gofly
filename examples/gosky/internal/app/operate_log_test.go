package app

import (
	"context"
	"errors"
	"testing"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

// Failure-mode inventory:
//
//	OL-01 record without a resolver stores the supplied operator id/name.
//	OL-02 record with a resolver maps subject -> user id/display name.
//	OL-03 record payloads are bounded (param/response/UA/URL).
//	OL-04 list/detail require the read capability; detail rejects zero id.
//	OL-05 unknown detail rows map to a client error; store errors to 503.
//	OL-06 own list requires a subject and scopes the query to the resolved id.
type fakeOperateLogStore struct {
	rows   []OperateLog
	page   OperateLogPage
	detail *OperateLog
	last   *OperateLog
	fail   error
	listQ  OperateLogQuery
}

func (s *fakeOperateLogStore) InsertOperateLog(_ context.Context, entry OperateLog) error {
	s.last = &entry
	s.rows = append(s.rows, entry)
	return s.fail
}

func (s *fakeOperateLogStore) ListOperateLogs(_ context.Context, _ uint64, query OperateLogQuery) (OperateLogPage, error) {
	s.listQ = query
	return s.page, s.fail
}

func (s *fakeOperateLogStore) GetOperateLog(_ context.Context, _, id uint64) (OperateLog, error) {
	if s.fail != nil {
		return OperateLog{}, s.fail
	}
	if s.detail != nil {
		return *s.detail, nil
	}
	for _, row := range s.rows {
		if row.ID == id {
			return row, nil
		}
	}
	return OperateLog{}, ErrOperateLogNotFound
}

type staticIdentityResolver struct {
	identity LoginIdentity
	found    bool
	err      error
}

func (r staticIdentityResolver) ResolveIdentityBySubject(_ context.Context, _ uint64, _ string) (LoginIdentity, bool, error) {
	return r.identity, r.found, r.err
}

func operateLogService(store OperateLogStore, caps ...authz.Capability) *OperateLogService {
	service := NewOperateLogService(store, dictCapAuthorizer{allowed: capSet(caps)})
	service.now = func() time.Time { return time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC) }
	return service
}

func TestOperateLogRecord(t *testing.T) {
	t.Run("OL-01 supplied operator is stored", func(t *testing.T) {
		store := &fakeOperateLogStore{}
		service := operateLogService(store)
		err := service.Record(fileCtx(), OperateLogEntry{
			TenantID: 1001, UserID: 9, UserName: "Admin",
			Module: "数据字典", Content: "新增字典", URL: "/support/dict/add",
			Method: "POST", Param: `{"name":"x"}`, Response: `{"ok":true}`,
			IP: "127.0.0.1", SuccessFlag: true,
		})
		if err != nil {
			t.Fatalf("record: %v", err)
		}
		if store.last == nil || store.last.OperateUserID == nil || *store.last.OperateUserID != 9 ||
			store.last.OperateUserName != "Admin" || store.last.TenantID != 1001 || !store.last.SuccessFlag {
			t.Fatalf("stored row = %#v", store.last)
		}
	})

	t.Run("OL-02 subject is resolved to the operator", func(t *testing.T) {
		store := &fakeOperateLogStore{}
		service := operateLogService(store)
		service.SetIdentityResolver(staticIdentityResolver{
			identity: LoginIdentity{UserID: 42, DisplayName: "Luna"}, found: true,
		})
		if err := service.Record(fileCtx(), OperateLogEntry{
			TenantID: 1001, Subject: "user:42", Module: "角色管理", Content: "新增角色",
		}); err != nil {
			t.Fatalf("record: %v", err)
		}
		if store.last.OperateUserID == nil || *store.last.OperateUserID != 42 ||
			store.last.OperateUserName != "Luna" {
			t.Fatalf("resolved row = %#v", store.last)
		}
	})

	t.Run("OL-03 payloads are bounded", func(t *testing.T) {
		store := &fakeOperateLogStore{}
		service := operateLogService(store)
		entry := OperateLogEntry{
			TenantID: 1001, UserID: 1,
			URL:       repeat("/x", maxOperateURLRunes+10),
			Param:     repeat("p", maxOperateParamRunes+10),
			Response:  repeat("r", maxOperateResponseRunes+10),
			UserAgent: repeat("u", maxHelpDocViewerUARunes+10),
			Method:    "POST",
		}
		if err := service.Record(fileCtx(), entry); err != nil {
			t.Fatalf("record: %v", err)
		}
		if len([]rune(store.last.URL)) != maxOperateURLRunes ||
			len([]rune(store.last.Param)) != maxOperateParamRunes ||
			len([]rune(store.last.Response)) != maxOperateResponseRunes ||
			len([]rune(store.last.UserAgent)) != maxHelpDocViewerUARunes {
			t.Fatalf("bounds not applied: url=%d param=%d resp=%d ua=%d",
				len([]rune(store.last.URL)), len([]rune(store.last.Param)),
				len([]rune(store.last.Response)), len([]rune(store.last.UserAgent)))
		}
	})
}

func TestOperateLogConsole(t *testing.T) {
	t.Run("OL-04 capability gates and zero id", func(t *testing.T) {
		store := &fakeOperateLogStore{}
		service := operateLogService(store)
		if _, err := service.List(fileCtx(), OperateLogQuery{}); err == nil || err.Code != coreerrors.CodePermissionDenied {
			t.Fatalf("list without cap = %v", err)
		}
		serviceCap := operateLogService(store, authz.CapabilitySupportOperateLogRead)
		if _, err := serviceCap.Detail(fileCtx(authz.CapabilitySupportOperateLogRead), 0); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("zero detail id = %v", err)
		}
	})

	t.Run("OL-05 missing row and store failure", func(t *testing.T) {
		store := &fakeOperateLogStore{}
		service := operateLogService(store, authz.CapabilitySupportOperateLogRead)
		ctx := fileCtx(authz.CapabilitySupportOperateLogRead)
		if _, err := service.Detail(ctx, 99); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("missing row error = %v", err)
		}
		store.fail = errors.New("db down")
		if _, err := service.Detail(ctx, 99); err == nil || err.Code != coreerrors.CodeUnavailable {
			t.Fatalf("store error = %v", err)
		}
	})

	t.Run("OL-06 own list resolves user id", func(t *testing.T) {
		store := &fakeOperateLogStore{}
		service := operateLogService(store, authz.CapabilitySupportOperateLogRead)
		service.SetIdentityResolver(staticIdentityResolver{
			identity: LoginIdentity{UserID: 77, DisplayName: "Admin"}, found: true,
		})
		page, err := service.ListOwn(fileCtx(), "user:77", OperateLogQuery{PageNum: 1, PageSize: 5})
		if err != nil {
			t.Fatalf("own list: %v", err)
		}
		if page.Total != 0 || store.listQ.OperateUserID == nil || *store.listQ.OperateUserID != 77 {
			t.Fatalf("own query = %#v", store.listQ)
		}
		if _, err := service.ListOwn(fileCtx(), " ", OperateLogQuery{}); err == nil {
			t.Fatal("blank subject accepted")
		}
	})
}
