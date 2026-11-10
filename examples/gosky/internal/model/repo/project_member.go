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

type ProjectMemberRepo struct {
	store   *storage.SQLStore
	cluster *storage.Cluster
	tx      *sql.Tx
	dialect storage.Dialect
}

func NewProjectMemberRepo(store *storage.SQLStore, dialect ...storage.Dialect) *ProjectMemberRepo {
	d := storage.DialectQuestion
	if len(dialect) > 0 {
		d = dialect[0]
	}
	return &ProjectMemberRepo{store: store, dialect: d}
}

func NewProjectMemberRepoWithCluster(cluster *storage.Cluster, dialect ...storage.Dialect) *ProjectMemberRepo {
	d := storage.DialectQuestion
	if len(dialect) > 0 {
		d = dialect[0]
	}
	var store *storage.SQLStore
	if cluster != nil {
		store = cluster.Writer()
	}
	return &ProjectMemberRepo{store: store, cluster: cluster, dialect: d}
}

func (r *ProjectMemberRepo) WithTx(tx *sql.Tx) *ProjectMemberRepo {
	if r == nil {
		return nil
	}
	clone := *r
	clone.tx = tx
	return &clone
}

func (r *ProjectMemberRepo) Transact(ctx context.Context, opts *sql.TxOptions, fn func(context.Context, *ProjectMemberRepo) error) error {
	if r == nil {
		return errors.New("projectMember repo is nil")
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
		return errors.New("projectMember repo store is nil")
	}
	return r.store.Transact(ctx, opts, func(ctx context.Context, tx *sql.Tx) error {
		return fn(ctx, r.WithTx(tx))
	})
}

func (r *ProjectMemberRepo) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if r == nil {
		return nil, errors.New("projectMember repo is nil")
	}
	if r.tx != nil {
		return r.tx.ExecContext(ctx, query, args...)
	}
	if r.cluster != nil {
		store := r.cluster.Writer()
		if store == nil {
			return nil, errors.New("projectMember repo cluster writer is nil")
		}
		return store.Exec(ctx, query, args...)
	}
	if r.store == nil {
		return nil, errors.New("projectMember repo store is nil")
	}
	return r.store.Exec(ctx, query, args...)
}

func (r *ProjectMemberRepo) queryOne(ctx context.Context, query string, scan func(*sql.Row) error, args ...any) error {
	if r == nil {
		return errors.New("projectMember repo is nil")
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
			return errors.New("projectMember repo cluster reader is nil")
		}
		return store.QueryOne(ctx, query, scan, args...)
	}
	if r.store == nil {
		return errors.New("projectMember repo store is nil")
	}
	return r.store.QueryOne(ctx, query, scan, args...)
}

func (r *ProjectMemberRepo) queryAll(ctx context.Context, query string, scan func(*sql.Rows) error, args ...any) error {
	if r == nil {
		return errors.New("projectMember repo is nil")
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
			return errors.New("projectMember repo cluster reader is nil")
		}
		return store.QueryAll(ctx, query, scan, args...)
	}
	if r.store == nil {
		return errors.New("projectMember repo store is nil")
	}
	return r.store.QueryAll(ctx, query, scan, args...)
}

func (r *ProjectMemberRepo) TableName() string { return entity.ProjectMemberTable }

func (r *ProjectMemberRepo) Columns() []string {
	return append([]string(nil), entity.ProjectMemberColumns...)
}

