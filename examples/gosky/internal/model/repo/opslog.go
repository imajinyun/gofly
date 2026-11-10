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

type OpslogRepo struct {
	store   *storage.SQLStore
	cluster *storage.Cluster
	tx      *sql.Tx
	dialect storage.Dialect
}

func NewOpslogRepo(store *storage.SQLStore, dialect ...storage.Dialect) *OpslogRepo {
	d := storage.DialectQuestion
	if len(dialect) > 0 {
		d = dialect[0]
	}
	return &OpslogRepo{store: store, dialect: d}
}

func NewOpslogRepoWithCluster(cluster *storage.Cluster, dialect ...storage.Dialect) *OpslogRepo {
	d := storage.DialectQuestion
	if len(dialect) > 0 {
		d = dialect[0]
	}
	var store *storage.SQLStore
	if cluster != nil {
		store = cluster.Writer()
	}
	return &OpslogRepo{store: store, cluster: cluster, dialect: d}
}

func (r *OpslogRepo) WithTx(tx *sql.Tx) *OpslogRepo {
	if r == nil {
		return nil
	}
	clone := *r
	clone.tx = tx
	return &clone
}

func (r *OpslogRepo) Transact(ctx context.Context, opts *sql.TxOptions, fn func(context.Context, *OpslogRepo) error) error {
	if r == nil {
		return errors.New("opslog repo is nil")
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
		return errors.New("opslog repo store is nil")
	}
	return r.store.Transact(ctx, opts, func(ctx context.Context, tx *sql.Tx) error {
		return fn(ctx, r.WithTx(tx))
	})
}

func (r *OpslogRepo) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if r == nil {
		return nil, errors.New("opslog repo is nil")
	}
	if r.tx != nil {
		return r.tx.ExecContext(ctx, query, args...)
	}
	if r.cluster != nil {
		store := r.cluster.Writer()
		if store == nil {
			return nil, errors.New("opslog repo cluster writer is nil")
		}
		return store.Exec(ctx, query, args...)
	}
	if r.store == nil {
		return nil, errors.New("opslog repo store is nil")
	}
	return r.store.Exec(ctx, query, args...)
}

func (r *OpslogRepo) queryOne(ctx context.Context, query string, scan func(*sql.Row) error, args ...any) error {
	if r == nil {
		return errors.New("opslog repo is nil")
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
			return errors.New("opslog repo cluster reader is nil")
		}
		return store.QueryOne(ctx, query, scan, args...)
	}
	if r.store == nil {
		return errors.New("opslog repo store is nil")
	}
	return r.store.QueryOne(ctx, query, scan, args...)
}

func (r *OpslogRepo) queryAll(ctx context.Context, query string, scan func(*sql.Rows) error, args ...any) error {
	if r == nil {
		return errors.New("opslog repo is nil")
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
			return errors.New("opslog repo cluster reader is nil")
		}
		return store.QueryAll(ctx, query, scan, args...)
	}
	if r.store == nil {
		return errors.New("opslog repo store is nil")
	}
	return r.store.QueryAll(ctx, query, scan, args...)
}

func (r *OpslogRepo) TableName() string { return entity.OpslogTable }

func (r *OpslogRepo) Columns() []string { return append([]string(nil), entity.OpslogColumns...) }

func (r *OpslogRepo) FindOne(ctx context.Context, opslogID int64) (*entity.Opslog, error) {
	query, err := storage.SelectByID(entity.OpslogTable, entity.OpslogColumns, "opslog_id", r.dialect)
	if err != nil {
		return nil, err
	}
	var out entity.Opslog
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.OpslogID, &out.OperatorTenantID, &out.OperatorSubject, &out.ProjectTenantID, &out.ProjectID, &out.TargetSubject, &out.TargetRole, &out.Operation, &out.Outcome, &out.AuthBasis, &out.Reason, &out.ErrorCode, &out.ErrorBody, &out.RequestID, &out.TraceID, &out.CreatedAt)
	}, opslogID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *OpslogRepo) InsertResult(ctx context.Context, in *entity.Opslog) (sql.Result, error) {
	if in == nil {
		return nil, errors.New("opslog is nil")
	}
	query, err := storage.Insert(entity.OpslogTable, []string{"operator_tenant_id", "operator_subject", "project_tenant_id", "project_id", "target_subject", "target_role", "operation", "outcome", "auth_basis", "reason", "error_code", "error_body", "request_id", "trace_id"}, r.dialect)
	if err != nil {
		return nil, err
	}
	return r.exec(ctx, query, in.OperatorTenantID, in.OperatorSubject, in.ProjectTenantID, in.ProjectID, in.TargetSubject, in.TargetRole, in.Operation, in.Outcome, in.AuthBasis, in.Reason, in.ErrorCode, in.ErrorBody, in.RequestID, in.TraceID)
}

