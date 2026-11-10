package repo

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/imajinyun/gofly/core/storage"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
)

var _ app.FileStore = (*FileRepository)(nil)

// FileRepository persists support_files metadata. All values are SQL
// parameters; the uploader subject is resolved to users rows in the same
// tenant and never taken from request data.
type FileRepository struct {
	store *storage.SQLStore
}

func NewFileRepository(store *storage.SQLStore) *FileRepository {
	return &FileRepository{store: store}
}

const fileColumns = `file_id, tenant_id, folder_type, file_name, file_key, file_size, file_type,
       creator_id, creator_name, created_at, updated_at`

func scanFile(scanner interface {
	Scan(dest ...any) error
}, record *app.FileRecord) error {
	var tenantID uint64
	if err := scanner.Scan(
		&record.FileID, &tenantID, &record.FolderType, &record.FileName, &record.FileKey,
		&record.FileSize, &record.FileType, &record.CreatorID, &record.CreatorName,
		&record.CreatedAt, &record.UpdatedAt,
	); err != nil {
		return err
	}
	record.TenantIDValue = tenantID
	return nil
}

// Create inserts metadata with creator resolved from the verified subject
// via an active same-tenant membership.
func (r *FileRepository) Create(ctx context.Context, tenantID uint64, record app.FileRecord, subject string) (app.FileRecord, error) {
	query := `INSERT INTO support_files
(tenant_id, folder_type, file_name, file_key, file_size, file_type, creator_id, creator_name)
SELECT ?, ?, ?, ?, ?, ?, u.user_id, COALESCE(NULLIF(tu.display_name, ''), u.display_name)
FROM users AS u
JOIN tenant_users AS tu ON tu.user_id = u.user_id
WHERE tu.tenant_id = ? AND u.subject = ?
  AND u.user_state = 'active' AND tu.member_state = 'active'`
	result, err := r.store.Exec(ctx, query,
		tenantID, record.FolderType, record.FileName, record.FileKey, record.FileSize, record.FileType,
		tenantID, subject)
	if err != nil {
		return app.FileRecord{}, fmt.Errorf("insert file metadata: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return app.FileRecord{}, err
	}
	if affected == 0 {
		return app.FileRecord{}, sql.ErrNoRows
	}
	id, err := result.LastInsertId()
	if err != nil {
		return app.FileRecord{}, err
	}
	return r.get(ctx, tenantID, record.FileKey, id)
}

func (r *FileRepository) get(ctx context.Context, tenantID uint64, key string, id int64) (app.FileRecord, error) {
	query := `SELECT ` + fileColumns + ` FROM support_files WHERE tenant_id = ? AND file_key = ? AND file_id = ?`
	rows, err := r.store.QueryRows(ctx, query, tenantID, key, id)
	if err != nil {
		return app.FileRecord{}, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return app.FileRecord{}, sql.ErrNoRows
	}
	var record app.FileRecord
	if err := scanFile(rows, &record); err != nil {
		return app.FileRecord{}, err
	}
	return record, rows.Err()
}

// Get returns one tenant-owned file by storage key.
func (r *FileRepository) Get(ctx context.Context, tenantID uint64, fileKey string) (app.FileRecord, error) {
	query := `SELECT ` + fileColumns + ` FROM support_files WHERE tenant_id = ? AND file_key = ?`
	rows, err := r.store.QueryRows(ctx, query, tenantID, fileKey)
	if err != nil {
		return app.FileRecord{}, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return app.FileRecord{}, sql.ErrNoRows
	}
	var record app.FileRecord
	err = scanFile(rows, &record)
	return record, err
}

// GetByKey resolves a key across tenants for signed-URL verification. The
// unguessable HMAC-signed URL proves authorization; callers must verify it.
func (r *FileRepository) GetByKey(ctx context.Context, fileKey string) (app.FileRecord, error) {
	rows, err := r.store.QueryRows(ctx,
		`SELECT `+fileColumns+` FROM support_files WHERE file_key = ?`, fileKey)
	if err != nil {
		return app.FileRecord{}, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return app.FileRecord{}, sql.ErrNoRows
	}
	var record app.FileRecord
	if err := scanFile(rows, &record); err != nil {
		return app.FileRecord{}, err
	}
	return record, rows.Err()
}

// List applies the console filters and returns one bounded page plus total.
func (r *FileRepository) List(ctx context.Context, tenantID uint64, query app.FileQuery) (app.FilePage, error) {
	where := []string{"tenant_id = ?"}
	args := []any{tenantID}
	if query.FolderType != nil {
		where = append(where, "folder_type = ?")
		args = append(args, *query.FolderType)
	}
	if value := strings.TrimSpace(query.FileName); value != "" {
		where = append(where, "file_name LIKE ?")
		args = append(args, "%"+value+"%")
	}
	if value := strings.TrimSpace(query.FileKey); value != "" {
		where = append(where, "file_key LIKE ?")
		args = append(args, "%"+value+"%")
	}
	if value := strings.TrimSpace(query.FileType); value != "" {
		where = append(where, "file_type LIKE ?")
		args = append(args, "%"+value+"%")
	}
	if value := strings.TrimSpace(query.CreatorName); value != "" {
		where = append(where, "creator_name LIKE ?")
		args = append(args, "%"+value+"%")
	}
	if value := strings.TrimSpace(query.CreateTimeBegin); value != "" {
		where = append(where, "created_at >= ?")
		args = append(args, value)
	}
	if value := strings.TrimSpace(query.CreateTimeEnd); value != "" {
		where = append(where, "created_at < DATE_ADD(?, INTERVAL 1 DAY)")
		args = append(args, value)
	}
	filter := strings.Join(where, " AND ")

	var total int
	countRows, err := r.store.QueryRows(ctx, "SELECT COUNT(*) FROM support_files WHERE "+filter, args...)
	if err != nil {
		return app.FilePage{}, err
	}
	if countRows.Next() {
		if err := countRows.Scan(&total); err != nil {
			_ = countRows.Close()
			return app.FilePage{}, err
		}
	}
	_ = countRows.Close()

	pageNum, pageSize := query.PageNum, query.PageSize
	listQuery := `SELECT ` + fileColumns + ` FROM support_files WHERE ` + filter +
		` ORDER BY file_id DESC LIMIT ? OFFSET ?`
	listArgs := append(append([]any{}, args...), pageSize, (pageNum-1)*pageSize)
	rows, err := r.store.QueryRows(ctx, listQuery, listArgs...)
	if err != nil {
		return app.FilePage{}, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]app.FileRecord, 0, pageSize)
	for rows.Next() {
		var record app.FileRecord
		if scanErr := scanFile(rows, &record); scanErr != nil {
			return app.FilePage{}, scanErr
		}
		items = append(items, record)
	}
	return app.FilePage{List: items, Total: total}, rows.Err()
}
