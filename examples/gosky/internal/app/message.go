package app

import (
	"context"
	"strings"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

// Message types mirror the unchanged client MESSAGE_TYPE_ENUM.
const (
	MessageTypeMail  = 1
	MessageTypeOrder = 2
)

// MessageReceiverEmployee mirrors USER_TYPE_ENUM.ADMIN_EMPLOYEE: Gosky only
// delivers messages to admin-console employees.
const MessageReceiverEmployee = 1

// Message is one in-app message row. Sender/Receiver stay canonical
// subjects internally; the admin console additionally projects the
// browser-facing receiver user id/type (always admin employee in Gosky).
type Message struct {
	ID               uint64     `json:"messageId"`
	Sender           string     `json:"-"`
	Receiver         string     `json:"-"`
	ReceiverUserID   uint64     `json:"receiverUserId"`
	ReceiverUserType int        `json:"receiverUserType"`
	Title            string     `json:"title"`
	Content          string     `json:"content"`
	Type             int        `json:"messageType"`
	ReadFlag         bool       `json:"readFlag"`
	ReadTime         *time.Time `json:"readTime"`
	CreatedAt        time.Time  `json:"createTime"`
}

// MessagePage is the {list,total} envelope shared by the inbox and admin views.
type MessagePage struct {
	List  []Message `json:"list"`
	Total int       `json:"total"`
}

// MessageQuery is the common paged filter. SearchWord matches title/content;
// ReadFlag and MessageType are optional; Start/End bound the creation time.
type MessageQuery struct {
	SearchWord  string
	ReadFlag    *bool
	MessageType *int
	StartDate   string
	EndDate     string
	PageNum     uint64
	PageSize    uint64
}

// MessageInput is one item of the sendMessages batch. The browser submits
// a receiver user id, which the repository resolves to a canonical subject.
type MessageInput struct {
	Title            string `json:"title"`
	Content          string `json:"content"`
	MessageType      int    `json:"messageType"`
	ReceiverUserID   uint64 `json:"receiverUserId"`
	ReceiverUserType int    `json:"receiverUserType"`
}

// MessageStore is the persistence boundary for the inbox.
type MessageStore interface {
	Send(ctx context.Context, sender string, receiverUserIDs []uint64, input MessageInput) (int, error)
	ListInbox(ctx context.Context, subject string, query MessageQuery) (MessagePage, error)
	CountUnread(ctx context.Context, subject string) (int, error)
	MarkRead(ctx context.Context, subject string, id uint64) error
	ListAdmin(ctx context.Context, query MessageQuery) (MessagePage, error)
	Delete(ctx context.Context, id uint64) error
}

// MessageService owns validation, authorization and subject scoping.
type MessageService struct {
	store      MessageStore
	authorizer RBACAuthorizer
	now        func() time.Time
}

func NewMessageService(store MessageStore, authorizer RBACAuthorizer) *MessageService {
	return &MessageService{store: store, authorizer: authorizer, now: time.Now}
}

func validMessageType(value int) bool {
	return value == MessageTypeMail || value == MessageTypeOrder
}

func (s *MessageService) validateMessage(input MessageInput) *coreerrors.Error {
	input.Title = strings.TrimSpace(input.Title)
	input.Content = strings.TrimSpace(input.Content)
	if input.Title == "" || len([]rune(input.Title)) > 200 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "message title is invalid")
	}
	if input.Content == "" || len([]rune(input.Content)) > 2000 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "message content is invalid")
	}
	if !validMessageType(input.MessageType) {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "message type is invalid")
	}
	return nil
}

// Send creates one message per receiver. All inputs must share the same
// title/content/type; the client sends one MessageInput per receiver.
func (s *MessageService) Send(ctx context.Context, sender string, inputs []MessageInput) *coreerrors.Error {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportMessageSend); err != nil {
		return err
	}
	sender = strings.TrimSpace(sender)
	if sender == "" {
		return coreerrors.New(coreerrors.CodeUnauthenticated, "sender is required")
	}
	if len(inputs) == 0 || len(inputs) > 500 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "message batch is invalid")
	}
	first := inputs[0]
	if err := s.validateMessage(first); err != nil {
		return err
	}
	receiverIDs := make([]uint64, 0, len(inputs))
	seen := make(map[uint64]struct{}, len(inputs))
	for _, input := range inputs {
		if err := s.validateMessage(input); err != nil {
			return err
		}
		if input.Title != first.Title || input.Content != first.Content || input.MessageType != first.MessageType {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "batch messages must share title, content and type")
		}
		if input.ReceiverUserID == 0 {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "receiver is required")
		}
		if _, duplicate := seen[input.ReceiverUserID]; duplicate {
			continue
		}
		seen[input.ReceiverUserID] = struct{}{}
		receiverIDs = append(receiverIDs, input.ReceiverUserID)
	}
	if _, err := s.store.Send(ctx, sender, receiverIDs, first); err != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "send messages", err)
	}
	return nil
}

// ListInbox returns the authenticated subject's own messages.
func (s *MessageService) ListInbox(ctx context.Context, subject string, query MessageQuery) (MessagePage, *coreerrors.Error) {
	if !authenticatedSubject(subject) {
		return MessagePage{}, coreerrors.New(coreerrors.CodeUnauthenticated, "subject is required")
	}
	query.PageNum, query.PageSize = NormalizePage(query.PageNum, query.PageSize)
	page, err := s.store.ListInbox(ctx, strings.TrimSpace(subject), query)
	if err != nil {
		return MessagePage{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "list messages", err)
	}
	return page, nil
}

// CountUnread returns the subject's unread count for header polling.
func (s *MessageService) CountUnread(ctx context.Context, subject string) (int, *coreerrors.Error) {
	if !authenticatedSubject(subject) {
		return 0, coreerrors.New(coreerrors.CodeUnauthenticated, "subject is required")
	}
	count, err := s.store.CountUnread(ctx, strings.TrimSpace(subject))
	if err != nil {
		return 0, coreerrors.Wrap(coreerrors.CodeUnavailable, "count unread messages", err)
	}
	return count, nil
}

// MarkRead marks one of the subject's own messages read.
func (s *MessageService) MarkRead(ctx context.Context, subject string, id uint64) *coreerrors.Error {
	if !authenticatedSubject(subject) {
		return coreerrors.New(coreerrors.CodeUnauthenticated, "subject is required")
	}
	if id == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "message id is required")
	}
	if err := s.store.MarkRead(ctx, strings.TrimSpace(subject), id); err != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "mark message read", err)
	}
	return nil
}

// ListAdmin returns all messages for the support console (manage capability).
func (s *MessageService) ListAdmin(ctx context.Context, query MessageQuery) (MessagePage, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportMessageRead); err != nil {
		return MessagePage{}, err
	}
	query.PageNum, query.PageSize = NormalizePage(query.PageNum, query.PageSize)
	page, err := s.store.ListAdmin(ctx, query)
	if err != nil {
		return MessagePage{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "list admin messages", err)
	}
	return page, nil
}

// Delete removes any message by id, mirroring the Java admin endpoint
// (system:message:delete). Gosky compresses send/delete into one mutation
// capability; sender ownership is intentionally not required.
func (s *MessageService) Delete(ctx context.Context, id uint64) *coreerrors.Error {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportMessageSend); err != nil {
		return err
	}
	if id == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "message id is required")
	}
	if err := s.store.Delete(ctx, id); err != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "delete message", err)
	}
	return nil
}

func authenticatedSubject(subject string) bool {
	return strings.TrimSpace(subject) != ""
}
