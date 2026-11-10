package repo

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/imajinyun/gofly/core/storage"
	entity "github.com/imajinyun/gofly/examples/gosky/internal/model/entity"
)

type ProjectRepo struct {
	store   *storage.SQLStore
	cluster *storage.Cluster
	tx      *sql.Tx
	dialect storage.Dialect
}

func NewProjectRepo(store *storage.SQLStore, dialect ...storage.Dialect) *ProjectRepo {
	d := storage.DialectQuestion
	if len(dialect) > 0 {
		d = dialect[0]
	}
	return &ProjectRepo{store: store, dialect: d}
}

func NewProjectRepoWithCluster(cluster *storage.Cluster, dialect ...storage.Dialect) *ProjectRepo {
	d := storage.DialectQuestion
	if len(dialect) > 0 {
		d = dialect[0]
	}
	var store *storage.SQLStore
	if cluster != nil {
		store = cluster.Writer()
	}
	return &ProjectRepo{store: store, cluster: cluster, dialect: d}
}

func (r *ProjectRepo) WithTx(tx *sql.Tx) *ProjectRepo {
	if r == nil {
		return nil
	}
	clone := *r
	clone.tx = tx
	return &clone
}

func (r *ProjectRepo) Transact(ctx context.Context, opts *sql.TxOptions, fn func(context.Context, *ProjectRepo) error) error {
	if r == nil {
		return errors.New("project repo is nil")
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
		return errors.New("project repo store is nil")
	}
	return r.store.Transact(ctx, opts, func(ctx context.Context, tx *sql.Tx) error {
		return fn(ctx, r.WithTx(tx))
	})
}

func (r *ProjectRepo) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if r == nil {
		return nil, errors.New("project repo is nil")
	}
	if r.tx != nil {
		return r.tx.ExecContext(ctx, query, args...)
	}
	if r.cluster != nil {
		store := r.cluster.Writer()
		if store == nil {
			return nil, errors.New("project repo cluster writer is nil")
		}
		return store.Exec(ctx, query, args...)
	}
	if r.store == nil {
		return nil, errors.New("project repo store is nil")
	}
	return r.store.Exec(ctx, query, args...)
}

func (r *ProjectRepo) queryOne(ctx context.Context, query string, scan func(*sql.Row) error, args ...any) error {
	if r == nil {
		return errors.New("project repo is nil")
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
			return errors.New("project repo cluster reader is nil")
		}
		return store.QueryOne(ctx, query, scan, args...)
	}
	if r.store == nil {
		return errors.New("project repo store is nil")
	}
	return r.store.QueryOne(ctx, query, scan, args...)
}

func (r *ProjectRepo) queryAll(ctx context.Context, query string, scan func(*sql.Rows) error, args ...any) error {
	if r == nil {
		return errors.New("project repo is nil")
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
			return errors.New("project repo cluster reader is nil")
		}
		return store.QueryAll(ctx, query, scan, args...)
	}
	if r.store == nil {
		return errors.New("project repo store is nil")
	}
	return r.store.QueryAll(ctx, query, scan, args...)
}

func (r *ProjectRepo) TableName() string { return entity.ProjectTable }

func (r *ProjectRepo) Columns() []string { return append([]string(nil), entity.ProjectColumns...) }

