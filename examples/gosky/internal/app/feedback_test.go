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
//	FB-01 add accepts any authenticated employee and resolves the tenant
//	FB-02 add rejects blank/oversize content and invalid attachment keys
//	FB-03 add normalizes and dedups attachment keys
//	FB-04 add surfaces store failures as unavailable
//	FB-05 list requires support.feedback.read and normalizes paging
//	FB-06 list rejects an over-length search word
//	FB-07 list signs attachment URLs; nil signer leaves URLs empty
//	FB-08 list surfaces store failures as unavailable
type fakeFeedbackStore struct {
	addTenant  uint64
	addSubject string
	addInput   FeedbackInput
	listQuery  FeedbackQuery
	page       FeedbackPage
	fail       error
}

func (s *fakeFeedbackStore) Add(_ context.Context, tenantID uint64, subject string, input FeedbackInput) error {
	if s.fail != nil {
		return s.fail
	}
	s.addTenant = tenantID
	s.addSubject = subject
	s.addInput = input
	return nil
}

func (s *fakeFeedbackStore) List(_ context.Context, _ uint64, query FeedbackQuery) (FeedbackPage, error) {
	s.listQuery = query
	if s.fail != nil {
		return FeedbackPage{}, s.fail
	}
	return s.page, nil
}

type fakeFeedbackSigner struct{}

func (fakeFeedbackSigner) Sign(tenantID uint64, key string, now time.Time) SignedFileURL {
	return SignedFileURL{URL: "/signed/" + key, ExpiresAt: now.Add(time.Hour).Unix()}
}

func feedbackService(store FeedbackStore, signer FeedbackURLSigner, caps ...authz.Capability) *FeedbackService {
	return NewFeedbackService(store, dictCapAuthorizer{allowed: capSet(caps)}, signer)
}

func validFeedbackKey(n int) string {
	// 32 lowercase hex characters plus an optional safe extension.
	key := "0123456789abcdef0123456789abcdef"
	if n == 0 {
		return key
	}
	return key[:32-n] + repeat("a", n)
}

func TestFeedbackAdd(t *testing.T) {
	t.Run("FB-01 authenticated employee adds feedback", func(t *testing.T) {
		store := &fakeFeedbackStore{}
		service := feedbackService(store, fakeFeedbackSigner{})
		err := service.Add(fileCtx(), "user:admin", FeedbackInput{Content: "  Great product  ", AttachmentKeys: []string{validFeedbackKey(0) + ".png"}})
		if err != nil {
			t.Fatalf("add: %v", err)
		}
		if store.addTenant != 1001 || store.addSubject != "user:admin" {
			t.Fatalf("add tenant=%d subject=%q", store.addTenant, store.addSubject)
		}
		if store.addInput.Content != "Great product" || len(store.addInput.AttachmentKeys) != 1 {
			t.Fatalf("add input = %#v", store.addInput)
		}
	})

	t.Run("FB-01 missing principal rejected", func(t *testing.T) {
		service := feedbackService(&fakeFeedbackStore{}, nil)
		if err := service.Add(context.Background(), "user:admin", FeedbackInput{Content: "x"}); err == nil || err.Code != coreerrors.CodeUnauthenticated {
			t.Fatalf("add error = %#v", err)
		}
	})

	invalid := []struct {
		name string
		in   FeedbackInput
	}{
		{"blank content", FeedbackInput{Content: " "}},
		{"oversize content", FeedbackInput{Content: repeat("好", maxFeedbackContentRunes+1)}},
		{"attachment path traversal", FeedbackInput{Content: "x", AttachmentKeys: []string{"../../etc/passwd"}}},
		{"attachment non-hex key", FeedbackInput{Content: "x", AttachmentKeys: []string{"ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ"}}},
		{"oversize attachment list", FeedbackInput{Content: "x", AttachmentKeys: repeatKeys(maxFeedbackAttachments + 1)}},
	}
	for _, tc := range invalid {
		t.Run("FB-02 "+tc.name, func(t *testing.T) {
			store := &fakeFeedbackStore{}
			service := feedbackService(store, nil)
			err := service.Add(fileCtx(), "user:admin", tc.in)
			if err == nil || err.Code != coreerrors.CodeInvalidArgument {
				t.Fatalf("add error = %#v, want invalid argument", err)
			}
		})
	}

	t.Run("FB-03 keys are trimmed and deduped", func(t *testing.T) {
		store := &fakeFeedbackStore{}
		key := validFeedbackKey(0)
		service := feedbackService(store, nil)
		err := service.Add(fileCtx(), "user:admin", FeedbackInput{
			Content:        "x",
			AttachmentKeys: []string{" " + key + " ", key, ""},
		})
		if err != nil {
			t.Fatalf("add: %v", err)
		}
		if len(store.addInput.AttachmentKeys) != 1 || store.addInput.AttachmentKeys[0] != key {
			t.Fatalf("keys = %#v", store.addInput.AttachmentKeys)
		}
	})

	t.Run("FB-04 store failure maps to unavailable", func(t *testing.T) {
		service := feedbackService(&fakeFeedbackStore{fail: errors.New("db down")}, nil)
		err := service.Add(fileCtx(), "user:admin", FeedbackInput{Content: "x"})
		if err == nil || err.Code != coreerrors.CodeUnavailable {
			t.Fatalf("add error = %#v", err)
		}
	})
}

