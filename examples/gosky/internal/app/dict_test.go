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
//	DICT-01 code/name validation on create
//	DICT-02 capability boundary read vs manage
//	DICT-03 value uniqueness per dictionary maps to invalid argument
//	DICT-04 data validation (value/label/sort)
//	DICT-05 delete requires a bounded non-empty list
type fakeDictStore struct {
	dicts    map[uint64]Dict
	data     map[uint64][]DictData
	nextDict uint64
	nextData uint64
	fail     error
}

func newFakeDictStore() *fakeDictStore {
	return &fakeDictStore{dicts: map[uint64]Dict{1: {ID: 1, Code: "GOODS_PLACE", Name: "Goods place"}},
		data: map[uint64][]DictData{1: {{ID: 10, DictID: 1, Value: "1", Label: "Domestic"}}}}
}

func (f *fakeDictStore) CreateDict(_ context.Context, input DictInput) (Dict, error) {
	if f.fail != nil {
		return Dict{}, f.fail
	}
	for _, dict := range f.dicts {
		if dict.Code == input.Code {
			return Dict{}, errors.New("duplicate dict code")
		}
	}
	f.nextDict++
	dict := Dict{ID: f.nextDict, Code: input.Code, Name: input.Name, Remark: input.Remark}
	f.dicts[dict.ID] = dict
	return dict, nil
}
func (f *fakeDictStore) UpdateDict(_ context.Context, input DictInput) error {
	if f.fail != nil {
		return f.fail
	}
	dict, ok := f.dicts[input.ID]
	if !ok {
		return ErrDictNotFound
	}
	dict.Name, dict.Remark = input.Name, input.Remark
	f.dicts[input.ID] = dict
	return nil
}
func (f *fakeDictStore) ListDicts(_ context.Context, query DictQuery) (DictPage, error) {
	items := make([]Dict, 0, len(f.dicts))
	for _, dict := range f.dicts {
		if query.DisabledFlag == nil || dict.DisabledFlag == *query.DisabledFlag {
			items = append(items, dict)
		}
	}
	page := Paginate(items, query.PageNum, query.PageSize)
	return DictPage{List: page, Total: len(items)}, f.fail
}
func (f *fakeDictStore) ListAllDicts(_ context.Context, _ bool) ([]Dict, error) {
	items := make([]Dict, 0, len(f.dicts))
	for _, dict := range f.dicts {
		items = append(items, dict)
	}
	return items, f.fail
}
func (f *fakeDictStore) ToggleDict(context.Context, uint64) error { return f.fail }
func (f *fakeDictStore) DeleteDicts(context.Context, []uint64) error {
	if f.fail != nil {
		return f.fail
	}
	return nil
}
func (f *fakeDictStore) CreateDictData(_ context.Context, input DictDataInput) (DictData, error) {
	if f.fail != nil {
		return DictData{}, f.fail
	}
	for _, data := range f.data[input.DictID] {
		if data.Value == input.Value {
			return DictData{}, errors.New("duplicate value")
		}
	}
	f.nextData++
	data := DictData{ID: f.nextData, DictID: input.DictID, Value: input.Value, Label: input.Label, Style: input.Style, SortOrder: input.SortOrder}
	f.data[input.DictID] = append(f.data[input.DictID], data)
	return data, nil
}
func (f *fakeDictStore) UpdateDictData(context.Context, DictDataInput) error { return f.fail }
func (f *fakeDictStore) ListDictDataByDictID(_ context.Context, dictID uint64) ([]DictData, error) {
	return f.data[dictID], f.fail
}
func (f *fakeDictStore) ListAllActiveDictData(context.Context) ([]DictData, error) {
	items := make([]DictData, 0)
	for _, data := range f.data {
		items = append(items, data...)
	}
	return items, f.fail
}
func (f *fakeDictStore) ToggleDictData(context.Context, uint64) error   { return f.fail }
func (f *fakeDictStore) DeleteDictData(context.Context, []uint64) error { return f.fail }

