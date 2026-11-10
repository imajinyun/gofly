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

type AuthLoginLogRepo struct {
	store   *storage.SQLStore
	cluster *storage.Cluster
	tx      *sql.Tx
	dialect storage.Dialect
}

func NewAuthLoginLogRepo(store *storage.SQLStore, dialect ...storage.Dialect) *AuthLoginLogRepo {
	d := storage.DialectQuestion
	if len(dialect) > 0 {
		d = dialect[0]
	}
	return &AuthLoginLogRepo{store: store, dialect: d}
}

func NewAuthLoginLogRepoWithCluster(cluster *storage.Cluster, dialect ...storage.Dialect) *AuthLoginLogRepo {
	d := storage.DialectQuestion
	if len(dialect) > 0 {
		d = dialect[0]
	}
	var store *storage.SQLStore
	if cluster != nil {
		store = cluster.Writer()
	}
	return &AuthLoginLogRepo{store: store, cluster: cluster, dialect: d}
}

func (r *AuthLoginLogRepo) WithTx(tx *sql.Tx) *AuthLoginLogRepo {
	if r == nil {
		return nil
	}
	clone := *r
	clone.tx = tx
	return &clone
}

func (r *AuthLoginLogRepo) Transact(ctx context.Context, opts *sql.TxOptions, fn func(context.Context, *AuthLoginLogRepo) error) error {
	if r == nil {
		return errors.New("authLoginLog repo is nil")
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
		return errors.New("authLoginLog repo store is nil")
	}
	return r.store.Transact(ctx, opts, func(ctx context.Context, tx *sql.Tx) error {
		return fn(ctx, r.WithTx(tx))
	})
}

func (r *AuthLoginLogRepo) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if r == nil {
		return nil, errors.New("authLoginLog repo is nil")
	}
	if r.tx != nil {
		return r.tx.ExecContext(ctx, query, args...)
	}
	if r.cluster != nil {
		store := r.cluster.Writer()
		if store == nil {
			return nil, errors.New("authLoginLog repo cluster writer is nil")
		}
		return store.Exec(ctx, query, args...)
	}
	if r.store == nil {
		return nil, errors.New("authLoginLog repo store is nil")
	}
	return r.store.Exec(ctx, query, args...)
}

func (r *AuthLoginLogRepo) queryOne(ctx context.Context, query string, scan func(*sql.Row) error, args ...any) error {
	if r == nil {
		return errors.New("authLoginLog repo is nil")
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
			return errors.New("authLoginLog repo cluster reader is nil")
		}
		return store.QueryOne(ctx, query, scan, args...)
	}
	if r.store == nil {
		return errors.New("authLoginLog repo store is nil")
	}
	return r.store.QueryOne(ctx, query, scan, args...)
}

func (r *AuthLoginLogRepo) queryAll(ctx context.Context, query string, scan func(*sql.Rows) error, args ...any) error {
	if r == nil {
		return errors.New("authLoginLog repo is nil")
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
			return errors.New("authLoginLog repo cluster reader is nil")
		}
		return store.QueryAll(ctx, query, scan, args...)
	}
	if r.store == nil {
		return errors.New("authLoginLog repo store is nil")
	}
	return r.store.QueryAll(ctx, query, scan, args...)
}

func (r *AuthLoginLogRepo) TableName() string { return entity.AuthLoginLogTable }

func (r *AuthLoginLogRepo) Columns() []string {
	return append([]string(nil), entity.AuthLoginLogColumns...)
}

func (r *AuthLoginLogRepo) FindOne(ctx context.Context, authLoginLogID int64) (*entity.AuthLoginLog, error) {
	query, err := storage.SelectByID(entity.AuthLoginLogTable, entity.AuthLoginLogColumns, "auth_login_log_id", r.dialect)
	if err != nil {
		return nil, err
	}
	var out entity.AuthLoginLog
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.AuthLoginLogID, &out.UserID, &out.TenantID, &out.Realm, &out.LoginName, &out.Subject, &out.Operation, &out.Outcome, &out.ReasonCode, &out.RequestID, &out.TraceID, &out.CreatedAt)
	}, authLoginLogID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *AuthLoginLogRepo) InsertResult(ctx context.Context, in *entity.AuthLoginLog) (sql.Result, error) {
	if in == nil {
		return nil, errors.New("authLoginLog is nil")
	}
	query, err := storage.Insert(entity.AuthLoginLogTable, []string{"user_id", "tenant_id", "realm", "login_name", "subject", "operation", "outcome", "reason_code", "request_id", "trace_id"}, r.dialect)
	if err != nil {
		return nil, err
	}
	return r.exec(ctx, query, in.UserID, in.TenantID, in.Realm, in.LoginName, in.Subject, in.Operation, in.Outcome, in.ReasonCode, in.RequestID, in.TraceID)
}

