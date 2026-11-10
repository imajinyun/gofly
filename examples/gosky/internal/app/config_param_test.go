package app

import (
	"context"
	"errors"
	"testing"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

// Failure-mode inventory:
//
//	CFG-01 key/name validation on create
//	CFG-02 capability boundary read vs manage
//	CFG-03 update requires id
//	CFG-04 key lookup rejects malformed keys
type fakeConfigStore struct {
	params map[uint64]ConfigParam
	byKey  map[string]uint64
	next   uint64
	fail   error
}

func newFakeConfigStore() *fakeConfigStore {
	return &fakeConfigStore{params: map[uint64]ConfigParam{1: {ID: 1, Key: "app.timeout", Name: "Timeout", Value: "30s"}},
		byKey: map[string]uint64{"app.timeout": 1}}
}

func (s *fakeConfigStore) Create(_ context.Context, input ConfigParamInput) (ConfigParam, error) {
	if s.fail != nil {
		return ConfigParam{}, s.fail
	}
	s.next++
	param := ConfigParam{ID: s.next, Key: input.Key, Name: input.Name, Value: input.Value, Remark: input.Remark}
	s.params[param.ID] = param
	s.byKey[param.Key] = param.ID
	return param, nil
}
func (s *fakeConfigStore) Update(_ context.Context, input ConfigParamInput) error {
	if s.fail != nil {
		return s.fail
	}
	param, ok := s.params[input.ID]
	if !ok {
		return errors.New("not found")
	}
	param.Name, param.Value, param.Remark = input.Name, input.Value, input.Remark
	s.params[input.ID] = param
	return nil
}
func (s *fakeConfigStore) List(_ context.Context, query ConfigParamQuery) (ConfigParamPage, error) {
	items := make([]ConfigParam, 0, len(s.params))
	for _, param := range s.params {
		items = append(items, param)
	}
	page := Paginate(items, query.PageNum, query.PageSize)
	return ConfigParamPage{List: page, Total: len(items)}, s.fail
}
func (s *fakeConfigStore) GetByKey(_ context.Context, key string) (ConfigParam, error) {
	id, ok := s.byKey[key]
	if !ok {
		return ConfigParam{}, errors.New("not found")
	}
	return s.params[id], s.fail
}

func TestConfigCreateValidation(t *testing.T) {
	service := NewConfigParamService(newFakeConfigStore(), dictCapAuthorizer{allowed: map[authz.Capability]bool{authz.CapabilitySupportConfigManage: true}})
	ctx := dictCtx(authz.CapabilitySupportConfigManage)
	for _, key := range []string{"a", "bad key", "x/" + string([]byte{})} {
		_, err := service.Create(ctx, ConfigParamInput{Key: key, Name: "N", Value: "v"})
		if err == nil {
			t.Fatalf("key=%q accepted", key)
		}
	}
	param, err := service.Create(ctx, ConfigParamInput{Key: "app.theme", Name: "Theme", Value: "dark"})
	if err != nil {
		t.Fatalf("valid create: %v", err)
	}
	if param.ID == 0 || param.Key != "app.theme" {
		t.Fatalf("created param=%+v", param)
	}
}

func TestConfigCapabilityBoundary(t *testing.T) {
	readOnly := NewConfigParamService(newFakeConfigStore(), dictCapAuthorizer{allowed: map[authz.Capability]bool{authz.CapabilitySupportConfigRead: true}})
	ctx := dictCtx(authz.CapabilitySupportConfigRead)
	if _, err := readOnly.Create(ctx, ConfigParamInput{Key: "k", Name: "n"}); err == nil {
		t.Fatal("create with read-only capability accepted")
	}
	if _, err := readOnly.GetByKey(ctx, "app.timeout"); err != nil {
		t.Fatalf("get by key with read capability: %v", err)
	}
	noCap := NewConfigParamService(newFakeConfigStore(), dictCapAuthorizer{})
	if _, err := noCap.List(context.Background(), ConfigParamQuery{PageNum: 1, PageSize: 10}); err == nil {
		t.Fatal("list without capability accepted")
	}
	if _, err := noCap.GetByKey(context.Background(), "bad key"); err == nil {
		t.Fatal("malformed key accepted")
	}
}

func TestConfigUpdateRequiresID(t *testing.T) {
	service := NewConfigParamService(newFakeConfigStore(), dictCapAuthorizer{allowed: map[authz.Capability]bool{authz.CapabilitySupportConfigManage: true}})
	ctx := dictCtx(authz.CapabilitySupportConfigManage)
	err := service.Update(ctx, ConfigParamInput{Key: "app.timeout", Name: "Timeout", Value: "60s"})
	if err == nil || err.Code != coreerrors.CodeInvalidArgument {
		t.Fatalf("update without id err=%v", err)
	}
	if err := service.Update(ctx, ConfigParamInput{ID: 1, Name: "Timeout", Value: "60s"}); err != nil {
		t.Fatalf("valid update: %v", err)
	}
}