func repeatKeys(n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = validFeedbackKey(i % 15)
	}
	return keys
}

func TestFeedbackList(t *testing.T) {
	t.Run("FB-05 requires read capability and normalizes paging", func(t *testing.T) {
		store := &fakeFeedbackStore{}
		if _, err := feedbackService(store, nil).List(fileCtx(), FeedbackQuery{}); err == nil || err.Code != coreerrors.CodePermissionDenied {
			t.Fatalf("list error = %#v", err)
		}
		service := feedbackService(store, nil, authz.CapabilitySupportFeedbackRead)
		page, err := service.List(fileCtx(authz.CapabilitySupportFeedbackRead), FeedbackQuery{})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if page.Total != 0 || store.listQuery.PageNum != 1 || store.listQuery.PageSize != 20 {
			t.Fatalf("page=%#v query=%#v", page, store.listQuery)
		}
	})

	t.Run("FB-06 rejects over-length search word", func(t *testing.T) {
		service := feedbackService(&fakeFeedbackStore{}, nil, authz.CapabilitySupportFeedbackRead)
		_, err := service.List(fileCtx(authz.CapabilitySupportFeedbackRead), FeedbackQuery{SearchWord: repeat("好", 26)})
		if err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("list error = %#v", err)
		}
	})

	t.Run("FB-07 signs attachment URLs", func(t *testing.T) {
		page := FeedbackPage{List: []Feedback{{
			ID: 1,
			Attachments: []FeedbackAttachment{
				{FileKey: "k1", FileName: "a.png"},
				{FileKey: "k2", FileName: "b.png"},
			},
		}}, Total: 1}
		service := feedbackService(&fakeFeedbackStore{page: page}, fakeFeedbackSigner{}, authz.CapabilitySupportFeedbackRead)
		result, err := service.List(fileCtx(authz.CapabilitySupportFeedbackRead), FeedbackQuery{PageNum: 1, PageSize: 10})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		urls := result.List[0].Attachments
		if len(urls) != 2 || urls[0].FileURL != "/signed/k1" || urls[1].FileURL != "/signed/k2" {
			t.Fatalf("signed attachments = %#v", urls)
		}
	})

	t.Run("FB-07 nil attachments stay an empty array", func(t *testing.T) {
		page := FeedbackPage{List: []Feedback{{ID: 1}}, Total: 1}
		service := feedbackService(&fakeFeedbackStore{page: page}, nil, authz.CapabilitySupportFeedbackRead)
		result, err := service.List(fileCtx(authz.CapabilitySupportFeedbackRead), FeedbackQuery{PageNum: 1, PageSize: 10})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(result.List[0].Attachments) != 0 {
			t.Fatalf("attachments = %#v, want empty array", result.List[0].Attachments)
		}
	})

	t.Run("FB-08 store failure maps to unavailable", func(t *testing.T) {
		service := feedbackService(&fakeFeedbackStore{fail: errors.New("db down")}, nil, authz.CapabilitySupportFeedbackRead)
		if _, err := service.List(fileCtx(authz.CapabilitySupportFeedbackRead), FeedbackQuery{}); err == nil || err.Code != coreerrors.CodeUnavailable {
			t.Fatalf("list error = %#v", err)
		}
	})
}
