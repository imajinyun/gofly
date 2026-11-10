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
//	MSG-01 batch send dedups receivers and shares one title/content/type
//	MSG-02 send requires support.message.send
//	MSG-03 invalid batch: empty, oversize, blank/long title, blank/long
//	      content, bad message type, zero receiver, mismatched payload
//	MSG-04 store failure maps to unavailable
//	MSG-05 personal inbox/count/mark-read require an authenticated subject
//	MSG-06 mark-read rejects a zero id and surfaces store failures
//	MSG-07 admin list requires support.message.read and normalizes paging
//	MSG-08 delete is id-only (Java parity), requires support.message.send,
//	      and rejects a zero id
type fakeMessageStore struct {
	sendCalls   int
	sender      string
	receiverIDs []uint64
	input       MessageInput

	inboxPage    MessagePage
	inboxQuery   MessageQuery
	inboxSubject string
	unread       int

	markedSubject string
	markedID      uint64
	adminQuery    MessageQuery
	deletedID     uint64
	fail          error
}

func (s *fakeMessageStore) Send(_ context.Context, sender string, receiverUserIDs []uint64, input MessageInput) (int, error) {
	s.sendCalls++
	if s.fail != nil {
		return 0, s.fail
	}
	s.sender = sender
	s.receiverIDs = append([]uint64(nil), receiverUserIDs...)
	s.input = input
	return len(receiverUserIDs), nil
}

func (s *fakeMessageStore) ListInbox(_ context.Context, subject string, query MessageQuery) (MessagePage, error) {
	s.inboxSubject = subject
	s.inboxQuery = query
	if s.fail != nil {
		return MessagePage{}, s.fail
	}
	return s.inboxPage, nil
}

func (s *fakeMessageStore) CountUnread(_ context.Context, _ string) (int, error) {
	if s.fail != nil {
		return 0, s.fail
	}
	return s.unread, nil
}

func (s *fakeMessageStore) MarkRead(_ context.Context, subject string, id uint64) error {
	if s.fail != nil {
		return s.fail
	}
	s.markedSubject = subject
	s.markedID = id
	return nil
}

func (s *fakeMessageStore) ListAdmin(_ context.Context, query MessageQuery) (MessagePage, error) {
	s.adminQuery = query
	if s.fail != nil {
		return MessagePage{}, s.fail
	}
	return s.inboxPage, nil
}

func (s *fakeMessageStore) Delete(_ context.Context, id uint64) error {
	if s.fail != nil {
		return s.fail
	}
	s.deletedID = id
	return nil
}

func messageService(store MessageStore, caps ...authz.Capability) *MessageService {
	return NewMessageService(store, dictCapAuthorizer{allowed: capSet(caps)})
}

func validMessageInput(receiverID uint64) MessageInput {
	return MessageInput{
		Title:            "Welcome",
		Content:          "Hello Gosky",
		MessageType:      MessageTypeMail,
		ReceiverUserID:   receiverID,
		ReceiverUserType: MessageReceiverEmployee,
	}
}

