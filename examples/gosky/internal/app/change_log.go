package app

import (
	"context"
	"strings"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

// Change log types mirror ChangeLogTypeEnum.
const (
	ChangeLogMajor   = 1
	ChangeLogFeature = 2
	ChangeLogBugfix  = 3
)

// ChangeLog is one product release note.
type ChangeLog struct {
	ID            uint64    `json:"changeLogId"`
	UpdateVersion string    `json:"updateVersion"`
	Type          int       `json:"type"`
	PublishAuthor string    `json:"publishAuthor"`
	PublicDate    string    `json:"publicDate"`
	Content       string    `json:"content"`
	Link          string    `json:"link"`
	CreatedAt     time.Time `json:"createTime"`
	UpdatedAt     time.Time `json:"updateTime"`
}

// ChangeLogPage is the {list,total} envelope.
type ChangeLogPage struct {
	List  []ChangeLog `json:"list"`
	Total int         `json:"total"`
}

// ChangeLogInput is the add/update payload.
type ChangeLogInput struct {
	ID            uint64 `json:"changeLogId"`
	UpdateVersion string `json:"updateVersion"`
	Type          int    `json:"type"`
	PublishAuthor string `json:"publishAuthor"`
	PublicDate    string `json:"publicDate"`
	Content       string `json:"content"`
	Link          string `json:"link"`
}

// ChangeLogQuery carries console filters.
type ChangeLogQuery struct {
	Type            *int
	Keyword         string
	PublicDateBegin string
	PublicDateEnd   string
	PageNum         uint64
	PageSize        uint64
}

// ChangeLogStore is the persistence boundary.
type ChangeLogStore interface {
	List(ctx context.Context, query ChangeLogQuery) (ChangeLogPage, error)
	Get(ctx context.Context, id uint64) (ChangeLog, error)
	Insert(ctx context.Context, input ChangeLogInput) (uint64, error)
	Update(ctx context.Context, input ChangeLogInput) (bool, error)
	Delete(ctx context.Context, id uint64) error
	DeleteBatch(ctx context.Context, ids []uint64) error
}

// ChangeLogService owns release-note validation and console queries.
type ChangeLogService struct {
	store      ChangeLogStore
	authorizer RBACAuthorizer
}

// NewChangeLogService constructs the change log service.
func NewChangeLogService(store ChangeLogStore, authorizer RBACAuthorizer) *ChangeLogService {
	return &ChangeLogService{store: store, authorizer: authorizer}
}

func validateChangeLogInput(input ChangeLogInput) *coreerrors.Error {
	if strings.TrimSpace(input.UpdateVersion) == "" {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "版本不能为空")
	}
	if input.Type < ChangeLogMajor || input.Type > ChangeLogBugfix {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "更新类型错误")
	}
	if strings.TrimSpace(input.PublishAuthor) == "" {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "发布人不能为空")
	}
	if _, err := time.Parse("2006-01-02", strings.TrimSpace(input.PublicDate)); err != nil {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "发布日期格式错误，应为 yyyy-MM-dd")
	}
	if strings.TrimSpace(input.Content) == "" {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "更新内容不能为空")
	}
	return nil
}

// List returns a change-log page.
func (s *ChangeLogService) List(ctx context.Context, query ChangeLogQuery) (ChangeLogPage, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportChangeLogRead); err != nil {
		return ChangeLogPage{}, err
	}
	query.PageNum, query.PageSize = NormalizePage(query.PageNum, query.PageSize)
	page, storeErr := s.store.List(ctx, query)
	if storeErr != nil {
		return ChangeLogPage{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "list change logs", storeErr)
	}
	return page, nil
}

// Detail returns one change log.
func (s *ChangeLogService) Detail(ctx context.Context, id uint64) (ChangeLog, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportChangeLogRead); err != nil {
		return ChangeLog{}, err
	}
	row, storeErr := s.store.Get(ctx, id)
	if storeErr != nil {
		return ChangeLog{}, mapChangeLogError(storeErr)
	}
	return row, nil
}

// Add creates a release note.
func (s *ChangeLogService) Add(ctx context.Context, input ChangeLogInput) *coreerrors.Error {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportChangeLogManage); err != nil {
		return err
	}
	if err := validateChangeLogInput(input); err != nil {
		return err
	}
	if _, storeErr := s.store.Insert(ctx, input); storeErr != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "add change log", storeErr)
	}
	return nil
}

// Update modifies a release note.
func (s *ChangeLogService) Update(ctx context.Context, input ChangeLogInput) *coreerrors.Error {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportChangeLogManage); err != nil {
		return err
	}
	if input.ID == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "更新日志id不能为空")
	}
	if err := validateChangeLogInput(input); err != nil {
		return err
	}
	updated, storeErr := s.store.Update(ctx, input)
	if storeErr != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "update change log", storeErr)
	}
	if !updated {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "数据不存在")
	}
	return nil
}

// Delete removes one release note.
func (s *ChangeLogService) Delete(ctx context.Context, id uint64) *coreerrors.Error {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportChangeLogManage); err != nil {
		return err
	}
	if id == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "更新日志id不能为空")
	}
	if storeErr := s.store.Delete(ctx, id); storeErr != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "delete change log", storeErr)
	}
	return nil
}

// DeleteBatch removes multiple release notes.
func (s *ChangeLogService) DeleteBatch(ctx context.Context, ids []uint64) *coreerrors.Error {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportChangeLogManage); err != nil {
		return err
	}
	if len(ids) == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "id列表不能为空")
	}
	if len(ids) > 500 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "too many ids")
	}
	for _, id := range ids {
		if id == 0 {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "id列表包含非法值")
		}
	}
	if storeErr := s.store.DeleteBatch(ctx, ids); storeErr != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "batch delete change logs", storeErr)
	}
	return nil
}

func mapChangeLogError(err error) *coreerrors.Error {
	if err == ErrChangeLogNotFound {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "数据不存在")
	}
	return coreerrors.Wrap(coreerrors.CodeUnavailable, "get change log", err)
}
