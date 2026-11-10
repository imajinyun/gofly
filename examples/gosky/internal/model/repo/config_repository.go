package repo

import (
	"context"
	"database/sql"
	"strings"

	"github.com/imajinyun/gofly/core/storage"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
)

var _ app.ConfigParamStore = (*ConfigRepository)(nil)

// ConfigRepository persists global runtime parameters. Every value in
// queries is a SQL parameter; the parameter table is intentionally not
// tenant scoped.
type ConfigRepository struct {
	store *storage.SQLStore
}

func NewConfigRepository(store *storage.SQLStore) *ConfigRepository {
	return &ConfigRepository{store: store}
}

const configColumns = `config_id, config_key, config_name, config_value, remark, system_flag, created_at, updated_at`

func scanConfig(scanner interface{ Scan(dest ...any) error }) (app.ConfigParam, error) {
	var param app.ConfigParam
	var system bool
	if err := scanner.Scan(&param.ID, &param.Key, &param.Name, &param.Value, &param.Remark, &system,
		&param.CreatedAt, &param.UpdatedAt); err != nil {
		return app.ConfigParam{}, err
	}
	param.System = system
	return param, nil
}

func (r *ConfigRepository) Create(ctx context.Context, input app.ConfigParamInput) (app.ConfigParam, error) {
	result, err := r.store.Exec(ctx,
		`INSERT INTO support_configs (config_key, config_name, config_value, remark) VALUES (?, ?, ?, ?)`,
		input.Key, input.Name, input.Value, input.Remark)
	if err != nil {
		return app.ConfigParam{}, mapConfigError(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return app.ConfigParam{}, err
	}
	return r.getByID(ctx, uint64(id))
}

func (r *ConfigRepository) Update(ctx context.Context, input app.ConfigParamInput) error {
	// System parameters keep their key; value/name/remark stay editable
	// through the console unless the deployment marks them immutable.
	result, err := r.store.Exec(ctx,
		`UPDATE support_configs SET config_name = ?, config_value = ?, remark = ?, config_key = config_key
WHERE config_id = ?`,
		input.Name, input.Value, input.Remark, input.ID)
	if err != nil {
		return mapConfigError(err)
	}
	return requireRows(result)
}

func (r *ConfigRepository) List(ctx context.Context, query app.ConfigParamQuery) (app.ConfigParamPage, error) {
	where := "1=1"
	args := make([]any, 0, 1)
	if value := strings.TrimSpace(query.Key); value != "" {
		where = "config_key LIKE ?"
		args = append(args, "%"+value+"%")
	}

	var total int
	countRows, err := r.store.QueryRows(ctx, `SELECT COUNT(*) FROM support_configs WHERE `+where, args...)
	if err != nil {
		return app.ConfigParamPage{}, err
	}
	if countRows.Next() {
		if err := countRows.Scan(&total); err != nil {
			_ = countRows.Close()
			return app.ConfigParamPage{}, err
		}
	}
	_ = countRows.Close()

	pageNum, pageSize := query.PageNum, query.PageSize
	rows, err := r.store.QueryRows(ctx,
		`SELECT `+configColumns+` FROM support_configs WHERE `+where+
			` ORDER BY config_id DESC LIMIT ? OFFSET ?`,
		append(args, pageSize, (pageNum-1)*pageSize)...)
	if err != nil {
		return app.ConfigParamPage{}, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]app.ConfigParam, 0, pageSize)
	for rows.Next() {
		param, scanErr := scanConfig(rows)
		if scanErr != nil {
			return app.ConfigParamPage{}, scanErr
		}
		items = append(items, param)
	}
	return app.ConfigParamPage{List: items, Total: total}, rows.Err()
}

func (r *ConfigRepository) GetByKey(ctx context.Context, key string) (app.ConfigParam, error) {
	rows, err := r.store.QueryRows(ctx,
		`SELECT `+configColumns+` FROM support_configs WHERE config_key = ?`, key)
	if err != nil {
		return app.ConfigParam{}, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return app.ConfigParam{}, sql.ErrNoRows
	}
	return scanConfig(rows)
}

func (r *ConfigRepository) getByID(ctx context.Context, id uint64) (app.ConfigParam, error) {
	rows, err := r.store.QueryRows(ctx,
		`SELECT `+configColumns+` FROM support_configs WHERE config_id = ?`, id)
	if err != nil {
		return app.ConfigParam{}, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return app.ConfigParam{}, sql.ErrNoRows
	}
	return scanConfig(rows)
}

func mapConfigError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "duplicate entry") {
		return errDuplicateConfigKey
	}
	return err
}
