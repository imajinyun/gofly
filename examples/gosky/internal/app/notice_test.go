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
//	NT-01 type list requires read; add/update/delete require manage
//	NT-02 type name/id validation; update and delete on missing rows
//	NT-03 type delete blocked while notices reference the type
//	NT-04 notice add validates title, type, flags, contents, author, source
//	NT-05 notice add with allVisible=false requires well-formed ranges
//	NT-06 notice publish time must use yyyy-MM-dd HH:mm:ss
//	NT-07 notice update requires an id; update/delete missing rows
//	NT-08 store failures map to unavailable
//	NT-09 employee board requires a resolved viewer; store errors surface
//	NT-10 view rejects zero ids, missing/unpublished notices, and hidden ranges
//	NT-11 view happy path records the view and returns reloaded counters
//	NT-12 view records require read capability and a notice id
//	NT-13 visibleTo matrix: admin bypass, employee match, department match

type fakeNoticeStore struct {
	types         []NoticeType
	typeInUse     bool
	page          NoticePage
	detail        NoticeDetail
	getErr        error
	fail          error
	visiblePage   NoticePage
	recordFirst   bool
	viewRecords   NoticeViewRecordPage
	viewer        IdentityScope
	addIn         NoticeInput
	updateIn      NoticeInput
	publishAt     time.Time
	deletedNotice uint64
	deletedType   uint64
	recordNotice  uint64
	recordUser    uint64
	recordIP      string
	recordUA      string
	recordQuery   ViewRecordQuery
}

func (s *fakeNoticeStore) ListTypes(_ context.Context) ([]NoticeType, error) {
	return s.types, s.fail
}

func (s *fakeNoticeStore) AddType(_ context.Context, name string) (uint64, error) {
	if s.fail != nil {
		return 0, s.fail
	}
	s.types = append(s.types, NoticeType{ID: uint64(len(s.types) + 1), Name: name})
	return uint64(len(s.types)), nil
}

func (s *fakeNoticeStore) UpdateType(_ context.Context, id uint64, name string) (bool, error) {
	if s.fail != nil {
		return false, s.fail
	}
	for i := range s.types {
		if s.types[i].ID == id {
			s.types[i].Name = name
			return true, nil
		}
	}
	return false, nil
}

func (s *fakeNoticeStore) DeleteType(_ context.Context, id uint64) (bool, error) {
	if s.fail != nil {
		return false, s.fail
	}
	for i := range s.types {
		if s.types[i].ID == id {
			s.types = append(s.types[:i], s.types[i+1:]...)
			s.deletedType = id
			return true, nil
		}
	}
	return false, nil
}

func (s *fakeNoticeStore) TypeInUse(_ context.Context, id uint64) (bool, error) {
	return s.typeInUse, s.fail
}

func (s *fakeNoticeStore) List(_ context.Context, _ NoticeQuery) (NoticePage, error) {
	return s.page, s.fail
}

func (s *fakeNoticeStore) Get(_ context.Context, _ uint64) (NoticeDetail, error) {
	if s.getErr != nil {
		return NoticeDetail{}, s.getErr
	}
	return s.detail, s.fail
}

func (s *fakeNoticeStore) Insert(_ context.Context, input NoticeInput, publishAt time.Time) (uint64, error) {
	if s.fail != nil {
		return 0, s.fail
	}
	s.addIn, s.publishAt = input, publishAt
	return 7, nil
}

func (s *fakeNoticeStore) Update(_ context.Context, input NoticeInput, publishAt time.Time) (bool, error) {
	if s.fail != nil {
		return false, s.fail
	}
	s.updateIn, s.publishAt = input, publishAt
	return true, nil
}

func (s *fakeNoticeStore) SoftDelete(_ context.Context, id uint64) error {
	if s.fail != nil {
		return s.fail
	}
	s.deletedNotice = id
	return nil
}

func (s *fakeNoticeStore) ResolveViewer(_ context.Context, _ uint64, _ string) (IdentityScope, error) {
	return s.viewer, s.fail
}

func (s *fakeNoticeStore) ListVisible(_ context.Context, _ IdentityScope, _ EmployeeNoticeQuery) (NoticePage, error) {
	return s.visiblePage, s.fail
}

func (s *fakeNoticeStore) RecordView(_ context.Context, noticeID, userID uint64, ip, userAgent string) (bool, int, int, error) {
	if s.fail != nil {
		return false, 0, 0, s.fail
	}
	s.recordNotice, s.recordUser, s.recordIP, s.recordUA = noticeID, userID, ip, userAgent
	delta := 0
	if s.recordFirst {
		delta = 1
	}
	return s.recordFirst, 1, delta, nil
}

func (s *fakeNoticeStore) ListViewRecords(_ context.Context, query ViewRecordQuery) (NoticeViewRecordPage, error) {
	s.recordQuery = query
	return s.viewRecords, s.fail
}

