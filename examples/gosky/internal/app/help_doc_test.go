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
//	HD-01 catalog add/update/delete require support.helpDoc.manage
//	HD-02 catalog name validation and duplicate-name sentinel mapping
//	HD-03 catalog delete guards (children, in-use, missing)
//	HD-04 document add validates title/catalog/author/content/keys/relations
//	HD-05 document update requires an id and read-only getDetail needs read
//	HD-06 store failures map to unavailable
//	HD-07 user-side list/view/relations require only an authenticated subject
//	HD-08 view requires a subject and a non-zero doc id; records IP/UA bounded
//	HD-09 view records require a doc id; keywords pass through
//	HD-10 detail attachments receive signed URLs; nil signer stays empty
type fakeHelpDocStore struct {
	catalogs        []HelpDocCatalog
	docs            []HelpDoc
	detail          HelpDocDetail
	addCatalogIn    HelpDocCatalogInput
	updateCatalogIn HelpDocCatalogInput
	deletedCatalog  uint64
	addDocIn        HelpDocInput
	updateDocIn     HelpDocInput
	deletedDoc      uint64
	listQuery       HelpDocQuery
	viewSubject     string
	viewIP          string
	viewUA          string
	recordQuery     HelpDocViewRecordQuery
	page            HelpDocViewRecordPage
	fail            error
}

func (s *fakeHelpDocStore) ListCatalogs(_ context.Context, _ uint64) ([]HelpDocCatalog, error) {
	return s.catalogs, s.fail
}

func (s *fakeHelpDocStore) CreateCatalog(_ context.Context, _ uint64, input HelpDocCatalogInput) error {
	if s.fail != nil {
		return s.fail
	}
	s.addCatalogIn = input
	return nil
}

func (s *fakeHelpDocStore) UpdateCatalog(_ context.Context, _ uint64, input HelpDocCatalogInput) error {
	if s.fail != nil {
		return s.fail
	}
	s.updateCatalogIn = input
	return nil
}

func (s *fakeHelpDocStore) DeleteCatalog(_ context.Context, _, id uint64) error {
	if s.fail != nil {
		return s.fail
	}
	s.deletedCatalog = id
	return nil
}

func (s *fakeHelpDocStore) ListDocs(_ context.Context, _ uint64, query HelpDocQuery) (HelpDocPage, error) {
	s.listQuery = query
	return HelpDocPage{List: s.docs, Total: len(s.docs)}, s.fail
}

func (s *fakeHelpDocStore) ListAllDocs(_ context.Context, _ uint64) ([]HelpDoc, error) {
	return s.docs, s.fail
}

func (s *fakeHelpDocStore) DocDetail(_ context.Context, _, _ uint64, _ bool) (HelpDocDetail, error) {
	return s.detail, s.fail
}

func (s *fakeHelpDocStore) CreateDoc(_ context.Context, _ uint64, input HelpDocInput) (uint64, error) {
	if s.fail != nil {
		return 0, s.fail
	}
	s.addDocIn = input
	return 1, nil
}

func (s *fakeHelpDocStore) UpdateDoc(_ context.Context, _ uint64, input HelpDocInput) error {
	if s.fail != nil {
		return s.fail
	}
	s.updateDocIn = input
	return nil
}

func (s *fakeHelpDocStore) DeleteDoc(_ context.Context, _, id uint64) error {
	if s.fail != nil {
		return s.fail
	}
	s.deletedDoc = id
	return nil
}

func (s *fakeHelpDocStore) ListDocsByRelation(_ context.Context, _, relationID uint64) ([]HelpDoc, error) {
	return s.docs, s.fail
}

func (s *fakeHelpDocStore) RecordView(_ context.Context, _, docID uint64, subject, ip, ua string) (bool, error) {
	if s.fail != nil {
		return false, s.fail
	}
	s.viewSubject, s.viewIP, s.viewUA = subject, ip, ua
	return true, nil
}

func (s *fakeHelpDocStore) ListViewRecords(_ context.Context, _ uint64, query HelpDocViewRecordQuery) (HelpDocViewRecordPage, error) {
	s.recordQuery = query
	return s.page, s.fail
}

type helpSigner struct{}

func (helpSigner) Sign(_ uint64, key string, now time.Time) SignedFileURL {
	return SignedFileURL{URL: "/signed/" + key, ExpiresAt: now.Unix()}
}

func helpDocService(store HelpDocStore, caps ...authz.Capability) *HelpDocService {
	return NewHelpDocService(store, dictCapAuthorizer{allowed: capSet(caps)}, helpSigner{})
}

func validHelpDocInput() HelpDocInput {
	return HelpDocInput{
		Title:       "Onboarding",
		CatalogID:   5,
		ContentText: "hello",
		ContentHTML: "<p>hello</p>",
		Author:      "Luna",
		Sort:        1,
		Relations:   []HelpDocRelation{{RelationName: "Home", RelationID: 0}},
	}
}

