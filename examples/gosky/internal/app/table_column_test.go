package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	coreerrors "github.com/imajinyun/gofly/core/errors"
)

// Failure-mode inventory:
//
//	TC-01 update validates subject/table/columns/keys/sorts
//	TC-02 get returns found=false for a missing preference
//	TC-03 update then get round-trips the JSON list
//	TC-04 delete removes the preference without requiring existence
//	TC-05 subject isolation: one subject cannot read another's layout
type fakeTableColumnStore struct {
	rows map[string]string
	fail error
}

func newFakeTableColumnStore() *fakeTableColumnStore {
	return &fakeTableColumnStore{rows: make(map[string]string)}
}

func tableColumnKey(subject string, tableID uint64) string {
	return subject + "#" + string(rune('0'+tableID%10))
}

func (s *fakeTableColumnStore) Save(_ context.Context, subject string, tableID uint64, configJSON string) error {
	if s.fail != nil {
		return s.fail
	}
	s.rows[tableColumnKey(subject, tableID)] = configJSON
	return nil
}

func (s *fakeTableColumnStore) Get(_ context.Context, subject string, tableID uint64) (string, bool, error) {
	if s.fail != nil {
		return "", false, s.fail
	}
	value, ok := s.rows[tableColumnKey(subject, tableID)]
	return value, ok, nil
}

func (s *fakeTableColumnStore) Delete(_ context.Context, subject string, tableID uint64) error {
	if s.fail != nil {
		return s.fail
	}
	delete(s.rows, tableColumnKey(subject, tableID))
	return nil
}

func TestTableColumnUpdateValidation(t *testing.T) {
	service := NewTableColumnService(newFakeTableColumnStore())
	validColumns := []TableColumn{{ColumnKey: "name", Sort: 1, ShowFlag: true}, {ColumnKey: "age", Sort: 2, Width: 120}}
	for _, tc := range []struct {
		name    string
		subject string
		update  TableColumnUpdate
	}{
		{name: "zero table", subject: "user:1", update: TableColumnUpdate{TableID: 0, ColumnList: validColumns}},
		{name: "table too large", subject: "user:1", update: TableColumnUpdate{TableID: maxTableID + 1, ColumnList: validColumns}},
		{name: "no columns", subject: "user:1", update: TableColumnUpdate{TableID: 1}},
		{name: "empty key", subject: "user:1", update: TableColumnUpdate{TableID: 1, ColumnList: []TableColumn{{Sort: 1}}}},
		{name: "bad sort", subject: "user:1", update: TableColumnUpdate{TableID: 1, ColumnList: []TableColumn{{ColumnKey: "k", Sort: 0}}}},
		{name: "bad width", subject: "user:1", update: TableColumnUpdate{TableID: 1, ColumnList: []TableColumn{{ColumnKey: "k", Sort: 1, Width: -1}}}},
		{name: "zero table", subject: "user:1", update: TableColumnUpdate{TableID: 0, ColumnList: validColumns}},
		{name: "table too large", subject: "user:1", update: TableColumnUpdate{TableID: maxTableID + 1, ColumnList: validColumns}},
		{name: "no columns", subject: "user:1", update: TableColumnUpdate{TableID: 1}},
		{name: "empty key", subject: "user:1", update: TableColumnUpdate{TableID: 1, ColumnList: []TableColumn{{Sort: 1}}}},
		{name: "bad sort", subject: "user:1", update: TableColumnUpdate{TableID: 1, ColumnList: []TableColumn{{ColumnKey: "k", Sort: 0}}}},
		{name: "bad width", subject: "user:1", update: TableColumnUpdate{TableID: 1, ColumnList: []TableColumn{{ColumnKey: "k", Sort: 1, Width: -1}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := service.Update(context.Background(), tc.subject, tc.update); err == nil || err.Code != coreerrors.CodeInvalidArgument {
				t.Fatalf("err=%v, want invalid_argument", err)
			}
		})
	}
}

func TestTableColumnRequiresSubject(t *testing.T) {
	service := NewTableColumnService(newFakeTableColumnStore())
	update := TableColumnUpdate{TableID: 1, ColumnList: []TableColumn{{ColumnKey: "k", Sort: 1}}}
	if err := service.Update(context.Background(), "  ", update); err == nil || err.Code != coreerrors.CodeUnauthenticated {
		t.Fatalf("update err=%v", err)
	}
	if _, _, err := service.GetColumns(context.Background(), "", 1); err == nil || err.Code != coreerrors.CodeUnauthenticated {
		t.Fatalf("get err=%v", err)
	}
	if err := service.Delete(context.Background(), "", 1); err == nil || err.Code != coreerrors.CodeUnauthenticated {
		t.Fatalf("delete err=%v", err)
	}
}

func TestTableColumnRoundTrip(t *testing.T) {
	store := newFakeTableColumnStore()
	service := NewTableColumnService(store)
	ctx := context.Background()
	columns := []TableColumn{{ColumnKey: "name", Sort: 1, ShowFlag: true}, {ColumnKey: "remark", Sort: 2, Width: 200, ShowFlag: false}}
	if err := service.Update(ctx, "user:1001", TableColumnUpdate{TableID: 42, ColumnList: columns}); err != nil {
		t.Fatalf("update: %v", err)
	}
	config, found, err := service.GetColumns(ctx, "user:1001", 42)
	if err != nil || !found {
		t.Fatalf("get: found=%v err=%v", found, err)
	}
	var decoded []TableColumn
	if err := json.Unmarshal([]byte(config), &decoded); err != nil {
		t.Fatalf("stored config is not JSON: %v", err)
	}
	if len(decoded) != 2 || decoded[1].Width != 200 || decoded[1].ShowFlag {
		t.Fatalf("decoded=%+v", decoded)
	}
	// Other subject gets no preference.
	if _, found, _ := service.GetColumns(ctx, "user:2002", 42); found {
		t.Fatal("subject isolation broken")
	}
	if err := service.Delete(ctx, "user:1001", 42); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, found, _ := service.GetColumns(ctx, "user:1001", 42); found {
		t.Fatal("preference still present after delete")
	}
}

func TestTableColumnMissingGet(t *testing.T) {
	service := NewTableColumnService(newFakeTableColumnStore())
	_, found, err := service.GetColumns(context.Background(), "user:1", 99)
	if err != nil || found {
		t.Fatalf("missing preference found=%v err=%v", found, err)
	}
}

func TestTableColumnStorageFailure(t *testing.T) {
	service := NewTableColumnService(&fakeTableColumnStore{rows: map[string]string{}, fail: errors.New("db down")})
	err := service.Update(context.Background(), "user:1", TableColumnUpdate{
		TableID:    1,
		ColumnList: []TableColumn{{ColumnKey: "k", Sort: 1}},
	})
	if err == nil || err.Code != coreerrors.CodeUnavailable {
		t.Fatalf("err=%v, want unavailable", err)
	}
}