func (r *OpslogRepo) Insert(ctx context.Context, in *entity.Opslog) error {
	_, err := r.InsertResult(ctx, in)
	return err
}

func (r *OpslogRepo) Update(ctx context.Context, in *entity.Opslog) error {
	if in == nil {
		return errors.New("opslog is nil")
	}
	query, err := storage.UpdateByID(entity.OpslogTable, []string{"operator_tenant_id", "operator_subject", "project_tenant_id", "project_id", "target_subject", "target_role", "operation", "outcome", "auth_basis", "reason", "error_code", "error_body", "request_id", "trace_id"}, "opslog_id", r.dialect)
	if err != nil {
		return err
	}
	if _, err := r.exec(ctx, query, in.OperatorTenantID, in.OperatorSubject, in.ProjectTenantID, in.ProjectID, in.TargetSubject, in.TargetRole, in.Operation, in.Outcome, in.AuthBasis, in.Reason, in.ErrorCode, in.ErrorBody, in.RequestID, in.TraceID, in.OpslogID); err != nil {
		return err
	}
	return nil
}

func (r *OpslogRepo) Delete(ctx context.Context, opslogID int64) error {
	query, err := storage.DeleteByID(entity.OpslogTable, "opslog_id", r.dialect)
	if err != nil {
		return err
	}
	if _, err := r.exec(ctx, query, opslogID); err != nil {
		return err
	}
	return nil
}