func TestHelpDocCatalogs(t *testing.T) {
	t.Run("HD-01 mutations require manage capability", func(t *testing.T) {
		store := &fakeHelpDocStore{}
		service := helpDocService(store)
		if _, err := service.ListCatalogs(fileCtx()); err != nil {
			t.Fatalf("list catalogs: %v", err)
		}
		if err := service.AddCatalog(fileCtx(), HelpDocCatalogInput{Name: "Ops"}); err == nil || err.Code != coreerrors.CodePermissionDenied {
			t.Fatalf("add catalog error = %#v", err)
		}
		if err := service.UpdateCatalog(fileCtx(), HelpDocCatalogInput{ID: 1, Name: "Ops"}); err == nil || err.Code != coreerrors.CodePermissionDenied {
			t.Fatalf("update catalog error = %#v", err)
		}
		if err := service.DeleteCatalog(fileCtx(), 1); err == nil || err.Code != coreerrors.CodePermissionDenied {
			t.Fatalf("delete catalog error = %#v", err)
		}
	})

	t.Run("HD-02 valid name and duplicates map to client error", func(t *testing.T) {
		store := &fakeHelpDocStore{fail: appErr(ErrHelpDocCatalogDuplicateName)}
		service := helpDocService(store, authz.CapabilitySupportHelpDocManage)
		err := service.AddCatalog(fileCtx(authz.CapabilitySupportHelpDocManage), HelpDocCatalogInput{Name: "Ops"})
		if err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("duplicate error = %#v", err)
		}
		store.fail = nil
		if err := service.AddCatalog(fileCtx(authz.CapabilitySupportHelpDocManage), HelpDocCatalogInput{Name: " "}); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("blank name error = %#v", err)
		}
		if err := service.UpdateCatalog(fileCtx(authz.CapabilitySupportHelpDocManage), HelpDocCatalogInput{Name: "Ops"}); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("missing id error = %#v", err)
		}
	})

	t.Run("HD-03 delete guards", func(t *testing.T) {
		for _, sentinel := range []error{ErrHelpDocCatalogNotFound, ErrHelpDocCatalogHasChildren, ErrHelpDocCatalogInUse} {
			store := &fakeHelpDocStore{fail: appErr(sentinel)}
			service := helpDocService(store, authz.CapabilitySupportHelpDocManage)
			if err := service.DeleteCatalog(fileCtx(authz.CapabilitySupportHelpDocManage), 9); err == nil || err.Code != coreerrors.CodeInvalidArgument {
				t.Fatalf("sentinel %v error = %#v", sentinel, err)
			}
		}
		store := &fakeHelpDocStore{}
		service := helpDocService(store, authz.CapabilitySupportHelpDocManage)
		if err := service.DeleteCatalog(fileCtx(authz.CapabilitySupportHelpDocManage), 0); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("zero id error = %#v", err)
		}
	})

	t.Run("HD-06 store failure maps to unavailable", func(t *testing.T) {
		service := helpDocService(&fakeHelpDocStore{fail: errors.New("db down")}, authz.CapabilitySupportHelpDocManage)
		if err := service.AddCatalog(fileCtx(authz.CapabilitySupportHelpDocManage), HelpDocCatalogInput{Name: "Ops"}); err == nil || err.Code != coreerrors.CodeUnavailable {
			t.Fatalf("add error = %#v", err)
		}
	})
}

func TestHelpDocDocuments(t *testing.T) {
	t.Run("HD-04 add validates the form", func(t *testing.T) {
		store := &fakeHelpDocStore{}
		service := helpDocService(store, authz.CapabilitySupportHelpDocManage)
		ctx := fileCtx(authz.CapabilitySupportHelpDocManage)
		valid := validHelpDocInput()
		if err := service.AddDoc(ctx, valid); err != nil {
			t.Fatalf("add: %v", err)
		}
		if store.addDocIn.Relations[0].RelationID != 0 {
			t.Fatalf("relations = %#v", store.addDocIn.Relations)
		}
		invalid := []struct {
			name   string
			mutate func(HelpDocInput) HelpDocInput
		}{
			{"blank title", func(in HelpDocInput) HelpDocInput { in.Title = " "; return in }},
			{"missing catalog", func(in HelpDocInput) HelpDocInput { in.CatalogID = 0; return in }},
			{"blank author", func(in HelpDocInput) HelpDocInput { in.Author = ""; return in }},
			{"blank text", func(in HelpDocInput) HelpDocInput { in.ContentText = ""; return in }},
			{"blank html", func(in HelpDocInput) HelpDocInput { in.ContentHTML = ""; return in }},
			{"bad attachment key", func(in HelpDocInput) HelpDocInput { in.AttachmentKeys = []string{"../etc/passwd"}; return in }},
			{"bad relation name", func(in HelpDocInput) HelpDocInput { in.Relations = []HelpDocRelation{{RelationID: 1}}; return in }},
		}
		for _, tc := range invalid {
			t.Run(tc.name, func(t *testing.T) {
				if err := service.AddDoc(ctx, tc.mutate(validHelpDocInput())); err == nil || err.Code != coreerrors.CodeInvalidArgument {
					t.Fatalf("error = %#v, want invalid argument", err)
				}
			})
		}
	})

	t.Run("HD-05 update requires id and getDetail requires read", func(t *testing.T) {
		store := &fakeHelpDocStore{}
		service := helpDocService(store, authz.CapabilitySupportHelpDocManage)
		in := validHelpDocInput()
		if err := service.UpdateDoc(fileCtx(authz.CapabilitySupportHelpDocManage), in); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("missing id error = %#v", err)
		}
		in.ID = 7
		if err := service.UpdateDoc(fileCtx(authz.CapabilitySupportHelpDocManage), in); err != nil {
			t.Fatalf("update: %v", err)
		}
		if store.updateDocIn.ID != 7 {
			t.Fatalf("updated id = %d", store.updateDocIn.ID)
		}
		if _, err := service.GetDetail(fileCtx(), 7); err == nil || err.Code != coreerrors.CodePermissionDenied {
			t.Fatalf("getDetail error = %#v", err)
		}
	})

	t.Run("HD-06 doc store failures map to unavailable", func(t *testing.T) {
		service := helpDocService(&fakeHelpDocStore{fail: errors.New("db down")}, authz.CapabilitySupportHelpDocManage)
		ctx := fileCtx(authz.CapabilitySupportHelpDocManage)
		if err := service.AddDoc(ctx, validHelpDocInput()); err == nil || err.Code != coreerrors.CodeUnavailable {
			t.Fatalf("add error = %#v", err)
		}
	})
}

