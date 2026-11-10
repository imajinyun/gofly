package app

import (
	"context"
	"encoding/json"
	"strings"

	coreerrors "github.com/imajinyun/gofly/core/errors"
)

// TableColumn is one persisted per-user column preference.
type TableColumn struct {
	ColumnKey string `json:"columnKey"`
	Sort      int    `json:"sort"`
	Width     int    `json:"width,omitempty"`
	ShowFlag  bool   `json:"showFlag"`
}

// TableColumnUpdate is the unchanged client payload for
// /support/tableColumn/update.
type TableColumnUpdate struct {
	TableID    uint64        `json:"tableId"`
	ColumnList []TableColumn `json:"columnList"`
}

// TableColumnStore persists the serialized preferences per subject.
type TableColumnStore interface {
	Save(ctx context.Context, subject string, tableID uint64, configJSON string) error
	Get(ctx context.Context, subject string, tableID uint64) (string, bool, error)
	Delete(ctx context.Context, subject string, tableID uint64) error
}

// TableColumnService validates and stores per-user table layouts.
type TableColumnService struct {
	store TableColumnStore
}

func NewTableColumnService(store TableColumnStore) *TableColumnService {
	return &TableColumnService{store: store}
}

const (
	maxTableID           uint64 = 1_000_000
	maxTableColumns             = 200
	maxColumnKeyLength          = 100
	maxColumnConfigBytes        = 64 * 1024
)

func (s *TableColumnService) Update(ctx context.Context, subject string, update TableColumnUpdate) *coreerrors.Error {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return coreerrors.New(coreerrors.CodeUnauthenticated, "user is required")
	}
	if update.TableID == 0 || update.TableID > maxTableID {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "table id is invalid")
	}
	if len(update.ColumnList) == 0 || len(update.ColumnList) > maxTableColumns {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "column list is invalid")
	}
	for index, column := range update.ColumnList {
		column.ColumnKey = strings.TrimSpace(column.ColumnKey)
		if column.ColumnKey == "" || len(column.ColumnKey) > maxColumnKeyLength {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "column key is invalid")
		}
		if column.Sort < 1 || column.Sort > maxTableColumns {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "column sort is invalid")
		}
		if column.Width < 0 || column.Width > 2000 {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "column width is invalid")
		}
		update.ColumnList[index] = column
	}
	encoded, err := json.Marshal(update.ColumnList)
	if err != nil {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "column list cannot be serialized")
	}
	if len(encoded) > maxColumnConfigBytes {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "column config is too large")
	}
	if err := s.store.Save(ctx, subject, update.TableID, string(encoded)); err != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "save table columns", err)
	}
	return nil
}

// GetColumns returns the stored layout for the subject/table pair. A missing
// configuration is an empty string with found=false (the client then falls
// back to its built-in defaults).
func (s *TableColumnService) GetColumns(ctx context.Context, subject string, tableID uint64) (string, bool, *coreerrors.Error) {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "", false, coreerrors.New(coreerrors.CodeUnauthenticated, "user is required")
	}
	if tableID == 0 || tableID > maxTableID {
		return "", false, coreerrors.New(coreerrors.CodeInvalidArgument, "table id is invalid")
	}
	config, found, err := s.store.Get(ctx, subject, tableID)
	if err != nil {
		return "", false, coreerrors.Wrap(coreerrors.CodeUnavailable, "read table columns", err)
	}
	return config, found, nil
}

// Delete removes one subject/table preference ("restore default").
func (s *TableColumnService) Delete(ctx context.Context, subject string, tableID uint64) *coreerrors.Error {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return coreerrors.New(coreerrors.CodeUnauthenticated, "user is required")
	}
	if tableID == 0 || tableID > maxTableID {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "table id is invalid")
	}
	if err := s.store.Delete(ctx, subject, tableID); err != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "delete table columns", err)
	}
	return nil
}