func TestMessageSend(t *testing.T) {
	ctx := fileCtx(authz.CapabilitySupportMessageSend)

	t.Run("MSG-01 dedups receivers and keeps shared payload", func(t *testing.T) {
		store := &fakeMessageStore{}
		service := messageService(store, authz.CapabilitySupportMessageSend)
		inputs := []MessageInput{validMessageInput(1002), validMessageInput(1003), validMessageInput(1002)}
		if err := service.Send(ctx, "user:admin", inputs); err != nil {
			t.Fatalf("send: %v", err)
		}
		if store.sendCalls != 1 || store.sender != "user:admin" {
			t.Fatalf("send calls=%d sender=%q", store.sendCalls, store.sender)
		}
		if len(store.receiverIDs) != 2 || store.receiverIDs[0] != 1002 || store.receiverIDs[1] != 1003 {
			t.Fatalf("receiver ids = %v, want [1002 1003]", store.receiverIDs)
		}
		if store.input.Title != "Welcome" || store.input.MessageType != MessageTypeMail {
			t.Fatalf("stored input = %#v", store.input)
		}
	})

	t.Run("MSG-02 requires send capability", func(t *testing.T) {
		store := &fakeMessageStore{}
		service := messageService(store)
		err := service.Send(fileCtx(), "user:admin", []MessageInput{validMessageInput(1002)})
		if err == nil || err.Code != coreerrors.CodePermissionDenied {
			t.Fatalf("send error = %#v, want permission denied", err)
		}
		if store.sendCalls != 0 {
			t.Fatalf("store touched on denial, calls=%d", store.sendCalls)
		}
	})

	badBatches := []struct {
		name   string
		inputs []MessageInput
	}{
		{"empty batch", nil},
		{"oversize batch", func() []MessageInput {
			items := make([]MessageInput, 501)
			for i := range items {
				items[i] = validMessageInput(uint64(i + 1))
			}
			return items
		}()},
		{"blank title", []MessageInput{{Content: "c", MessageType: MessageTypeMail, ReceiverUserID: 1}}},
		{"title too long", []MessageInput{func() MessageInput {
			item := validMessageInput(1)
			item.Title = repeat("好", 201)
			return item
		}()}},
		{"blank content", []MessageInput{{Title: "t", MessageType: MessageTypeMail, ReceiverUserID: 1}}},
		{"content too long", []MessageInput{func() MessageInput {
			item := validMessageInput(1)
			item.Content = repeat("x", 2001)
			return item
		}()}},
		{"bad type", []MessageInput{func() MessageInput {
			item := validMessageInput(1)
			item.MessageType = 9
			return item
		}()}},
		{"zero receiver", []MessageInput{func() MessageInput {
			item := validMessageInput(0)
			return item
		}()}},
		{"mismatched title", []MessageInput{validMessageInput(1), func() MessageInput {
			item := validMessageInput(2)
			item.Title = "Other"
			return item
		}()}},
		{"mismatched type", []MessageInput{validMessageInput(1), func() MessageInput {
			item := validMessageInput(2)
			item.MessageType = MessageTypeOrder
			return item
		}()}},
	}
	for _, tc := range badBatches {
		t.Run("MSG-03 "+tc.name, func(t *testing.T) {
			store := &fakeMessageStore{}
			service := messageService(store, authz.CapabilitySupportMessageSend)
			err := service.Send(ctx, "user:admin", tc.inputs)
			if err == nil || err.Code != coreerrors.CodeInvalidArgument {
				t.Fatalf("send error = %#v, want invalid argument", err)
			}
			if store.sendCalls != 0 {
				t.Fatalf("store touched on invalid batch, calls=%d", store.sendCalls)
			}
		})
	}

	t.Run("MSG-03 blank sender is unauthenticated", func(t *testing.T) {
		store := &fakeMessageStore{}
		service := messageService(store, authz.CapabilitySupportMessageSend)
		err := service.Send(ctx, "  ", []MessageInput{validMessageInput(1)})
		if err == nil || err.Code != coreerrors.CodeUnauthenticated {
			t.Fatalf("send error = %#v, want unauthenticated", err)
		}
	})

	t.Run("MSG-04 store failure maps to unavailable", func(t *testing.T) {
		store := &fakeMessageStore{fail: errors.New("db down")}
		service := messageService(store, authz.CapabilitySupportMessageSend)
		err := service.Send(ctx, "user:admin", []MessageInput{validMessageInput(1)})
		if err == nil || err.Code != coreerrors.CodeUnavailable {
			t.Fatalf("send error = %#v, want unavailable", err)
		}
	})
}

