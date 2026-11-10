package app

import (
	"context"
	"strings"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

// Feedback limits mirror the unchanged feedback modal and t_feedback size.
const (
	FeedbackUserTypeEmployee = 1
	maxFeedbackContentRunes  = 2000
	maxFeedbackAttachments   = 10
)

// FeedbackAttachment is one resolved picture of a feedback row. The shape
// matches the FileVO array the unchanged FilePreview component iterates
// (fileUrl/fileName/fileType/fileKey).
type FeedbackAttachment struct {
	FileID   uint64 `json:"fileId"`
	FileName string `json:"fileName"`
	FileKey  string `json:"fileKey"`
	FileSize int64  `json:"fileSize"`
	FileType string `json:"fileType"`
	FileURL  string `json:"fileUrl"`
}

// Feedback is one t_feedback row projected for the admin console.
type Feedback struct {
	ID          uint64               `json:"feedbackId"`
	Content     string               `json:"feedbackContent"`
	Attachments []FeedbackAttachment `json:"feedbackAttachment"`
	UserID      uint64               `json:"userId"`
	UserName    string               `json:"userName"`
	UserType    int                  `json:"userType"`
	UpdatedAt   time.Time            `json:"updateTime"`
	CreatedAt   time.Time            `json:"createTime"`
}

// FeedbackPage is the {list,total} envelope returned by /support/feedback/query.
type FeedbackPage struct {
	List  []Feedback `json:"list"`
	Total int        `json:"total"`
}

// FeedbackQuery carries the console filters. SearchWord matches content and
// submitter name; Start/End bound the creation date.
type FeedbackQuery struct {
	SearchWord string
	StartDate  string
	EndDate    string
	PageNum    uint64
	PageSize   uint64
}

// FeedbackInput is the /support/feedback/add body after transport decoding.
// AttachmentKeys are server-generated file keys joined in the database.
type FeedbackInput struct {
	Content        string
	AttachmentKeys []string
}

// FeedbackStore is the persistence boundary.
type FeedbackStore interface {
	Add(ctx context.Context, tenantID uint64, subject string, input FeedbackInput) error
	List(ctx context.Context, tenantID uint64, query FeedbackQuery) (FeedbackPage, error)
}

// FeedbackURLSigner signs capability URLs for embedded attachment keys. It is
// satisfied by *FileService; a nil signer leaves file URLs empty.
type FeedbackURLSigner interface {
	Sign(tenantID uint64, key string, now time.Time) SignedFileURL
}

// FeedbackService owns validation, authorization and attachment projection.
type FeedbackService struct {
	store      FeedbackStore
	authorizer RBACAuthorizer
	signer     FeedbackURLSigner
	now        func() time.Time
}

func NewFeedbackService(store FeedbackStore, authorizer RBACAuthorizer, signer FeedbackURLSigner) *FeedbackService {
	return &FeedbackService{store: store, authorizer: authorizer, signer: signer, now: time.Now}
}

// Add records one feedback from the side-help modal. Every authenticated
// employee may submit; the subject is resolved to an active membership and
// attachment keys must be server-generated upload keys.
func (s *FeedbackService) Add(ctx context.Context, subject string, input FeedbackInput) *coreerrors.Error {
	tenantID, err := tenantFromPrincipalOrReject(ctx)
	if err != nil {
		return err
	}
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return coreerrors.New(coreerrors.CodeUnauthenticated, "subject is required")
	}
	input.Content = strings.TrimSpace(input.Content)
	if input.Content == "" || len([]rune(input.Content)) > maxFeedbackContentRunes {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "feedback content is invalid")
	}
	keys, err := normalizeAttachmentKeys(input.AttachmentKeys)
	if err != nil {
		return err
	}
	input.AttachmentKeys = keys
	if storeErr := s.store.Add(ctx, tenantID, subject, input); storeErr != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "add feedback", storeErr)
	}
	return nil
}

// List returns the tenant feedback page with signed attachment URLs.
func (s *FeedbackService) List(ctx context.Context, query FeedbackQuery) (FeedbackPage, *coreerrors.Error) {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportFeedbackRead)
	if err != nil {
		return FeedbackPage{}, err
	}
	if word := strings.TrimSpace(query.SearchWord); len([]rune(word)) > 25 {
		return FeedbackPage{}, coreerrors.New(coreerrors.CodeInvalidArgument, "search word is invalid")
	}
	query.PageNum, query.PageSize = NormalizePage(query.PageNum, query.PageSize)
	page, storeErr := s.store.List(ctx, tenantID, query)
	if storeErr != nil {
		return FeedbackPage{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "list feedback", storeErr)
	}
	for index := range page.List {
		page.List[index].Attachments = s.signAttachments(tenantID, page.List[index].Attachments)
	}
	return page, nil
}

func (s *FeedbackService) signAttachments(tenantID uint64, attachments []FeedbackAttachment) []FeedbackAttachment {
	if attachments == nil {
		return []FeedbackAttachment{}
	}
	if s.signer == nil {
		for index := range attachments {
			attachments[index].FileURL = ""
		}
		return attachments
	}
	now := s.now()
	for index := range attachments {
		attachments[index].FileURL = s.signer.Sign(tenantID, attachments[index].FileKey, now).URL
	}
	return attachments
}
