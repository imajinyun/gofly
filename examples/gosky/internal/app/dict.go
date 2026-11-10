package app

import (
	"context"
	"errors"
	"strings"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

// Dict is one global data dictionary.
type Dict struct {
	ID           uint64    `json:"dictId"`
	Code         string    `json:"dictCode"`
	Name         string    `json:"dictName"`
	Remark       string    `json:"remark"`
	DisabledFlag bool      `json:"disabledFlag"`
	SystemFlag   bool      `json:"-"`
	CreatedAt    time.Time `json:"createTime"`
	UpdatedAt    time.Time `json:"updateTime"`
}

// DictData is one dictionary value.
type DictData struct {
	ID               uint64    `json:"dictDataId"`
	DictID           uint64    `json:"dictId"`
	DictCode         string    `json:"dictCode"`
	DictName         string    `json:"dictName"`
	DictDisabledFlag bool      `json:"dictDisabledFlag"`
	Value            string    `json:"dataValue"`
	Label            string    `json:"dataLabel"`
	Style            string    `json:"dataStyle"`
	Remark           string    `json:"remark"`
	SortOrder        int       `json:"sortOrder"`
	DisabledFlag     bool      `json:"disabledFlag"`
	CreatedAt        time.Time `json:"createTime"`
	UpdatedAt        time.Time `json:"updateTime"`
}

// DictPage is the SmartAdmin paged response ({list,total}).
type DictPage struct {
	List  []Dict `json:"list"`
	Total int    `json:"total"`
}

type DictQuery struct {
	Keywords     string
	DisabledFlag *bool
	PageNum      uint64
	PageSize     uint64
}

type DictInput struct {
	ID     uint64 `json:"dictId"`
	Code   string `json:"dictCode"`
	Name   string `json:"dictName"`
	Remark string `json:"remark"`
}

type DictDataInput struct {
	ID        uint64 `json:"dictDataId"`
	DictID    uint64 `json:"dictId"`
	DictCode  string `json:"dictCode"`
	Value     string `json:"dataValue"`
	Label     string `json:"dataLabel"`
	Style     string `json:"dataStyle"`
	Remark    string `json:"remark"`
	SortOrder int    `json:"sortOrder"`
}

// DictStore is the global dictionary repository.
type DictStore interface {
	CreateDict(ctx context.Context, input DictInput) (Dict, error)
	UpdateDict(ctx context.Context, input DictInput) error
	ListDicts(ctx context.Context, query DictQuery) (DictPage, error)
	ListAllDicts(ctx context.Context, includeDisabled bool) ([]Dict, error)
	ToggleDict(ctx context.Context, id uint64) error
	DeleteDicts(ctx context.Context, ids []uint64) error

	CreateDictData(ctx context.Context, input DictDataInput) (DictData, error)
	UpdateDictData(ctx context.Context, input DictDataInput) error
	ListDictDataByDictID(ctx context.Context, dictID uint64) ([]DictData, error)
	ListAllActiveDictData(ctx context.Context) ([]DictData, error)
	ToggleDictData(ctx context.Context, id uint64) error
	DeleteDictData(ctx context.Context, ids []uint64) error
}

// DictService owns validation/authorization for the global dictionary.
type DictService struct {
	store      DictStore
	authorizer RBACAuthorizer
}

func NewDictService(store DictStore, authorizer RBACAuthorizer) *DictService {
	return &DictService{store: store, authorizer: authorizer}
}

func dictCodePattern(value string) bool {
	if len(value) < 2 || len(value) > 100 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}

func (s *DictService) validateDict(input DictInput, forCreate bool) *coreerrors.Error {
	input.Code = strings.TrimSpace(input.Code)
	input.Name = strings.TrimSpace(input.Name)
	if forCreate && !dictCodePattern(input.Code) {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "dict code must be 2-100 letters, digits or underscores")
	}
	if input.Name == "" || len([]rune(input.Name)) > 100 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "dict name is invalid")
	}
	if len([]rune(input.Remark)) > 255 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "dict remark is too long")
	}
	return nil
}

func (s *DictService) CreateDict(ctx context.Context, input DictInput) (Dict, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportDictManage); err != nil {
		return Dict{}, err
	}
	if err := s.validateDict(input, true); err != nil {
		return Dict{}, err
	}
	dict, storeErr := s.store.CreateDict(ctx, input)
	if storeErr != nil {
		return Dict{}, dictStoreError(storeErr)
	}
	return dict, nil
}

func (s *DictService) UpdateDict(ctx context.Context, input DictInput) *coreerrors.Error {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportDictManage); err != nil {
		return err
	}
	if input.ID == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "dict id is required")
	}
	if err := s.validateDict(input, false); err != nil {
		return err
	}
	if err := s.store.UpdateDict(ctx, input); err != nil {
		return dictStoreError(err)
	}
	return nil
}