func TestMessageInbox(t *testing.T) {
	store := &fakeMessageStore{inboxPage: MessagePage{List: []Message{{ID: 9}}, Total: 1}, unread: 4}
	service := messageService(store)

	t.Run("MSG-05 list scoped to subject", func(t *testing.T) {
		page, err := service.ListInbox(context.Background(), "user:admin", MessageQuery{PageNum: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if page.Total != 1 || len(page.List) != 1 || page.List[0].ID != 9 {
			t.Fatalf("page = %#v", page)
		}
		if store.inboxSubject != "user:admin" || store.inboxQuery.PageSize != 20 {
			t.Fatalf("inbox query = %#v subject=%q", store.inboxQuery, store.inboxSubject)
		}
	})

	t.Run("MSG-05 blank subject rejected across inbox endpoints", func(t *testing.T) {
		if _, err := service.ListInbox(context.Background(), " ", MessageQuery{}); err == nil || err.Code != coreerrors.CodeUnauthenticated {
			t.Fatalf("list error = %#v", err)
		}
		if _, err := service.CountUnread(context.Background(), ""); err == nil || err.Code != coreerrors.CodeUnauthenticated {
			t.Fatalf("count error = %#v", err)
		}
		if err := service.MarkRead(context.Background(), " ", 9); err == nil || err.Code != coreerrors.CodeUnauthenticated {
			t.Fatalf("read error = %#v", err)
		}
	})

	t.Run("MSG-05 unread count", func(t *testing.T) {
		count, err := service.CountUnread(context.Background(), "user:admin")
		if err != nil || count != 4 {
			t.Fatalf("count=%d err=%v", count, err)
		}
	})

	t.Run("MSG-06 mark read validates id and records subject", func(t *testing.T) {
		if err := service.MarkRead(context.Background(), "user:admin", 9); err != nil {
			t.Fatalf("mark read: %v", err)
		}
		if store.markedID != 9 || store.markedSubject != "user:admin" {
			t.Fatalf("marked id=%d subject=%q", store.markedID, store.markedSubject)
		}
		if err := service.MarkRead(context.Background(), "user:admin", 0); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("zero id error = %#v", err)
		}
	})

	t.Run("MSG-06 store failure maps to unavailable", func(t *testing.T) {
		failing := messageService(&fakeMessageStore{fail: errors.New("db down")})
		if err := failing.MarkRead(context.Background(), "user:admin", 9); err == nil || err.Code != coreerrors.CodeUnavailable {
			t.Fatalf("mark read error = %#v", err)
		}
	})
}

func TestMessageAdminList(t *testing.T) {
	t.Run("MSG-07 requires read capability", func(t *testing.T) {
		store := &fakeMessageStore{}
		if _, err := messageService(store).ListAdmin(fileCtx(), MessageQuery{}); err == nil || err.Code != coreerrors.CodePermissionDenied {
			t.Fatalf("list admin error = %#v", err)
		}
	})

	t.Run("MSG-07 returns page and normalizes paging", func(t *testing.T) {
		store := &fakeMessageStore{inboxPage: MessagePage{Total: 0}}
		service := messageService(store, authz.CapabilitySupportMessageRead)
		page, err := service.ListAdmin(fileCtx(authz.CapabilitySupportMessageRead), MessageQuery{})
		if err != nil {
			t.Fatalf("list admin: %v", err)
		}
		if page.Total != 0 || store.adminQuery.PageNum != 1 || store.adminQuery.PageSize != 20 {
			t.Fatalf("page=%#v query=%#v", page, store.adminQuery)
		}
	})

	t.Run("MSG-07 store failure maps to unavailable", func(t *testing.T) {
		service := messageService(&fakeMessageStore{fail: errors.New("db down")}, authz.CapabilitySupportMessageRead)
		if _, err := service.ListAdmin(fileCtx(authz.CapabilitySupportMessageRead), MessageQuery{}); err == nil || err.Code != coreerrors.CodeUnavailable {
			t.Fatalf("list admin error = %#v", err)
		}
	})
}

func TestMessageDelete(t *testing.T) {
	t.Run("MSG-08 requires send capability", func(t *testing.T) {
		store := &fakeMessageStore{}
		if err := messageService(store).Delete(fileCtx(), 9); err == nil || err.Code != coreerrors.CodePermissionDenied {
			t.Fatalf("delete error = %#v", err)
		}
	})

	t.Run("MSG-08 deletes by id only and rejects zero", func(t *testing.T) {
		store := &fakeMessageStore{}
		service := messageService(store, authz.CapabilitySupportMessageSend)
		if err := service.Delete(fileCtx(authz.CapabilitySupportMessageSend), 77); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if store.deletedID != 77 {
			t.Fatalf("deleted id = %d, want 77", store.deletedID)
		}
		if err := service.Delete(fileCtx(authz.CapabilitySupportMessageSend), 0); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("zero id error = %#v", err)
		}
	})

	t.Run("MSG-08 store failure maps to unavailable", func(t *testing.T) {
		service := messageService(&fakeMessageStore{fail: errors.New("db down")}, authz.CapabilitySupportMessageSend)
		if err := service.Delete(fileCtx(authz.CapabilitySupportMessageSend), 1); err == nil || err.Code != coreerrors.CodeUnavailable {
			t.Fatalf("delete error = %#v", err)
		}
	})
}
