package repo

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"

	"github.com/imajinyun/gofly/core/storage"
	entity "github.com/imajinyun/gofly/examples/gosky/internal/model/entity"
)

type TenantRepo struct {
	store   *storage.SQLStore
	cluster *storage.Cluster
	tx      *sql.Tx
	dialect storage.Dialect
}

func NewTenantRepo(store *storage.SQLStore, dialect ...storage.Dialect) *TenantRepo {
	d := storage.DialectQuestion
	if len(dialect) > 0 {
		d = dialect[0]
	}
	return &TenantRepo{store: store, dialect: d}
}

func NewTenantRepoWithCluster(cluster *storage.Cluster, dialect ...storage.Dialect) *TenantRepo {
	d := storage.DialectQuestion
	if len(dialect) > 0 {
		d = dialect[0]
	}
	var store *storage.SQLStore
	if cluster != nil {
		store = cluster.Writer()
	}
	return &TenantRepo{store: store, cluster: cluster, dialect: d}
}

func (r *TenantRepo) WithTx(tx *sql.Tx) *TenantRepo {
	if r == nil {
		return nil
	}
	clone := *r
	clone.tx = tx
	return &clone
}

func (r *TenantRepo) Transact(ctx context.Context, opts *sql.TxOptions, fn func(context.Context, *TenantRepo) error) error {
	if r == nil {
		return errors.New("tenant repo is nil")
	}
	if fn == nil {
		return errors.New("transaction function is required")
	}
	if r.cluster != nil {
		return r.cluster.Transact(ctx, opts, func(ctx context.Context, tx *sql.Tx) error {
			return fn(ctx, r.WithTx(tx))
		})
	}
	if r.store == nil {
		return errors.New("tenant repo store is nil")
	}
	return r.store.Transact(ctx, opts, func(ctx context.Context, tx *sql.Tx) error {
		return fn(ctx, r.WithTx(tx))
	})
}

func (r *TenantRepo) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if r == nil {
		return nil, errors.New("tenant repo is nil")
	}
	if r.tx != nil {
		return r.tx.ExecContext(ctx, query, args...)
	}
	if r.cluster != nil {
		store := r.cluster.Writer()
		if store == nil {
			return nil, errors.New("tenant repo cluster writer is nil")
		}
		return store.Exec(ctx, query, args...)
	}
	if r.store == nil {
		return nil, errors.New("tenant repo store is nil")
	}
	return r.store.Exec(ctx, query, args...)
}

func (r *TenantRepo) queryOne(ctx context.Context, query string, scan func(*sql.Row) error, args ...any) error {
	if r == nil {
		return errors.New("tenant repo is nil")
	}
	if scan == nil {
		return errors.New("scan function is required")
	}
	if r.tx != nil {
		return scan(r.tx.QueryRowContext(ctx, query, args...))
	}
	if r.cluster != nil {
		store := r.cluster.ForQuery(query)
		if store == nil {
			return errors.New("tenant repo cluster reader is nil")
		}
		return store.QueryOne(ctx, query, scan, args...)
	}
	if r.store == nil {
		return errors.New("tenant repo store is nil")
	}
	return r.store.QueryOne(ctx, query, scan, args...)
}

func (r *TenantRepo) queryAll(ctx context.Context, query string, scan func(*sql.Rows) error, args ...any) error {
	if r == nil {
		return errors.New("tenant repo is nil")
	}
	if scan == nil {
		return errors.New("scan function is required")
	}
	if r.tx != nil {
		rows, err := r.tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		if err := scan(rows); err != nil {
			return err
		}
		return rows.Err()
	}
	if r.cluster != nil {
		store := r.cluster.ForQuery(query)
		if store == nil {
			return errors.New("tenant repo cluster reader is nil")
		}
		return store.QueryAll(ctx, query, scan, args...)
	}
	if r.store == nil {
		return errors.New("tenant repo store is nil")
	}
	return r.store.QueryAll(ctx, query, scan, args...)
}