func TestHelpDocUserSide(t *testing.T) {
	t.Run("HD-07 browsing requires only a subject", func(t *testing.T) {
		store := &fakeHelpDocStore{docs: []HelpDoc{{ID: 1, Title: "Welcome"}}}
		service := helpDocService(store)
		if _, err := service.ListAllDocs(context.Background()); err == nil || err.Code != coreerrors.CodeUnauthenticated {
			t.Fatalf("list error = %#v", err)
		}
		docs, err := service.ListAllDocs(fileCtx())
		if err != nil || len(docs) != 1 {
			t.Fatalf("docs = %#v err = %v", docs, err)
		}
		relations, err := service.ListDocsByRelation(fileCtx(), 3)
		if err != nil || len(relations) != 1 {
			t.Fatalf("relations = %#v err = %v", relations, err)
		}
	})

	t.Run("HD-08 view validates and records bounded viewer fields", func(t *testing.T) {
		store := &fakeHelpDocStore{detail: HelpDocDetail{ID: 4}}
		service := helpDocService(store)
		if _, err := service.View(fileCtx(), 0, "user:admin", "127.0.0.1", "ua"); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("zero id error = %#v", err)
		}
		if _, err := service.View(fileCtx(), 4, " ", "127.0.0.1", "ua"); err == nil || err.Code != coreerrors.CodeUnauthenticated {
			t.Fatalf("blank subject error = %#v", err)
		}
		longUA := repeat("x", maxHelpDocViewerUARunes+50)
		detail, err := service.View(fileCtx(), 4, " user:admin ", "127.0.0.1", longUA)
		if err != nil {
			t.Fatalf("view: %v", err)
		}
		if detail.ID != 4 || store.viewSubject != "user:admin" || len([]rune(store.viewUA)) != maxHelpDocViewerUARunes {
			t.Fatalf("view record = %#v ua len=%d", store, len([]rune(store.viewUA)))
		}
	})

	t.Run("HD-08 missing doc is a client error", func(t *testing.T) {
		service := helpDocService(&fakeHelpDocStore{fail: appErr(ErrHelpDocNotFound)})
		_, err := service.View(fileCtx(), 99, "user:admin", "", "")
		if err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("missing doc error = %#v", err)
		}
	})

	t.Run("HD-09 view records require a doc id", func(t *testing.T) {
		store := &fakeHelpDocStore{}
		service := helpDocService(store)
		if _, err := service.ListViewRecords(fileCtx(), HelpDocViewRecordQuery{}); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("missing doc error = %#v", err)
		}
		page, err := service.ListViewRecords(fileCtx(), HelpDocViewRecordQuery{HelpDocID: 4, PageNum: 1, PageSize: 5})
		if err != nil {
			t.Fatalf("records: %v", err)
		}
		if page.Total != 0 || store.recordQuery.HelpDocID != 4 || store.recordQuery.PageSize != 5 {
			t.Fatalf("query = %#v page = %#v", store.recordQuery, page)
		}
	})

	t.Run("HD-10 detail attachments are signed", func(t *testing.T) {
		store := &fakeHelpDocStore{detail: HelpDocDetail{ID: 1, Attachments: []FeedbackAttachment{
			{FileKey: "k1"}, {FileKey: "k2"},
		}}}
		service := helpDocService(store, authz.CapabilitySupportHelpDocRead)
		detail, err := service.GetDetail(fileCtx(authz.CapabilitySupportHelpDocRead), 1)
		if err != nil {
			t.Fatalf("detail: %v", err)
		}
		if detail.Attachments[0].FileURL != "/signed/k1" || detail.Attachments[1].FileURL != "/signed/k2" {
			t.Fatalf("attachments = %#v", detail.Attachments)
		}
	})
}

func appErr(err error) error { return err }
