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
//	LS-01 protection disabled (config absent / maxTimes<1) never blocks or
//	      records fail state.
//	LS-02 below the threshold: counter increments and a will-lock hint is
//	      returned.
//	LS-03 at the threshold: lock begins and a lock message is returned.
//	LS-04 inside the lock window CheckLock rejects; after the window it passes.
//	LS-05 unknown login name writes no fail state and yields no hint.
//	LS-06 success clears the fail state.
//	LS-07 console queries require the matching capabilities.
//	LS-08 protect config validation rejects incomplete/out-of-range forms.
//	LS-09 store failures map to unavailable and never block the audit path.
type fakeLoginSecurityStore struct {
	identity    *LoginIdentity
	resolveErr  error
	logs        []LoginLog
	fails       map[uint64]LoginFail
	deleted     []uint64
	configRaw   string
	upsertCount int
	page        LoginLogPage
	failPage    LoginFailPage
	fail        error
}

func newFakeLoginSecurityStore() *fakeLoginSecurityStore {
	return &fakeLoginSecurityStore{fails: map[uint64]LoginFail{}}
}

func (s *fakeLoginSecurityStore) ResolveLoginIdentity(_ context.Context, _ string) (LoginIdentity, bool, error) {
	if s.resolveErr != nil {
		return LoginIdentity{}, false, s.resolveErr
	}
	if s.identity == nil {
		return LoginIdentity{}, false, nil
	}
	return *s.identity, true, nil
}

func (s *fakeLoginSecurityStore) ResolveIdentityBySubject(_ context.Context, _ uint64, _ string) (LoginIdentity, bool, error) {
	if s.identity == nil {
		return LoginIdentity{}, false, nil
	}
	return *s.identity, true, nil
}

func (s *fakeLoginSecurityStore) InsertLoginLog(_ context.Context, _ uint64, entry LoginLog) error {
	s.logs = append(s.logs, entry)
	return s.fail
}

func (s *fakeLoginSecurityStore) ListLoginLogs(_ context.Context, _ uint64, _ LoginLogQuery) (LoginLogPage, error) {
	return s.page, s.fail
}

func (s *fakeLoginSecurityStore) GetFailState(_ context.Context, _, userID uint64) (LoginFail, bool, error) {
	state, ok := s.fails[userID]
	return state, ok, s.fail
}

func (s *fakeLoginSecurityStore) UpsertFailState(_ context.Context, _, userID uint64, state LoginFail) error {
	s.upsertCount++
	s.fails[userID] = state
	return s.fail
}

func (s *fakeLoginSecurityStore) DeleteFailState(_ context.Context, _, userID uint64) error {
	delete(s.fails, userID)
	s.deleted = append(s.deleted, userID)
	return s.fail
}

func (s *fakeLoginSecurityStore) ListLoginFails(_ context.Context, _ uint64, _ LoginFailQuery) (LoginFailPage, error) {
	return s.failPage, s.fail
}

func (s *fakeLoginSecurityStore) DeleteLoginFails(_ context.Context, _ uint64, ids []uint64) error {
	s.deleted = append(s.deleted, ids...)
	return s.fail
}

func (s *fakeLoginSecurityStore) GetProtectConfig(_ context.Context) (string, error) {
	return s.configRaw, s.fail
}

func (s *fakeLoginSecurityStore) PutProtectConfig(_ context.Context, raw string) error {
	s.configRaw = raw
	return s.fail
}

func loginProtectionService(store LoginSecurityStore, caps ...authz.Capability) *LoginProtectionService {
	service := NewLoginProtectionService(store, dictCapAuthorizer{allowed: capSet(caps)})
	service.now = func() time.Time { return time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC) }
	return service
}

func testIdentity() *LoginIdentity {
	return &LoginIdentity{UserID: 7, TenantID: 1001, Subject: "user:7", LoginName: "admin", DisplayName: "Admin"}
}

func protectConfigJSON(maxTimes, lockMinutes int) string {
	cfg := DefaultLevel3ProtectConfig()
	*cfg.LoginFailMaxTimes = maxTimes
	*cfg.LoginFailLockMinutes = lockMinutes
	raw, _ := jsonMarshalProtect(cfg)
	return raw
}