func (r *TenantRepo) TableName() string { return entity.TenantTable }

func (r *TenantRepo) Columns() []string { return append([]string(nil), entity.TenantColumns...) }

func (r *TenantRepo) FindOne(ctx context.Context, tenantID int64) (*entity.Tenant, error) {
	query, err := storage.SelectByID(entity.TenantTable, entity.TenantColumns, "tenant_id", r.dialect)
	if err != nil {
		return nil, err
	}
	var out entity.Tenant
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.TenantID, &out.TenantCode, &out.TenantName, &out.TenantState, &out.CreatedAt, &out.UpdatedAt)
	}, tenantID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *TenantRepo) InsertResult(ctx context.Context, in *entity.Tenant) (sql.Result, error) {
	if in == nil {
		return nil, errors.New("tenant is nil")
	}
	query, err := storage.Insert(entity.TenantTable, []string{"tenant_code", "tenant_name", "tenant_state"}, r.dialect)
	if err != nil {
		return nil, err
	}
	return r.exec(ctx, query, in.TenantCode, in.TenantName, in.TenantState)
}

func (r *TenantRepo) Insert(ctx context.Context, in *entity.Tenant) error {
	_, err := r.InsertResult(ctx, in)
	return err
}

func (r *TenantRepo) Update(ctx context.Context, in *entity.Tenant) error {
	if in == nil {
		return errors.New("tenant is nil")
	}
	query, err := storage.UpdateByID(entity.TenantTable, []string{"tenant_code", "tenant_name", "tenant_state"}, "tenant_id", r.dialect)
	if err != nil {
		return err
	}
	if _, err := r.exec(ctx, query, in.TenantCode, in.TenantName, in.TenantState, in.TenantID); err != nil {
		return err
	}
	return nil
}

func (r *TenantRepo) Delete(ctx context.Context, tenantID int64) error {
	query, err := storage.DeleteByID(entity.TenantTable, "tenant_id", r.dialect)
	if err != nil {
		return err
	}
	if _, err := r.exec(ctx, query, tenantID); err != nil {
		return err
	}
	return nil
}

func (r *TenantRepo) List(ctx context.Context, limit int, offset int) ([]entity.Tenant, error) {
	query, err := storage.SelectPage(entity.TenantTable, entity.TenantColumns, "tenant_id", r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.Tenant, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Tenant
			if err := rows.Scan(&item.TenantID, &item.TenantCode, &item.TenantName, &item.TenantState, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			out = append(out, item)
		}
		return nil
	}, limit, offset); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *TenantRepo) Count(ctx context.Context) (int64, error) {
	query, err := storage.CountAll(entity.TenantTable)
	if err != nil {
		return 0, err
	}
	var count int64
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&count)
	}); err != nil {
		return 0, err
	}
	return count, nil
}

func (r *TenantRepo) FindWhere(ctx context.Context, where *storage.Where) ([]entity.Tenant, error) {
	query, args, err := storage.SelectWhere(entity.TenantTable, entity.TenantColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.Tenant, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Tenant
			if err := rows.Scan(&item.TenantID, &item.TenantCode, &item.TenantName, &item.TenantState, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			out = append(out, item)
		}
		return nil
	}, args...); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *TenantRepo) CountWhere(ctx context.Context, where *storage.Where) (int64, error) {
	query, args, err := storage.CountWhere(entity.TenantTable, where, r.dialect)
	if err != nil {
		return 0, err
	}
	var count int64
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&count)
	}, args...); err != nil {
		return 0, err
	}
	return count, nil
}

