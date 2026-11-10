package repo

import (
	"context"
	"database/sql"

	"github.com/imajinyun/gofly/core/storage"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
)

const dictDataColumns = `dd.dict_data_id, dd.dict_id, d.dict_code, d.dict_name, d.disabled_flag,
       dd.data_value, dd.data_label, dd.data_style, dd.remark, dd.sort_order,
       dd.disabled_flag, dd.created_at, dd.updated_at`

func scanDictData(scanner interface{ Scan(dest ...any) error }) (app.DictData, error) {
	var data app.DictData
	var dictDisabled, disabled bool
	if err := scanner.Scan(
		&data.ID, &data.DictID, &data.DictCode, &data.DictName, &dictDisabled,
		&data.Value, &data.Label, &data.Style, &data.Remark, &data.SortOrder,
		&disabled, &data.CreatedAt, &data.UpdatedAt,
	); err != nil {
		return app.DictData{}, err
	}
	data.DictDisabledFlag = dictDisabled
	data.DisabledFlag = disabled
	return data, nil
}

// dictDataRepository shares the DictRepository store through a separate type
// to keep value-specific SQL isolated.
type dictDataRepository struct {
	store *storage.SQLStore
}

func newDictDataRepository(store *storage.SQLStore) *dictDataRepository {
	return &dictDataRepository{store: store}
}

func (r *DictRepository) dataStore() *dictDataRepository {
	return &dictDataRepository{store: r.store}
}

func (r *dictDataRepository) create(ctx context.Context, input app.DictDataInput) (app.DictData, error) {
	result, err := r.store.Exec(ctx,
		`INSERT INTO support_dict_data (dict_id, data_value, data_label, data_style, remark, sort_order)
SELECT ?, ?, ?, ?, ?, ?
FROM support_dicts WHERE dict_id = ? AND system_flag = 0`,
		input.DictID, input.Value, input.Label, input.Style, input.Remark, input.SortOrder, input.DictID)
	if err != nil {
		return app.DictData{}, mapDictError(err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return app.DictData{}, err
	}
	if rowsAffected == 0 {
		return app.DictData{}, app.ErrDictNotFound
	}
	id, err := result.LastInsertId()
	if err != nil {
		return app.DictData{}, err
	}
	return r.getByID(ctx, uint64(id))
}

func (r *dictDataRepository) update(ctx context.Context, input app.DictDataInput) error {
	result, err := r.store.Exec(ctx,
		`UPDATE support_dict_data SET data_value = ?, data_label = ?, data_style = ?, remark = ?, sort_order = ?
WHERE dict_data_id = ? AND dict_id = ?`,
		input.Value, input.Label, input.Style, input.Remark, input.SortOrder, input.ID, input.DictID)
	if err != nil {
		return mapDictError(err)
	}
	return requireRows(result)
}

func (r *dictDataRepository) listByDictID(ctx context.Context, dictID uint64) ([]app.DictData, error) {
	rows, err := r.store.QueryRows(ctx,
		`SELECT `+dictDataColumns+`
FROM support_dict_data AS dd
JOIN support_dicts AS d ON d.dict_id = dd.dict_id
WHERE dd.dict_id = ? ORDER BY dd.sort_order DESC, dd.dict_data_id ASC`, dictID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return collectDictData(rows)
}

func (r *dictDataRepository) listAllActive(ctx context.Context) ([]app.DictData, error) {
	sql := `SELECT ` + dictDataColumns + `
FROM support_dict_data AS dd
JOIN support_dicts AS d ON d.dict_id = dd.dict_id
WHERE dd.disabled_flag = 0 AND d.disabled_flag = 0
ORDER BY d.dict_code ASC, dd.sort_order DESC, dd.dict_data_id ASC`
	rows, err := r.store.QueryRows(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return collectDictData(rows)
}

func (r *dictDataRepository) toggle(ctx context.Context, id uint64) error {
	result, err := r.store.Exec(ctx,
		`UPDATE support_dict_data SET disabled_flag = 1 - disabled_flag WHERE dict_data_id = ?`, id)
	if err != nil {
		return err
	}
	return requireRows(result)
}

func (r *dictDataRepository) delete(ctx context.Context, ids []uint64) error {
	result, err := r.store.Exec(ctx,
		`DELETE FROM support_dict_data WHERE dict_data_id IN (`+placeholdersFor(ids)+`)`, toAnyArgs(ids)...)
	if err != nil {
		return mapDictError(err)
	}
	return requireRows(result)
}

func (r *dictDataRepository) getByID(ctx context.Context, id uint64) (app.DictData, error) {
	rows, err := r.store.QueryRows(ctx,
		`SELECT `+dictDataColumns+`
FROM support_dict_data AS dd
JOIN support_dicts AS d ON d.dict_id = dd.dict_id
WHERE dd.dict_data_id = ?`, id)
	if err != nil {
		return app.DictData{}, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return app.DictData{}, sql.ErrNoRows
	}
	return scanDictData(rows)
}

func collectDictData(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close() error
}) ([]app.DictData, error) {
	items := make([]app.DictData, 0)
	for rows.Next() {
		data, err := scanDictData(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, data)
	}
	return items, rows.Err()
}

// app-facing methods on DictRepository delegate to the value store.
func (r *DictRepository) CreateDictData(ctx context.Context, input app.DictDataInput) (app.DictData, error) {
	return r.dataStore().create(ctx, input)
}

func (r *DictRepository) UpdateDictData(ctx context.Context, input app.DictDataInput) error {
	return r.dataStore().update(ctx, input)
}

func (r *DictRepository) ListDictDataByDictID(ctx context.Context, dictID uint64) ([]app.DictData, error) {
	return r.dataStore().listByDictID(ctx, dictID)
}

func (r *DictRepository) ListAllActiveDictData(ctx context.Context) ([]app.DictData, error) {
	return r.dataStore().listAllActive(ctx)
}

func (r *DictRepository) ToggleDictData(ctx context.Context, id uint64) error {
	return r.dataStore().toggle(ctx, id)
}

func (r *DictRepository) DeleteDictData(ctx context.Context, ids []uint64) error {
	return r.dataStore().delete(ctx, ids)
}
