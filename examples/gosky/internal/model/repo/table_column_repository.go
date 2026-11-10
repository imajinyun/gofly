package repo

import (
	"context"
	"database/sql"
	"errors"

	"github.com/imajinyun/gofly/core/storage"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
)

var _ app.TableColumnStore = (*TableColumnRepository)(nil)

// TableColumnRepository persists per-subject table layouts. The subject is
// a canonical users.subject value, never a browser-supplied user id.
type TableColumnRepository struct {
	store *storage.SQLStore
}

func NewTableColumnRepository(store *storage.SQLStore) *TableColumnRepository {
	return &TableColumnRepository{store: store}
}

func (r *TableColumnRepository) Save(ctx context.Context, subject string, tableID uint64, configJSON string) error {
	_, err := r.store.Exec(ctx,
		`INSERT INTO support_table_columns (subject, table_id, column_config)
VALUES (?, ?, ?)
ON DUPLICATE KEY UPDATE column_config = VALUES(column_config), updated_at = CURRENT_TIMESTAMP(6)`,
		subject, tableID, configJSON)
	return err
}

func (r *TableColumnRepository) Get(ctx context.Context, subject string, tableID uint64) (string, bool, error) {
	rows, err := r.store.QueryRows(ctx,
		`SELECT column_config FROM support_table_columns WHERE subject = ? AND table_id = ?`,
		subject, tableID)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return "", false, nil
	}
	var config string
	if err := rows.Scan(&config); err != nil {
		return "", false, err
	}
	return config, true, nil
}

func (r *TableColumnRepository) Delete(ctx context.Context, subject string, tableID uint64) error {
	result, err := r.store.Exec(ctx,
		`DELETE FROM support_table_columns WHERE subject = ? AND table_id = ?`,
		subject, tableID)
	if err != nil {
		return err
	}
	if _, err := result.RowsAffected(); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}