func (r *TenantRepo) FindByTenantCode(ctx context.Context, tenantCode string) (*entity.Tenant, error) {
	where := storage.NewWhere().Eq("tenant_code", tenantCode).Limit(1)
	query, args, err := storage.SelectWhere(entity.TenantTable, entity.TenantColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	var out entity.Tenant
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.TenantID, &out.TenantCode, &out.TenantName, &out.TenantState, &out.CreatedAt, &out.UpdatedAt)
	}, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *TenantRepo) FindByTenantState(ctx context.Context, tenantState string, limit int, offset int) ([]entity.Tenant, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_state", tenantState)
	where = where.OrderBy("tenant_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.TenantTable, entity.TenantColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.Tenant, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Tenant
			if err := rows.Scan(&item.TenantID, &item.TenantCode, &item.TenantName, &item.TenantState, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			out = append(out, item)
		}
		return nil
	}, args...); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *TenantRepo) FindByTenantStateForUpdate(ctx context.Context, tenantState string, limit int, offset int) ([]entity.Tenant, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_state", tenantState)
	where = where.OrderBy("tenant_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.TenantTable, entity.TenantColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	out := make([]entity.Tenant, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Tenant
			if err := rows.Scan(&item.TenantID, &item.TenantCode, &item.TenantName, &item.TenantState, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			out = append(out, item)
		}
		return nil
	}, args...); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *TenantRepo) FindByTenantStateForUpdateSkipLocked(ctx context.Context, tenantState string, limit int, offset int) ([]entity.Tenant, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_state", tenantState)
	where = where.OrderBy("tenant_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.TenantTable, entity.TenantColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	out := make([]entity.Tenant, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Tenant
			if err := rows.Scan(&item.TenantID, &item.TenantCode, &item.TenantName, &item.TenantState, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			out = append(out, item)
		}
		return nil
	}, args...); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *TenantRepo) CountByTenantState(ctx context.Context, tenantState string) (int64, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_state", tenantState)
	query, args, err := storage.CountWhere(entity.TenantTable, where, r.dialect)
	if err != nil {
		return 0, err
	}
	var count int64
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&count)
	}, args...); err != nil {
		return 0, err
	}
	return count, nil
}

func (r *TenantRepo) FindByIDs(ctx context.Context, ids []int64) ([]entity.Tenant, error) {
	if len(ids) == 0 {
		return []entity.Tenant{}, nil
	}
	values := make([]any, 0, len(ids))
	for _, id := range ids {
		values = append(values, id)
	}
	where := storage.NewWhere().In("tenant_id", values...).OrderBy("tenant_id")
	query, args, err := storage.SelectWhere(entity.TenantTable, entity.TenantColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	found := make(map[int64]entity.Tenant, len(ids))
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Tenant
			if err := rows.Scan(&item.TenantID, &item.TenantCode, &item.TenantName, &item.TenantState, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			found[item.TenantID] = item
		}
		return nil
	}, args...); err != nil {
		return nil, err
	}
	out := make([]entity.Tenant, 0, len(found))
	for _, id := range ids {
		if item, ok := found[id]; ok {
			out = append(out, item)
		}
	}
	return out, nil
}

func (r *TenantRepo) UpsertByTenantCode(ctx context.Context, in *entity.Tenant) error {
	if in == nil {
		return errors.New("tenant is nil")
	}
	query, err := storage.Upsert(entity.TenantTable, []string{"tenant_code", "tenant_name", "tenant_state"}, []string{"tenant_code"}, []string{"tenant_name", "tenant_state"}, r.dialect)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx, query, in.TenantCode, in.TenantName, in.TenantState)
	return err
}

