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

type AuthSessionRepo struct {
	store   *storage.SQLStore
	cluster *storage.Cluster
	tx      *sql.Tx
	dialect storage.Dialect
}

func NewAuthSessionRepo(store *storage.SQLStore, dialect ...storage.Dialect) *AuthSessionRepo {
	d := storage.DialectQuestion
	if len(dialect) > 0 {
		d = dialect[0]
	}
	return &AuthSessionRepo{store: store, dialect: d}
}

func NewAuthSessionRepoWithCluster(cluster *storage.Cluster, dialect ...storage.Dialect) *AuthSessionRepo {
	d := storage.DialectQuestion
	if len(dialect) > 0 {
		d = dialect[0]
	}
	var store *storage.SQLStore
	if cluster != nil {
		store = cluster.Writer()
	}
	return &AuthSessionRepo{store: store, cluster: cluster, dialect: d}
}

func (r *AuthSessionRepo) WithTx(tx *sql.Tx) *AuthSessionRepo {
	if r == nil {
		return nil
	}
	clone := *r
	clone.tx = tx
	return &clone
}

func (r *AuthSessionRepo) Transact(ctx context.Context, opts *sql.TxOptions, fn func(context.Context, *AuthSessionRepo) error) error {
	if r == nil {
		return errors.New("authSession repo is nil")
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
		return errors.New("authSession repo store is nil")
	}
	return r.store.Transact(ctx, opts, func(ctx context.Context, tx *sql.Tx) error {
		return fn(ctx, r.WithTx(tx))
	})
}

func (r *AuthSessionRepo) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if r == nil {
		return nil, errors.New("authSession repo is nil")
	}
	if r.tx != nil {
		return r.tx.ExecContext(ctx, query, args...)
	}
	if r.cluster != nil {
		store := r.cluster.Writer()
		if store == nil {
			return nil, errors.New("authSession repo cluster writer is nil")
		}
		return store.Exec(ctx, query, args...)
	}
	if r.store == nil {
		return nil, errors.New("authSession repo store is nil")
	}
	return r.store.Exec(ctx, query, args...)
}

func (r *AuthSessionRepo) queryOne(ctx context.Context, query string, scan func(*sql.Row) error, args ...any) error {
	if r == nil {
		return errors.New("authSession repo is nil")
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
			return errors.New("authSession repo cluster reader is nil")
		}
		return store.QueryOne(ctx, query, scan, args...)
	}
	if r.store == nil {
		return errors.New("authSession repo store is nil")
	}
	return r.store.QueryOne(ctx, query, scan, args...)
}

func (r *AuthSessionRepo) queryAll(ctx context.Context, query string, scan func(*sql.Rows) error, args ...any) error {
	if r == nil {
		return errors.New("authSession repo is nil")
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
			return errors.New("authSession repo cluster reader is nil")
		}
		return store.QueryAll(ctx, query, scan, args...)
	}
	if r.store == nil {
		return errors.New("authSession repo store is nil")
	}
	return r.store.QueryAll(ctx, query, scan, args...)
}

func (r *AuthSessionRepo) TableName() string { return entity.AuthSessionTable }

func (r *AuthSessionRepo) Columns() []string {
	return append([]string(nil), entity.AuthSessionColumns...)
}

func (r *AuthSessionRepo) FindOne(ctx context.Context, sessionID string) (*entity.AuthSession, error) {
	query, err := storage.SelectByID(entity.AuthSessionTable, entity.AuthSessionColumns, "session_id", r.dialect)
	if err != nil {
		return nil, err
	}
	var out entity.AuthSession
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.SessionID, &out.UserID, &out.TenantID, &out.Realm, &out.RefreshTokenHash, &out.RefreshGeneration, &out.IssuedAt, &out.ExpiresAt, &out.LastSeenAt, &out.RevokedAt, &out.CreatedAt, &out.UpdatedAt)
	}, sessionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *AuthSessionRepo) InsertResult(ctx context.Context, in *entity.AuthSession) (sql.Result, error) {
	if in == nil {
		return nil, errors.New("authSession is nil")
	}
	query, err := storage.Insert(entity.AuthSessionTable, []string{"session_id", "user_id", "tenant_id", "realm", "refresh_token_hash", "refresh_generation", "issued_at", "expires_at", "last_seen_at", "revoked_at"}, r.dialect)
	if err != nil {
		return nil, err
	}
	return r.exec(ctx, query, in.SessionID, in.UserID, in.TenantID, in.Realm, in.RefreshTokenHash, in.RefreshGeneration, in.IssuedAt, in.ExpiresAt, in.LastSeenAt, in.RevokedAt)
}