func noticeServices(store NoticeStore, caps ...authz.Capability) (*NoticeTypeService, *NoticeService) {
	authorizer := dictCapAuthorizer{allowed: capSet(caps)}
	return NewNoticeTypeService(store, authorizer), NewNoticeService(store, authorizer)
}

func validNoticeInput() NoticeInput {
	allVisible, scheduled := true, false
	return NoticeInput{
		Title:                "Holiday arrangement",
		TypeID:               1,
		AllVisibleFlag:       &allVisible,
		ScheduledPublishFlag: &scheduled,
		PublishTime:          "2026-10-22 09:00:00",
		ContentText:          "text",
		ContentHTML:          "<p>text</p>",
		Author:               "HR",
		Source:               "HQ",
	}
}

func publishedNoticeDetail(allVisible bool, ranges []NoticeVisibleRange) NoticeDetail {
	return NoticeDetail{
		Notice: Notice{
			ID: 9, TypeID: 1, Title: "Published",
			AllVisibleFlag: allVisible, PublishFlag: true,
			PublishTime: time.Now().Add(-time.Minute),
		},
		ContentText:      "text",
		ContentHTML:      "<p>text</p>",
		VisibleRangeList: ranges,
	}
}

func TestNoticeTypeService(t *testing.T) {
	ctx := fileCtx(authz.CapabilityOANoticeRead, authz.CapabilityOANoticeManage)

	t.Run("NT-01 mutations require capabilities", func(t *testing.T) {
		typeSvc, _ := noticeServices(&fakeNoticeStore{})
		if err := typeSvc.Add(fileCtx(), "Ops"); err == nil || err.Code != coreerrors.CodePermissionDenied {
			t.Fatalf("add error = %#v", err)
		}
		if err := typeSvc.Update(fileCtx(), 1, "Ops"); err == nil || err.Code != coreerrors.CodePermissionDenied {
			t.Fatalf("update error = %#v", err)
		}
		if err := typeSvc.Delete(fileCtx(), 1); err == nil || err.Code != coreerrors.CodePermissionDenied {
			t.Fatalf("delete error = %#v", err)
		}
		if _, err := typeSvc.ListAll(fileCtx()); err == nil || err.Code != coreerrors.CodePermissionDenied {
			t.Fatalf("list error = %#v", err)
		}
	})

	t.Run("NT-02 validation and missing rows", func(t *testing.T) {
		store := &fakeNoticeStore{}
		typeSvc, _ := noticeServices(store, authz.CapabilityOANoticeRead, authz.CapabilityOANoticeManage)
		if err := typeSvc.Add(ctx, "  "); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("blank add error = %#v", err)
		}
		if err := typeSvc.Add(ctx, repeat("类", 101)); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("long add error = %#v", err)
		}
		if err := typeSvc.Update(ctx, 0, "Ops"); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("zero id error = %#v", err)
		}
		if err := typeSvc.Update(ctx, 99, "Ops"); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("missing update error = %#v", err)
		}
		if err := typeSvc.Delete(ctx, 0); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("zero delete error = %#v", err)
		}
		if err := typeSvc.Delete(ctx, 99); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("missing delete error = %#v", err)
		}
	})

	t.Run("NT-03 delete blocked when in use", func(t *testing.T) {
		store := &fakeNoticeStore{typeInUse: true}
		typeSvc, _ := noticeServices(store, authz.CapabilityOANoticeManage)
		if err := typeSvc.Delete(ctx, 2); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("in-use error = %#v", err)
		}
	})

	t.Run("NT-08 store failures map to unavailable", func(t *testing.T) {
		store := &fakeNoticeStore{fail: errors.New("db down")}
		typeSvc, _ := noticeServices(store, authz.CapabilityOANoticeRead, authz.CapabilityOANoticeManage)
		if _, err := typeSvc.ListAll(ctx); err == nil || err.Code != coreerrors.CodeUnavailable {
			t.Fatalf("list error = %#v", err)
		}
		if err := typeSvc.Add(ctx, "Ops"); err == nil || err.Code != coreerrors.CodeUnavailable {
			t.Fatalf("add error = %#v", err)
		}
	})

	t.Run("happy path round trips", func(t *testing.T) {
		store := &fakeNoticeStore{}
		typeSvc, _ := noticeServices(store, authz.CapabilityOANoticeRead, authz.CapabilityOANoticeManage)
		if err := typeSvc.Add(ctx, " 公告 "); err != nil {
			t.Fatalf("add: %v", err)
		}
		if got := store.types[0].Name; got != "公告" {
			t.Fatalf("trimmed name = %q", got)
		}
		if err := typeSvc.Update(ctx, 1, "Notice"); err != nil {
			t.Fatalf("update: %v", err)
		}
		if err := typeSvc.Delete(ctx, 1); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if store.deletedType != 1 {
			t.Fatalf("deleted type = %d", store.deletedType)
		}
	})
}

