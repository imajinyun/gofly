package repo

import (
	"context"
	"strings"

	"github.com/imajinyun/gofly/core/storage"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
)

var _ app.ReloadStore = (*ReloadRepository)(nil)

// ReloadRepository persists SmartReload heartbeat items and results.
type ReloadRepository struct {
	store *storage.SQLStore
}

// NewReloadRepository constructs the reload repository.
func NewReloadRepository(store *storage.SQLStore) *ReloadRepository {
	return &ReloadRepository{store: store}
}

// ListItems returns all reload tags ordered for stable polling.
func (r *ReloadRepository) ListItems(ctx context.Context) ([]app.ReloadItem, error) {
	rows, err := r.store.QueryRows(ctx,
		`SELECT tag, COALESCE(args, ''), identification, created_at, updated_at
FROM support_reload_items ORDER BY tag ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]app.ReloadItem, 0)
	for rows.Next() {
		var item app.ReloadItem
		if err := rows.Scan(&item.Tag, &item.Args, &item.Identification,
			&item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// UpdateItem updates identification/args by tag; updated=false means the tag
// does not exist.
func (r *ReloadRepository) UpdateItem(ctx context.Context, form app.ReloadForm) (bool, error) {
	result, err := r.store.Exec(ctx,
		`UPDATE support_reload_items SET identification = ?, args = ? WHERE tag = ?`,
		form.Identification, form.Args, form.Tag)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// InsertResult records one execution outcome.
func (r *ReloadRepository) InsertResult(ctx context.Context, result app.ReloadResult) error {
	_, err := r.store.Exec(ctx,
		`INSERT INTO support_reload_results
(tag, identification, args, result_flag, exception)
VALUES (?, ?, ?, ?, ?)`,
		result.Tag, result.Identification, result.Args,
		boolToTinyInt(result.Result), result.Exception)
	return err
}

// ListResults returns recent execution results for one tag, newest first.
func (r *ReloadRepository) ListResults(ctx context.Context, tag string, limit int) ([]app.ReloadResult, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.store.QueryRows(ctx,
		`SELECT result_id, tag, identification, COALESCE(args, ''), result_flag,
COALESCE(exception, ''), created_at
FROM support_reload_results WHERE tag = ? ORDER BY created_at DESC, result_id DESC LIMIT ?`,
		strings.TrimSpace(tag), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	results := make([]app.ReloadResult, 0, limit)
	for rows.Next() {
		var result app.ReloadResult
		var flag int
		if err := rows.Scan(&result.ResultID, &result.Tag, &result.Identification, &result.Args,
			&flag, &result.Exception, &result.CreatedAt); err != nil {
			return nil, err
		}
		result.Result = flag == 1
		results = append(results, result)
	}
	return results, rows.Err()
}