func (r *ProjectRepo) FindOne(ctx context.Context, projectID int64) (*entity.Project, error) {
	columns, err := storage.JoinIdentifiers(entity.ProjectColumns)
	if err != nil {
		return nil, err
	}
	query := "SELECT " + columns + " FROM " + entity.ProjectTable + " WHERE project_id = " + storage.Placeholder(r.dialect, 1) + " AND deleted_at IS NULL LIMIT 1"
	var out entity.Project
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.ProjectID, &out.TenantID, &out.OwnerSubject, &out.ProjectName, &out.ProjectMemo, &out.ProjectState, &out.CreatedBy, &out.UpdatedBy, &out.DeletedAt, &out.CreatedAt, &out.UpdatedAt, &out.ActiveProjectName)
	}, projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *ProjectRepo) InsertResult(ctx context.Context, in *entity.Project) (sql.Result, error) {
	if in == nil {
		return nil, errors.New("project is nil")
	}
	query, err := storage.Insert(entity.ProjectTable, []string{"tenant_id", "owner_subject", "project_name", "project_memo", "project_state", "created_by", "updated_by", "deleted_at", "active_project_name"}, r.dialect)
	if err != nil {
		return nil, err
	}
	return r.exec(ctx, query, in.TenantID, in.OwnerSubject, in.ProjectName, in.ProjectMemo, in.ProjectState, in.CreatedBy, in.UpdatedBy, in.DeletedAt, in.ActiveProjectName)
}

func (r *ProjectRepo) Insert(ctx context.Context, in *entity.Project) error {
	_, err := r.InsertResult(ctx, in)
	return err
}

func (r *ProjectRepo) Update(ctx context.Context, in *entity.Project) error {
	if in == nil {
		return errors.New("project is nil")
	}
	query, err := storage.UpdateByID(entity.ProjectTable, []string{"tenant_id", "owner_subject", "project_name", "project_memo", "project_state", "created_by", "updated_by", "active_project_name"}, "project_id", r.dialect)
	if err != nil {
		return err
	}
	query += " AND deleted_at IS NULL"
	if _, err := r.exec(ctx, query, in.TenantID, in.OwnerSubject, in.ProjectName, in.ProjectMemo, in.ProjectState, in.CreatedBy, in.UpdatedBy, in.ActiveProjectName, in.ProjectID); err != nil {
		return err
	}
	return nil
}

func (r *ProjectRepo) Delete(ctx context.Context, projectID int64) error {
	query := "UPDATE " + entity.ProjectTable + " SET deleted_at = " + storage.Placeholder(r.dialect, 1) + " WHERE project_id = " + storage.Placeholder(r.dialect, 2) + " AND deleted_at IS NULL"
	if _, err := r.exec(ctx, query, time.Now().UTC(), projectID); err != nil {
		return err
	}
	return nil
}

func (r *ProjectRepo) List(ctx context.Context, limit int, offset int) ([]entity.Project, error) {
	columns, err := storage.JoinIdentifiers(entity.ProjectColumns)
	if err != nil {
		return nil, err
	}
	query := "SELECT " + columns + " FROM " + entity.ProjectTable + " WHERE deleted_at IS NULL ORDER BY project_id LIMIT " + storage.Placeholder(r.dialect, 1) + " OFFSET " + storage.Placeholder(r.dialect, 2)
	out := make([]entity.Project, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Project
			if err := rows.Scan(&item.ProjectID, &item.TenantID, &item.OwnerSubject, &item.ProjectName, &item.ProjectMemo, &item.ProjectState, &item.CreatedBy, &item.UpdatedBy, &item.DeletedAt, &item.CreatedAt, &item.UpdatedAt, &item.ActiveProjectName); err != nil {
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

func (r *ProjectRepo) Count(ctx context.Context) (int64, error) {
	query := "SELECT COUNT(*) FROM " + entity.ProjectTable + " WHERE deleted_at IS NULL"
	var count int64
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&count)
	}); err != nil {
		return 0, err
	}
	return count, nil
}

