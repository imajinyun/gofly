package app

import (
	"context"
	"strings"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

// ConfigParam is one global runtime configuration parameter.
type ConfigParam struct {
	ID        uint64    `json:"configId"`
	Key       string    `json:"configKey"`
	Name      string    `json:"configName"`
	Value     string    `json:"configValue"`
	Remark    string    `json:"remark"`
	System    bool      `json:"-"`
	CreatedAt time.Time `json:"createTime"`
	UpdatedAt time.Time `json:"updateTime"`
}

// ConfigParamPage is the SmartAdmin {list,total} envelope.
type ConfigParamPage struct {
	List  []ConfigParam `json:"list"`
	Total int           `json:"total"`
}

type ConfigParamQuery struct {
	Key      string `json:"configKey"`
	PageNum  uint64 `json:"pageNum"`
	PageSize uint64 `json:"pageSize"`
}

type ConfigParamInput struct {
	ID     uint64 `json:"configId"`
	Key    string `json:"configKey"`
	Name   string `json:"configName"`
	Value  string `json:"configValue"`
	Remark string `json:"remark"`
}

// ConfigParamStore is the global parameter repository.
type ConfigParamStore interface {
	Create(ctx context.Context, input ConfigParamInput) (ConfigParam, error)
	Update(ctx context.Context, input ConfigParamInput) error
	List(ctx context.Context, query ConfigParamQuery) (ConfigParamPage, error)
	GetByKey(ctx context.Context, key string) (ConfigParam, error)
}

// ConfigParamService validates and authorizes parameter changes.
type ConfigParamService struct {
	store      ConfigParamStore
	authorizer RBACAuthorizer
}

func NewConfigParamService(store ConfigParamStore, authorizer RBACAuthorizer) *ConfigParamService {
	return &ConfigParamService{store: store, authorizer: authorizer}
}

func configKeyPattern(value string) bool {
	if len(value) < 2 || len(value) > 120 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
		default:
			return false
		}
	}
	return true
}

func (s *ConfigParamService) validate(input ConfigParamInput, forCreate bool) *coreerrors.Error {
	input.Key = strings.TrimSpace(input.Key)
	input.Name = strings.TrimSpace(input.Name)
	if forCreate && !configKeyPattern(input.Key) {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "config key must be 2-120 letters, digits, underscore, dot or dash")
	}
	if input.Name == "" || len([]rune(input.Name)) > 120 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "config name is invalid")
	}
	if len([]rune(input.Value)) > 2000 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "config value is too long")
	}
	if len([]rune(input.Remark)) > 255 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "config remark is too long")
	}
	return nil
}

func (s *ConfigParamService) Create(ctx context.Context, input ConfigParamInput) (ConfigParam, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportConfigManage); err != nil {
		return ConfigParam{}, err
	}
	if err := s.validate(input, true); err != nil {
		return ConfigParam{}, err
	}
	param, storeErr := s.store.Create(ctx, input)
	if storeErr != nil {
		return ConfigParam{}, configStoreError(storeErr)
	}
	return param, nil
}

func (s *ConfigParamService) Update(ctx context.Context, input ConfigParamInput) *coreerrors.Error {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportConfigManage); err != nil {
		return err
	}
	if input.ID == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "config id is required")
	}
	if err := s.validate(input, false); err != nil {
		return err
	}
	return configStoreError(s.store.Update(ctx, input))
}

func (s *ConfigParamService) List(ctx context.Context, query ConfigParamQuery) (ConfigParamPage, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportConfigRead); err != nil {
		return ConfigParamPage{}, err
	}
	query.PageNum, query.PageSize = NormalizePage(query.PageNum, query.PageSize)
	page, err := s.store.List(ctx, query)
	if err != nil {
		return ConfigParamPage{}, configStoreError(err)
	}
	return page, nil
}

func (s *ConfigParamService) GetByKey(ctx context.Context, key string) (ConfigParam, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportConfigRead); err != nil {
		return ConfigParam{}, err
	}
	key = strings.TrimSpace(key)
	if !configKeyPattern(key) {
		return ConfigParam{}, coreerrors.New(coreerrors.CodeInvalidArgument, "config key is invalid")
	}
	param, err := s.store.GetByKey(ctx, key)
	if err != nil {
		return ConfigParam{}, configStoreError(err)
	}
	return param, nil
}

func configStoreError(err error) *coreerrors.Error {
	if err == nil {
		return nil
	}
	if coreErr, ok := err.(*coreerrors.Error); ok {
		return coreErr
	}
	return coreerrors.Wrap(coreerrors.CodeUnavailable, "config storage", err)
}