func TestLoginProtectionDisabledByDefault(t *testing.T) {
	store := newFakeLoginSecurityStore()
	store.identity = testIdentity()
	service := loginProtectionService(store)

	if err := service.CheckLock(fileCtx(), "admin"); err != nil {
		t.Fatalf("check lock with no config: %v", err)
	}
	hint, err := service.RecordFailure(fileCtx(), "admin", LoginMeta{IP: "127.0.0.1"})
	if err != nil || hint != "" {
		t.Fatalf("record failure disabled = (%q, %v), want empty hint", hint, err)
	}
	if store.upsertCount != 0 {
		t.Fatalf("fail state upserted while protection disabled: %d", store.upsertCount)
	}
	if len(store.logs) != 1 || store.logs[0].LoginResult != LoginResultFail {
		t.Fatalf("fail log = %#v", store.logs)
	}
}

func TestLoginProtectionWillLockHint(t *testing.T) {
	store := newFakeLoginSecurityStore()
	store.identity = testIdentity()
	store.configRaw = protectConfigJSON(3, 30)
	service := loginProtectionService(store)

	hint, err := service.RecordFailure(fileCtx(), "admin", LoginMeta{})
	if err != nil {
		t.Fatalf("record failure: %v", err)
	}
	if hint == "" {
		t.Fatal("expected will-lock hint")
	}
	state := store.fails[7]
	if state.LoginFailCount != 1 || state.LockFlag != 1-1 {
		t.Fatalf("state = %#v", state)
	}
	if err := service.CheckLock(fileCtx(), "admin"); err != nil {
		t.Fatalf("check lock below threshold: %v", err)
	}
}

func TestLoginProtectionLocksAtThreshold(t *testing.T) {
	store := newFakeLoginSecurityStore()
	store.identity = testIdentity()
	store.configRaw = protectConfigJSON(3, 30)
	service := loginProtectionService(store)

	var lastHint string
	for range 3 {
		var err *coreerrors.Error
		lastHint, err = service.RecordFailure(fileCtx(), "admin", LoginMeta{})
		if err != nil {
			t.Fatalf("record failure: %v", err)
		}
	}
	state := store.fails[7]
	if state.LoginFailCount != 3 || state.LockFlag != 1 || state.LoginLockBeginTime == nil {
		t.Fatalf("locked state = %#v", state)
	}
	if err := service.CheckLock(fileCtx(), "admin"); err == nil {
		t.Fatal("expected lock rejection")
	} else if err.Code != LoginFailLockedCode {
		t.Fatalf("lock error code = %v", err.Code)
	}
	if lastHint == "" {
		t.Fatal("expected lock message hint")
	}
}

func TestLoginProtectionLockExpiry(t *testing.T) {
	store := newFakeLoginSecurityStore()
	store.identity = testIdentity()
	store.configRaw = protectConfigJSON(2, 30)
	begin := time.Date(2026, 10, 19, 11, 0, 0, 0, time.UTC)
	store.fails[7] = LoginFail{UserID: 7, LoginFailCount: 2, LockFlag: 1, LoginLockBeginTime: &begin}
	service := loginProtectionService(store)

	if err := service.CheckLock(fileCtx(), "admin"); err != nil {
		t.Fatalf("expired lock should pass: %v", err)
	}
}

func TestLoginProtectionUnknownName(t *testing.T) {
	store := newFakeLoginSecurityStore()
	store.configRaw = protectConfigJSON(3, 30)
	service := loginProtectionService(store)

	hint, err := service.RecordFailure(fileCtx(), "ghost", LoginMeta{})
	if err != nil || hint != "" {
		t.Fatalf("unknown name = (%q, %v)", hint, err)
	}
	if store.upsertCount != 0 {
		t.Fatal("fail state written for unknown name")
	}
}

