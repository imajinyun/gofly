package app

import (
	"context"
	"strings"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

// DataTracerRecord is one business-level field change trail row.
type DataTracerRecord struct {
	ID        uint64    `json:"dataTracerId"`
	DataID    uint64    `json:"dataId"`
	Type      int       `json:"type"`
	Content   string    `json:"content"`
	DiffOld   string    `json:"diffOld"`
	DiffNew   string    `json:"diffNew"`
	ExtraData string    `json:"extraData"`
	UserID    *uint64   `json:"userId"`
	UserType  int       `json:"userType"`
	UserName  string    `json:"userName"`
	UserAgent string    `json:"userAgent"`
	IP        string    `json:"ip"`
	IPRegion  string    `json:"ipRegion"`
	CreatedAt time.Time `json:"createTime"`
}

// DataTracerPage is the {list,total} envelope.
type DataTracerPage struct {
	List  []DataTracerRecord `json:"list"`
	Total int                `json:"total"`
}

// DataTracerQuery carries the business-trail query.
type DataTracerQuery struct {
	Type     *int
	DataID   uint64
	Keywords string
	PageNum  uint64
	PageSize uint64
}

// DataTracerStore persists business change trails.
type DataTracerStore interface {
	List(ctx context.Context, query DataTracerQuery) (DataTracerPage, error)
}

// DataTracerService serves business data-change trails. gosky records
// trails for business modules added from T19 onward; the console endpoint
// exists now and returns an empty trail until producers are wired.
type DataTracerService struct {
	store      DataTracerStore
	authorizer RBACAuthorizer
}

// NewDataTracerService constructs the data tracer service.
func NewDataTracerService(store DataTracerStore, authorizer RBACAuthorizer) *DataTracerService {
	return &DataTracerService{store: store, authorizer: authorizer}
}

// List returns change trails for one business row.
func (s *DataTracerService) List(ctx context.Context, query DataTracerQuery) (DataTracerPage, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportDevToolsRead); err != nil {
		return DataTracerPage{}, err
	}
	if query.DataID == 0 {
		return DataTracerPage{}, coreerrors.New(coreerrors.CodeInvalidArgument, "业务id不能为空")
	}
	query.Keywords = strings.TrimSpace(query.Keywords)
	query.PageNum, query.PageSize = NormalizePage(query.PageNum, query.PageSize)
	page, storeErr := s.store.List(ctx, query)
	if storeErr != nil {
		return DataTracerPage{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "list data tracer", storeErr)
	}
	return page, nil
}
