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

type TenantUserRepo struct {
	store   *storage.SQLStore
	cluster *storage.Cluster
	tx      *sql.Tx
	dialect storage.Dialect
}

func NewTenantUserRepo(store *storage.SQLStore, dialect ...storage.Dialect) *TenantUserRepo {
	d := storage.DialectQuestion
	if len(dialect) > 0 {
		d = dialect[0]
	}
	return &TenantUserRepo{store: store, dialect: d}
}

func NewTenantUserRepoWithCluster(cluster *storage.Cluster, dialect ...storage.Dialect) *TenantUserRepo {
	d := storage.DialectQuestion
	if len(dialect) > 0 {
		d = dialect[0]
	}
	var store *storage.SQLStore
	if cluster != nil {
		store = cluster.Writer()
	}
	return &TenantUserRepo{store: store, cluster: cluster, dialect: d}
}

func (r *TenantUserRepo) WithTx(tx *sql.Tx) *TenantUserRepo {
	if r == nil {
		return nil
	}
	clone := *r
	clone.tx = tx
	return &clone
}

func (r *TenantUserRepo) Transact(ctx context.Context, opts *sql.TxOptions, fn func(context.Context, *TenantUserRepo) error) error {
	if r == nil {
		return errors.New("tenantUser repo is nil")
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
		return errors.New("tenantUser repo store is nil")
	}
	return r.store.Transact(ctx, opts, func(ctx context.Context, tx *sql.Tx) error {
		return fn(ctx, r.WithTx(tx))
	})
}

func (r *TenantUserRepo) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if r == nil {
		return nil, errors.New("tenantUser repo is nil")
	}
	if r.tx != nil {
		return r.tx.ExecContext(ctx, query, args...)
	}
	if r.cluster != nil {
		store := r.cluster.Writer()
		if store == nil {
			return nil, errors.New("tenantUser repo cluster writer is nil")
		}
		return store.Exec(ctx, query, args...)
	}
	if r.store == nil {
		return nil, errors.New("tenantUser repo store is nil")
	}
	return r.store.Exec(ctx, query, args...)
}

func (r *TenantUserRepo) queryOne(ctx context.Context, query string, scan func(*sql.Row) error, args ...any) error {
	if r == nil {
		return errors.New("tenantUser repo is nil")
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
			return errors.New("tenantUser repo cluster reader is nil")
		}
		return store.QueryOne(ctx, query, scan, args...)
	}
	if r.store == nil {
		return errors.New("tenantUser repo store is nil")
	}
	return r.store.QueryOne(ctx, query, scan, args...)
}

func (r *TenantUserRepo) queryAll(ctx context.Context, query string, scan func(*sql.Rows) error, args ...any) error {
	if r == nil {
		return errors.New("tenantUser repo is nil")
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
			return errors.New("tenantUser repo cluster reader is nil")
		}
		return store.QueryAll(ctx, query, scan, args...)
	}
	if r.store == nil {
		return errors.New("tenantUser repo store is nil")
	}
	return r.store.QueryAll(ctx, query, scan, args...)
}

func (r *TenantUserRepo) TableName() string { return entity.TenantUserTable }

func (r *TenantUserRepo) Columns() []string {
	return append([]string(nil), entity.TenantUserColumns...)
}

func (r *TenantUserRepo) FindOne(ctx context.Context, tenantUserID int64) (*entity.TenantUser, error) {
	query, err := storage.SelectByID(entity.TenantUserTable, entity.TenantUserColumns, "tenant_user_id", r.dialect)
	if err != nil {
		return nil, err
	}
	var out entity.TenantUser
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.TenantUserID, &out.TenantID, &out.UserID, &out.MemberRealm, &out.MemberState, &out.DisplayName, &out.JoinedAt, &out.LeftAt, &out.CreatedAt, &out.UpdatedAt)
	}, tenantUserID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *TenantUserRepo) InsertResult(ctx context.Context, in *entity.TenantUser) (sql.Result, error) {
	if in == nil {
		return nil, errors.New("tenantUser is nil")
	}
	query, err := storage.Insert(entity.TenantUserTable, []string{"tenant_id", "user_id", "member_realm", "member_state", "display_name", "joined_at", "left_at"}, r.dialect)
	if err != nil {
		return nil, err
	}
	return r.exec(ctx, query, in.TenantID, in.UserID, in.MemberRealm, in.MemberState, in.DisplayName, in.JoinedAt, in.LeftAt)
}

func (r *TenantUserRepo) Insert(ctx context.Context, in *entity.TenantUser) error {
	_, err := r.InsertResult(ctx, in)
	return err
}