func (r *TenantRepo) FindOneForUpdate(ctx context.Context, tenantID int64) (*entity.Tenant, error) {
	query, err := storage.SelectForUpdate(entity.TenantTable, entity.TenantColumns, "tenant_id", r.dialect, false)
	if err != nil {
		return nil, err
	}
	var out entity.Tenant
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.TenantID, &out.TenantCode, &out.TenantName, &out.TenantState, &out.CreatedAt, &out.UpdatedAt)
	}, tenantID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *TenantRepo) FindOneForUpdateSkipLocked(ctx context.Context, tenantID int64) (*entity.Tenant, error) {
	query, err := storage.SelectForUpdate(entity.TenantTable, entity.TenantColumns, "tenant_id", r.dialect, true)
	if err != nil {
		return nil, err
	}
	var out entity.Tenant
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.TenantID, &out.TenantCode, &out.TenantName, &out.TenantState, &out.CreatedAt, &out.UpdatedAt)
	}, tenantID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *TenantRepo) FindByTenantCodeForUpdate(ctx context.Context, tenantCode string) (*entity.Tenant, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_code", tenantCode)
	query, args, err := storage.SelectWhere(entity.TenantTable, entity.TenantColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	var out entity.Tenant
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.TenantID, &out.TenantCode, &out.TenantName, &out.TenantState, &out.CreatedAt, &out.UpdatedAt)
	}, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *TenantRepo) FindByTenantCodeForUpdateSkipLocked(ctx context.Context, tenantCode string) (*entity.Tenant, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_code", tenantCode)
	query, args, err := storage.SelectWhere(entity.TenantTable, entity.TenantColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	var out entity.Tenant
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.TenantID, &out.TenantCode, &out.TenantName, &out.TenantState, &out.CreatedAt, &out.UpdatedAt)
	}, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *TenantRepo) InsertMany(ctx context.Context, items []*entity.Tenant) error {
	if len(items) == 0 {
		return nil
	}
	args := make([]any, 0, len(items)*len(entity.TenantColumns))
	rows := 0
	for _, item := range items {
		if item == nil {
			continue
		}
		args = append(args, item.TenantCode, item.TenantName, item.TenantState)
		rows++
	}
	if rows == 0 {
		return nil
	}
	query, err := storage.BatchInsert(entity.TenantTable, []string{"tenant_code", "tenant_name", "tenant_state"}, rows, r.dialect)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx, query, args...)
	return err
}

func (r *TenantRepo) UpdateMany(ctx context.Context, items []*entity.Tenant) error {
	for _, item := range items {
		if err := r.Update(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func (r *TenantRepo) DeleteMany(ctx context.Context, ids ...int64) error {
	for _, id := range ids {
		if err := r.Delete(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (r *TenantRepo) UpdateFields(ctx context.Context, tenantID int64, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	allowed := map[string]struct{}{"tenant_code": {}, "tenant_name": {}, "tenant_state": {}}
	fieldNames := make([]string, 0, len(fields))
	for column := range fields {
		if _, ok := allowed[column]; !ok {
			return errors.New("field is not updatable: " + column)
		}
		fieldNames = append(fieldNames, column)
	}
	sort.Strings(fieldNames)
	setParts := make([]string, 0, len(fieldNames))
	args := make([]any, 0, len(fieldNames)+1)
	idx := 1
	for _, column := range fieldNames {
		setParts = append(setParts, column+" = "+storage.Placeholder(r.dialect, idx))
		args = append(args, fields[column])
		idx++
	}
	args = append(args, tenantID)
	query := "UPDATE " + entity.TenantTable + " SET " + strings.Join(setParts, ", ") + " WHERE tenant_id = " + storage.Placeholder(r.dialect, idx)
	_, err := r.exec(ctx, query, args...)
	return err
}

func (r *TenantRepo) ListAfter(ctx context.Context, after int64, limit int) ([]entity.Tenant, error) {
	columns, err := storage.JoinIdentifiers(entity.TenantColumns)
	if err != nil {
		return nil, err
	}
	query := "SELECT " + columns + " FROM " + entity.TenantTable + " WHERE tenant_id > " + storage.Placeholder(r.dialect, 1) + " ORDER BY tenant_id ASC LIMIT " + storage.Placeholder(r.dialect, 2)
	out := make([]entity.Tenant, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Tenant
			if err := rows.Scan(&item.TenantID, &item.TenantCode, &item.TenantName, &item.TenantState, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			out = append(out, item)
		}
		return nil
	}, after, limit); err != nil {
		return nil, err
	}
	return out, nil
}
