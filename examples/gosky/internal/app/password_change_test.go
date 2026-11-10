package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
)

// Failure-mode inventory:
//
//	OPC-01 missing/blank fields rejected
//	OPC-02 new password identical to old rejected
//	OPC-03 weak new password rejected before repository call
//	OPC-04 wrong old password surfaces failed_precondition
//	OPC-05 success passes both hashes and revokes sessions in the repository
type fakeOwnPasswordStore struct {
	err            error
	gotSubject     string
	gotTenant      uint64
	gotOld, gotNew string
	calls          int
}

func (f *fakeOwnPasswordStore) ChangeOwnPassword(_ context.Context, subject string, tenantID uint64, oldHash, newHash string, _ time.Time) error {
	f.calls++
	f.gotSubject, f.gotTenant, f.gotOld, f.gotNew = subject, tenantID, oldHash, newHash
	return f.err
}

func ownPasswordCtx() context.Context {
	return auth.NewContext(context.Background(), auth.Principal{
		Subject: "user:1001",
		Claims:  map[string]any{"tenant_id": "1001", "realm": "admin", "sid": "s1"},
	})
}

func principalFixture() authn.RequestPrincipal {
	return authn.RequestPrincipal{Subject: "user:1001", TenantID: 1001, Realm: authn.RealmAdmin, SessionID: "s1"}
}

func TestOwnPasswordChangeValidation(t *testing.T) {
	service := NewOwnPasswordChange(&fakeOwnPasswordStore{}, false)
	ctx := ownPasswordCtx()
	for _, tc := range []struct {
		name        string
		oldPassword string
		newPassword string
		wantCode    coreerrors.Code
	}{
		{name: "blank old", oldPassword: "  ", newPassword: "Newpass123", wantCode: coreerrors.CodeInvalidArgument},
		{name: "blank new", oldPassword: "Oldpass123", newPassword: "", wantCode: coreerrors.CodeInvalidArgument},
		{name: "same password", oldPassword: "Samepass1", newPassword: "Samepass1", wantCode: coreerrors.CodeInvalidArgument},
		{name: "weak new", oldPassword: "Oldpass123", newPassword: "short", wantCode: coreerrors.CodeInvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeOwnPasswordStore{}
			service := NewOwnPasswordChange(store, false)
			err := service.Change(ctx, principalFixture(), tc.oldPassword, tc.newPassword)
			if err == nil || err.Code != tc.wantCode || store.calls != 0 {
				t.Fatalf("err=%v calls=%d, want code %s before storage", err, store.calls, tc.wantCode)
			}
		})
	}
	_ = service
}

func TestOwnPasswordChangeOldMismatch(t *testing.T) {
	store := &fakeOwnPasswordStore{err: ErrOldPasswordMismatch}
	service := NewOwnPasswordChange(store, false)
	err := service.Change(ownPasswordCtx(), principalFixture(), "Oldpass123", "Newpass123")
	if err == nil || err.Code != coreerrors.CodeFailedPrecondition {
		t.Fatalf("mismatch err=%#v, want failed_precondition", err)
	}
}

func TestOwnPasswordChangeSuccess(t *testing.T) {
	store := &fakeOwnPasswordStore{}
	service := NewOwnPasswordChange(store, false)
	err := service.Change(ownPasswordCtx(), principalFixture(), "Oldpass123", "Newpass1234")
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if store.calls != 1 || store.gotSubject != "user:1001" || store.gotTenant != 1001 {
		t.Fatalf("repository call = %+v", store)
	}
	if store.gotOld == store.gotNew || store.gotOld == "" || store.gotNew == "" {
		t.Fatalf("hashes not populated distinctly: old=%q new=%q", store.gotOld, store.gotNew)
	}
}

func TestOwnPasswordChangeStorageUnavailable(t *testing.T) {
	store := &fakeOwnPasswordStore{err: errors.New("db down")}
	service := NewOwnPasswordChange(store, false)
	err := service.Change(ownPasswordCtx(), principalFixture(), "Oldpass123", "Newpass1234")
	if err == nil || err.Code != coreerrors.CodeUnavailable {
		t.Fatalf("storage err=%#v, want unavailable", err)
	}
}
