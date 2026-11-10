package app

import (
	"context"
	"errors"
	"testing"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
	"github.com/imajinyun/gofly/examples/gosky/internal/runtimecache"
)

// Failure-mode inventory:
//
//	CC-01 names/keys require read capability; remove requires manage.
//	CC-02 unknown cache names return empty keys but remove rejects them.
//
//	RL-01 initialization records current identifications and does not
//	      re-execute seeded tags.
//	RL-02 an identification change invokes the registered command and
//	      records a success result.
//	RL-03 a command failure records a false result with the exception text.
//	RL-04 an unregistered tag records a false result.
//	RL-05 update validates form and unknown tags; results require read cap.
//	RL-06 cache_service command with a cache name clears only that cache.
type fakeReloadStore struct {
	items   []ReloadItem
	results []ReloadResult
	updated []ReloadForm
	update  bool
	fail    error
}

func (s *fakeReloadStore) ListItems(_ context.Context) ([]ReloadItem, error) {
	return s.items, s.fail
}

func (s *fakeReloadStore) UpdateItem(_ context.Context, form ReloadForm) (bool, error) {
	if s.fail != nil {
		return false, s.fail
	}
	if !s.update {
		return false, nil
	}
	s.updated = append(s.updated, form)
	for i := range s.items {
		if s.items[i].Tag == form.Tag {
			s.items[i].Identification = form.Identification
			s.items[i].Args = form.Args
		}
	}
	return true, nil
}

func (s *fakeReloadStore) InsertResult(_ context.Context, result ReloadResult) error {
	if s.fail != nil {
		return s.fail
	}
	s.results = append(s.results, result)
	return nil
}

func (s *fakeReloadStore) ListResults(_ context.Context, _ string, _ int) ([]ReloadResult, error) {
	return s.results, s.fail
}

type recordedCache struct {
	cleared bool
}

func (c *recordedCache) Keys() ([]string, error) { return []string{"k1", "k2"}, nil }
func (c *recordedCache) Clear() error {
	c.cleared = true
	return nil
}

func TestCacheConsoleCapabilities(t *testing.T) {
	registry := runtimecache.NewRegistry()
	cache := &recordedCache{}
	_ = registry.Register("captcha", cache)
	service := NewCacheConsoleService(registry, dictCapAuthorizer{allowed: capSet(nil)})

	if _, err := service.Names(fileCtx()); err == nil || err.Code != coreerrors.CodePermissionDenied {
		t.Fatalf("names without cap = %v", err)
	}
	if err := service.Remove(fileCtx(), "captcha"); err == nil || err.Code != coreerrors.CodePermissionDenied {
		t.Fatalf("remove without cap = %v", err)
	}

	readService := NewCacheConsoleService(registry, dictCapAuthorizer{allowed: capSet([]authz.Capability{authz.CapabilitySupportCacheRead})})
	names, err := readService.Names(fileCtx(authz.CapabilitySupportCacheRead))
	if err != nil || len(names) != 1 || names[0] != "captcha" {
		t.Fatalf("names = %v err = %v", names, err)
	}
	keys, err := readService.Keys(fileCtx(authz.CapabilitySupportCacheRead), "captcha")
	if err != nil || len(keys) != 2 {
		t.Fatalf("keys = %v err = %v", keys, err)
	}
	keysUnknown, err := readService.Keys(fileCtx(authz.CapabilitySupportCacheRead), "ghost")
	if err != nil || len(keysUnknown) != 0 {
		t.Fatalf("unknown keys = %v err = %v", keysUnknown, err)
	}

	manageService := NewCacheConsoleService(registry, dictCapAuthorizer{allowed: capSet([]authz.Capability{
		authz.CapabilitySupportCacheRead, authz.CapabilitySupportCacheManage})})
	if err := manageService.Remove(fileCtx(authz.CapabilitySupportCacheManage), "ghost"); err == nil {
		t.Fatal("unknown cache removed")
	}
	if err := manageService.Remove(fileCtx(authz.CapabilitySupportCacheManage), "captcha"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !cache.cleared {
		t.Fatal("cache was not cleared")
	}
}

func reloadTestService(store ReloadStore, registry *runtimecache.Registry) *ReloadService {
	service := NewReloadService(store, registry, dictCapAuthorizer{allowed: capSet([]authz.Capability{
		authz.CapabilitySupportReloadRead, authz.CapabilitySupportReloadManage})})
	service.now = func() time.Time { return time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC) }
	return service
}

func TestReloadPollLifecycle(t *testing.T) {
	registry := runtimecache.NewRegistry()
	cache := &recordedCache{}
	_ = registry.Register("cache_service", cache)
	store := &fakeReloadStore{items: []ReloadItem{
		{Tag: ReloadTagCacheService, Identification: "init"},
		{Tag: ReloadTagSystemConfig, Identification: "init"},
	}}
	service := reloadTestService(store, registry)

	if err := service.Init(fileCtx()); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := service.PollOnce(fileCtx()); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	if len(store.results) != 0 {
		t.Fatalf("seeded tags re-executed: %d", len(store.results))
	}

	store.items[0].Identification = "v2"
	if err := service.PollOnce(fileCtx()); err != nil {
		t.Fatalf("changed poll: %v", err)
	}
	if len(store.results) != 1 || !store.results[0].Result || store.results[0].Identification != "v2" {
		t.Fatalf("result = %#v", store.results)
	}
}

func TestReloadUnknownTagAndFailure(t *testing.T) {
	store := &fakeReloadStore{items: []ReloadItem{{Tag: "ghost", Identification: "v1"}}}
	service := reloadTestService(store, runtimecache.NewRegistry())
	if err := service.PollOnce(fileCtx()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(store.results) != 1 || store.results[0].Result || store.results[0].Exception == "" {
		t.Fatalf("unknown-tag result = %#v", store.results)
	}

	registry := runtimecache.NewRegistry()
	store2 := &fakeReloadStore{items: []ReloadItem{{Tag: "bad", Identification: "v1"}}}
	service2 := reloadTestService(store2, registry)
	service2.RegisterCommand("bad", func(_ context.Context, _ string) error { return errors.New("boom") })
	if err := service2.PollOnce(fileCtx()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if store2.results[0].Result || store2.results[0].Exception != "boom" {
		t.Fatalf("failure result = %#v", store2.results)
	}
}

func TestReloadUpdateValidation(t *testing.T) {
	store := &fakeReloadStore{update: true, items: []ReloadItem{{Tag: ReloadTagCacheService, Identification: "v1"}}}
	service := reloadTestService(store, runtimecache.NewRegistry())
	ctx := fileCtx(authz.CapabilitySupportReloadManage)

	if err := service.UpdateItem(ctx, ReloadForm{Tag: "", Identification: "x"}); err == nil {
		t.Fatal("blank tag accepted")
	}
	if err := service.UpdateItem(ctx, ReloadForm{Tag: ReloadTagCacheService, Identification: ""}); err == nil {
		t.Fatal("blank identification accepted")
	}
	store.update = false
	if err := service.UpdateItem(ctx, ReloadForm{Tag: "ghost", Identification: "x"}); err == nil {
		t.Fatal("unknown tag accepted")
	}
	store.update = true
	if err := service.UpdateItem(ctx, ReloadForm{Tag: ReloadTagCacheService, Identification: "v2", Args: "captcha"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := service.ListResults(fileCtx(authz.CapabilitySupportReloadRead), ReloadTagCacheService); err != nil {
		t.Fatalf("list results: %v", err)
	}
}