func TestNoticeServiceAdmin(t *testing.T) {
	ctx := fileCtx(authz.CapabilityOANoticeRead, authz.CapabilityOANoticeManage)

	t.Run("NT-04/05/06 input validation", func(t *testing.T) {
		_, svc := noticeServices(&fakeNoticeStore{}, authz.CapabilityOANoticeManage)
		valid := validNoticeInput()
		valid.Title = ""
		if err := svc.Add(ctx, valid); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("title error = %#v", err)
		}
		valid = validNoticeInput()
		valid.TypeID = 0
		if err := svc.Add(ctx, valid); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("type error = %#v", err)
		}
		valid = validNoticeInput()
		valid.AllVisibleFlag = nil
		if err := svc.Add(ctx, valid); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("flag error = %#v", err)
		}
		valid = validNoticeInput()
		valid.ContentText = " "
		if err := svc.Add(ctx, valid); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("content error = %#v", err)
		}
		valid = validNoticeInput()
		valid.PublishTime = "2026-10-22"
		if err := svc.Add(ctx, valid); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("time error = %#v", err)
		}
		hidden := false
		valid = validNoticeInput()
		valid.AllVisibleFlag = &hidden
		if err := svc.Add(ctx, valid); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("missing range error = %#v", err)
		}
		valid.VisibleRangeList = []NoticeVisibleRange{{DataType: 9, DataID: 1}}
		if err := svc.Add(ctx, valid); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("bad range type error = %#v", err)
		}
		valid.VisibleRangeList = []NoticeVisibleRange{{DataType: NoticeRangeEmployee, DataID: 0}}
		if err := svc.Add(ctx, valid); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("zero range id error = %#v", err)
		}
	})

	t.Run("add parses publish time and snapshots creator", func(t *testing.T) {
		store := &fakeNoticeStore{}
		_, svc := noticeServices(store, authz.CapabilityOANoticeManage)
		input := validNoticeInput()
		input.CreateUserID = 1001
		input.CreateUserName = "Admin"
		if err := svc.Add(ctx, input); err != nil {
			t.Fatalf("add: %v", err)
		}
		if store.addIn.Title != input.Title || store.publishAt.IsZero() {
			t.Fatalf("stored input = %+v publishAt = %v", store.addIn, store.publishAt)
		}
	})

	t.Run("NT-07 update and delete guards", func(t *testing.T) {
		_, svc := noticeServices(&fakeNoticeStore{}, authz.CapabilityOANoticeManage)
		input := validNoticeInput()
		if err := svc.Update(ctx, input); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("update without id error = %#v", err)
		}
		if err := svc.Delete(ctx, 0); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("delete zero error = %#v", err)
		}
		store := &fakeNoticeStore{getErr: ErrNoticeNotFound}
		_, missingSvc := noticeServices(store, authz.CapabilityOANoticeRead, authz.CapabilityOANoticeManage)
		if _, err := missingSvc.Detail(ctx, 404); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("missing detail error = %#v", err)
		}
	})

	t.Run("detail rejects soft-deleted notices", func(t *testing.T) {
		store := &fakeNoticeStore{detail: NoticeDetail{Notice: Notice{DeletedFlag: true}}}
		_, svc := noticeServices(store, authz.CapabilityOANoticeRead)
		if _, err := svc.Detail(ctx, 9); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("deleted detail error = %#v", err)
		}
	})

	t.Run("NT-12 view records require read and notice id", func(t *testing.T) {
		store := &fakeNoticeStore{}
		_, denySvc := noticeServices(store)
		if _, err := denySvc.ListViewRecords(fileCtx(), ViewRecordQuery{NoticeID: 1}); err == nil || err.Code != coreerrors.CodePermissionDenied {
			t.Fatalf("deny error = %#v", err)
		}
		_, svc := noticeServices(store, authz.CapabilityOANoticeRead)
		if _, err := svc.ListViewRecords(ctx, ViewRecordQuery{TenantID: 1001}); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("zero notice error = %#v", err)
		}
		page, err := svc.ListViewRecords(ctx, ViewRecordQuery{TenantID: 1001, NoticeID: 9, DepartmentID: noticeUintPtr(3), Keywords: "a", PageNum: 1, PageSize: 10})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if page.Total != 0 || store.recordQuery.NoticeID != 9 || store.recordQuery.TenantID != 1001 {
			t.Fatalf("query = %+v", store.recordQuery)
		}
	})
}