func (r *AuthLoginLogRepo) Insert(ctx context.Context, in *entity.AuthLoginLog) error {
	_, err := r.InsertResult(ctx, in)
	return err
}

func (r *AuthLoginLogRepo) Update(ctx context.Context, in *entity.AuthLoginLog) error {
	if in == nil {
		return errors.New("authLoginLog is nil")
	}
	query, err := storage.UpdateByID(entity.AuthLoginLogTable, []string{"user_id", "tenant_id", "realm", "login_name", "subject", "operation", "outcome", "reason_code", "request_id", "trace_id"}, "auth_login_log_id", r.dialect)
	if err != nil {
		return err
	}
	if _, err := r.exec(ctx, query, in.UserID, in.TenantID, in.Realm, in.LoginName, in.Subject, in.Operation, in.Outcome, in.ReasonCode, in.RequestID, in.TraceID, in.AuthLoginLogID); err != nil {
		return err
	}
	return nil
}

func (r *AuthLoginLogRepo) Delete(ctx context.Context, authLoginLogID int64) error {
	query, err := storage.DeleteByID(entity.AuthLoginLogTable, "auth_login_log_id", r.dialect)
	if err != nil {
		return err
	}
	if _, err := r.exec(ctx, query, authLoginLogID); err != nil {
		return err
	}
	return nil
}

