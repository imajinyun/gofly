package app

import (
	"context"
	"errors"
	"strings"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

// Help doc validation bounds mirror the unchanged SmartAdmin forms and the
// database columns.
const (
	maxHelpDocTitleRunes       = 200
	maxHelpDocCatalogNameRunes = 200
	maxHelpDocAuthorRunes      = 100
	maxHelpDocRelations        = 50
	maxHelpDocRelationRunes    = 200
	maxHelpDocAttachments      = 24
	maxAttachmentJoinedLength  = 1000
	maxHelpDocViewerIPRunes    = 45
	maxHelpDocViewerUARunes    = 512
)

// HelpDocCatalog is one tenant catalog node.
type HelpDocCatalog struct {
	ID       uint64 `json:"helpDocCatalogId"`
	Name     string `json:"name"`
	ParentID uint64 `json:"parentId"`
	Sort     int    `json:"sort"`
}

// HelpDoc is the management list projection.
type HelpDoc struct {
	ID            uint64    `json:"helpDocId"`
	CatalogID     uint64    `json:"helpDocCatalogId"`
	CatalogName   string    `json:"helpDocCatalogName"`
	Title         string    `json:"title"`
	Author        string    `json:"author"`
	Sort          int       `json:"sort"`
	PageViewCount int       `json:"pageViewCount"`
	UserViewCount int       `json:"userViewCount"`
	CreatedAt     time.Time `json:"createTime"`
	UpdatedAt     time.Time `json:"updateTime"`
}

// HelpDocPage is the {list,total} envelope for /support/helpDoc/query.
type HelpDocPage struct {
	List  []HelpDoc `json:"list"`
	Total int       `json:"total"`
}

// HelpDocDetail carries the full document and related menu/page entries.
type HelpDocDetail struct {
	ID            uint64               `json:"helpDocId"`
	CatalogID     uint64               `json:"helpDocCatalogId"`
	Title         string               `json:"title"`
	ContentText   string               `json:"contentText"`
	ContentHTML   string               `json:"contentHtml"`
	Attachments   []FeedbackAttachment `json:"attachment"`
	Author        string               `json:"author"`
	Sort          int                  `json:"sort"`
	PageViewCount int                  `json:"pageViewCount"`
	UserViewCount int                  `json:"userViewCount"`
	Relations     []HelpDocRelation    `json:"relationList"`
	CreatedAt     time.Time            `json:"createTime"`
	UpdatedAt     time.Time            `json:"updateTime"`
}

// HelpDocRelation is one related menu/page entry (relationId 0 = home).
type HelpDocRelation struct {
	RelationName string `json:"relationName"`
	RelationID   uint64 `json:"relationId"`
}

// HelpDocViewRecord is the per-user view row.
type HelpDocViewRecord struct {
	UserID         uint64    `json:"userId"`
	UserName       string    `json:"userName"`
	PageViewCount  int       `json:"pageViewCount"`
	FirstIP        string    `json:"firstIp"`
	FirstUserAgent string    `json:"firstUserAgent"`
	LastIP         string    `json:"lastIp"`
	LastUserAgent  string    `json:"lastUserAgent"`
	CreatedAt      time.Time `json:"createTime"`
	UpdatedAt      time.Time `json:"updateTime"`
}

// HelpDocViewRecordPage is the {list,total} envelope for view records.
type HelpDocViewRecordPage struct {
	List  []HelpDocViewRecord `json:"list"`
	Total int                 `json:"total"`
}

// HelpDocQuery carries the management filters.
type HelpDocQuery struct {
	CatalogID       *uint64
	Keywords        string
	CreateTimeBegin string
	CreateTimeEnd   string
	PageNum         uint64
	PageSize        uint64
}

// HelpDocViewRecordQuery carries the view-record filters.
type HelpDocViewRecordQuery struct {
	HelpDocID uint64
	UserID    *uint64
	Keywords  string
	PageNum   uint64
	PageSize  uint64
}

// HelpDocCatalogInput is the add/update body for a catalog node.
type HelpDocCatalogInput struct {
	ID       uint64
	Name     string
	ParentID uint64
	Sort     int
}

// HelpDocInput is the add/update body for a document.
type HelpDocInput struct {
	ID             uint64
	Title          string
	CatalogID      uint64
	ContentText    string
	ContentHTML    string
	AttachmentKeys []string
	Sort           int
	Author         string
	Relations      []HelpDocRelation
}

// HelpDocStore is the persistence boundary.
type HelpDocStore interface {
	ListCatalogs(ctx context.Context, tenantID uint64) ([]HelpDocCatalog, error)
	CreateCatalog(ctx context.Context, tenantID uint64, input HelpDocCatalogInput) error
	UpdateCatalog(ctx context.Context, tenantID uint64, input HelpDocCatalogInput) error
	DeleteCatalog(ctx context.Context, tenantID, id uint64) error

	ListDocs(ctx context.Context, tenantID uint64, query HelpDocQuery) (HelpDocPage, error)
	ListAllDocs(ctx context.Context, tenantID uint64) ([]HelpDoc, error)
	DocDetail(ctx context.Context, tenantID, id uint64, includeRelations bool) (HelpDocDetail, error)
	CreateDoc(ctx context.Context, tenantID uint64, input HelpDocInput) (uint64, error)
	UpdateDoc(ctx context.Context, tenantID uint64, input HelpDocInput) error
	DeleteDoc(ctx context.Context, tenantID, id uint64) error
	ListDocsByRelation(ctx context.Context, tenantID, relationID uint64) ([]HelpDoc, error)

	RecordView(ctx context.Context, tenantID, docID uint64, subject, ip, userAgent string) (newUser bool, err error)
	ListViewRecords(ctx context.Context, tenantID uint64, query HelpDocViewRecordQuery) (HelpDocViewRecordPage, error)
}

// HelpDocService owns validation, authorization and attachment projection.
type HelpDocService struct {
	store      HelpDocStore
	authorizer RBACAuthorizer
	signer     FeedbackURLSigner
	now        func() time.Time
}

func NewHelpDocService(store HelpDocStore, authorizer RBACAuthorizer, signer FeedbackURLSigner) *HelpDocService {
	return &HelpDocService{store: store, authorizer: authorizer, signer: signer, now: time.Now}
}

// ---------------- catalogs ----------------

// ListCatalogs is available to every authenticated employee (management tree
// and the side-help panel both bootstrap from it).
func (s *HelpDocService) ListCatalogs(ctx context.Context) ([]HelpDocCatalog, *coreerrors.Error) {
	tenantID, err := tenantFromPrincipalOrReject(ctx)
	if err != nil {
		return nil, err
	}
	catalogs, storeErr := s.store.ListCatalogs(ctx, tenantID)
	if storeErr != nil {
		return nil, coreerrors.Wrap(coreerrors.CodeUnavailable, "list help doc catalogs", storeErr)
	}
	return catalogs, nil
}

func (s *HelpDocService) AddCatalog(ctx context.Context, input HelpDocCatalogInput) *coreerrors.Error {
	tenantID, err := s.requireManage(ctx)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || len([]rune(name)) > maxHelpDocCatalogNameRunes {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "catalog name is invalid")
	}
	input.Name = name
	if storeErr := s.store.CreateCatalog(ctx, tenantID, input); storeErr != nil {
		return mapHelpDocStoreError(storeErr)
	}
	return nil
}