func (r *AuthSessionRepo) Insert(ctx context.Context, in *entity.AuthSession) error {
	_, err := r.InsertResult(ctx, in)
	return err
}

func (r *AuthSessionRepo) Update(ctx context.Context, in *entity.AuthSession) error {
	if in == nil {
		return errors.New("authSession is nil")
	}
	query, err := storage.UpdateByID(entity.AuthSessionTable, []string{"user_id", "tenant_id", "realm", "refresh_token_hash", "refresh_generation", "issued_at", "expires_at", "last_seen_at", "revoked_at"}, "session_id", r.dialect)
	if err != nil {
		return err
	}
	if _, err := r.exec(ctx, query, in.UserID, in.TenantID, in.Realm, in.RefreshTokenHash, in.RefreshGeneration, in.IssuedAt, in.ExpiresAt, in.LastSeenAt, in.RevokedAt, in.SessionID); err != nil {
		return err
	}
	return nil
}

func (r *AuthSessionRepo) Delete(ctx context.Context, sessionID string) error {
	query, err := storage.DeleteByID(entity.AuthSessionTable, "session_id", r.dialect)
	if err != nil {
		return err
	}
	if _, err := r.exec(ctx, query, sessionID); err != nil {
		return err
	}
	return nil
}

func (r *AuthSessionRepo) List(ctx context.Context, limit int, offset int) ([]entity.AuthSession, error) {
	query, err := storage.SelectPage(entity.AuthSessionTable, entity.AuthSessionColumns, "session_id", r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.AuthSession, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.AuthSession
			if err := rows.Scan(&item.SessionID, &item.UserID, &item.TenantID, &item.Realm, &item.RefreshTokenHash, &item.RefreshGeneration, &item.IssuedAt, &item.ExpiresAt, &item.LastSeenAt, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *AuthSessionRepo) Count(ctx context.Context) (int64, error) {
	query, err := storage.CountAll(entity.AuthSessionTable)
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

func (r *AuthSessionRepo) FindWhere(ctx context.Context, where *storage.Where) ([]entity.AuthSession, error) {
	query, args, err := storage.SelectWhere(entity.AuthSessionTable, entity.AuthSessionColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.AuthSession, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.AuthSession
			if err := rows.Scan(&item.SessionID, &item.UserID, &item.TenantID, &item.Realm, &item.RefreshTokenHash, &item.RefreshGeneration, &item.IssuedAt, &item.ExpiresAt, &item.LastSeenAt, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *AuthSessionRepo) CountWhere(ctx context.Context, where *storage.Where) (int64, error) {
	query, args, err := storage.CountWhere(entity.AuthSessionTable, where, r.dialect)
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

func (r *AuthSessionRepo) FindByRefreshTokenHash(ctx context.Context, refreshTokenHash string) (*entity.AuthSession, error) {
	where := storage.NewWhere().Eq("refresh_token_hash", refreshTokenHash).Limit(1)
	query, args, err := storage.SelectWhere(entity.AuthSessionTable, entity.AuthSessionColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	var out entity.AuthSession
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.SessionID, &out.UserID, &out.TenantID, &out.Realm, &out.RefreshTokenHash, &out.RefreshGeneration, &out.IssuedAt, &out.ExpiresAt, &out.LastSeenAt, &out.RevokedAt, &out.CreatedAt, &out.UpdatedAt)
	}, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *AuthSessionRepo) FindByUserIDAndTenantIDAndRealm(ctx context.Context, userID int64, tenantID int64, realm string, limit int, offset int) ([]entity.AuthSession, error) {
	where := storage.NewWhere()
	where = where.Eq("user_id", userID)
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("realm", realm)
	where = where.OrderBy("expires_at")
	where = where.OrderBy("session_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.AuthSessionTable, entity.AuthSessionColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.AuthSession, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.AuthSession
			if err := rows.Scan(&item.SessionID, &item.UserID, &item.TenantID, &item.Realm, &item.RefreshTokenHash, &item.RefreshGeneration, &item.IssuedAt, &item.ExpiresAt, &item.LastSeenAt, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *AuthSessionRepo) FindByUserIDAndTenantIDAndRealmForUpdate(ctx context.Context, userID int64, tenantID int64, realm string, limit int, offset int) ([]entity.AuthSession, error) {
	where := storage.NewWhere()
	where = where.Eq("user_id", userID)
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("realm", realm)
	where = where.OrderBy("expires_at")
	where = where.OrderBy("session_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.AuthSessionTable, entity.AuthSessionColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	out := make([]entity.AuthSession, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.AuthSession
			if err := rows.Scan(&item.SessionID, &item.UserID, &item.TenantID, &item.Realm, &item.RefreshTokenHash, &item.RefreshGeneration, &item.IssuedAt, &item.ExpiresAt, &item.LastSeenAt, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *AuthSessionRepo) FindByUserIDAndTenantIDAndRealmForUpdateSkipLocked(ctx context.Context, userID int64, tenantID int64, realm string, limit int, offset int) ([]entity.AuthSession, error) {
	where := storage.NewWhere()
	where = where.Eq("user_id", userID)
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("realm", realm)
	where = where.OrderBy("expires_at")
	where = where.OrderBy("session_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.AuthSessionTable, entity.AuthSessionColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	out := make([]entity.AuthSession, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.AuthSession
			if err := rows.Scan(&item.SessionID, &item.UserID, &item.TenantID, &item.Realm, &item.RefreshTokenHash, &item.RefreshGeneration, &item.IssuedAt, &item.ExpiresAt, &item.LastSeenAt, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *AuthSessionRepo) CountByUserIDAndTenantIDAndRealm(ctx context.Context, userID int64, tenantID int64, realm string) (int64, error) {
	where := storage.NewWhere()
	where = where.Eq("user_id", userID)
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("realm", realm)
	query, args, err := storage.CountWhere(entity.AuthSessionTable, where, r.dialect)
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

func (r *AuthSessionRepo) FindByExpiresAt(ctx context.Context, expiresAt time.Time, limit int, offset int) ([]entity.AuthSession, error) {
	where := storage.NewWhere()
	where = where.Eq("expires_at", expiresAt)
	where = where.OrderBy("session_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.AuthSessionTable, entity.AuthSessionColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.AuthSession, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.AuthSession
			if err := rows.Scan(&item.SessionID, &item.UserID, &item.TenantID, &item.Realm, &item.RefreshTokenHash, &item.RefreshGeneration, &item.IssuedAt, &item.ExpiresAt, &item.LastSeenAt, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *AuthSessionRepo) FindByExpiresAtForUpdate(ctx context.Context, expiresAt time.Time, limit int, offset int) ([]entity.AuthSession, error) {
	where := storage.NewWhere()
	where = where.Eq("expires_at", expiresAt)
	where = where.OrderBy("session_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.AuthSessionTable, entity.AuthSessionColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	out := make([]entity.AuthSession, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.AuthSession
			if err := rows.Scan(&item.SessionID, &item.UserID, &item.TenantID, &item.Realm, &item.RefreshTokenHash, &item.RefreshGeneration, &item.IssuedAt, &item.ExpiresAt, &item.LastSeenAt, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *AuthSessionRepo) FindByExpiresAtForUpdateSkipLocked(ctx context.Context, expiresAt time.Time, limit int, offset int) ([]entity.AuthSession, error) {
	where := storage.NewWhere()
	where = where.Eq("expires_at", expiresAt)
	where = where.OrderBy("session_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.AuthSessionTable, entity.AuthSessionColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	out := make([]entity.AuthSession, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.AuthSession
			if err := rows.Scan(&item.SessionID, &item.UserID, &item.TenantID, &item.Realm, &item.RefreshTokenHash, &item.RefreshGeneration, &item.IssuedAt, &item.ExpiresAt, &item.LastSeenAt, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *AuthSessionRepo) CountByExpiresAt(ctx context.Context, expiresAt time.Time) (int64, error) {
	where := storage.NewWhere()
	where = where.Eq("expires_at", expiresAt)
	query, args, err := storage.CountWhere(entity.AuthSessionTable, where, r.dialect)
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

func (r *AuthSessionRepo) FindByIDs(ctx context.Context, ids []string) ([]entity.AuthSession, error) {
	if len(ids) == 0 {
		return []entity.AuthSession{}, nil
	}
	values := make([]any, 0, len(ids))
	for _, id := range ids {
		values = append(values, id)
	}
	where := storage.NewWhere().In("session_id", values...).OrderBy("session_id")
	query, args, err := storage.SelectWhere(entity.AuthSessionTable, entity.AuthSessionColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	found := make(map[string]entity.AuthSession, len(ids))
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.AuthSession
			if err := rows.Scan(&item.SessionID, &item.UserID, &item.TenantID, &item.Realm, &item.RefreshTokenHash, &item.RefreshGeneration, &item.IssuedAt, &item.ExpiresAt, &item.LastSeenAt, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			found[item.SessionID] = item
		}
		return nil
	}, args...); err != nil {
		return nil, err
	}
	out := make([]entity.AuthSession, 0, len(found))
	for _, id := range ids {
		if item, ok := found[id]; ok {
			out = append(out, item)
		}
	}
	return out, nil
}

func (r *AuthSessionRepo) UpsertByRefreshTokenHash(ctx context.Context, in *entity.AuthSession) error {
	if in == nil {
		return errors.New("authSession is nil")
	}
	query, err := storage.Upsert(entity.AuthSessionTable, []string{"session_id", "user_id", "tenant_id", "realm", "refresh_token_hash", "refresh_generation", "issued_at", "expires_at", "last_seen_at", "revoked_at"}, []string{"refresh_token_hash"}, []string{"user_id", "tenant_id", "realm", "refresh_generation", "issued_at", "expires_at", "last_seen_at", "revoked_at"}, r.dialect)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx, query, in.SessionID, in.UserID, in.TenantID, in.Realm, in.RefreshTokenHash, in.RefreshGeneration, in.IssuedAt, in.ExpiresAt, in.LastSeenAt, in.RevokedAt)
	return err
}

func (r *AuthSessionRepo) FindOneForUpdate(ctx context.Context, sessionID string) (*entity.AuthSession, error) {
	query, err := storage.SelectForUpdate(entity.AuthSessionTable, entity.AuthSessionColumns, "session_id", r.dialect, false)
	if err != nil {
		return nil, err
	}
	var out entity.AuthSession
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.SessionID, &out.UserID, &out.TenantID, &out.Realm, &out.RefreshTokenHash, &out.RefreshGeneration, &out.IssuedAt, &out.ExpiresAt, &out.LastSeenAt, &out.RevokedAt, &out.CreatedAt, &out.UpdatedAt)
	}, sessionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *AuthSessionRepo) FindOneForUpdateSkipLocked(ctx context.Context, sessionID string) (*entity.AuthSession, error) {
	query, err := storage.SelectForUpdate(entity.AuthSessionTable, entity.AuthSessionColumns, "session_id", r.dialect, true)
	if err != nil {
		return nil, err
	}
	var out entity.AuthSession
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.SessionID, &out.UserID, &out.TenantID, &out.Realm, &out.RefreshTokenHash, &out.RefreshGeneration, &out.IssuedAt, &out.ExpiresAt, &out.LastSeenAt, &out.RevokedAt, &out.CreatedAt, &out.UpdatedAt)
	}, sessionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *AuthSessionRepo) FindByRefreshTokenHashForUpdate(ctx context.Context, refreshTokenHash string) (*entity.AuthSession, error) {
	where := storage.NewWhere()
	where = where.Eq("refresh_token_hash", refreshTokenHash)
	query, args, err := storage.SelectWhere(entity.AuthSessionTable, entity.AuthSessionColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	var out entity.AuthSession
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.SessionID, &out.UserID, &out.TenantID, &out.Realm, &out.RefreshTokenHash, &out.RefreshGeneration, &out.IssuedAt, &out.ExpiresAt, &out.LastSeenAt, &out.RevokedAt, &out.CreatedAt, &out.UpdatedAt)
	}, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *AuthSessionRepo) FindByRefreshTokenHashForUpdateSkipLocked(ctx context.Context, refreshTokenHash string) (*entity.AuthSession, error) {
	where := storage.NewWhere()
	where = where.Eq("refresh_token_hash", refreshTokenHash)
	query, args, err := storage.SelectWhere(entity.AuthSessionTable, entity.AuthSessionColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	var out entity.AuthSession
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.SessionID, &out.UserID, &out.TenantID, &out.Realm, &out.RefreshTokenHash, &out.RefreshGeneration, &out.IssuedAt, &out.ExpiresAt, &out.LastSeenAt, &out.RevokedAt, &out.CreatedAt, &out.UpdatedAt)
	}, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *AuthSessionRepo) InsertMany(ctx context.Context, items []*entity.AuthSession) error {
	if len(items) == 0 {
		return nil
	}
	args := make([]any, 0, len(items)*len(entity.AuthSessionColumns))
	rows := 0
	for _, item := range items {
		if item == nil {
			continue
		}
		args = append(args, item.SessionID, item.UserID, item.TenantID, item.Realm, item.RefreshTokenHash, item.RefreshGeneration, item.IssuedAt, item.ExpiresAt, item.LastSeenAt, item.RevokedAt)
		rows++
	}
	if rows == 0 {
		return nil
	}
	query, err := storage.BatchInsert(entity.AuthSessionTable, []string{"session_id", "user_id", "tenant_id", "realm", "refresh_token_hash", "refresh_generation", "issued_at", "expires_at", "last_seen_at", "revoked_at"}, rows, r.dialect)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx, query, args...)
	return err
}

func (r *AuthSessionRepo) UpdateMany(ctx context.Context, items []*entity.AuthSession) error {
	for _, item := range items {
		if err := r.Update(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func (r *AuthSessionRepo) DeleteMany(ctx context.Context, ids ...string) error {
	for _, id := range ids {
		if err := r.Delete(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (r *AuthSessionRepo) UpdateFields(ctx context.Context, sessionID string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	allowed := map[string]struct{}{"user_id": {}, "tenant_id": {}, "realm": {}, "refresh_token_hash": {}, "refresh_generation": {}, "issued_at": {}, "expires_at": {}, "last_seen_at": {}, "revoked_at": {}}
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
	args = append(args, sessionID)
	query := "UPDATE " + entity.AuthSessionTable + " SET " + strings.Join(setParts, ", ") + " WHERE session_id = " + storage.Placeholder(r.dialect, idx)
	_, err := r.exec(ctx, query, args...)
	return err
}

func (r *AuthSessionRepo) ListAfter(ctx context.Context, after string, limit int) ([]entity.AuthSession, error) {
	columns, err := storage.JoinIdentifiers(entity.AuthSessionColumns)
	if err != nil {
		return nil, err
	}
	query := "SELECT " + columns + " FROM " + entity.AuthSessionTable + " WHERE session_id > " + storage.Placeholder(r.dialect, 1) + " ORDER BY session_id ASC LIMIT " + storage.Placeholder(r.dialect, 2)
	out := make([]entity.AuthSession, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.AuthSession
			if err := rows.Scan(&item.SessionID, &item.UserID, &item.TenantID, &item.Realm, &item.RefreshTokenHash, &item.RefreshGeneration, &item.IssuedAt, &item.ExpiresAt, &item.LastSeenAt, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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