func (r *AuthLoginLogRepo) List(ctx context.Context, limit int, offset int) ([]entity.AuthLoginLog, error) {
	query, err := storage.SelectPage(entity.AuthLoginLogTable, entity.AuthLoginLogColumns, "auth_login_log_id", r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.AuthLoginLog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.AuthLoginLog
			if err := rows.Scan(&item.AuthLoginLogID, &item.UserID, &item.TenantID, &item.Realm, &item.LoginName, &item.Subject, &item.Operation, &item.Outcome, &item.ReasonCode, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *AuthLoginLogRepo) Count(ctx context.Context) (int64, error) {
	query, err := storage.CountAll(entity.AuthLoginLogTable)
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

func (r *AuthLoginLogRepo) FindWhere(ctx context.Context, where *storage.Where) ([]entity.AuthLoginLog, error) {
	query, args, err := storage.SelectWhere(entity.AuthLoginLogTable, entity.AuthLoginLogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.AuthLoginLog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.AuthLoginLog
			if err := rows.Scan(&item.AuthLoginLogID, &item.UserID, &item.TenantID, &item.Realm, &item.LoginName, &item.Subject, &item.Operation, &item.Outcome, &item.ReasonCode, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *AuthLoginLogRepo) CountWhere(ctx context.Context, where *storage.Where) (int64, error) {
	query, args, err := storage.CountWhere(entity.AuthLoginLogTable, where, r.dialect)
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

func (r *AuthLoginLogRepo) FindByTenantIDAndCreatedAt(ctx context.Context, tenantID *int64, createdAt time.Time, limit int, offset int) ([]entity.AuthLoginLog, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("created_at", createdAt)
	where = where.OrderBy("auth_login_log_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.AuthLoginLogTable, entity.AuthLoginLogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.AuthLoginLog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.AuthLoginLog
			if err := rows.Scan(&item.AuthLoginLogID, &item.UserID, &item.TenantID, &item.Realm, &item.LoginName, &item.Subject, &item.Operation, &item.Outcome, &item.ReasonCode, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *AuthLoginLogRepo) FindByTenantIDAndCreatedAtForUpdate(ctx context.Context, tenantID *int64, createdAt time.Time, limit int, offset int) ([]entity.AuthLoginLog, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("created_at", createdAt)
	where = where.OrderBy("auth_login_log_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.AuthLoginLogTable, entity.AuthLoginLogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	out := make([]entity.AuthLoginLog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.AuthLoginLog
			if err := rows.Scan(&item.AuthLoginLogID, &item.UserID, &item.TenantID, &item.Realm, &item.LoginName, &item.Subject, &item.Operation, &item.Outcome, &item.ReasonCode, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *AuthLoginLogRepo) FindByTenantIDAndCreatedAtForUpdateSkipLocked(ctx context.Context, tenantID *int64, createdAt time.Time, limit int, offset int) ([]entity.AuthLoginLog, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("created_at", createdAt)
	where = where.OrderBy("auth_login_log_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.AuthLoginLogTable, entity.AuthLoginLogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	out := make([]entity.AuthLoginLog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.AuthLoginLog
			if err := rows.Scan(&item.AuthLoginLogID, &item.UserID, &item.TenantID, &item.Realm, &item.LoginName, &item.Subject, &item.Operation, &item.Outcome, &item.ReasonCode, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *AuthLoginLogRepo) CountByTenantIDAndCreatedAt(ctx context.Context, tenantID *int64, createdAt time.Time) (int64, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("created_at", createdAt)
	query, args, err := storage.CountWhere(entity.AuthLoginLogTable, where, r.dialect)
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

func (r *AuthLoginLogRepo) FindByUserIDAndCreatedAt(ctx context.Context, userID *int64, createdAt time.Time, limit int, offset int) ([]entity.AuthLoginLog, error) {
	where := storage.NewWhere()
	where = where.Eq("user_id", userID)
	where = where.Eq("created_at", createdAt)
	where = where.OrderBy("auth_login_log_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.AuthLoginLogTable, entity.AuthLoginLogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.AuthLoginLog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.AuthLoginLog
			if err := rows.Scan(&item.AuthLoginLogID, &item.UserID, &item.TenantID, &item.Realm, &item.LoginName, &item.Subject, &item.Operation, &item.Outcome, &item.ReasonCode, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *AuthLoginLogRepo) FindByUserIDAndCreatedAtForUpdate(ctx context.Context, userID *int64, createdAt time.Time, limit int, offset int) ([]entity.AuthLoginLog, error) {
	where := storage.NewWhere()
	where = where.Eq("user_id", userID)
	where = where.Eq("created_at", createdAt)
	where = where.OrderBy("auth_login_log_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.AuthLoginLogTable, entity.AuthLoginLogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	out := make([]entity.AuthLoginLog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.AuthLoginLog
			if err := rows.Scan(&item.AuthLoginLogID, &item.UserID, &item.TenantID, &item.Realm, &item.LoginName, &item.Subject, &item.Operation, &item.Outcome, &item.ReasonCode, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *AuthLoginLogRepo) FindByUserIDAndCreatedAtForUpdateSkipLocked(ctx context.Context, userID *int64, createdAt time.Time, limit int, offset int) ([]entity.AuthLoginLog, error) {
	where := storage.NewWhere()
	where = where.Eq("user_id", userID)
	where = where.Eq("created_at", createdAt)
	where = where.OrderBy("auth_login_log_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.AuthLoginLogTable, entity.AuthLoginLogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	out := make([]entity.AuthLoginLog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.AuthLoginLog
			if err := rows.Scan(&item.AuthLoginLogID, &item.UserID, &item.TenantID, &item.Realm, &item.LoginName, &item.Subject, &item.Operation, &item.Outcome, &item.ReasonCode, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *AuthLoginLogRepo) CountByUserIDAndCreatedAt(ctx context.Context, userID *int64, createdAt time.Time) (int64, error) {
	where := storage.NewWhere()
	where = where.Eq("user_id", userID)
	where = where.Eq("created_at", createdAt)
	query, args, err := storage.CountWhere(entity.AuthLoginLogTable, where, r.dialect)
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

func (r *AuthLoginLogRepo) FindByRequestID(ctx context.Context, requestID string, limit int, offset int) ([]entity.AuthLoginLog, error) {
	where := storage.NewWhere()
	where = where.Eq("request_id", requestID)
	where = where.OrderBy("auth_login_log_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.AuthLoginLogTable, entity.AuthLoginLogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.AuthLoginLog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.AuthLoginLog
			if err := rows.Scan(&item.AuthLoginLogID, &item.UserID, &item.TenantID, &item.Realm, &item.LoginName, &item.Subject, &item.Operation, &item.Outcome, &item.ReasonCode, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *AuthLoginLogRepo) FindByRequestIDForUpdate(ctx context.Context, requestID string, limit int, offset int) ([]entity.AuthLoginLog, error) {
	where := storage.NewWhere()
	where = where.Eq("request_id", requestID)
	where = where.OrderBy("auth_login_log_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.AuthLoginLogTable, entity.AuthLoginLogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	out := make([]entity.AuthLoginLog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.AuthLoginLog
			if err := rows.Scan(&item.AuthLoginLogID, &item.UserID, &item.TenantID, &item.Realm, &item.LoginName, &item.Subject, &item.Operation, &item.Outcome, &item.ReasonCode, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *AuthLoginLogRepo) FindByRequestIDForUpdateSkipLocked(ctx context.Context, requestID string, limit int, offset int) ([]entity.AuthLoginLog, error) {
	where := storage.NewWhere()
	where = where.Eq("request_id", requestID)
	where = where.OrderBy("auth_login_log_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.AuthLoginLogTable, entity.AuthLoginLogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	out := make([]entity.AuthLoginLog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.AuthLoginLog
			if err := rows.Scan(&item.AuthLoginLogID, &item.UserID, &item.TenantID, &item.Realm, &item.LoginName, &item.Subject, &item.Operation, &item.Outcome, &item.ReasonCode, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *AuthLoginLogRepo) CountByRequestID(ctx context.Context, requestID string) (int64, error) {
	where := storage.NewWhere()
	where = where.Eq("request_id", requestID)
	query, args, err := storage.CountWhere(entity.AuthLoginLogTable, where, r.dialect)
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

func (r *AuthLoginLogRepo) FindByOperationAndOutcomeAndCreatedAt(ctx context.Context, operation string, outcome string, createdAt time.Time, limit int, offset int) ([]entity.AuthLoginLog, error) {
	where := storage.NewWhere()
	where = where.Eq("operation", operation)
	where = where.Eq("outcome", outcome)
	where = where.Eq("created_at", createdAt)
	where = where.OrderBy("auth_login_log_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.AuthLoginLogTable, entity.AuthLoginLogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.AuthLoginLog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.AuthLoginLog
			if err := rows.Scan(&item.AuthLoginLogID, &item.UserID, &item.TenantID, &item.Realm, &item.LoginName, &item.Subject, &item.Operation, &item.Outcome, &item.ReasonCode, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *AuthLoginLogRepo) FindByOperationAndOutcomeAndCreatedAtForUpdate(ctx context.Context, operation string, outcome string, createdAt time.Time, limit int, offset int) ([]entity.AuthLoginLog, error) {
	where := storage.NewWhere()
	where = where.Eq("operation", operation)
	where = where.Eq("outcome", outcome)
	where = where.Eq("created_at", createdAt)
	where = where.OrderBy("auth_login_log_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.AuthLoginLogTable, entity.AuthLoginLogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	out := make([]entity.AuthLoginLog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.AuthLoginLog
			if err := rows.Scan(&item.AuthLoginLogID, &item.UserID, &item.TenantID, &item.Realm, &item.LoginName, &item.Subject, &item.Operation, &item.Outcome, &item.ReasonCode, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *AuthLoginLogRepo) FindByOperationAndOutcomeAndCreatedAtForUpdateSkipLocked(ctx context.Context, operation string, outcome string, createdAt time.Time, limit int, offset int) ([]entity.AuthLoginLog, error) {
	where := storage.NewWhere()
	where = where.Eq("operation", operation)
	where = where.Eq("outcome", outcome)
	where = where.Eq("created_at", createdAt)
	where = where.OrderBy("auth_login_log_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.AuthLoginLogTable, entity.AuthLoginLogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	out := make([]entity.AuthLoginLog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.AuthLoginLog
			if err := rows.Scan(&item.AuthLoginLogID, &item.UserID, &item.TenantID, &item.Realm, &item.LoginName, &item.Subject, &item.Operation, &item.Outcome, &item.ReasonCode, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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

func (r *AuthLoginLogRepo) CountByOperationAndOutcomeAndCreatedAt(ctx context.Context, operation string, outcome string, createdAt time.Time) (int64, error) {
	where := storage.NewWhere()
	where = where.Eq("operation", operation)
	where = where.Eq("outcome", outcome)
	where = where.Eq("created_at", createdAt)
	query, args, err := storage.CountWhere(entity.AuthLoginLogTable, where, r.dialect)
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

func (r *AuthLoginLogRepo) FindByIDs(ctx context.Context, ids []int64) ([]entity.AuthLoginLog, error) {
	if len(ids) == 0 {
		return []entity.AuthLoginLog{}, nil
	}
	values := make([]any, 0, len(ids))
	for _, id := range ids {
		values = append(values, id)
	}
	where := storage.NewWhere().In("auth_login_log_id", values...).OrderBy("auth_login_log_id")
	query, args, err := storage.SelectWhere(entity.AuthLoginLogTable, entity.AuthLoginLogColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	found := make(map[int64]entity.AuthLoginLog, len(ids))
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.AuthLoginLog
			if err := rows.Scan(&item.AuthLoginLogID, &item.UserID, &item.TenantID, &item.Realm, &item.LoginName, &item.Subject, &item.Operation, &item.Outcome, &item.ReasonCode, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
				return err
			}
			found[item.AuthLoginLogID] = item
		}
		return nil
	}, args...); err != nil {
		return nil, err
	}
	out := make([]entity.AuthLoginLog, 0, len(found))
	for _, id := range ids {
		if item, ok := found[id]; ok {
			out = append(out, item)
		}
	}
	return out, nil
}

func (r *AuthLoginLogRepo) FindOneForUpdate(ctx context.Context, authLoginLogID int64) (*entity.AuthLoginLog, error) {
	query, err := storage.SelectForUpdate(entity.AuthLoginLogTable, entity.AuthLoginLogColumns, "auth_login_log_id", r.dialect, false)
	if err != nil {
		return nil, err
	}
	var out entity.AuthLoginLog
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.AuthLoginLogID, &out.UserID, &out.TenantID, &out.Realm, &out.LoginName, &out.Subject, &out.Operation, &out.Outcome, &out.ReasonCode, &out.RequestID, &out.TraceID, &out.CreatedAt)
	}, authLoginLogID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *AuthLoginLogRepo) FindOneForUpdateSkipLocked(ctx context.Context, authLoginLogID int64) (*entity.AuthLoginLog, error) {
	query, err := storage.SelectForUpdate(entity.AuthLoginLogTable, entity.AuthLoginLogColumns, "auth_login_log_id", r.dialect, true)
	if err != nil {
		return nil, err
	}
	var out entity.AuthLoginLog
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.AuthLoginLogID, &out.UserID, &out.TenantID, &out.Realm, &out.LoginName, &out.Subject, &out.Operation, &out.Outcome, &out.ReasonCode, &out.RequestID, &out.TraceID, &out.CreatedAt)
	}, authLoginLogID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *AuthLoginLogRepo) InsertMany(ctx context.Context, items []*entity.AuthLoginLog) error {
	if len(items) == 0 {
		return nil
	}
	args := make([]any, 0, len(items)*len(entity.AuthLoginLogColumns))
	rows := 0
	for _, item := range items {
		if item == nil {
			continue
		}
		args = append(args, item.UserID, item.TenantID, item.Realm, item.LoginName, item.Subject, item.Operation, item.Outcome, item.ReasonCode, item.RequestID, item.TraceID)
		rows++
	}
	if rows == 0 {
		return nil
	}
	query, err := storage.BatchInsert(entity.AuthLoginLogTable, []string{"user_id", "tenant_id", "realm", "login_name", "subject", "operation", "outcome", "reason_code", "request_id", "trace_id"}, rows, r.dialect)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx, query, args...)
	return err
}

func (r *AuthLoginLogRepo) UpdateMany(ctx context.Context, items []*entity.AuthLoginLog) error {
	for _, item := range items {
		if err := r.Update(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func (r *AuthLoginLogRepo) DeleteMany(ctx context.Context, ids ...int64) error {
	for _, id := range ids {
		if err := r.Delete(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (r *AuthLoginLogRepo) UpdateFields(ctx context.Context, authLoginLogID int64, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	allowed := map[string]struct{}{"user_id": {}, "tenant_id": {}, "realm": {}, "login_name": {}, "subject": {}, "operation": {}, "outcome": {}, "reason_code": {}, "request_id": {}, "trace_id": {}}
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
	args = append(args, authLoginLogID)
	query := "UPDATE " + entity.AuthLoginLogTable + " SET " + strings.Join(setParts, ", ") + " WHERE auth_login_log_id = " + storage.Placeholder(r.dialect, idx)
	_, err := r.exec(ctx, query, args...)
	return err
}

func (r *AuthLoginLogRepo) ListAfter(ctx context.Context, after int64, limit int) ([]entity.AuthLoginLog, error) {
	columns, err := storage.JoinIdentifiers(entity.AuthLoginLogColumns)
	if err != nil {
		return nil, err
	}
	query := "SELECT " + columns + " FROM " + entity.AuthLoginLogTable + " WHERE auth_login_log_id > " + storage.Placeholder(r.dialect, 1) + " ORDER BY auth_login_log_id ASC LIMIT " + storage.Placeholder(r.dialect, 2)
	out := make([]entity.AuthLoginLog, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.AuthLoginLog
			if err := rows.Scan(&item.AuthLoginLogID, &item.UserID, &item.TenantID, &item.Realm, &item.LoginName, &item.Subject, &item.Operation, &item.Outcome, &item.ReasonCode, &item.RequestID, &item.TraceID, &item.CreatedAt); err != nil {
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