func TestLoginProtectionSuccessClearsFail(t *testing.T) {
	store := newFakeLoginSecurityStore()
	store.identity = testIdentity()
	store.fails[7] = LoginFail{UserID: 7, LoginFailCount: 2}
	service := loginProtectionService(store)

	service.RecordSuccess(fileCtx(), "admin", LoginMeta{IP: "10.0.0.1", UserAgent: "ua"})
	if _, ok := store.fails[7]; ok {
		t.Fatal("fail state not cleared")
	}
	if len(store.logs) != 1 || store.logs[0].LoginResult != LoginResultSuccess || store.logs[0].LoginIP != "10.0.0.1" {
		t.Fatalf("success log = %#v", store.logs)
	}
}

func TestLoginProtectionConsoleCapabilities(t *testing.T) {
	store := newFakeLoginSecurityStore()
	service := loginProtectionService(store)

	if _, err := service.ListLogs(fileCtx(), LoginLogQuery{}); err == nil || err.Code != coreerrors.CodePermissionDenied {
		t.Fatalf("list logs without cap = %v", err)
	}
	if _, err := service.ListFails(fileCtx(), 1001, LoginFailQuery{}); err == nil || err.Code != coreerrors.CodePermissionDenied {
		t.Fatalf("list fails without cap = %v", err)
	}
	if err := service.DeleteFails(fileCtx(), []uint64{1}); err == nil || err.Code != coreerrors.CodePermissionDenied {
		t.Fatalf("delete without cap = %v", err)
	}
	if _, err := service.GetProtectConfig(fileCtx()); err == nil || err.Code != coreerrors.CodePermissionDenied {
		t.Fatalf("get config without cap = %v", err)
	}

	allowed := loginProtectionService(store, authz.CapabilitySupportLoginLogRead, authz.CapabilitySupportLoginLogManage)
	if _, err := allowed.ListLogs(fileCtx(authz.CapabilitySupportLoginLogRead), LoginLogQuery{PageNum: 1, PageSize: 10}); err != nil {
		t.Fatalf("list logs with cap: %v", err)
	}
	if err := allowed.DeleteFails(fileCtx(authz.CapabilitySupportLoginLogManage), nil); err != nil {
		t.Fatalf("empty delete: %v", err)
	}
	if err := allowed.DeleteFails(fileCtx(authz.CapabilitySupportLoginLogManage), []uint64{0}); err == nil {
		t.Fatal("zero id accepted")
	}
}

func TestLoginProtectionConfigValidation(t *testing.T) {
	store := newFakeLoginSecurityStore()
	service := loginProtectionService(store, authz.CapabilitySupportLoginLogManage)
	ctx := fileCtx(authz.CapabilitySupportLoginLogManage)

	empty := Level3ProtectConfig{}
	if err := service.UpdateProtectConfig(ctx, empty); err == nil {
		t.Fatal("incomplete config accepted")
	}
	cfg := DefaultLevel3ProtectConfig()
	*cfg.LoginFailLockMinutes = 0
	if err := service.UpdateProtectConfig(ctx, cfg); err == nil {
		t.Fatal("zero lock minutes accepted")
	}
	cfg = DefaultLevel3ProtectConfig()
	*cfg.MaxUploadFileSizeMB = 0
	if err := service.UpdateProtectConfig(ctx, cfg); err == nil {
		t.Fatal("zero upload size accepted")
	}
	valid := DefaultLevel3ProtectConfig()
	if err := service.UpdateProtectConfig(ctx, valid); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	if store.configRaw == "" {
		t.Fatal("config not persisted")
	}
}

func TestLoginProtectionStoreFailuresFailClosedForChecks(t *testing.T) {
	store := newFakeLoginSecurityStore()
	store.identity = testIdentity()
	store.fail = errors.New("db down")
	service := loginProtectionService(store, authz.CapabilitySupportLoginLogRead, authz.CapabilitySupportLoginLogManage)

	if _, err := service.ListLogs(fileCtx(authz.CapabilitySupportLoginLogRead), LoginLogQuery{}); err == nil || err.Code != coreerrors.CodeUnavailable {
		t.Fatalf("list logs store error = %v", err)
	}
	if err := service.CheckLock(fileCtx(), "admin"); err == nil || err.Code != coreerrors.CodeUnavailable {
		t.Fatalf("check lock store error = %v", err)
	}
}
