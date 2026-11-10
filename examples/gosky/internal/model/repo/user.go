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

type UserRepo struct {
	store   *storage.SQLStore
	cluster *storage.Cluster
	tx      *sql.Tx
	dialect storage.Dialect
}

func NewUserRepo(store *storage.SQLStore, dialect ...storage.Dialect) *UserRepo {
	d := storage.DialectQuestion
	if len(dialect) > 0 {
		d = dialect[0]
	}
	return &UserRepo{store: store, dialect: d}
}

func NewUserRepoWithCluster(cluster *storage.Cluster, dialect ...storage.Dialect) *UserRepo {
	d := storage.DialectQuestion
	if len(dialect) > 0 {
		d = dialect[0]
	}
	var store *storage.SQLStore
	if cluster != nil {
		store = cluster.Writer()
	}
	return &UserRepo{store: store, cluster: cluster, dialect: d}
}

func (r *UserRepo) WithTx(tx *sql.Tx) *UserRepo {
	if r == nil {
		return nil
	}
	clone := *r
	clone.tx = tx
	return &clone
}

func (r *UserRepo) Transact(ctx context.Context, opts *sql.TxOptions, fn func(context.Context, *UserRepo) error) error {
	if r == nil {
		return errors.New("user repo is nil")
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
		return errors.New("user repo store is nil")
	}
	return r.store.Transact(ctx, opts, func(ctx context.Context, tx *sql.Tx) error {
		return fn(ctx, r.WithTx(tx))
	})
}

func (r *UserRepo) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if r == nil {
		return nil, errors.New("user repo is nil")
	}
	if r.tx != nil {
		return r.tx.ExecContext(ctx, query, args...)
	}
	if r.cluster != nil {
		store := r.cluster.Writer()
		if store == nil {
			return nil, errors.New("user repo cluster writer is nil")
		}
		return store.Exec(ctx, query, args...)
	}
	if r.store == nil {
		return nil, errors.New("user repo store is nil")
	}
	return r.store.Exec(ctx, query, args...)
}

func (r *UserRepo) queryOne(ctx context.Context, query string, scan func(*sql.Row) error, args ...any) error {
	if r == nil {
		return errors.New("user repo is nil")
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
			return errors.New("user repo cluster reader is nil")
		}
		return store.QueryOne(ctx, query, scan, args...)
	}
	if r.store == nil {
		return errors.New("user repo store is nil")
	}
	return r.store.QueryOne(ctx, query, scan, args...)
}

func (r *UserRepo) queryAll(ctx context.Context, query string, scan func(*sql.Rows) error, args ...any) error {
	if r == nil {
		return errors.New("user repo is nil")
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
			return errors.New("user repo cluster reader is nil")
		}
		return store.QueryAll(ctx, query, scan, args...)
	}
	if r.store == nil {
		return errors.New("user repo store is nil")
	}
	return r.store.QueryAll(ctx, query, scan, args...)
}

func (r *UserRepo) TableName() string { return entity.UserTable }

func (r *UserRepo) Columns() []string { return append([]string(nil), entity.UserColumns...) }

func (r *UserRepo) FindOne(ctx context.Context, userID int64) (*entity.User, error) {
	query, err := storage.SelectByID(entity.UserTable, entity.UserColumns, "user_id", r.dialect)
	if err != nil {
		return nil, err
	}
	var out entity.User
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.UserID, &out.Subject, &out.LoginName, &out.PasswordHash, &out.AccountType, &out.UserState, &out.DisplayName, &out.AvatarURL, &out.CreatedAt, &out.UpdatedAt)
	}, userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *UserRepo) InsertResult(ctx context.Context, in *entity.User) (sql.Result, error) {
	if in == nil {
		return nil, errors.New("user is nil")
	}
	query, err := storage.Insert(entity.UserTable, []string{"subject", "login_name", "password_hash", "account_type", "user_state", "display_name", "avatar_url"}, r.dialect)
	if err != nil {
		return nil, err
	}
	return r.exec(ctx, query, in.Subject, in.LoginName, in.PasswordHash, in.AccountType, in.UserState, in.DisplayName, in.AvatarURL)
}

func (r *UserRepo) Insert(ctx context.Context, in *entity.User) error {
	_, err := r.InsertResult(ctx, in)
	return err
}