func (s *HelpDocService) UpdateCatalog(ctx context.Context, input HelpDocCatalogInput) *coreerrors.Error {
	tenantID, err := s.requireManage(ctx)
	if err != nil {
		return err
	}
	if input.ID == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "catalog id is required")
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || len([]rune(name)) > maxHelpDocCatalogNameRunes {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "catalog name is invalid")
	}
	input.Name = name
	if storeErr := s.store.UpdateCatalog(ctx, tenantID, input); storeErr != nil {
		return mapHelpDocStoreError(storeErr)
	}
	return nil
}

func (s *HelpDocService) DeleteCatalog(ctx context.Context, id uint64) *coreerrors.Error {
	tenantID, err := s.requireManage(ctx)
	if err != nil {
		return err
	}
	if id == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "catalog id is required")
	}
	if storeErr := s.store.DeleteCatalog(ctx, tenantID, id); storeErr != nil {
		return mapHelpDocStoreError(storeErr)
	}
	return nil
}

// ---------------- management documents ----------------

func (s *HelpDocService) ListDocs(ctx context.Context, query HelpDocQuery) (HelpDocPage, *coreerrors.Error) {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportHelpDocRead)
	if err != nil {
		return HelpDocPage{}, err
	}
	query.PageNum, query.PageSize = NormalizePage(query.PageNum, query.PageSize)
	page, storeErr := s.store.ListDocs(ctx, tenantID, query)
	if storeErr != nil {
		return HelpDocPage{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "list help docs", storeErr)
	}
	return page, nil
}

func (s *HelpDocService) GetDetail(ctx context.Context, id uint64) (HelpDocDetail, *coreerrors.Error) {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportHelpDocRead)
	if err != nil {
		return HelpDocDetail{}, err
	}
	return s.loadDetail(ctx, tenantID, id, true)
}

