package repo

import (
	"context"
	"database/sql"
	"strings"

	"github.com/imajinyun/gofly/core/storage"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
)

var _ app.DictStore = (*DictRepository)(nil)

// DictRepository persists the global data dictionary. Every value in queries
// is a SQL parameter; the dictionary is intentionally not tenant scoped.
type DictRepository struct {
	store *storage.SQLStore
}

func NewDictRepository(store *storage.SQLStore) *DictRepository {
	return &DictRepository{store: store}
}

const dictColumns = `dict_id, dict_code, dict_name, remark, disabled_flag, system_flag, created_at, updated_at`

func scanDict(scanner interface{ Scan(dest ...any) error }) (app.Dict, error) {
	var dict app.Dict
	var disabled, system bool
	if err := scanner.Scan(&dict.ID, &dict.Code, &dict.Name, &dict.Remark, &disabled, &system,
		&dict.CreatedAt, &dict.UpdatedAt); err != nil {
		return app.Dict{}, err
	}
	dict.DisabledFlag = disabled
	dict.SystemFlag = system
	return dict, nil
}

func (r *DictRepository) CreateDict(ctx context.Context, input app.DictInput) (app.Dict, error) {
	result, err := r.store.Exec(ctx,
		`INSERT INTO support_dicts (dict_code, dict_name, remark) VALUES (?, ?, ?)`,
		input.Code, input.Name, input.Remark)
	if err != nil {
		return app.Dict{}, mapDictError(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return app.Dict{}, err
	}
	return r.getDictByID(ctx, uint64(id))
}

func (r *DictRepository) UpdateDict(ctx context.Context, input app.DictInput) error {
	result, err := r.store.Exec(ctx,
		`UPDATE support_dicts SET dict_name = ?, remark = ? WHERE dict_id = ? AND system_flag = 0`,
		input.Name, input.Remark, input.ID)
	if err != nil {
		return mapDictError(err)
	}
	return requireRows(result)
}

func (r *DictRepository) ListDicts(ctx context.Context, query app.DictQuery) (app.DictPage, error) {
	where := make([]string, 0, 2)
	args := make([]any, 0, 2)
	if value := strings.TrimSpace(query.Keywords); value != "" {
		where = append(where, "(dict_code LIKE ? OR dict_name LIKE ? OR remark LIKE ?)")
		like := "%" + value + "%"
		args = append(args, like, like, like)
	}
	if query.DisabledFlag != nil {
		where = append(where, "disabled_flag = ?")
		args = append(args, *query.DisabledFlag)
	}
	filter := "1=1"
	if len(where) > 0 {
		filter = strings.Join(where, " AND ")
	}

	var total int
	countRows, err := r.store.QueryRows(ctx, `SELECT COUNT(*) FROM support_dicts WHERE `+filter, args...)
	if err != nil {
		return app.DictPage{}, err
	}
	if countRows.Next() {
		if err := countRows.Scan(&total); err != nil {
			_ = countRows.Close()
			return app.DictPage{}, err
		}
	}
	_ = countRows.Close()

	pageNum, pageSize := query.PageNum, query.PageSize
	rows, err := r.store.QueryRows(ctx,
		`SELECT `+dictColumns+` FROM support_dicts WHERE `+filter+
			` ORDER BY dict_id DESC LIMIT ? OFFSET ?`,
		append(args, pageSize, (pageNum-1)*pageSize)...)
	if err != nil {
		return app.DictPage{}, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]app.Dict, 0, pageSize)
	for rows.Next() {
		dict, scanErr := scanDict(rows)
		if scanErr != nil {
			return app.DictPage{}, scanErr
		}
		items = append(items, dict)
	}
	return app.DictPage{List: items, Total: total}, rows.Err()
}

func (r *DictRepository) ListAllDicts(ctx context.Context, _ bool) ([]app.Dict, error) {
	rows, err := r.store.QueryRows(ctx,
		`SELECT `+dictColumns+` FROM support_dicts ORDER BY dict_id ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]app.Dict, 0)
	for rows.Next() {
		dict, scanErr := scanDict(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, dict)
	}
	return items, rows.Err()
}

func (r *DictRepository) ToggleDict(ctx context.Context, id uint64) error {
	result, err := r.store.Exec(ctx,
		`UPDATE support_dicts SET disabled_flag = 1 - disabled_flag WHERE dict_id = ?`, id)
	if err != nil {
		return err
	}
	return requireRows(result)
}

func (r *DictRepository) DeleteDicts(ctx context.Context, ids []uint64) error {
	if err := r.deleteValuesForDicts(ctx, ids); err != nil {
		return err
	}
	placeholders := placeholdersFor(ids)
	result, err := r.store.Exec(ctx,
		`DELETE FROM support_dicts WHERE dict_id IN (`+placeholders+`) AND system_flag = 0`, toAnyArgs(ids)...)
	if err != nil {
		return mapDictError(err)
	}
	return requireRows(result)
}

func (r *DictRepository) getDictByID(ctx context.Context, id uint64) (app.Dict, error) {
	rows, err := r.store.QueryRows(ctx,
		`SELECT `+dictColumns+` FROM support_dicts WHERE dict_id = ?`, id)
	if err != nil {
		return app.Dict{}, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return app.Dict{}, sql.ErrNoRows
	}
	dict, err := scanDict(rows)
	return dict, err
}

func (r *DictRepository) deleteValuesForDicts(ctx context.Context, dictIDs []uint64) error {
	placeholders := placeholdersFor(dictIDs)
	_, err := r.store.Exec(ctx,
		`DELETE FROM support_dict_data WHERE dict_id IN (`+placeholders+`)`, toAnyArgs(dictIDs)...)
	return err
}