func (r *UserRepo) Update(ctx context.Context, in *entity.User) error {
	if in == nil {
		return errors.New("user is nil")
	}
	query, err := storage.UpdateByID(entity.UserTable, []string{"subject", "login_name", "password_hash", "account_type", "user_state", "display_name", "avatar_url"}, "user_id", r.dialect)
	if err != nil {
		return err
	}
	if _, err := r.exec(ctx, query, in.Subject, in.LoginName, in.PasswordHash, in.AccountType, in.UserState, in.DisplayName, in.AvatarURL, in.UserID); err != nil {
		return err
	}
	return nil
}

func (r *UserRepo) Delete(ctx context.Context, userID int64) error {
	query, err := storage.DeleteByID(entity.UserTable, "user_id", r.dialect)
	if err != nil {
		return err
	}
	if _, err := r.exec(ctx, query, userID); err != nil {
		return err
	}
	return nil
}

func (r *UserRepo) List(ctx context.Context, limit int, offset int) ([]entity.User, error) {
	query, err := storage.SelectPage(entity.UserTable, entity.UserColumns, "user_id", r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.User, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.User
			if err := rows.Scan(&item.UserID, &item.Subject, &item.LoginName, &item.PasswordHash, &item.AccountType, &item.UserState, &item.DisplayName, &item.AvatarURL, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *UserRepo) Count(ctx context.Context) (int64, error) {
	query, err := storage.CountAll(entity.UserTable)
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

func (r *UserRepo) FindWhere(ctx context.Context, where *storage.Where) ([]entity.User, error) {
	query, args, err := storage.SelectWhere(entity.UserTable, entity.UserColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.User, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.User
			if err := rows.Scan(&item.UserID, &item.Subject, &item.LoginName, &item.PasswordHash, &item.AccountType, &item.UserState, &item.DisplayName, &item.AvatarURL, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *UserRepo) CountWhere(ctx context.Context, where *storage.Where) (int64, error) {
	query, args, err := storage.CountWhere(entity.UserTable, where, r.dialect)
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

func (r *UserRepo) FindBySubject(ctx context.Context, subject string) (*entity.User, error) {
	where := storage.NewWhere().Eq("subject", subject).Limit(1)
	query, args, err := storage.SelectWhere(entity.UserTable, entity.UserColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	var out entity.User
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.UserID, &out.Subject, &out.LoginName, &out.PasswordHash, &out.AccountType, &out.UserState, &out.DisplayName, &out.AvatarURL, &out.CreatedAt, &out.UpdatedAt)
	}, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *UserRepo) FindByLoginName(ctx context.Context, loginName string) (*entity.User, error) {
	where := storage.NewWhere().Eq("login_name", loginName).Limit(1)
	query, args, err := storage.SelectWhere(entity.UserTable, entity.UserColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	var out entity.User
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.UserID, &out.Subject, &out.LoginName, &out.PasswordHash, &out.AccountType, &out.UserState, &out.DisplayName, &out.AvatarURL, &out.CreatedAt, &out.UpdatedAt)
	}, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *UserRepo) FindByUserStateAndAccountType(ctx context.Context, userState string, accountType string, limit int, offset int) ([]entity.User, error) {
	where := storage.NewWhere()
	where = where.Eq("user_state", userState)
	where = where.Eq("account_type", accountType)
	where = where.OrderBy("user_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.UserTable, entity.UserColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.User, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.User
			if err := rows.Scan(&item.UserID, &item.Subject, &item.LoginName, &item.PasswordHash, &item.AccountType, &item.UserState, &item.DisplayName, &item.AvatarURL, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *UserRepo) FindByUserStateAndAccountTypeForUpdate(ctx context.Context, userState string, accountType string, limit int, offset int) ([]entity.User, error) {
	where := storage.NewWhere()
	where = where.Eq("user_state", userState)
	where = where.Eq("account_type", accountType)
	where = where.OrderBy("user_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.UserTable, entity.UserColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	out := make([]entity.User, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.User
			if err := rows.Scan(&item.UserID, &item.Subject, &item.LoginName, &item.PasswordHash, &item.AccountType, &item.UserState, &item.DisplayName, &item.AvatarURL, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *UserRepo) FindByUserStateAndAccountTypeForUpdateSkipLocked(ctx context.Context, userState string, accountType string, limit int, offset int) ([]entity.User, error) {
	where := storage.NewWhere()
	where = where.Eq("user_state", userState)
	where = where.Eq("account_type", accountType)
	where = where.OrderBy("user_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.UserTable, entity.UserColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	out := make([]entity.User, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.User
			if err := rows.Scan(&item.UserID, &item.Subject, &item.LoginName, &item.PasswordHash, &item.AccountType, &item.UserState, &item.DisplayName, &item.AvatarURL, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *UserRepo) CountByUserStateAndAccountType(ctx context.Context, userState string, accountType string) (int64, error) {
	where := storage.NewWhere()
	where = where.Eq("user_state", userState)
	where = where.Eq("account_type", accountType)
	query, args, err := storage.CountWhere(entity.UserTable, where, r.dialect)
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

func (r *UserRepo) FindByIDs(ctx context.Context, ids []int64) ([]entity.User, error) {
	if len(ids) == 0 {
		return []entity.User{}, nil
	}
	values := make([]any, 0, len(ids))
	for _, id := range ids {
		values = append(values, id)
	}
	where := storage.NewWhere().In("user_id", values...).OrderBy("user_id")
	query, args, err := storage.SelectWhere(entity.UserTable, entity.UserColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	found := make(map[int64]entity.User, len(ids))
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.User
			if err := rows.Scan(&item.UserID, &item.Subject, &item.LoginName, &item.PasswordHash, &item.AccountType, &item.UserState, &item.DisplayName, &item.AvatarURL, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			found[item.UserID] = item
		}
		return nil
	}, args...); err != nil {
		return nil, err
	}
	out := make([]entity.User, 0, len(found))
	for _, id := range ids {
		if item, ok := found[id]; ok {
			out = append(out, item)
		}
	}
	return out, nil
}

func (r *UserRepo) UpsertBySubject(ctx context.Context, in *entity.User) error {
	if in == nil {
		return errors.New("user is nil")
	}
	query, err := storage.Upsert(entity.UserTable, []string{"subject", "login_name", "password_hash", "account_type", "user_state", "display_name", "avatar_url"}, []string{"subject"}, []string{"login_name", "password_hash", "account_type", "user_state", "display_name", "avatar_url"}, r.dialect)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx, query, in.Subject, in.LoginName, in.PasswordHash, in.AccountType, in.UserState, in.DisplayName, in.AvatarURL)
	return err
}

func (r *UserRepo) UpsertByLoginName(ctx context.Context, in *entity.User) error {
	if in == nil {
		return errors.New("user is nil")
	}
	query, err := storage.Upsert(entity.UserTable, []string{"subject", "login_name", "password_hash", "account_type", "user_state", "display_name", "avatar_url"}, []string{"login_name"}, []string{"subject", "password_hash", "account_type", "user_state", "display_name", "avatar_url"}, r.dialect)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx, query, in.Subject, in.LoginName, in.PasswordHash, in.AccountType, in.UserState, in.DisplayName, in.AvatarURL)
	return err
}

func (r *UserRepo) FindOneForUpdate(ctx context.Context, userID int64) (*entity.User, error) {
	query, err := storage.SelectForUpdate(entity.UserTable, entity.UserColumns, "user_id", r.dialect, false)
	if err != nil {
		return nil, err
	}
	var out entity.User
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.UserID, &out.Subject, &out.LoginName, &out.PasswordHash, &out.AccountType, &out.UserState, &out.DisplayName, &out.AvatarURL, &out.CreatedAt, &out.UpdatedAt)
	}, userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *UserRepo) FindOneForUpdateSkipLocked(ctx context.Context, userID int64) (*entity.User, error) {
	query, err := storage.SelectForUpdate(entity.UserTable, entity.UserColumns, "user_id", r.dialect, true)
	if err != nil {
		return nil, err
	}
	var out entity.User
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.UserID, &out.Subject, &out.LoginName, &out.PasswordHash, &out.AccountType, &out.UserState, &out.DisplayName, &out.AvatarURL, &out.CreatedAt, &out.UpdatedAt)
	}, userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *UserRepo) FindBySubjectForUpdate(ctx context.Context, subject string) (*entity.User, error) {
	where := storage.NewWhere()
	where = where.Eq("subject", subject)
	query, args, err := storage.SelectWhere(entity.UserTable, entity.UserColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	var out entity.User
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.UserID, &out.Subject, &out.LoginName, &out.PasswordHash, &out.AccountType, &out.UserState, &out.DisplayName, &out.AvatarURL, &out.CreatedAt, &out.UpdatedAt)
	}, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *UserRepo) FindBySubjectForUpdateSkipLocked(ctx context.Context, subject string) (*entity.User, error) {
	where := storage.NewWhere()
	where = where.Eq("subject", subject)
	query, args, err := storage.SelectWhere(entity.UserTable, entity.UserColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	var out entity.User
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.UserID, &out.Subject, &out.LoginName, &out.PasswordHash, &out.AccountType, &out.UserState, &out.DisplayName, &out.AvatarURL, &out.CreatedAt, &out.UpdatedAt)
	}, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *UserRepo) FindByLoginNameForUpdate(ctx context.Context, loginName string) (*entity.User, error) {
	where := storage.NewWhere()
	where = where.Eq("login_name", loginName)
	query, args, err := storage.SelectWhere(entity.UserTable, entity.UserColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	var out entity.User
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.UserID, &out.Subject, &out.LoginName, &out.PasswordHash, &out.AccountType, &out.UserState, &out.DisplayName, &out.AvatarURL, &out.CreatedAt, &out.UpdatedAt)
	}, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *UserRepo) FindByLoginNameForUpdateSkipLocked(ctx context.Context, loginName string) (*entity.User, error) {
	where := storage.NewWhere()
	where = where.Eq("login_name", loginName)
	query, args, err := storage.SelectWhere(entity.UserTable, entity.UserColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	var out entity.User
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.UserID, &out.Subject, &out.LoginName, &out.PasswordHash, &out.AccountType, &out.UserState, &out.DisplayName, &out.AvatarURL, &out.CreatedAt, &out.UpdatedAt)
	}, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *UserRepo) InsertMany(ctx context.Context, items []*entity.User) error {
	if len(items) == 0 {
		return nil
	}
	args := make([]any, 0, len(items)*len(entity.UserColumns))
	rows := 0
	for _, item := range items {
		if item == nil {
			continue
		}
		args = append(args, item.Subject, item.LoginName, item.PasswordHash, item.AccountType, item.UserState, item.DisplayName, item.AvatarURL)
		rows++
	}
	if rows == 0 {
		return nil
	}
	query, err := storage.BatchInsert(entity.UserTable, []string{"subject", "login_name", "password_hash", "account_type", "user_state", "display_name", "avatar_url"}, rows, r.dialect)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx, query, args...)
	return err
}