func (r *ProjectRepo) FindWhere(ctx context.Context, where *storage.Where) ([]entity.Project, error) {
	where = where.IsNull("deleted_at")
	query, args, err := storage.SelectWhere(entity.ProjectTable, entity.ProjectColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.Project, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Project
			if err := rows.Scan(&item.ProjectID, &item.TenantID, &item.OwnerSubject, &item.ProjectName, &item.ProjectMemo, &item.ProjectState, &item.CreatedBy, &item.UpdatedBy, &item.DeletedAt, &item.CreatedAt, &item.UpdatedAt, &item.ActiveProjectName); err != nil {
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

func (r *ProjectRepo) CountWhere(ctx context.Context, where *storage.Where) (int64, error) {
	where = where.IsNull("deleted_at")
	query, args, err := storage.CountWhere(entity.ProjectTable, where, r.dialect)
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

func (r *ProjectRepo) FindByProjectIDAndTenantID(ctx context.Context, projectID int64, tenantID int64) (*entity.Project, error) {
	where := storage.NewWhere()
	where = where.Eq("project_id", projectID)
	where = where.Eq("tenant_id", tenantID)
	where = where.IsNull("deleted_at")
	where = where.Limit(1)
	query, args, err := storage.SelectWhere(entity.ProjectTable, entity.ProjectColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	var out entity.Project
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.ProjectID, &out.TenantID, &out.OwnerSubject, &out.ProjectName, &out.ProjectMemo, &out.ProjectState, &out.CreatedBy, &out.UpdatedBy, &out.DeletedAt, &out.CreatedAt, &out.UpdatedAt, &out.ActiveProjectName)
	}, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *ProjectRepo) FindByTenantIDAndActiveProjectName(ctx context.Context, tenantID int64, activeProjectName *string) (*entity.Project, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("active_project_name", activeProjectName)
	where = where.IsNull("deleted_at")
	where = where.Limit(1)
	query, args, err := storage.SelectWhere(entity.ProjectTable, entity.ProjectColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	var out entity.Project
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.ProjectID, &out.TenantID, &out.OwnerSubject, &out.ProjectName, &out.ProjectMemo, &out.ProjectState, &out.CreatedBy, &out.UpdatedBy, &out.DeletedAt, &out.CreatedAt, &out.UpdatedAt, &out.ActiveProjectName)
	}, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *ProjectRepo) FindByTenantIDAndOwnerSubjectAndProjectState(ctx context.Context, tenantID int64, ownerSubject string, projectState string, limit int, offset int) ([]entity.Project, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("owner_subject", ownerSubject)
	where = where.Eq("project_state", projectState)
	where = where.IsNull("deleted_at")
	where = where.OrderBy("project_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.ProjectTable, entity.ProjectColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.Project, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Project
			if err := rows.Scan(&item.ProjectID, &item.TenantID, &item.OwnerSubject, &item.ProjectName, &item.ProjectMemo, &item.ProjectState, &item.CreatedBy, &item.UpdatedBy, &item.DeletedAt, &item.CreatedAt, &item.UpdatedAt, &item.ActiveProjectName); err != nil {
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

func (r *ProjectRepo) FindByTenantIDAndOwnerSubjectAndProjectStateForUpdate(ctx context.Context, tenantID int64, ownerSubject string, projectState string, limit int, offset int) ([]entity.Project, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("owner_subject", ownerSubject)
	where = where.Eq("project_state", projectState)
	where = where.IsNull("deleted_at")
	where = where.OrderBy("project_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.ProjectTable, entity.ProjectColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	out := make([]entity.Project, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Project
			if err := rows.Scan(&item.ProjectID, &item.TenantID, &item.OwnerSubject, &item.ProjectName, &item.ProjectMemo, &item.ProjectState, &item.CreatedBy, &item.UpdatedBy, &item.DeletedAt, &item.CreatedAt, &item.UpdatedAt, &item.ActiveProjectName); err != nil {
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

func (r *ProjectRepo) FindByTenantIDAndOwnerSubjectAndProjectStateForUpdateSkipLocked(ctx context.Context, tenantID int64, ownerSubject string, projectState string, limit int, offset int) ([]entity.Project, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("owner_subject", ownerSubject)
	where = where.Eq("project_state", projectState)
	where = where.IsNull("deleted_at")
	where = where.OrderBy("project_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.ProjectTable, entity.ProjectColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	out := make([]entity.Project, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Project
			if err := rows.Scan(&item.ProjectID, &item.TenantID, &item.OwnerSubject, &item.ProjectName, &item.ProjectMemo, &item.ProjectState, &item.CreatedBy, &item.UpdatedBy, &item.DeletedAt, &item.CreatedAt, &item.UpdatedAt, &item.ActiveProjectName); err != nil {
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

func (r *ProjectRepo) CountByTenantIDAndOwnerSubjectAndProjectState(ctx context.Context, tenantID int64, ownerSubject string, projectState string) (int64, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("owner_subject", ownerSubject)
	where = where.Eq("project_state", projectState)
	where = where.IsNull("deleted_at")
	query, args, err := storage.CountWhere(entity.ProjectTable, where, r.dialect)
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

func (r *ProjectRepo) FindByTenantIDAndProjectState(ctx context.Context, tenantID int64, projectState string, limit int, offset int) ([]entity.Project, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("project_state", projectState)
	where = where.IsNull("deleted_at")
	where = where.OrderBy("project_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.ProjectTable, entity.ProjectColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.Project, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Project
			if err := rows.Scan(&item.ProjectID, &item.TenantID, &item.OwnerSubject, &item.ProjectName, &item.ProjectMemo, &item.ProjectState, &item.CreatedBy, &item.UpdatedBy, &item.DeletedAt, &item.CreatedAt, &item.UpdatedAt, &item.ActiveProjectName); err != nil {
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

func (r *ProjectRepo) FindByTenantIDAndProjectStateForUpdate(ctx context.Context, tenantID int64, projectState string, limit int, offset int) ([]entity.Project, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("project_state", projectState)
	where = where.IsNull("deleted_at")
	where = where.OrderBy("project_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.ProjectTable, entity.ProjectColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	out := make([]entity.Project, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Project
			if err := rows.Scan(&item.ProjectID, &item.TenantID, &item.OwnerSubject, &item.ProjectName, &item.ProjectMemo, &item.ProjectState, &item.CreatedBy, &item.UpdatedBy, &item.DeletedAt, &item.CreatedAt, &item.UpdatedAt, &item.ActiveProjectName); err != nil {
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

func (r *ProjectRepo) FindByTenantIDAndProjectStateForUpdateSkipLocked(ctx context.Context, tenantID int64, projectState string, limit int, offset int) ([]entity.Project, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("project_state", projectState)
	where = where.IsNull("deleted_at")
	where = where.OrderBy("project_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.ProjectTable, entity.ProjectColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	out := make([]entity.Project, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Project
			if err := rows.Scan(&item.ProjectID, &item.TenantID, &item.OwnerSubject, &item.ProjectName, &item.ProjectMemo, &item.ProjectState, &item.CreatedBy, &item.UpdatedBy, &item.DeletedAt, &item.CreatedAt, &item.UpdatedAt, &item.ActiveProjectName); err != nil {
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

func (r *ProjectRepo) CountByTenantIDAndProjectState(ctx context.Context, tenantID int64, projectState string) (int64, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("project_state", projectState)
	where = where.IsNull("deleted_at")
	query, args, err := storage.CountWhere(entity.ProjectTable, where, r.dialect)
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

func (r *ProjectRepo) FindByTenantIDAndUpdatedAt(ctx context.Context, tenantID int64, updatedAt time.Time, limit int, offset int) ([]entity.Project, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("updated_at", updatedAt)
	where = where.IsNull("deleted_at")
	where = where.OrderBy("project_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.ProjectTable, entity.ProjectColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.Project, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Project
			if err := rows.Scan(&item.ProjectID, &item.TenantID, &item.OwnerSubject, &item.ProjectName, &item.ProjectMemo, &item.ProjectState, &item.CreatedBy, &item.UpdatedBy, &item.DeletedAt, &item.CreatedAt, &item.UpdatedAt, &item.ActiveProjectName); err != nil {
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

func (r *ProjectRepo) FindByTenantIDAndUpdatedAtForUpdate(ctx context.Context, tenantID int64, updatedAt time.Time, limit int, offset int) ([]entity.Project, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("updated_at", updatedAt)
	where = where.IsNull("deleted_at")
	where = where.OrderBy("project_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.ProjectTable, entity.ProjectColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	out := make([]entity.Project, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Project
			if err := rows.Scan(&item.ProjectID, &item.TenantID, &item.OwnerSubject, &item.ProjectName, &item.ProjectMemo, &item.ProjectState, &item.CreatedBy, &item.UpdatedBy, &item.DeletedAt, &item.CreatedAt, &item.UpdatedAt, &item.ActiveProjectName); err != nil {
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

func (r *ProjectRepo) FindByTenantIDAndUpdatedAtForUpdateSkipLocked(ctx context.Context, tenantID int64, updatedAt time.Time, limit int, offset int) ([]entity.Project, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("updated_at", updatedAt)
	where = where.IsNull("deleted_at")
	where = where.OrderBy("project_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.ProjectTable, entity.ProjectColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	out := make([]entity.Project, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Project
			if err := rows.Scan(&item.ProjectID, &item.TenantID, &item.OwnerSubject, &item.ProjectName, &item.ProjectMemo, &item.ProjectState, &item.CreatedBy, &item.UpdatedBy, &item.DeletedAt, &item.CreatedAt, &item.UpdatedAt, &item.ActiveProjectName); err != nil {
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

func (r *ProjectRepo) CountByTenantIDAndUpdatedAt(ctx context.Context, tenantID int64, updatedAt time.Time) (int64, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("updated_at", updatedAt)
	where = where.IsNull("deleted_at")
	query, args, err := storage.CountWhere(entity.ProjectTable, where, r.dialect)
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

func (r *ProjectRepo) FindByIDs(ctx context.Context, ids []int64) ([]entity.Project, error) {
	if len(ids) == 0 {
		return []entity.Project{}, nil
	}
	values := make([]any, 0, len(ids))
	for _, id := range ids {
		values = append(values, id)
	}
	where := storage.NewWhere().In("project_id", values...).OrderBy("project_id")
	where = where.IsNull("deleted_at")
	query, args, err := storage.SelectWhere(entity.ProjectTable, entity.ProjectColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	found := make(map[int64]entity.Project, len(ids))
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Project
			if err := rows.Scan(&item.ProjectID, &item.TenantID, &item.OwnerSubject, &item.ProjectName, &item.ProjectMemo, &item.ProjectState, &item.CreatedBy, &item.UpdatedBy, &item.DeletedAt, &item.CreatedAt, &item.UpdatedAt, &item.ActiveProjectName); err != nil {
				return err
			}
			found[item.ProjectID] = item
		}
		return nil
	}, args...); err != nil {
		return nil, err
	}
	out := make([]entity.Project, 0, len(found))
	for _, id := range ids {
		if item, ok := found[id]; ok {
			out = append(out, item)
		}
	}
	return out, nil
}

func (r *ProjectRepo) UpsertByProjectIDAndTenantID(ctx context.Context, in *entity.Project) error {
	if in == nil {
		return errors.New("project is nil")
	}
	query, err := storage.Upsert(entity.ProjectTable, []string{"tenant_id", "owner_subject", "project_name", "project_memo", "project_state", "created_by", "updated_by", "deleted_at", "active_project_name"}, []string{"project_id", "tenant_id"}, []string{"owner_subject", "project_name", "project_memo", "project_state", "created_by", "updated_by", "active_project_name"}, r.dialect)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx, query, in.TenantID, in.OwnerSubject, in.ProjectName, in.ProjectMemo, in.ProjectState, in.CreatedBy, in.UpdatedBy, in.DeletedAt, in.ActiveProjectName)
	return err
}

func (r *ProjectRepo) UpsertByTenantIDAndActiveProjectName(ctx context.Context, in *entity.Project) error {
	if in == nil {
		return errors.New("project is nil")
	}
	query, err := storage.Upsert(entity.ProjectTable, []string{"tenant_id", "owner_subject", "project_name", "project_memo", "project_state", "created_by", "updated_by", "deleted_at", "active_project_name"}, []string{"tenant_id", "active_project_name"}, []string{"owner_subject", "project_name", "project_memo", "project_state", "created_by", "updated_by"}, r.dialect)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx, query, in.TenantID, in.OwnerSubject, in.ProjectName, in.ProjectMemo, in.ProjectState, in.CreatedBy, in.UpdatedBy, in.DeletedAt, in.ActiveProjectName)
	return err
}

func (r *ProjectRepo) FindOneForUpdate(ctx context.Context, projectID int64) (*entity.Project, error) {
	query, err := storage.SelectForUpdate(entity.ProjectTable, entity.ProjectColumns, "project_id", r.dialect, false)
	if err != nil {
		return nil, err
	}
	var out entity.Project
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.ProjectID, &out.TenantID, &out.OwnerSubject, &out.ProjectName, &out.ProjectMemo, &out.ProjectState, &out.CreatedBy, &out.UpdatedBy, &out.DeletedAt, &out.CreatedAt, &out.UpdatedAt, &out.ActiveProjectName)
	}, projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *ProjectRepo) FindOneForUpdateSkipLocked(ctx context.Context, projectID int64) (*entity.Project, error) {
	query, err := storage.SelectForUpdate(entity.ProjectTable, entity.ProjectColumns, "project_id", r.dialect, true)
	if err != nil {
		return nil, err
	}
	var out entity.Project
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.ProjectID, &out.TenantID, &out.OwnerSubject, &out.ProjectName, &out.ProjectMemo, &out.ProjectState, &out.CreatedBy, &out.UpdatedBy, &out.DeletedAt, &out.CreatedAt, &out.UpdatedAt, &out.ActiveProjectName)
	}, projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *ProjectRepo) FindByProjectIDAndTenantIDForUpdate(ctx context.Context, projectID int64, tenantID int64) (*entity.Project, error) {
	where := storage.NewWhere()
	where = where.Eq("project_id", projectID)
	where = where.Eq("tenant_id", tenantID)
	where = where.IsNull("deleted_at")
	query, args, err := storage.SelectWhere(entity.ProjectTable, entity.ProjectColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	var out entity.Project
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.ProjectID, &out.TenantID, &out.OwnerSubject, &out.ProjectName, &out.ProjectMemo, &out.ProjectState, &out.CreatedBy, &out.UpdatedBy, &out.DeletedAt, &out.CreatedAt, &out.UpdatedAt, &out.ActiveProjectName)
	}, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *ProjectRepo) FindByProjectIDAndTenantIDForUpdateSkipLocked(ctx context.Context, projectID int64, tenantID int64) (*entity.Project, error) {
	where := storage.NewWhere()
	where = where.Eq("project_id", projectID)
	where = where.Eq("tenant_id", tenantID)
	where = where.IsNull("deleted_at")
	query, args, err := storage.SelectWhere(entity.ProjectTable, entity.ProjectColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	var out entity.Project
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.ProjectID, &out.TenantID, &out.OwnerSubject, &out.ProjectName, &out.ProjectMemo, &out.ProjectState, &out.CreatedBy, &out.UpdatedBy, &out.DeletedAt, &out.CreatedAt, &out.UpdatedAt, &out.ActiveProjectName)
	}, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *ProjectRepo) FindByTenantIDAndActiveProjectNameForUpdate(ctx context.Context, tenantID int64, activeProjectName *string) (*entity.Project, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("active_project_name", activeProjectName)
	where = where.IsNull("deleted_at")
	query, args, err := storage.SelectWhere(entity.ProjectTable, entity.ProjectColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	var out entity.Project
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.ProjectID, &out.TenantID, &out.OwnerSubject, &out.ProjectName, &out.ProjectMemo, &out.ProjectState, &out.CreatedBy, &out.UpdatedBy, &out.DeletedAt, &out.CreatedAt, &out.UpdatedAt, &out.ActiveProjectName)
	}, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *ProjectRepo) FindByTenantIDAndActiveProjectNameForUpdateSkipLocked(ctx context.Context, tenantID int64, activeProjectName *string) (*entity.Project, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("active_project_name", activeProjectName)
	where = where.IsNull("deleted_at")
	query, args, err := storage.SelectWhere(entity.ProjectTable, entity.ProjectColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	var out entity.Project
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.ProjectID, &out.TenantID, &out.OwnerSubject, &out.ProjectName, &out.ProjectMemo, &out.ProjectState, &out.CreatedBy, &out.UpdatedBy, &out.DeletedAt, &out.CreatedAt, &out.UpdatedAt, &out.ActiveProjectName)
	}, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *ProjectRepo) InsertMany(ctx context.Context, items []*entity.Project) error {
	if len(items) == 0 {
		return nil
	}
	args := make([]any, 0, len(items)*len(entity.ProjectColumns))
	rows := 0
	for _, item := range items {
		if item == nil {
			continue
		}
		args = append(args, item.TenantID, item.OwnerSubject, item.ProjectName, item.ProjectMemo, item.ProjectState, item.CreatedBy, item.UpdatedBy, item.DeletedAt, item.ActiveProjectName)
		rows++
	}
	if rows == 0 {
		return nil
	}
	query, err := storage.BatchInsert(entity.ProjectTable, []string{"tenant_id", "owner_subject", "project_name", "project_memo", "project_state", "created_by", "updated_by", "deleted_at", "active_project_name"}, rows, r.dialect)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx, query, args...)
	return err
}

func (r *ProjectRepo) UpdateMany(ctx context.Context, items []*entity.Project) error {
	for _, item := range items {
		if err := r.Update(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func (r *ProjectRepo) DeleteMany(ctx context.Context, ids ...int64) error {
	for _, id := range ids {
		if err := r.Delete(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (r *ProjectRepo) UpdateFields(ctx context.Context, projectID int64, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	allowed := map[string]struct{}{"tenant_id": {}, "owner_subject": {}, "project_name": {}, "project_memo": {}, "project_state": {}, "created_by": {}, "updated_by": {}, "active_project_name": {}}
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
	args = append(args, projectID)
	query := "UPDATE " + entity.ProjectTable + " SET " + strings.Join(setParts, ", ") + " WHERE project_id = " + storage.Placeholder(r.dialect, idx)
	query += " AND deleted_at IS NULL"
	_, err := r.exec(ctx, query, args...)
	return err
}

func (r *ProjectRepo) ListAfter(ctx context.Context, after int64, limit int) ([]entity.Project, error) {
	columns, err := storage.JoinIdentifiers(entity.ProjectColumns)
	if err != nil {
		return nil, err
	}
	query := "SELECT " + columns + " FROM " + entity.ProjectTable + " WHERE project_id > " + storage.Placeholder(r.dialect, 1) + " AND deleted_at IS NULL" + " ORDER BY project_id ASC LIMIT " + storage.Placeholder(r.dialect, 2)
	out := make([]entity.Project, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Project
			if err := rows.Scan(&item.ProjectID, &item.TenantID, &item.OwnerSubject, &item.ProjectName, &item.ProjectMemo, &item.ProjectState, &item.CreatedBy, &item.UpdatedBy, &item.DeletedAt, &item.CreatedAt, &item.UpdatedAt, &item.ActiveProjectName); err != nil {
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