func (r *ProjectMemberRepo) FindOne(ctx context.Context, projectMemberID int64) (*entity.ProjectMember, error) {
	query, err := storage.SelectByID(entity.ProjectMemberTable, entity.ProjectMemberColumns, "project_member_id", r.dialect)
	if err != nil {
		return nil, err
	}
	var out entity.ProjectMember
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.ProjectMemberID, &out.ProjectID, &out.ProjectTenantID, &out.MemberSubject, &out.MemberRole, &out.MemberState, &out.MemberMemo, &out.GrantedBy, &out.RevokedBy, &out.RevokedAt, &out.CreatedAt, &out.UpdatedAt)
	}, projectMemberID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *ProjectMemberRepo) InsertResult(ctx context.Context, in *entity.ProjectMember) (sql.Result, error) {
	if in == nil {
		return nil, errors.New("projectMember is nil")
	}
	query, err := storage.Insert(entity.ProjectMemberTable, []string{"project_id", "project_tenant_id", "member_subject", "member_role", "member_state", "member_memo", "granted_by", "revoked_by", "revoked_at"}, r.dialect)
	if err != nil {
		return nil, err
	}
	return r.exec(ctx, query, in.ProjectID, in.ProjectTenantID, in.MemberSubject, in.MemberRole, in.MemberState, in.MemberMemo, in.GrantedBy, in.RevokedBy, in.RevokedAt)
}

func (r *ProjectMemberRepo) Insert(ctx context.Context, in *entity.ProjectMember) error {
	_, err := r.InsertResult(ctx, in)
	return err
}

func (r *ProjectMemberRepo) Update(ctx context.Context, in *entity.ProjectMember) error {
	if in == nil {
		return errors.New("projectMember is nil")
	}
	query, err := storage.UpdateByID(entity.ProjectMemberTable, []string{"project_id", "project_tenant_id", "member_subject", "member_role", "member_state", "member_memo", "granted_by", "revoked_by", "revoked_at"}, "project_member_id", r.dialect)
	if err != nil {
		return err
	}
	if _, err := r.exec(ctx, query, in.ProjectID, in.ProjectTenantID, in.MemberSubject, in.MemberRole, in.MemberState, in.MemberMemo, in.GrantedBy, in.RevokedBy, in.RevokedAt, in.ProjectMemberID); err != nil {
		return err
	}
	return nil
}

func (r *ProjectMemberRepo) Delete(ctx context.Context, projectMemberID int64) error {
	query, err := storage.DeleteByID(entity.ProjectMemberTable, "project_member_id", r.dialect)
	if err != nil {
		return err
	}
	if _, err := r.exec(ctx, query, projectMemberID); err != nil {
		return err
	}
	return nil
}

