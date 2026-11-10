package smartoperatelog

import (
	"context"
	"testing"

	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
)

type middlewareStore struct {
	inserted []app.OperateLog
}

func (s *middlewareStore) InsertOperateLog(_ context.Context, entry app.OperateLog) error {
	s.inserted = append(s.inserted, entry)
	return nil
}
func (s *middlewareStore) ListOperateLogs(_ context.Context, _ uint64, _ app.OperateLogQuery) (app.OperateLogPage, error) {
	return app.OperateLogPage{}, nil
}
func (s *middlewareStore) GetOperateLog(_ context.Context, _, _ uint64) (app.OperateLog, error) {
	return app.OperateLog{}, nil
}

type middlewareResolver struct{}

func (middlewareResolver) ResolveIdentityBySubject(_ context.Context, _ uint64, _ string) (app.LoginIdentity, bool, error) {
	return app.LoginIdentity{UserID: 9, DisplayName: "Admin"}, true, nil
}

func TestCatalogLabel(t *testing.T) {
	module, content, audited := catalogLabel("/support/dict/add")
	if !audited || module != "数据字典" || content != "新增字典" {
		t.Fatalf("catalog label = %q/%q/%v", module, content, audited)
	}
	if _, _, audited := catalogLabel("/support/dict/queryPage"); audited {
		t.Fatal("read route should not be audited")
	}
}

func TestRecordAfterBuildsOperateLog(t *testing.T) {
	store := &middlewareStore{}
	service := app.NewOperateLogService(store, nil)
	service.SetIdentityResolver(middlewareResolver{})

	principal := authn.RequestPrincipal{Subject: "user:9", TenantID: 1001, Realm: authn.RealmAdmin}
	entry := buildEntry(principal, "/support/dict/add", "POST",
		`{"name":"system"}`, `{"code":0,"ok":true,"msg":"操作成功"}`, 200, "127.0.0.1", "ua")
	if err := service.Record(context.Background(), entry); err != nil {
		t.Fatalf("record: %v", err)
	}
	if len(store.inserted) != 1 {
		t.Fatalf("inserted = %d", len(store.inserted))
	}
	row := store.inserted[0]
	if row.Module != "数据字典" || row.Content != "新增字典" || !row.SuccessFlag ||
		row.Method != "POST" || row.URL != "/support/dict/add" {
		t.Fatalf("row = %#v", row)
	}
	if row.OperateUserID == nil || *row.OperateUserID != 9 || row.OperateUserName != "Admin" {
		t.Fatalf("operator = %#v name=%q", row.OperateUserID, row.OperateUserName)
	}
	if row.Param != `{"name":"system"}` || row.Response == "" {
		t.Fatalf("payloads param=%q response=%q", row.Param, row.Response)
	}
}

func TestRecordAfterMarksFailure(t *testing.T) {
	store := &middlewareStore{}
	service := app.NewOperateLogService(store, nil)
	principal := authn.RequestPrincipal{Subject: "user:9", TenantID: 1001, Realm: authn.RealmAdmin}
	entry := buildEntry(principal, "/role/add", "POST", `{}`, ``, 500, "127.0.0.1", "ua")
	if err := service.Record(context.Background(), entry); err != nil {
		t.Fatalf("record: %v", err)
	}
	row := store.inserted[0]
	if row.SuccessFlag || row.FailReason == "" {
		t.Fatalf("failure row = %#v", row)
	}
}

func TestBuildEntrySkipsUnknownRoute(t *testing.T) {
	if _, _, audited := catalogLabel("/support/dict/queryPage"); audited {
		t.Fatal("query route audited")
	}
}