func dictCtx(caps ...authz.Capability) context.Context {
	return fileCtx(caps...)
}

func TestDictCreateValidation(t *testing.T) {
	service := NewDictService(newFakeDictStore(), dictCapAuthorizer{allowed: map[authz.Capability]bool{authz.CapabilitySupportDictManage: true}})
	for _, code := range []string{"a", "bad code", "TOO_LONG_" + repeat("X", 100)} {
		_, err := service.CreateDict(dictCtx(authz.CapabilitySupportDictManage), DictInput{Code: code, Name: "Name"})
		if err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("code=%q err=%v", code, err)
		}
	}
	_, err := service.CreateDict(dictCtx(authz.CapabilitySupportDictManage), DictInput{Code: "VALID_CODE", Name: ""})
	if err == nil || err.Code != coreerrors.CodeInvalidArgument {
		t.Fatalf("empty name err=%v", err)
	}
}

func TestDictCapabilityBoundary(t *testing.T) {
	service := NewDictService(newFakeDictStore(), dictCapAuthorizer{allowed: map[authz.Capability]bool{authz.CapabilitySupportDictRead: true}})
	if _, err := service.CreateDict(dictCtx(authz.CapabilitySupportDictRead), DictInput{Code: "X", Name: "X"}); err == nil {
		t.Fatal("create without manage capability accepted")
	}
	if _, err := service.ListDicts(dictCtx(authz.CapabilitySupportDictRead), DictQuery{PageNum: 1, PageSize: 10}); err != nil {
		t.Fatalf("list with read capability: %v", err)
	}
	noCap := context.Background()
	if _, err := service.AllActiveDictData(noCap); err == nil {
		t.Fatal("read without capability accepted")
	}
}

func TestDictDataValidation(t *testing.T) {
	service := NewDictService(newFakeDictStore(), dictCapAuthorizer{allowed: map[authz.Capability]bool{authz.CapabilitySupportDictManage: true}})
	ctx := dictCtx(authz.CapabilitySupportDictManage)
	for _, input := range []DictDataInput{
		{DictID: 0, Value: "1", Label: "One"},
		{DictID: 1, Value: "", Label: "One"},
		{DictID: 1, Value: "1", Label: ""},
		{DictID: 1, Value: "1", Label: "One", SortOrder: -1},
		{DictID: 1, Value: "1", Label: "One", SortOrder: 1001},
	} {
		_, err := service.CreateDictData(ctx, input)
		if err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("input=%+v err=%v", input, err)
		}
	}
	data, err := service.CreateDictData(ctx, DictDataInput{DictID: 1, Value: "2", Label: "Overseas", SortOrder: 10})
	if err != nil {
		t.Fatalf("valid create: %v", err)
	}
	if data.ID == 0 {
		t.Fatal("created data has no id")
	}
}

func TestDictDeleteBounded(t *testing.T) {
	service := NewDictService(newFakeDictStore(), dictCapAuthorizer{allowed: map[authz.Capability]bool{authz.CapabilitySupportDictManage: true}})
	ctx := dictCtx(authz.CapabilitySupportDictManage)
	if err := service.DeleteDicts(ctx, nil); err == nil {
		t.Fatal("empty delete accepted")
	}
	if err := service.DeleteDictData(ctx, make([]uint64, 201)); err == nil {
		t.Fatal("oversized data delete accepted")
	}
}

type dictCapAuthorizer struct{ allowed map[authz.Capability]bool }

func (a dictCapAuthorizer) Authorize(_ context.Context, capability, _ string) *coreerrors.Error {
	if a.allowed[authz.Capability(capability)] {
		return nil
	}
	return coreerrors.New(coreerrors.CodePermissionDenied, "denied")
}
func (a dictCapAuthorizer) ReloadPolicy() error { return nil }

var _ = time.Now

func repeat(value string, count int) string {
	out := ""
	for i := 0; i < count; i++ {
		out += value
	}
	return out
}