func (s *HelpDocService) AddDoc(ctx context.Context, input HelpDocInput) *coreerrors.Error {
	tenantID, err := s.requireManage(ctx)
	if err != nil {
		return err
	}
	normalized, verr := s.validateDoc(input)
	if verr != nil {
		return verr
	}
	if _, storeErr := s.store.CreateDoc(ctx, tenantID, normalized); storeErr != nil {
		return mapHelpDocStoreError(storeErr)
	}
	return nil
}

func (s *HelpDocService) UpdateDoc(ctx context.Context, input HelpDocInput) *coreerrors.Error {
	tenantID, err := s.requireManage(ctx)
	if err != nil {
		return err
	}
	if input.ID == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "help doc id is required")
	}
	normalized, verr := s.validateDoc(input)
	if verr != nil {
		return verr
	}
	if storeErr := s.store.UpdateDoc(ctx, tenantID, normalized); storeErr != nil {
		return mapHelpDocStoreError(storeErr)
	}
	return nil
}

func (s *HelpDocService) DeleteDoc(ctx context.Context, id uint64) *coreerrors.Error {
	tenantID, err := s.requireManage(ctx)
	if err != nil {
		return err
	}
	if id == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "help doc id is required")
	}
	if storeErr := s.store.DeleteDoc(ctx, tenantID, id); storeErr != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "delete help doc", storeErr)
	}
	return nil
}

// ---------------- user-side browsing (authenticated only) ----------------

// ListAllDocs is the side-panel and /help-doc/detail bootstrap list.
func (s *HelpDocService) ListAllDocs(ctx context.Context) ([]HelpDoc, *coreerrors.Error) {
	tenantID, err := tenantFromPrincipalOrReject(ctx)
	if err != nil {
		return nil, err
	}
	docs, storeErr := s.store.ListAllDocs(ctx, tenantID)
	if storeErr != nil {
		return nil, coreerrors.Wrap(coreerrors.CodeUnavailable, "list help docs", storeErr)
	}
	return docs, nil
}

// ListDocsByRelation powers the contextual "?" help entry beside a page.
func (s *HelpDocService) ListDocsByRelation(ctx context.Context, relationID uint64) ([]HelpDoc, *coreerrors.Error) {
	tenantID, err := tenantFromPrincipalOrReject(ctx)
	if err != nil {
		return nil, err
	}
	docs, storeErr := s.store.ListDocsByRelation(ctx, tenantID, relationID)
	if storeErr != nil {
		return nil, coreerrors.Wrap(coreerrors.CodeUnavailable, "list related help docs", storeErr)
	}
	return docs, nil
}

// View returns the document and records one view by the authenticated
// subject; the repository resolves user id and name from the membership.
func (s *HelpDocService) View(ctx context.Context, id uint64, subject, ip, userAgent string) (HelpDocDetail, *coreerrors.Error) {
	tenantID, err := tenantFromPrincipalOrReject(ctx)
	if err != nil {
		return HelpDocDetail{}, err
	}
	if id == 0 {
		return HelpDocDetail{}, coreerrors.New(coreerrors.CodeInvalidArgument, "help doc id is required")
	}
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return HelpDocDetail{}, coreerrors.New(coreerrors.CodeUnauthenticated, "subject is required")
	}
	ip = sanitizeViewerField(ip, maxHelpDocViewerIPRunes)
	userAgent = sanitizeViewerField(userAgent, maxHelpDocViewerUARunes)
	if _, storeErr := s.store.RecordView(ctx, tenantID, id, subject, ip, userAgent); storeErr != nil {
		return HelpDocDetail{}, mapHelpDocStoreError(storeErr)
	}
	return s.loadDetail(ctx, tenantID, id, false)
}

// ListViewRecords is exposed to the document page for every authenticated
// employee, matching the Java base controller (no permission annotation).
func (s *HelpDocService) ListViewRecords(ctx context.Context, query HelpDocViewRecordQuery) (HelpDocViewRecordPage, *coreerrors.Error) {
	tenantID, err := tenantFromPrincipalOrReject(ctx)
	if err != nil {
		return HelpDocViewRecordPage{}, err
	}
	if query.HelpDocID == 0 {
		return HelpDocViewRecordPage{}, coreerrors.New(coreerrors.CodeInvalidArgument, "help doc id is required")
	}
	query.PageNum, query.PageSize = NormalizePage(query.PageNum, query.PageSize)
	page, storeErr := s.store.ListViewRecords(ctx, tenantID, query)
	if storeErr != nil {
		return HelpDocViewRecordPage{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "list help doc view records", storeErr)
	}
	return page, nil
}

// ---------------- helpers ----------------

func (s *HelpDocService) requireManage(ctx context.Context) (uint64, *coreerrors.Error) {
	return authorizeContext(ctx, s.authorizer, authz.CapabilitySupportHelpDocManage)
}