func (s *DictService) ListDicts(ctx context.Context, query DictQuery) (DictPage, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportDictRead); err != nil {
		return DictPage{}, err
	}
	query.PageNum, query.PageSize = NormalizePage(query.PageNum, query.PageSize)
	page, err := s.store.ListDicts(ctx, query)
	if err != nil {
		return DictPage{}, dictStoreError(err)
	}
	return page, nil
}

func (s *DictService) ListAllDicts(ctx context.Context) ([]Dict, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportDictRead); err != nil {
		return nil, err
	}
	items, err := s.store.ListAllDicts(ctx, true)
	if err != nil {
		return nil, dictStoreError(err)
	}
	return items, nil
}

func (s *DictService) ToggleDict(ctx context.Context, id uint64) *coreerrors.Error {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportDictManage); err != nil {
		return err
	}
	if id == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "dict id is required")
	}
	return dictStoreError(s.store.ToggleDict(ctx, id))
}

func (s *DictService) DeleteDicts(ctx context.Context, ids []uint64) *coreerrors.Error {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportDictManage); err != nil {
		return err
	}
	if len(ids) == 0 || len(ids) > 100 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "dict id list is invalid")
	}
	return dictStoreError(s.store.DeleteDicts(ctx, ids))
}

// ---- dictionary values ----

func (s *DictService) validateDictData(input DictDataInput) *coreerrors.Error {
	input.Value = strings.TrimSpace(input.Value)
	input.Label = strings.TrimSpace(input.Label)
	if input.DictID == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "dict id is required")
	}
	if input.Value == "" || len([]rune(input.Value)) > 100 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "dict value is invalid")
	}
	if input.Label == "" || len([]rune(input.Label)) > 200 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "dict label is invalid")
	}
	if input.SortOrder < 0 || input.SortOrder > 1000 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "sort order must be 0..1000")
	}
	return nil
}

func (s *DictService) CreateDictData(ctx context.Context, input DictDataInput) (DictData, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportDictManage); err != nil {
		return DictData{}, err
	}
	if err := s.validateDictData(input); err != nil {
		return DictData{}, err
	}
	data, storeErr := s.store.CreateDictData(ctx, input)
	if storeErr != nil {
		return DictData{}, dictStoreError(storeErr)
	}
	return data, nil
}

func (s *DictService) UpdateDictData(ctx context.Context, input DictDataInput) *coreerrors.Error {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportDictManage); err != nil {
		return err
	}
	if input.ID == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "dict data id is required")
	}
	if err := s.validateDictData(input); err != nil {
		return err
	}
	return dictStoreError(s.store.UpdateDictData(ctx, input))
}

func (s *DictService) ListDictData(ctx context.Context, dictID uint64) ([]DictData, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportDictRead); err != nil {
		return nil, err
	}
	if dictID == 0 {
		return nil, coreerrors.New(coreerrors.CodeInvalidArgument, "dict id is required")
	}
	items, err := s.store.ListDictDataByDictID(ctx, dictID)
	if err != nil {
		return nil, dictStoreError(err)
	}
	return items, nil
}

// AllActiveDictData powers the post-login bootstrap cache used across
// console dropdowns. It needs the read capability like other dict reads.
func (s *DictService) AllActiveDictData(ctx context.Context) ([]DictData, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportDictRead); err != nil {
		return nil, err
	}
	items, err := s.store.ListAllActiveDictData(ctx)
	if err != nil {
		return nil, dictStoreError(err)
	}
	return items, nil
}

func (s *DictService) ToggleDictData(ctx context.Context, id uint64) *coreerrors.Error {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportDictManage); err != nil {
		return err
	}
	if id == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "dict data id is required")
	}
	return dictStoreError(s.store.ToggleDictData(ctx, id))
}

func (s *DictService) DeleteDictData(ctx context.Context, ids []uint64) *coreerrors.Error {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportDictManage); err != nil {
		return err
	}
	if len(ids) == 0 || len(ids) > 200 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "dict data id list is invalid")
	}
	return dictStoreError(s.store.DeleteDictData(ctx, ids))
}

func dictStoreError(err error) *coreerrors.Error {
	if err == nil {
		return nil
	}
	if coreErr, ok := err.(*coreerrors.Error); ok {
		return coreErr
	}
	return coreerrors.Wrap(coreerrors.CodeUnavailable, "dictionary storage", err)
}

// Dictionary domain sentinels shared with the repository mapping.
var (
	ErrDictNotFound = errors.New("dict not found")
	ErrDictInUse    = errors.New("dict still has values")
)