func (r *TenantUserRepo) Update(ctx context.Context, in *entity.TenantUser) error {
	if in == nil {
		return errors.New("tenantUser is nil")
	}
	query, err := storage.UpdateByID(entity.TenantUserTable, []string{"tenant_id", "user_id", "member_realm", "member_state", "display_name", "joined_at", "left_at"}, "tenant_user_id", r.dialect)
	if err != nil {
		return err
	}
	if _, err := r.exec(ctx, query, in.TenantID, in.UserID, in.MemberRealm, in.MemberState, in.DisplayName, in.JoinedAt, in.LeftAt, in.TenantUserID); err != nil {
		return err
	}
	return nil
}

func (r *TenantUserRepo) Delete(ctx context.Context, tenantUserID int64) error {
	query, err := storage.DeleteByID(entity.TenantUserTable, "tenant_user_id", r.dialect)
	if err != nil {
		return err
	}
	if _, err := r.exec(ctx, query, tenantUserID); err != nil {
		return err
	}
	return nil
}

func (r *TenantUserRepo) List(ctx context.Context, limit int, offset int) ([]entity.TenantUser, error) {
	query, err := storage.SelectPage(entity.TenantUserTable, entity.TenantUserColumns, "tenant_user_id", r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.TenantUser, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.TenantUser
			if err := rows.Scan(&item.TenantUserID, &item.TenantID, &item.UserID, &item.MemberRealm, &item.MemberState, &item.DisplayName, &item.JoinedAt, &item.LeftAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *TenantUserRepo) Count(ctx context.Context) (int64, error) {
	query, err := storage.CountAll(entity.TenantUserTable)
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

func (r *TenantUserRepo) FindWhere(ctx context.Context, where *storage.Where) ([]entity.TenantUser, error) {
	query, args, err := storage.SelectWhere(entity.TenantUserTable, entity.TenantUserColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.TenantUser, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.TenantUser
			if err := rows.Scan(&item.TenantUserID, &item.TenantID, &item.UserID, &item.MemberRealm, &item.MemberState, &item.DisplayName, &item.JoinedAt, &item.LeftAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *TenantUserRepo) CountWhere(ctx context.Context, where *storage.Where) (int64, error) {
	query, args, err := storage.CountWhere(entity.TenantUserTable, where, r.dialect)
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

func (r *TenantUserRepo) FindByTenantIDAndUserID(ctx context.Context, tenantID int64, userID int64) (*entity.TenantUser, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("user_id", userID)
	where = where.Limit(1)
	query, args, err := storage.SelectWhere(entity.TenantUserTable, entity.TenantUserColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	var out entity.TenantUser
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.TenantUserID, &out.TenantID, &out.UserID, &out.MemberRealm, &out.MemberState, &out.DisplayName, &out.JoinedAt, &out.LeftAt, &out.CreatedAt, &out.UpdatedAt)
	}, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *TenantUserRepo) FindByUserIDAndMemberState(ctx context.Context, userID int64, memberState string, limit int, offset int) ([]entity.TenantUser, error) {
	where := storage.NewWhere()
	where = where.Eq("user_id", userID)
	where = where.Eq("member_state", memberState)
	where = where.OrderBy("tenant_id")
	where = where.OrderBy("tenant_user_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.TenantUserTable, entity.TenantUserColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.TenantUser, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.TenantUser
			if err := rows.Scan(&item.TenantUserID, &item.TenantID, &item.UserID, &item.MemberRealm, &item.MemberState, &item.DisplayName, &item.JoinedAt, &item.LeftAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *TenantUserRepo) FindByUserIDAndMemberStateForUpdate(ctx context.Context, userID int64, memberState string, limit int, offset int) ([]entity.TenantUser, error) {
	where := storage.NewWhere()
	where = where.Eq("user_id", userID)
	where = where.Eq("member_state", memberState)
	where = where.OrderBy("tenant_id")
	where = where.OrderBy("tenant_user_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.TenantUserTable, entity.TenantUserColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	out := make([]entity.TenantUser, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.TenantUser
			if err := rows.Scan(&item.TenantUserID, &item.TenantID, &item.UserID, &item.MemberRealm, &item.MemberState, &item.DisplayName, &item.JoinedAt, &item.LeftAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *TenantUserRepo) FindByUserIDAndMemberStateForUpdateSkipLocked(ctx context.Context, userID int64, memberState string, limit int, offset int) ([]entity.TenantUser, error) {
	where := storage.NewWhere()
	where = where.Eq("user_id", userID)
	where = where.Eq("member_state", memberState)
	where = where.OrderBy("tenant_id")
	where = where.OrderBy("tenant_user_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.TenantUserTable, entity.TenantUserColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	out := make([]entity.TenantUser, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.TenantUser
			if err := rows.Scan(&item.TenantUserID, &item.TenantID, &item.UserID, &item.MemberRealm, &item.MemberState, &item.DisplayName, &item.JoinedAt, &item.LeftAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *TenantUserRepo) CountByUserIDAndMemberState(ctx context.Context, userID int64, memberState string) (int64, error) {
	where := storage.NewWhere()
	where = where.Eq("user_id", userID)
	where = where.Eq("member_state", memberState)
	query, args, err := storage.CountWhere(entity.TenantUserTable, where, r.dialect)
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

func (r *TenantUserRepo) FindByTenantIDAndMemberState(ctx context.Context, tenantID int64, memberState string, limit int, offset int) ([]entity.TenantUser, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("member_state", memberState)
	where = where.OrderBy("user_id")
	where = where.OrderBy("tenant_user_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.TenantUserTable, entity.TenantUserColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.TenantUser, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.TenantUser
			if err := rows.Scan(&item.TenantUserID, &item.TenantID, &item.UserID, &item.MemberRealm, &item.MemberState, &item.DisplayName, &item.JoinedAt, &item.LeftAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *TenantUserRepo) FindByTenantIDAndMemberStateForUpdate(ctx context.Context, tenantID int64, memberState string, limit int, offset int) ([]entity.TenantUser, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("member_state", memberState)
	where = where.OrderBy("user_id")
	where = where.OrderBy("tenant_user_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.TenantUserTable, entity.TenantUserColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	out := make([]entity.TenantUser, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.TenantUser
			if err := rows.Scan(&item.TenantUserID, &item.TenantID, &item.UserID, &item.MemberRealm, &item.MemberState, &item.DisplayName, &item.JoinedAt, &item.LeftAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *TenantUserRepo) FindByTenantIDAndMemberStateForUpdateSkipLocked(ctx context.Context, tenantID int64, memberState string, limit int, offset int) ([]entity.TenantUser, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("member_state", memberState)
	where = where.OrderBy("user_id")
	where = where.OrderBy("tenant_user_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.TenantUserTable, entity.TenantUserColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	out := make([]entity.TenantUser, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.TenantUser
			if err := rows.Scan(&item.TenantUserID, &item.TenantID, &item.UserID, &item.MemberRealm, &item.MemberState, &item.DisplayName, &item.JoinedAt, &item.LeftAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *TenantUserRepo) CountByTenantIDAndMemberState(ctx context.Context, tenantID int64, memberState string) (int64, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("member_state", memberState)
	query, args, err := storage.CountWhere(entity.TenantUserTable, where, r.dialect)
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

func (r *TenantUserRepo) FindByIDs(ctx context.Context, ids []int64) ([]entity.TenantUser, error) {
	if len(ids) == 0 {
		return []entity.TenantUser{}, nil
	}
	values := make([]any, 0, len(ids))
	for _, id := range ids {
		values = append(values, id)
	}
	where := storage.NewWhere().In("tenant_user_id", values...).OrderBy("tenant_user_id")
	query, args, err := storage.SelectWhere(entity.TenantUserTable, entity.TenantUserColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	found := make(map[int64]entity.TenantUser, len(ids))
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.TenantUser
			if err := rows.Scan(&item.TenantUserID, &item.TenantID, &item.UserID, &item.MemberRealm, &item.MemberState, &item.DisplayName, &item.JoinedAt, &item.LeftAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			found[item.TenantUserID] = item
		}
		return nil
	}, args...); err != nil {
		return nil, err
	}
	out := make([]entity.TenantUser, 0, len(found))
	for _, id := range ids {
		if item, ok := found[id]; ok {
			out = append(out, item)
		}
	}
	return out, nil
}

func (r *TenantUserRepo) UpsertByTenantIDAndUserID(ctx context.Context, in *entity.TenantUser) error {
	if in == nil {
		return errors.New("tenantUser is nil")
	}
	query, err := storage.Upsert(entity.TenantUserTable, []string{"tenant_id", "user_id", "member_realm", "member_state", "display_name", "joined_at", "left_at"}, []string{"tenant_id", "user_id"}, []string{"member_realm", "member_state", "display_name", "joined_at", "left_at"}, r.dialect)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx, query, in.TenantID, in.UserID, in.MemberRealm, in.MemberState, in.DisplayName, in.JoinedAt, in.LeftAt)
	return err
}

func (r *TenantUserRepo) FindOneForUpdate(ctx context.Context, tenantUserID int64) (*entity.TenantUser, error) {
	query, err := storage.SelectForUpdate(entity.TenantUserTable, entity.TenantUserColumns, "tenant_user_id", r.dialect, false)
	if err != nil {
		return nil, err
	}
	var out entity.TenantUser
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.TenantUserID, &out.TenantID, &out.UserID, &out.MemberRealm, &out.MemberState, &out.DisplayName, &out.JoinedAt, &out.LeftAt, &out.CreatedAt, &out.UpdatedAt)
	}, tenantUserID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *TenantUserRepo) FindOneForUpdateSkipLocked(ctx context.Context, tenantUserID int64) (*entity.TenantUser, error) {
	query, err := storage.SelectForUpdate(entity.TenantUserTable, entity.TenantUserColumns, "tenant_user_id", r.dialect, true)
	if err != nil {
		return nil, err
	}
	var out entity.TenantUser
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.TenantUserID, &out.TenantID, &out.UserID, &out.MemberRealm, &out.MemberState, &out.DisplayName, &out.JoinedAt, &out.LeftAt, &out.CreatedAt, &out.UpdatedAt)
	}, tenantUserID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *TenantUserRepo) FindByTenantIDAndUserIDForUpdate(ctx context.Context, tenantID int64, userID int64) (*entity.TenantUser, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("user_id", userID)
	query, args, err := storage.SelectWhere(entity.TenantUserTable, entity.TenantUserColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	var out entity.TenantUser
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.TenantUserID, &out.TenantID, &out.UserID, &out.MemberRealm, &out.MemberState, &out.DisplayName, &out.JoinedAt, &out.LeftAt, &out.CreatedAt, &out.UpdatedAt)
	}, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *TenantUserRepo) FindByTenantIDAndUserIDForUpdateSkipLocked(ctx context.Context, tenantID int64, userID int64) (*entity.TenantUser, error) {
	where := storage.NewWhere()
	where = where.Eq("tenant_id", tenantID)
	where = where.Eq("user_id", userID)
	query, args, err := storage.SelectWhere(entity.TenantUserTable, entity.TenantUserColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	var out entity.TenantUser
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.TenantUserID, &out.TenantID, &out.UserID, &out.MemberRealm, &out.MemberState, &out.DisplayName, &out.JoinedAt, &out.LeftAt, &out.CreatedAt, &out.UpdatedAt)
	}, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *TenantUserRepo) InsertMany(ctx context.Context, items []*entity.TenantUser) error {
	if len(items) == 0 {
		return nil
	}
	args := make([]any, 0, len(items)*len(entity.TenantUserColumns))
	rows := 0
	for _, item := range items {
		if item == nil {
			continue
		}
		args = append(args, item.TenantID, item.UserID, item.MemberRealm, item.MemberState, item.DisplayName, item.JoinedAt, item.LeftAt)
		rows++
	}
	if rows == 0 {
		return nil
	}
	query, err := storage.BatchInsert(entity.TenantUserTable, []string{"tenant_id", "user_id", "member_realm", "member_state", "display_name", "joined_at", "left_at"}, rows, r.dialect)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx, query, args...)
	return err
}

func (r *TenantUserRepo) UpdateMany(ctx context.Context, items []*entity.TenantUser) error {
	for _, item := range items {
		if err := r.Update(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func (r *TenantUserRepo) DeleteMany(ctx context.Context, ids ...int64) error {
	for _, id := range ids {
		if err := r.Delete(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (r *TenantUserRepo) UpdateFields(ctx context.Context, tenantUserID int64, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	allowed := map[string]struct{}{"tenant_id": {}, "user_id": {}, "member_realm": {}, "member_state": {}, "display_name": {}, "joined_at": {}, "left_at": {}}
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
	args = append(args, tenantUserID)
	query := "UPDATE " + entity.TenantUserTable + " SET " + strings.Join(setParts, ", ") + " WHERE tenant_user_id = " + storage.Placeholder(r.dialect, idx)
	_, err := r.exec(ctx, query, args...)
	return err
}

func (r *TenantUserRepo) ListAfter(ctx context.Context, after int64, limit int) ([]entity.TenantUser, error) {
	columns, err := storage.JoinIdentifiers(entity.TenantUserColumns)
	if err != nil {
		return nil, err
	}
	query := "SELECT " + columns + " FROM " + entity.TenantUserTable + " WHERE tenant_user_id > " + storage.Placeholder(r.dialect, 1) + " ORDER BY tenant_user_id ASC LIMIT " + storage.Placeholder(r.dialect, 2)
	out := make([]entity.TenantUser, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.TenantUser
			if err := rows.Scan(&item.TenantUserID, &item.TenantID, &item.UserID, &item.MemberRealm, &item.MemberState, &item.DisplayName, &item.JoinedAt, &item.LeftAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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