func TestNoticeServiceEmployee(t *testing.T) {
	t.Run("NT-09 viewer resolution", func(t *testing.T) {
		store := &fakeNoticeStore{}
		_, svc := noticeServices(store, authz.CapabilityOANoticeRead)
		ctx := fileCtx()
		if _, err := svc.ResolveViewer(ctx, 1001, "user:admin"); err == nil || err.Code != coreerrors.CodeUnauthenticated {
			t.Fatalf("zero viewer error = %#v", err)
		}
		store.viewer = IdentityScope{UserID: 1001, DepartmentID: 8, DisplayName: "Admin"}
		scope, err := svc.ResolveViewer(ctx, 1001, "user:admin")
		if err != nil || scope.UserID != 1001 || scope.DepartmentID != 8 {
			t.Fatalf("scope = %+v err = %v", scope, err)
		}
		if _, err := svc.ListVisible(ctx, IdentityScope{}, EmployeeNoticeQuery{}); err == nil || err.Code != coreerrors.CodeUnauthenticated {
			t.Fatalf("board without viewer error = %#v", err)
		}
		store.fail = errors.New("db down")
		if _, err := svc.ListVisible(ctx, scope, EmployeeNoticeQuery{PageNum: 1, PageSize: 10}); err == nil || err.Code != coreerrors.CodeUnavailable {
			t.Fatalf("board store error = %#v", err)
		}
	})

	t.Run("NT-10 view guards", func(t *testing.T) {
		_, svc := noticeServices(&fakeNoticeStore{}, authz.CapabilityOANoticeRead)
		ctx := fileCtx()
		if _, err := svc.View(ctx, IdentityScope{UserID: 1}, 0, "ip", "ua"); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("zero id error = %#v", err)
		}
		store := &fakeNoticeStore{getErr: ErrNoticeNotFound}
		_, missing := noticeServices(store, authz.CapabilityOANoticeRead)
		if _, err := missing.View(ctx, IdentityScope{UserID: 1}, 404, "ip", "ua"); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("missing error = %#v", err)
		}
		store = &fakeNoticeStore{detail: NoticeDetail{Notice: Notice{ID: 9, PublishFlag: false}}}
		_, unpublished := noticeServices(store, authz.CapabilityOANoticeRead)
		if _, err := unpublished.View(ctx, IdentityScope{UserID: 1}, 9, "ip", "ua"); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("unpublished error = %#v", err)
		}
		store = &fakeNoticeStore{detail: publishedNoticeDetail(false,
			[]NoticeVisibleRange{{DataType: NoticeRangeEmployee, DataID: 2}})}
		_, hidden := noticeServices(store, authz.CapabilityOANoticeRead)
		if _, err := hidden.View(ctx, IdentityScope{UserID: 1, DepartmentID: 7}, 9, "ip", "ua"); err == nil || err.Code != coreerrors.CodePermissionDenied {
			t.Fatalf("hidden error = %#v", err)
		}
	})

	t.Run("NT-11 first view records and reloads", func(t *testing.T) {
		detail := publishedNoticeDetail(true, nil)
		detail.PageViewCount, detail.UserViewCount = 1, 1
		store := &fakeNoticeStore{detail: detail, recordFirst: true}
		_, svc := noticeServices(store, authz.CapabilityOANoticeRead)
		got, err := svc.View(fileCtx(), IdentityScope{UserID: 1001, Admin: true}, 9, "127.0.0.1", "go-test")
		if err != nil {
			t.Fatalf("view: %v", err)
		}
		if store.recordNotice != 9 || store.recordUser != 1001 || store.recordIP != "127.0.0.1" || store.recordUA != "go-test" {
			t.Fatalf("record args = %d/%d %q %q", store.recordNotice, store.recordUser, store.recordIP, store.recordUA)
		}
		if got.PageViewCount != 1 || got.UserViewCount != 1 {
			t.Fatalf("reloaded counters = %d/%d", got.PageViewCount, got.UserViewCount)
		}
	})
}

func noticeUintPtr(value uint64) *uint64 { return &value }

func TestVisibleTo(t *testing.T) {
	ranges := []NoticeVisibleRange{
		{DataType: NoticeRangeEmployee, DataID: 1001},
		{DataType: NoticeRangeDepartment, DataID: 20},
	}
	cases := []struct {
		name   string
		viewer IdentityScope
		want   bool
	}{
		{"NT-13 admin bypass", IdentityScope{UserID: 1, Admin: true}, true},
		{"employee match", IdentityScope{UserID: 1001, DepartmentID: 9}, true},
		{"department match", IdentityScope{UserID: 2, DepartmentID: 20}, true},
		{"no match", IdentityScope{UserID: 3, DepartmentID: 9}, false},
		{"zero department ignored", IdentityScope{UserID: 4, DepartmentID: 0}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := visibleTo(ranges, tc.viewer); got != tc.want {
				t.Fatalf("visibleTo() = %v, want %v", got, tc.want)
			}
		})
	}
}