func (r *OpslogRepo) List(ctx context.Context, limit int, offset int) ([]entity.Opslog, error) {
	query, err := storage.SelectPage(entity.OpslogTable, entity.OpslogColumns, "opslog_id", r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.Opslog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Opslog
			if err := rows.Scan(&item.OpslogID, &item.OperatorTenantID, &item.OperatorSubject, &item.ProjectTenantID, &item.ProjectID, &item.TargetSubject, &item.TargetRole, &item.Operation, &item.Outcome, &item.AuthBasis, &item.Reason, &item.ErrorCode, &item.ErrorBody, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *OpslogRepo) Count(ctx context.Context) (int64, error) {
	query, err := storage.CountAll(entity.OpslogTable)
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

func (r *OpslogRepo) FindWhere(ctx context.Context, where *storage.Where) ([]entity.Opslog, error) {
	query, args, err := storage.SelectWhere(entity.OpslogTable, entity.OpslogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.Opslog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Opslog
			if err := rows.Scan(&item.OpslogID, &item.OperatorTenantID, &item.OperatorSubject, &item.ProjectTenantID, &item.ProjectID, &item.TargetSubject, &item.TargetRole, &item.Operation, &item.Outcome, &item.AuthBasis, &item.Reason, &item.ErrorCode, &item.ErrorBody, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *OpslogRepo) CountWhere(ctx context.Context, where *storage.Where) (int64, error) {
	query, args, err := storage.CountWhere(entity.OpslogTable, where, r.dialect)
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

func (r *OpslogRepo) FindByProjectIDAndCreatedAt(ctx context.Context, projectID int64, createdAt time.Time, limit int, offset int) ([]entity.Opslog, error) {
	where := storage.NewWhere()
	where = where.Eq("project_id", projectID)
	where = where.Eq("created_at", createdAt)
	where = where.OrderBy("opslog_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.OpslogTable, entity.OpslogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.Opslog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Opslog
			if err := rows.Scan(&item.OpslogID, &item.OperatorTenantID, &item.OperatorSubject, &item.ProjectTenantID, &item.ProjectID, &item.TargetSubject, &item.TargetRole, &item.Operation, &item.Outcome, &item.AuthBasis, &item.Reason, &item.ErrorCode, &item.ErrorBody, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *OpslogRepo) FindByProjectIDAndCreatedAtForUpdate(ctx context.Context, projectID int64, createdAt time.Time, limit int, offset int) ([]entity.Opslog, error) {
	where := storage.NewWhere()
	where = where.Eq("project_id", projectID)
	where = where.Eq("created_at", createdAt)
	where = where.OrderBy("opslog_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.OpslogTable, entity.OpslogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	out := make([]entity.Opslog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Opslog
			if err := rows.Scan(&item.OpslogID, &item.OperatorTenantID, &item.OperatorSubject, &item.ProjectTenantID, &item.ProjectID, &item.TargetSubject, &item.TargetRole, &item.Operation, &item.Outcome, &item.AuthBasis, &item.Reason, &item.ErrorCode, &item.ErrorBody, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *OpslogRepo) FindByProjectIDAndCreatedAtForUpdateSkipLocked(ctx context.Context, projectID int64, createdAt time.Time, limit int, offset int) ([]entity.Opslog, error) {
	where := storage.NewWhere()
	where = where.Eq("project_id", projectID)
	where = where.Eq("created_at", createdAt)
	where = where.OrderBy("opslog_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.OpslogTable, entity.OpslogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	out := make([]entity.Opslog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Opslog
			if err := rows.Scan(&item.OpslogID, &item.OperatorTenantID, &item.OperatorSubject, &item.ProjectTenantID, &item.ProjectID, &item.TargetSubject, &item.TargetRole, &item.Operation, &item.Outcome, &item.AuthBasis, &item.Reason, &item.ErrorCode, &item.ErrorBody, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *OpslogRepo) CountByProjectIDAndCreatedAt(ctx context.Context, projectID int64, createdAt time.Time) (int64, error) {
	where := storage.NewWhere()
	where = where.Eq("project_id", projectID)
	where = where.Eq("created_at", createdAt)
	query, args, err := storage.CountWhere(entity.OpslogTable, where, r.dialect)
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

func (r *OpslogRepo) FindByOperatorTenantIDAndOperatorSubjectAndCreatedAt(ctx context.Context, operatorTenantID int64, operatorSubject string, createdAt time.Time, limit int, offset int) ([]entity.Opslog, error) {
	where := storage.NewWhere()
	where = where.Eq("operator_tenant_id", operatorTenantID)
	where = where.Eq("operator_subject", operatorSubject)
	where = where.Eq("created_at", createdAt)
	where = where.OrderBy("opslog_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.OpslogTable, entity.OpslogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.Opslog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Opslog
			if err := rows.Scan(&item.OpslogID, &item.OperatorTenantID, &item.OperatorSubject, &item.ProjectTenantID, &item.ProjectID, &item.TargetSubject, &item.TargetRole, &item.Operation, &item.Outcome, &item.AuthBasis, &item.Reason, &item.ErrorCode, &item.ErrorBody, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *OpslogRepo) FindByOperatorTenantIDAndOperatorSubjectAndCreatedAtForUpdate(ctx context.Context, operatorTenantID int64, operatorSubject string, createdAt time.Time, limit int, offset int) ([]entity.Opslog, error) {
	where := storage.NewWhere()
	where = where.Eq("operator_tenant_id", operatorTenantID)
	where = where.Eq("operator_subject", operatorSubject)
	where = where.Eq("created_at", createdAt)
	where = where.OrderBy("opslog_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.OpslogTable, entity.OpslogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	out := make([]entity.Opslog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Opslog
			if err := rows.Scan(&item.OpslogID, &item.OperatorTenantID, &item.OperatorSubject, &item.ProjectTenantID, &item.ProjectID, &item.TargetSubject, &item.TargetRole, &item.Operation, &item.Outcome, &item.AuthBasis, &item.Reason, &item.ErrorCode, &item.ErrorBody, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *OpslogRepo) FindByOperatorTenantIDAndOperatorSubjectAndCreatedAtForUpdateSkipLocked(ctx context.Context, operatorTenantID int64, operatorSubject string, createdAt time.Time, limit int, offset int) ([]entity.Opslog, error) {
	where := storage.NewWhere()
	where = where.Eq("operator_tenant_id", operatorTenantID)
	where = where.Eq("operator_subject", operatorSubject)
	where = where.Eq("created_at", createdAt)
	where = where.OrderBy("opslog_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.OpslogTable, entity.OpslogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	out := make([]entity.Opslog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Opslog
			if err := rows.Scan(&item.OpslogID, &item.OperatorTenantID, &item.OperatorSubject, &item.ProjectTenantID, &item.ProjectID, &item.TargetSubject, &item.TargetRole, &item.Operation, &item.Outcome, &item.AuthBasis, &item.Reason, &item.ErrorCode, &item.ErrorBody, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *OpslogRepo) CountByOperatorTenantIDAndOperatorSubjectAndCreatedAt(ctx context.Context, operatorTenantID int64, operatorSubject string, createdAt time.Time) (int64, error) {
	where := storage.NewWhere()
	where = where.Eq("operator_tenant_id", operatorTenantID)
	where = where.Eq("operator_subject", operatorSubject)
	where = where.Eq("created_at", createdAt)
	query, args, err := storage.CountWhere(entity.OpslogTable, where, r.dialect)
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

func (r *OpslogRepo) FindByTargetSubjectAndCreatedAt(ctx context.Context, targetSubject string, createdAt time.Time, limit int, offset int) ([]entity.Opslog, error) {
	where := storage.NewWhere()
	where = where.Eq("target_subject", targetSubject)
	where = where.Eq("created_at", createdAt)
	where = where.OrderBy("opslog_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.OpslogTable, entity.OpslogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.Opslog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Opslog
			if err := rows.Scan(&item.OpslogID, &item.OperatorTenantID, &item.OperatorSubject, &item.ProjectTenantID, &item.ProjectID, &item.TargetSubject, &item.TargetRole, &item.Operation, &item.Outcome, &item.AuthBasis, &item.Reason, &item.ErrorCode, &item.ErrorBody, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *OpslogRepo) FindByTargetSubjectAndCreatedAtForUpdate(ctx context.Context, targetSubject string, createdAt time.Time, limit int, offset int) ([]entity.Opslog, error) {
	where := storage.NewWhere()
	where = where.Eq("target_subject", targetSubject)
	where = where.Eq("created_at", createdAt)
	where = where.OrderBy("opslog_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.OpslogTable, entity.OpslogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	out := make([]entity.Opslog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Opslog
			if err := rows.Scan(&item.OpslogID, &item.OperatorTenantID, &item.OperatorSubject, &item.ProjectTenantID, &item.ProjectID, &item.TargetSubject, &item.TargetRole, &item.Operation, &item.Outcome, &item.AuthBasis, &item.Reason, &item.ErrorCode, &item.ErrorBody, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *OpslogRepo) FindByTargetSubjectAndCreatedAtForUpdateSkipLocked(ctx context.Context, targetSubject string, createdAt time.Time, limit int, offset int) ([]entity.Opslog, error) {
	where := storage.NewWhere()
	where = where.Eq("target_subject", targetSubject)
	where = where.Eq("created_at", createdAt)
	where = where.OrderBy("opslog_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.OpslogTable, entity.OpslogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	out := make([]entity.Opslog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Opslog
			if err := rows.Scan(&item.OpslogID, &item.OperatorTenantID, &item.OperatorSubject, &item.ProjectTenantID, &item.ProjectID, &item.TargetSubject, &item.TargetRole, &item.Operation, &item.Outcome, &item.AuthBasis, &item.Reason, &item.ErrorCode, &item.ErrorBody, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *OpslogRepo) CountByTargetSubjectAndCreatedAt(ctx context.Context, targetSubject string, createdAt time.Time) (int64, error) {
	where := storage.NewWhere()
	where = where.Eq("target_subject", targetSubject)
	where = where.Eq("created_at", createdAt)
	query, args, err := storage.CountWhere(entity.OpslogTable, where, r.dialect)
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

func (r *OpslogRepo) FindByRequestID(ctx context.Context, requestID string, limit int, offset int) ([]entity.Opslog, error) {
	where := storage.NewWhere()
	where = where.Eq("request_id", requestID)
	where = where.OrderBy("opslog_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.OpslogTable, entity.OpslogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.Opslog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Opslog
			if err := rows.Scan(&item.OpslogID, &item.OperatorTenantID, &item.OperatorSubject, &item.ProjectTenantID, &item.ProjectID, &item.TargetSubject, &item.TargetRole, &item.Operation, &item.Outcome, &item.AuthBasis, &item.Reason, &item.ErrorCode, &item.ErrorBody, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *OpslogRepo) FindByRequestIDForUpdate(ctx context.Context, requestID string, limit int, offset int) ([]entity.Opslog, error) {
	where := storage.NewWhere()
	where = where.Eq("request_id", requestID)
	where = where.OrderBy("opslog_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.OpslogTable, entity.OpslogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	out := make([]entity.Opslog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Opslog
			if err := rows.Scan(&item.OpslogID, &item.OperatorTenantID, &item.OperatorSubject, &item.ProjectTenantID, &item.ProjectID, &item.TargetSubject, &item.TargetRole, &item.Operation, &item.Outcome, &item.AuthBasis, &item.Reason, &item.ErrorCode, &item.ErrorBody, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *OpslogRepo) FindByRequestIDForUpdateSkipLocked(ctx context.Context, requestID string, limit int, offset int) ([]entity.Opslog, error) {
	where := storage.NewWhere()
	where = where.Eq("request_id", requestID)
	where = where.OrderBy("opslog_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.OpslogTable, entity.OpslogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	out := make([]entity.Opslog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Opslog
			if err := rows.Scan(&item.OpslogID, &item.OperatorTenantID, &item.OperatorSubject, &item.ProjectTenantID, &item.ProjectID, &item.TargetSubject, &item.TargetRole, &item.Operation, &item.Outcome, &item.AuthBasis, &item.Reason, &item.ErrorCode, &item.ErrorBody, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *OpslogRepo) CountByRequestID(ctx context.Context, requestID string) (int64, error) {
	where := storage.NewWhere()
	where = where.Eq("request_id", requestID)
	query, args, err := storage.CountWhere(entity.OpslogTable, where, r.dialect)
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

func (r *OpslogRepo) FindByTraceID(ctx context.Context, traceID string, limit int, offset int) ([]entity.Opslog, error) {
	where := storage.NewWhere()
	where = where.Eq("trace_id", traceID)
	where = where.OrderBy("opslog_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.OpslogTable, entity.OpslogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.Opslog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Opslog
			if err := rows.Scan(&item.OpslogID, &item.OperatorTenantID, &item.OperatorSubject, &item.ProjectTenantID, &item.ProjectID, &item.TargetSubject, &item.TargetRole, &item.Operation, &item.Outcome, &item.AuthBasis, &item.Reason, &item.ErrorCode, &item.ErrorBody, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *OpslogRepo) FindByTraceIDForUpdate(ctx context.Context, traceID string, limit int, offset int) ([]entity.Opslog, error) {
	where := storage.NewWhere()
	where = where.Eq("trace_id", traceID)
	where = where.OrderBy("opslog_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.OpslogTable, entity.OpslogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	out := make([]entity.Opslog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Opslog
			if err := rows.Scan(&item.OpslogID, &item.OperatorTenantID, &item.OperatorSubject, &item.ProjectTenantID, &item.ProjectID, &item.TargetSubject, &item.TargetRole, &item.Operation, &item.Outcome, &item.AuthBasis, &item.Reason, &item.ErrorCode, &item.ErrorBody, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *OpslogRepo) FindByTraceIDForUpdateSkipLocked(ctx context.Context, traceID string, limit int, offset int) ([]entity.Opslog, error) {
	where := storage.NewWhere()
	where = where.Eq("trace_id", traceID)
	where = where.OrderBy("opslog_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.OpslogTable, entity.OpslogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	out := make([]entity.Opslog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Opslog
			if err := rows.Scan(&item.OpslogID, &item.OperatorTenantID, &item.OperatorSubject, &item.ProjectTenantID, &item.ProjectID, &item.TargetSubject, &item.TargetRole, &item.Operation, &item.Outcome, &item.AuthBasis, &item.Reason, &item.ErrorCode, &item.ErrorBody, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *OpslogRepo) CountByTraceID(ctx context.Context, traceID string) (int64, error) {
	where := storage.NewWhere()
	where = where.Eq("trace_id", traceID)
	query, args, err := storage.CountWhere(entity.OpslogTable, where, r.dialect)
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

func (r *OpslogRepo) FindByProjectTenantIDAndCreatedAt(ctx context.Context, projectTenantID int64, createdAt time.Time, limit int, offset int) ([]entity.Opslog, error) {
	where := storage.NewWhere()
	where = where.Eq("project_tenant_id", projectTenantID)
	where = where.Eq("created_at", createdAt)
	where = where.OrderBy("opslog_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.OpslogTable, entity.OpslogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.Opslog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Opslog
			if err := rows.Scan(&item.OpslogID, &item.OperatorTenantID, &item.OperatorSubject, &item.ProjectTenantID, &item.ProjectID, &item.TargetSubject, &item.TargetRole, &item.Operation, &item.Outcome, &item.AuthBasis, &item.Reason, &item.ErrorCode, &item.ErrorBody, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *OpslogRepo) FindByProjectTenantIDAndCreatedAtForUpdate(ctx context.Context, projectTenantID int64, createdAt time.Time, limit int, offset int) ([]entity.Opslog, error) {
	where := storage.NewWhere()
	where = where.Eq("project_tenant_id", projectTenantID)
	where = where.Eq("created_at", createdAt)
	where = where.OrderBy("opslog_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.OpslogTable, entity.OpslogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	out := make([]entity.Opslog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Opslog
			if err := rows.Scan(&item.OpslogID, &item.OperatorTenantID, &item.OperatorSubject, &item.ProjectTenantID, &item.ProjectID, &item.TargetSubject, &item.TargetRole, &item.Operation, &item.Outcome, &item.AuthBasis, &item.Reason, &item.ErrorCode, &item.ErrorBody, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *OpslogRepo) FindByProjectTenantIDAndCreatedAtForUpdateSkipLocked(ctx context.Context, projectTenantID int64, createdAt time.Time, limit int, offset int) ([]entity.Opslog, error) {
	where := storage.NewWhere()
	where = where.Eq("project_tenant_id", projectTenantID)
	where = where.Eq("created_at", createdAt)
	where = where.OrderBy("opslog_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.OpslogTable, entity.OpslogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	out := make([]entity.Opslog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Opslog
			if err := rows.Scan(&item.OpslogID, &item.OperatorTenantID, &item.OperatorSubject, &item.ProjectTenantID, &item.ProjectID, &item.TargetSubject, &item.TargetRole, &item.Operation, &item.Outcome, &item.AuthBasis, &item.Reason, &item.ErrorCode, &item.ErrorBody, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *OpslogRepo) CountByProjectTenantIDAndCreatedAt(ctx context.Context, projectTenantID int64, createdAt time.Time) (int64, error) {
	where := storage.NewWhere()
	where = where.Eq("project_tenant_id", projectTenantID)
	where = where.Eq("created_at", createdAt)
	query, args, err := storage.CountWhere(entity.OpslogTable, where, r.dialect)
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

func (r *OpslogRepo) FindByOperationAndOutcome(ctx context.Context, operation string, outcome string, limit int, offset int) ([]entity.Opslog, error) {
	where := storage.NewWhere()
	where = where.Eq("operation", operation)
	where = where.Eq("outcome", outcome)
	where = where.OrderBy("created_at")
	where = where.OrderBy("opslog_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.OpslogTable, entity.OpslogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.Opslog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Opslog
			if err := rows.Scan(&item.OpslogID, &item.OperatorTenantID, &item.OperatorSubject, &item.ProjectTenantID, &item.ProjectID, &item.TargetSubject, &item.TargetRole, &item.Operation, &item.Outcome, &item.AuthBasis, &item.Reason, &item.ErrorCode, &item.ErrorBody, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *OpslogRepo) FindByOperationAndOutcomeForUpdate(ctx context.Context, operation string, outcome string, limit int, offset int) ([]entity.Opslog, error) {
	where := storage.NewWhere()
	where = where.Eq("operation", operation)
	where = where.Eq("outcome", outcome)
	where = where.OrderBy("created_at")
	where = where.OrderBy("opslog_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.OpslogTable, entity.OpslogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	out := make([]entity.Opslog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Opslog
			if err := rows.Scan(&item.OpslogID, &item.OperatorTenantID, &item.OperatorSubject, &item.ProjectTenantID, &item.ProjectID, &item.TargetSubject, &item.TargetRole, &item.Operation, &item.Outcome, &item.AuthBasis, &item.Reason, &item.ErrorCode, &item.ErrorBody, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *OpslogRepo) FindByOperationAndOutcomeForUpdateSkipLocked(ctx context.Context, operation string, outcome string, limit int, offset int) ([]entity.Opslog, error) {
	where := storage.NewWhere()
	where = where.Eq("operation", operation)
	where = where.Eq("outcome", outcome)
	where = where.OrderBy("created_at")
	where = where.OrderBy("opslog_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.OpslogTable, entity.OpslogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	out := make([]entity.Opslog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Opslog
			if err := rows.Scan(&item.OpslogID, &item.OperatorTenantID, &item.OperatorSubject, &item.ProjectTenantID, &item.ProjectID, &item.TargetSubject, &item.TargetRole, &item.Operation, &item.Outcome, &item.AuthBasis, &item.Reason, &item.ErrorCode, &item.ErrorBody, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *OpslogRepo) CountByOperationAndOutcome(ctx context.Context, operation string, outcome string) (int64, error) {
	where := storage.NewWhere()
	where = where.Eq("operation", operation)
	where = where.Eq("outcome", outcome)
	query, args, err := storage.CountWhere(entity.OpslogTable, where, r.dialect)
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

func (r *OpslogRepo) FindByIDs(ctx context.Context, ids []int64) ([]entity.Opslog, error) {
	if len(ids) == 0 {
		return []entity.Opslog{}, nil
	}
	values := make([]any, 0, len(ids))
	for _, id := range ids {
		values = append(values, id)
	}
	where := storage.NewWhere().In("opslog_id", values...).OrderBy("opslog_id")
	query, args, err := storage.SelectWhere(entity.OpslogTable, entity.OpslogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	found := make(map[int64]entity.Opslog, len(ids))
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Opslog
			if err := rows.Scan(&item.OpslogID, &item.OperatorTenantID, &item.OperatorSubject, &item.ProjectTenantID, &item.ProjectID, &item.TargetSubject, &item.TargetRole, &item.Operation, &item.Outcome, &item.AuthBasis, &item.Reason, &item.ErrorCode, &item.ErrorBody, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
				return err
			}
			found[item.OpslogID] = item
		}
		return nil
	}, args...); err != nil {
		return nil, err
	}
	out := make([]entity.Opslog, 0, len(found))
	for _, id := range ids {
		if item, ok := found[id]; ok {
			out = append(out, item)
		}
	}
	return out, nil
}

func (r *OpslogRepo) FindOneForUpdate(ctx context.Context, opslogID int64) (*entity.Opslog, error) {
	query, err := storage.SelectForUpdate(entity.OpslogTable, entity.OpslogColumns, "opslog_id", r.dialect, false)
	if err != nil {
		return nil, err
	}
	var out entity.Opslog
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.OpslogID, &out.OperatorTenantID, &out.OperatorSubject, &out.ProjectTenantID, &out.ProjectID, &out.TargetSubject, &out.TargetRole, &out.Operation, &out.Outcome, &out.AuthBasis, &out.Reason, &out.ErrorCode, &out.ErrorBody, &out.RequestID, &out.TraceID, &out.CreatedAt)
	}, opslogID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *OpslogRepo) FindOneForUpdateSkipLocked(ctx context.Context, opslogID int64) (*entity.Opslog, error) {
	query, err := storage.SelectForUpdate(entity.OpslogTable, entity.OpslogColumns, "opslog_id", r.dialect, true)
	if err != nil {
		return nil, err
	}
	var out entity.Opslog
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.OpslogID, &out.OperatorTenantID, &out.OperatorSubject, &out.ProjectTenantID, &out.ProjectID, &out.TargetSubject, &out.TargetRole, &out.Operation, &out.Outcome, &out.AuthBasis, &out.Reason, &out.ErrorCode, &out.ErrorBody, &out.RequestID, &out.TraceID, &out.CreatedAt)
	}, opslogID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *OpslogRepo) InsertMany(ctx context.Context, items []*entity.Opslog) error {
	if len(items) == 0 {
		return nil
	}
	args := make([]any, 0, len(items)*len(entity.OpslogColumns))
	rows := 0
	for _, item := range items {
		if item == nil {
			continue
		}
		args = append(args, item.OperatorTenantID, item.OperatorSubject, item.ProjectTenantID, item.ProjectID, item.TargetSubject, item.TargetRole, item.Operation, item.Outcome, item.AuthBasis, item.Reason, item.ErrorCode, item.ErrorBody, item.RequestID, item.TraceID)
		rows++
	}
	if rows == 0 {
		return nil
	}
	query, err := storage.BatchInsert(entity.OpslogTable, []string{"operator_tenant_id", "operator_subject", "project_tenant_id", "project_id", "target_subject", "target_role", "operation", "outcome", "auth_basis", "reason", "error_code", "error_body", "request_id", "trace_id"}, rows, r.dialect)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx, query, args...)
	return err
}

func (r *OpslogRepo) UpdateMany(ctx context.Context, items []*entity.Opslog) error {
	for _, item := range items {
		if err := r.Update(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func (r *OpslogRepo) DeleteMany(ctx context.Context, ids ...int64) error {
	for _, id := range ids {
		if err := r.Delete(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (r *OpslogRepo) UpdateFields(ctx context.Context, opslogID int64, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	allowed := map[string]struct{}{"operator_tenant_id": {}, "operator_subject": {}, "project_tenant_id": {}, "project_id": {}, "target_subject": {}, "target_role": {}, "operation": {}, "outcome": {}, "auth_basis": {}, "reason": {}, "error_code": {}, "error_body": {}, "request_id": {}, "trace_id": {}}
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
	args = append(args, opslogID)
	query := "UPDATE " + entity.OpslogTable + " SET " + strings.Join(setParts, ", ") + " WHERE opslog_id = " + storage.Placeholder(r.dialect, idx)
	_, err := r.exec(ctx, query, args...)
	return err
}

func (r *OpslogRepo) ListAfter(ctx context.Context, after int64, limit int) ([]entity.Opslog, error) {
	columns, err := storage.JoinIdentifiers(entity.OpslogColumns)
	if err != nil {
		return nil, err
	}
	query := "SELECT " + columns + " FROM " + entity.OpslogTable + " WHERE opslog_id > " + storage.Placeholder(r.dialect, 1) + " ORDER BY opslog_id ASC LIMIT " + storage.Placeholder(r.dialect, 2)
	out := make([]entity.Opslog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.Opslog
			if err := rows.Scan(&item.OpslogID, &item.OperatorTenantID, &item.OperatorSubject, &item.ProjectTenantID, &item.ProjectID, &item.TargetSubject, &item.TargetRole, &item.Operation, &item.Outcome, &item.AuthBasis, &item.Reason, &item.ErrorCode, &item.ErrorBody, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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