func (r *UserRepo) UpdateMany(ctx context.Context, items []*entity.User) error {
	for _, item := range items {
		if err := r.Update(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func (r *UserRepo) DeleteMany(ctx context.Context, ids ...int64) error {
	for _, id := range ids {
		if err := r.Delete(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (r *UserRepo) UpdateFields(ctx context.Context, userID int64, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	allowed := map[string]struct{}{"subject": {}, "login_name": {}, "password_hash": {}, "account_type": {}, "user_state": {}, "display_name": {}, "avatar_url": {}}
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
	args = append(args, userID)
	query := "UPDATE " + entity.UserTable + " SET " + strings.Join(setParts, ", ") + " WHERE user_id = " + storage.Placeholder(r.dialect, idx)
	_, err := r.exec(ctx, query, args...)
	return err
}

func (r *UserRepo) ListAfter(ctx context.Context, after int64, limit int) ([]entity.User, error) {
	columns, err := storage.JoinIdentifiers(entity.UserColumns)
	if err != nil {
		return nil, err
	}
	query := "SELECT " + columns + " FROM " + entity.UserTable + " WHERE user_id > " + storage.Placeholder(r.dialect, 1) + " ORDER BY user_id ASC LIMIT " + storage.Placeholder(r.dialect, 2)
	out := make([]entity.User, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.User
			if err := rows.Scan(&item.UserID, &item.Subject, &item.LoginName, &item.PasswordHash, &item.AccountType, &item.UserState, &item.DisplayName, &item.AvatarURL, &item.CreatedAt, &item.UpdatedAt); err != nil {
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