func (s *HelpDocService) validateDoc(input HelpDocInput) (HelpDocInput, *coreerrors.Error) {
	input.Title = strings.TrimSpace(input.Title)
	input.Author = strings.TrimSpace(input.Author)
	if input.Title == "" || len([]rune(input.Title)) > maxHelpDocTitleRunes {
		return HelpDocInput{}, coreerrors.New(coreerrors.CodeInvalidArgument, "help doc title is invalid")
	}
	if input.CatalogID == 0 {
		return HelpDocInput{}, coreerrors.New(coreerrors.CodeInvalidArgument, "help doc catalog is required")
	}
	if input.Author == "" || len([]rune(input.Author)) > maxHelpDocAuthorRunes {
		return HelpDocInput{}, coreerrors.New(coreerrors.CodeInvalidArgument, "help doc author is invalid")
	}
	if strings.TrimSpace(input.ContentText) == "" || strings.TrimSpace(input.ContentHTML) == "" {
		return HelpDocInput{}, coreerrors.New(coreerrors.CodeInvalidArgument, "help doc content is required")
	}
	keys, err := normalizeAttachmentKeysWithLimit(input.AttachmentKeys, maxHelpDocAttachments)
	if err != nil {
		return HelpDocInput{}, err
	}
	input.AttachmentKeys = keys
	relations, err := normalizeRelations(input.Relations)
	if err != nil {
		return HelpDocInput{}, err
	}
	input.Relations = relations
	return input, nil
}

func normalizeRelations(relations []HelpDocRelation) ([]HelpDocRelation, *coreerrors.Error) {
	if len(relations) > maxHelpDocRelations {
		return nil, coreerrors.New(coreerrors.CodeInvalidArgument, "too many relations")
	}
	normalized := make([]HelpDocRelation, 0, len(relations))
	seen := make(map[uint64]struct{}, len(relations))
	for _, relation := range relations {
		name := strings.TrimSpace(relation.RelationName)
		if name == "" || len([]rune(name)) > maxHelpDocRelationRunes {
			return nil, coreerrors.New(coreerrors.CodeInvalidArgument, "relation name is invalid")
		}
		if _, duplicate := seen[relation.RelationID]; duplicate {
			continue
		}
		seen[relation.RelationID] = struct{}{}
		normalized = append(normalized, HelpDocRelation{RelationName: name, RelationID: relation.RelationID})
	}
	return normalized, nil
}

func sanitizeViewerField(value string, maxRunes int) string {
	value = strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(value))
	if runes := []rune(value); len(runes) > maxRunes {
		value = string(runes[:maxRunes])
	}
	return value
}

func (s *HelpDocService) loadDetail(ctx context.Context, tenantID, id uint64, includeRelations bool) (HelpDocDetail, *coreerrors.Error) {
	detail, storeErr := s.store.DocDetail(ctx, tenantID, id, includeRelations)
	if storeErr != nil {
		return HelpDocDetail{}, mapHelpDocStoreError(storeErr)
	}
	tenant := tenantID
	if s.signer != nil {
		now := s.now()
		for index := range detail.Attachments {
			detail.Attachments[index].FileURL = s.signer.Sign(tenant, detail.Attachments[index].FileKey, now).URL
		}
	}
	return detail, nil
}

// mapHelpDocStoreError converts repository sentinels into SmartAdmin-compatible
// client errors while everything else stays a 503-class failure.
func mapHelpDocStoreError(storeErr error) *coreerrors.Error {
	switch {
	case errors.Is(storeErr, ErrHelpDocNotFound):
		return coreerrors.New(coreerrors.CodeInvalidArgument, "帮助文档不存在")
	case errors.Is(storeErr, ErrHelpDocCatalogNotFound):
		return coreerrors.New(coreerrors.CodeInvalidArgument, "目录不存在")
	case errors.Is(storeErr, ErrHelpDocCatalogDuplicateName):
		return coreerrors.New(coreerrors.CodeInvalidArgument, "存在相同名称的目录了")
	case errors.Is(storeErr, ErrHelpDocCatalogHasChildren):
		return coreerrors.New(coreerrors.CodeInvalidArgument, "存在子目录，不能删除")
	case errors.Is(storeErr, ErrHelpDocCatalogInUse):
		return coreerrors.New(coreerrors.CodeInvalidArgument, "目录下存在文档，不能删除")
	case errors.Is(storeErr, ErrHelpDocCatalogCycle):
		return coreerrors.New(coreerrors.CodeInvalidArgument, "目录父级不能选择自身或其子目录")
	default:
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "help doc store failure", storeErr)
	}
}