func (r *ProjectMemberRepo) List(ctx context.Context, limit int, offset int) ([]entity.ProjectMember, error) {
	query, err := storage.SelectPage(entity.ProjectMemberTable, entity.ProjectMemberColumns, "project_member_id", r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.ProjectMember, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.ProjectMember
			if err := rows.Scan(&item.ProjectMemberID, &item.ProjectID, &item.ProjectTenantID, &item.MemberSubject, &item.MemberRole, &item.MemberState, &item.MemberMemo, &item.GrantedBy, &item.RevokedBy, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *ProjectMemberRepo) Count(ctx context.Context) (int64, error) {
	query, err := storage.CountAll(entity.ProjectMemberTable)
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

func (r *ProjectMemberRepo) FindWhere(ctx context.Context, where *storage.Where) ([]entity.ProjectMember, error) {
	query, args, err := storage.SelectWhere(entity.ProjectMemberTable, entity.ProjectMemberColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.ProjectMember, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.ProjectMember
			if err := rows.Scan(&item.ProjectMemberID, &item.ProjectID, &item.ProjectTenantID, &item.MemberSubject, &item.MemberRole, &item.MemberState, &item.MemberMemo, &item.GrantedBy, &item.RevokedBy, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *ProjectMemberRepo) CountWhere(ctx context.Context, where *storage.Where) (int64, error) {
	query, args, err := storage.CountWhere(entity.ProjectMemberTable, where, r.dialect)
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

func (r *ProjectMemberRepo) FindByProjectIDAndMemberSubject(ctx context.Context, projectID int64, memberSubject string) (*entity.ProjectMember, error) {
	where := storage.NewWhere()
	where = where.Eq("project_id", projectID)
	where = where.Eq("member_subject", memberSubject)
	where = where.Limit(1)
	query, args, err := storage.SelectWhere(entity.ProjectMemberTable, entity.ProjectMemberColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	var out entity.ProjectMember
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.ProjectMemberID, &out.ProjectID, &out.ProjectTenantID, &out.MemberSubject, &out.MemberRole, &out.MemberState, &out.MemberMemo, &out.GrantedBy, &out.RevokedBy, &out.RevokedAt, &out.CreatedAt, &out.UpdatedAt)
	}, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *ProjectMemberRepo) FindByMemberSubjectAndMemberState(ctx context.Context, memberSubject string, memberState string, limit int, offset int) ([]entity.ProjectMember, error) {
	where := storage.NewWhere()
	where = where.Eq("member_subject", memberSubject)
	where = where.Eq("member_state", memberState)
	where = where.OrderBy("project_id")
	where = where.OrderBy("project_member_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.ProjectMemberTable, entity.ProjectMemberColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.ProjectMember, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.ProjectMember
			if err := rows.Scan(&item.ProjectMemberID, &item.ProjectID, &item.ProjectTenantID, &item.MemberSubject, &item.MemberRole, &item.MemberState, &item.MemberMemo, &item.GrantedBy, &item.RevokedBy, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *ProjectMemberRepo) FindByMemberSubjectAndMemberStateForUpdate(ctx context.Context, memberSubject string, memberState string, limit int, offset int) ([]entity.ProjectMember, error) {
	where := storage.NewWhere()
	where = where.Eq("member_subject", memberSubject)
	where = where.Eq("member_state", memberState)
	where = where.OrderBy("project_id")
	where = where.OrderBy("project_member_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.ProjectMemberTable, entity.ProjectMemberColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	out := make([]entity.ProjectMember, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.ProjectMember
			if err := rows.Scan(&item.ProjectMemberID, &item.ProjectID, &item.ProjectTenantID, &item.MemberSubject, &item.MemberRole, &item.MemberState, &item.MemberMemo, &item.GrantedBy, &item.RevokedBy, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *ProjectMemberRepo) FindByMemberSubjectAndMemberStateForUpdateSkipLocked(ctx context.Context, memberSubject string, memberState string, limit int, offset int) ([]entity.ProjectMember, error) {
	where := storage.NewWhere()
	where = where.Eq("member_subject", memberSubject)
	where = where.Eq("member_state", memberState)
	where = where.OrderBy("project_id")
	where = where.OrderBy("project_member_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.ProjectMemberTable, entity.ProjectMemberColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	out := make([]entity.ProjectMember, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.ProjectMember
			if err := rows.Scan(&item.ProjectMemberID, &item.ProjectID, &item.ProjectTenantID, &item.MemberSubject, &item.MemberRole, &item.MemberState, &item.MemberMemo, &item.GrantedBy, &item.RevokedBy, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *ProjectMemberRepo) CountByMemberSubjectAndMemberState(ctx context.Context, memberSubject string, memberState string) (int64, error) {
	where := storage.NewWhere()
	where = where.Eq("member_subject", memberSubject)
	where = where.Eq("member_state", memberState)
	query, args, err := storage.CountWhere(entity.ProjectMemberTable, where, r.dialect)
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

func (r *ProjectMemberRepo) FindByProjectIDAndMemberState(ctx context.Context, projectID int64, memberState string, limit int, offset int) ([]entity.ProjectMember, error) {
	where := storage.NewWhere()
	where = where.Eq("project_id", projectID)
	where = where.Eq("member_state", memberState)
	where = where.OrderBy("member_role")
	where = where.OrderBy("project_member_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.ProjectMemberTable, entity.ProjectMemberColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.ProjectMember, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.ProjectMember
			if err := rows.Scan(&item.ProjectMemberID, &item.ProjectID, &item.ProjectTenantID, &item.MemberSubject, &item.MemberRole, &item.MemberState, &item.MemberMemo, &item.GrantedBy, &item.RevokedBy, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *ProjectMemberRepo) FindByProjectIDAndMemberStateForUpdate(ctx context.Context, projectID int64, memberState string, limit int, offset int) ([]entity.ProjectMember, error) {
	where := storage.NewWhere()
	where = where.Eq("project_id", projectID)
	where = where.Eq("member_state", memberState)
	where = where.OrderBy("member_role")
	where = where.OrderBy("project_member_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.ProjectMemberTable, entity.ProjectMemberColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	out := make([]entity.ProjectMember, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.ProjectMember
			if err := rows.Scan(&item.ProjectMemberID, &item.ProjectID, &item.ProjectTenantID, &item.MemberSubject, &item.MemberRole, &item.MemberState, &item.MemberMemo, &item.GrantedBy, &item.RevokedBy, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *ProjectMemberRepo) FindByProjectIDAndMemberStateForUpdateSkipLocked(ctx context.Context, projectID int64, memberState string, limit int, offset int) ([]entity.ProjectMember, error) {
	where := storage.NewWhere()
	where = where.Eq("project_id", projectID)
	where = where.Eq("member_state", memberState)
	where = where.OrderBy("member_role")
	where = where.OrderBy("project_member_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.ProjectMemberTable, entity.ProjectMemberColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	out := make([]entity.ProjectMember, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.ProjectMember
			if err := rows.Scan(&item.ProjectMemberID, &item.ProjectID, &item.ProjectTenantID, &item.MemberSubject, &item.MemberRole, &item.MemberState, &item.MemberMemo, &item.GrantedBy, &item.RevokedBy, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *ProjectMemberRepo) CountByProjectIDAndMemberState(ctx context.Context, projectID int64, memberState string) (int64, error) {
	where := storage.NewWhere()
	where = where.Eq("project_id", projectID)
	where = where.Eq("member_state", memberState)
	query, args, err := storage.CountWhere(entity.ProjectMemberTable, where, r.dialect)
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

func (r *ProjectMemberRepo) FindByProjectTenantIDAndMemberSubjectAndMemberState(ctx context.Context, projectTenantID int64, memberSubject string, memberState string, limit int, offset int) ([]entity.ProjectMember, error) {
	where := storage.NewWhere()
	where = where.Eq("project_tenant_id", projectTenantID)
	where = where.Eq("member_subject", memberSubject)
	where = where.Eq("member_state", memberState)
	where = where.OrderBy("project_id")
	where = where.OrderBy("project_member_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.ProjectMemberTable, entity.ProjectMemberColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.ProjectMember, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.ProjectMember
			if err := rows.Scan(&item.ProjectMemberID, &item.ProjectID, &item.ProjectTenantID, &item.MemberSubject, &item.MemberRole, &item.MemberState, &item.MemberMemo, &item.GrantedBy, &item.RevokedBy, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *ProjectMemberRepo) FindByProjectTenantIDAndMemberSubjectAndMemberStateForUpdate(ctx context.Context, projectTenantID int64, memberSubject string, memberState string, limit int, offset int) ([]entity.ProjectMember, error) {
	where := storage.NewWhere()
	where = where.Eq("project_tenant_id", projectTenantID)
	where = where.Eq("member_subject", memberSubject)
	where = where.Eq("member_state", memberState)
	where = where.OrderBy("project_id")
	where = where.OrderBy("project_member_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.ProjectMemberTable, entity.ProjectMemberColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	out := make([]entity.ProjectMember, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.ProjectMember
			if err := rows.Scan(&item.ProjectMemberID, &item.ProjectID, &item.ProjectTenantID, &item.MemberSubject, &item.MemberRole, &item.MemberState, &item.MemberMemo, &item.GrantedBy, &item.RevokedBy, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *ProjectMemberRepo) FindByProjectTenantIDAndMemberSubjectAndMemberStateForUpdateSkipLocked(ctx context.Context, projectTenantID int64, memberSubject string, memberState string, limit int, offset int) ([]entity.ProjectMember, error) {
	where := storage.NewWhere()
	where = where.Eq("project_tenant_id", projectTenantID)
	where = where.Eq("member_subject", memberSubject)
	where = where.Eq("member_state", memberState)
	where = where.OrderBy("project_id")
	where = where.OrderBy("project_member_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.ProjectMemberTable, entity.ProjectMemberColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	out := make([]entity.ProjectMember, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.ProjectMember
			if err := rows.Scan(&item.ProjectMemberID, &item.ProjectID, &item.ProjectTenantID, &item.MemberSubject, &item.MemberRole, &item.MemberState, &item.MemberMemo, &item.GrantedBy, &item.RevokedBy, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *ProjectMemberRepo) CountByProjectTenantIDAndMemberSubjectAndMemberState(ctx context.Context, projectTenantID int64, memberSubject string, memberState string) (int64, error) {
	where := storage.NewWhere()
	where = where.Eq("project_tenant_id", projectTenantID)
	where = where.Eq("member_subject", memberSubject)
	where = where.Eq("member_state", memberState)
	query, args, err := storage.CountWhere(entity.ProjectMemberTable, where, r.dialect)
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

func (r *ProjectMemberRepo) FindByProjectIDAndUpdatedAt(ctx context.Context, projectID int64, updatedAt time.Time, limit int, offset int) ([]entity.ProjectMember, error) {
	where := storage.NewWhere()
	where = where.Eq("project_id", projectID)
	where = where.Eq("updated_at", updatedAt)
	where = where.OrderBy("project_member_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.ProjectMemberTable, entity.ProjectMemberColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	out := make([]entity.ProjectMember, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.ProjectMember
			if err := rows.Scan(&item.ProjectMemberID, &item.ProjectID, &item.ProjectTenantID, &item.MemberSubject, &item.MemberRole, &item.MemberState, &item.MemberMemo, &item.GrantedBy, &item.RevokedBy, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *ProjectMemberRepo) FindByProjectIDAndUpdatedAtForUpdate(ctx context.Context, projectID int64, updatedAt time.Time, limit int, offset int) ([]entity.ProjectMember, error) {
	where := storage.NewWhere()
	where = where.Eq("project_id", projectID)
	where = where.Eq("updated_at", updatedAt)
	where = where.OrderBy("project_member_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.ProjectMemberTable, entity.ProjectMemberColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	out := make([]entity.ProjectMember, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.ProjectMember
			if err := rows.Scan(&item.ProjectMemberID, &item.ProjectID, &item.ProjectTenantID, &item.MemberSubject, &item.MemberRole, &item.MemberState, &item.MemberMemo, &item.GrantedBy, &item.RevokedBy, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *ProjectMemberRepo) FindByProjectIDAndUpdatedAtForUpdateSkipLocked(ctx context.Context, projectID int64, updatedAt time.Time, limit int, offset int) ([]entity.ProjectMember, error) {
	where := storage.NewWhere()
	where = where.Eq("project_id", projectID)
	where = where.Eq("updated_at", updatedAt)
	where = where.OrderBy("project_member_id")
	where = where.Limit(limit).Offset(offset)
	query, args, err := storage.SelectWhere(entity.ProjectMemberTable, entity.ProjectMemberColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	out := make([]entity.ProjectMember, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.ProjectMember
			if err := rows.Scan(&item.ProjectMemberID, &item.ProjectID, &item.ProjectTenantID, &item.MemberSubject, &item.MemberRole, &item.MemberState, &item.MemberMemo, &item.GrantedBy, &item.RevokedBy, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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

func (r *ProjectMemberRepo) CountByProjectIDAndUpdatedAt(ctx context.Context, projectID int64, updatedAt time.Time) (int64, error) {
	where := storage.NewWhere()
	where = where.Eq("project_id", projectID)
	where = where.Eq("updated_at", updatedAt)
	query, args, err := storage.CountWhere(entity.ProjectMemberTable, where, r.dialect)
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

func (r *ProjectMemberRepo) FindByIDs(ctx context.Context, ids []int64) ([]entity.ProjectMember, error) {
	if len(ids) == 0 {
		return []entity.ProjectMember{}, nil
	}
	values := make([]any, 0, len(ids))
	for _, id := range ids {
		values = append(values, id)
	}
	where := storage.NewWhere().In("project_member_id", values...).OrderBy("project_member_id")
	query, args, err := storage.SelectWhere(entity.ProjectMemberTable, entity.ProjectMemberColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	found := make(map[int64]entity.ProjectMember, len(ids))
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.ProjectMember
			if err := rows.Scan(&item.ProjectMemberID, &item.ProjectID, &item.ProjectTenantID, &item.MemberSubject, &item.MemberRole, &item.MemberState, &item.MemberMemo, &item.GrantedBy, &item.RevokedBy, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			found[item.ProjectMemberID] = item
		}
		return nil
	}, args...); err != nil {
		return nil, err
	}
	out := make([]entity.ProjectMember, 0, len(found))
	for _, id := range ids {
		if item, ok := found[id]; ok {
			out = append(out, item)
		}
	}
	return out, nil
}

func (r *ProjectMemberRepo) UpsertByProjectIDAndMemberSubject(ctx context.Context, in *entity.ProjectMember) error {
	if in == nil {
		return errors.New("projectMember is nil")
	}
	query, err := storage.Upsert(entity.ProjectMemberTable, []string{"project_id", "project_tenant_id", "member_subject", "member_role", "member_state", "member_memo", "granted_by", "revoked_by", "revoked_at"}, []string{"project_id", "member_subject"}, []string{"project_tenant_id", "member_role", "member_state", "member_memo", "granted_by", "revoked_by", "revoked_at"}, r.dialect)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx, query, in.ProjectID, in.ProjectTenantID, in.MemberSubject, in.MemberRole, in.MemberState, in.MemberMemo, in.GrantedBy, in.RevokedBy, in.RevokedAt)
	return err
}

func (r *ProjectMemberRepo) FindOneForUpdate(ctx context.Context, projectMemberID int64) (*entity.ProjectMember, error) {
	query, err := storage.SelectForUpdate(entity.ProjectMemberTable, entity.ProjectMemberColumns, "project_member_id", r.dialect, false)
	if err != nil {
		return nil, err
	}
	var out entity.ProjectMember
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.ProjectMemberID, &out.ProjectID, &out.ProjectTenantID, &out.MemberSubject, &out.MemberRole, &out.MemberState, &out.MemberMemo, &out.GrantedBy, &out.RevokedBy, &out.RevokedAt, &out.CreatedAt, &out.UpdatedAt)
	}, projectMemberID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *ProjectMemberRepo) FindOneForUpdateSkipLocked(ctx context.Context, projectMemberID int64) (*entity.ProjectMember, error) {
	query, err := storage.SelectForUpdate(entity.ProjectMemberTable, entity.ProjectMemberColumns, "project_member_id", r.dialect, true)
	if err != nil {
		return nil, err
	}
	var out entity.ProjectMember
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.ProjectMemberID, &out.ProjectID, &out.ProjectTenantID, &out.MemberSubject, &out.MemberRole, &out.MemberState, &out.MemberMemo, &out.GrantedBy, &out.RevokedBy, &out.RevokedAt, &out.CreatedAt, &out.UpdatedAt)
	}, projectMemberID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *ProjectMemberRepo) FindByProjectIDAndMemberSubjectForUpdate(ctx context.Context, projectID int64, memberSubject string) (*entity.ProjectMember, error) {
	where := storage.NewWhere()
	where = where.Eq("project_id", projectID)
	where = where.Eq("member_subject", memberSubject)
	query, args, err := storage.SelectWhere(entity.ProjectMemberTable, entity.ProjectMemberColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	var out entity.ProjectMember
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.ProjectMemberID, &out.ProjectID, &out.ProjectTenantID, &out.MemberSubject, &out.MemberRole, &out.MemberState, &out.MemberMemo, &out.GrantedBy, &out.RevokedBy, &out.RevokedAt, &out.CreatedAt, &out.UpdatedAt)
	}, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *ProjectMemberRepo) FindByProjectIDAndMemberSubjectForUpdateSkipLocked(ctx context.Context, projectID int64, memberSubject string) (*entity.ProjectMember, error) {
	where := storage.NewWhere()
	where = where.Eq("project_id", projectID)
	where = where.Eq("member_subject", memberSubject)
	query, args, err := storage.SelectWhere(entity.ProjectMemberTable, entity.ProjectMemberColumns, where, r.dialect)
	if err != nil {
		return nil, err
	}
	query += " FOR UPDATE"
	query += " SKIP LOCKED"
	var out entity.ProjectMember
	if err := r.queryOne(ctx, query, func(row *sql.Row) error {
		return row.Scan(&out.ProjectMemberID, &out.ProjectID, &out.ProjectTenantID, &out.MemberSubject, &out.MemberRole, &out.MemberState, &out.MemberMemo, &out.GrantedBy, &out.RevokedBy, &out.RevokedAt, &out.CreatedAt, &out.UpdatedAt)
	}, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *ProjectMemberRepo) InsertMany(ctx context.Context, items []*entity.ProjectMember) error {
	if len(items) == 0 {
		return nil
	}
	args := make([]any, 0, len(items)*len(entity.ProjectMemberColumns))
	rows := 0
	for _, item := range items {
		if item == nil {
			continue
		}
		args = append(args, item.ProjectID, item.ProjectTenantID, item.MemberSubject, item.MemberRole, item.MemberState, item.MemberMemo, item.GrantedBy, item.RevokedBy, item.RevokedAt)
		rows++
	}
	if rows == 0 {
		return nil
	}
	query, err := storage.BatchInsert(entity.ProjectMemberTable, []string{"project_id", "project_tenant_id", "member_subject", "member_role", "member_state", "member_memo", "granted_by", "revoked_by", "revoked_at"}, rows, r.dialect)
	if err != nil {
		return err
	}
	_, err = r.exec(ctx, query, args...)
	return err
}

func (r *ProjectMemberRepo) UpdateMany(ctx context.Context, items []*entity.ProjectMember) error {
	for _, item := range items {
		if err := r.Update(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func (r *ProjectMemberRepo) DeleteMany(ctx context.Context, ids ...int64) error {
	for _, id := range ids {
		if err := r.Delete(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (r *ProjectMemberRepo) UpdateFields(ctx context.Context, projectMemberID int64, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	allowed := map[string]struct{}{"project_id": {}, "project_tenant_id": {}, "member_subject": {}, "member_role": {}, "member_state": {}, "member_memo": {}, "granted_by": {}, "revoked_by": {}, "revoked_at": {}}
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
	args = append(args, projectMemberID)
	query := "UPDATE " + entity.ProjectMemberTable + " SET " + strings.Join(setParts, ", ") + " WHERE project_member_id = " + storage.Placeholder(r.dialect, idx)
	_, err := r.exec(ctx, query, args...)
	return err
}

func (r *ProjectMemberRepo) ListAfter(ctx context.Context, after int64, limit int) ([]entity.ProjectMember, error) {
	columns, err := storage.JoinIdentifiers(entity.ProjectMemberColumns)
	if err != nil {
		return nil, err
	}
	query := "SELECT " + columns + " FROM " + entity.ProjectMemberTable + " WHERE project_member_id > " + storage.Placeholder(r.dialect, 1) + " ORDER BY project_member_id ASC LIMIT " + storage.Placeholder(r.dialect, 2)
	out := make([]entity.ProjectMember, 0)
	if err := r.queryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var item entity.ProjectMember
			if err := rows.Scan(&item.ProjectMemberID, &item.ProjectID, &item.ProjectTenantID, &item.MemberSubject, &item.MemberRole, &item.MemberState, &item.MemberMemo, &item.GrantedBy, &item.RevokedBy, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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
