package repo

import (
	"context"
	"database/sql"
	"strings"

	"github.com/imajinyun/gofly/core/storage"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
)

var _ app.OperateLogStore = (*OperateLogRepository)(nil)

// OperateLogRepository persists the append-only operation audit trail.
type OperateLogRepository struct {
	store *storage.SQLStore
}

func NewOperateLogRepository(store *storage.SQLStore) *OperateLogRepository {
	return &OperateLogRepository{store: store}
}

func (r *OperateLogRepository) InsertOperateLog(ctx context.Context, entry app.OperateLog) error {
	var userID any
	if entry.OperateUserID != nil {
		userID = *entry.OperateUserID
	}
	_, err := r.store.Exec(ctx,
		`INSERT INTO support_operate_logs
(tenant_id, operate_user_id, operate_user_type, operate_user_name, module, content,
url, method, param, response, ip, ip_region, user_agent, success_flag, fail_reason)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.TenantID, userID, entry.OperateUserType, entry.OperateUserName, entry.Module,
		entry.Content, entry.URL, entry.Method, nullableText(entry.Param), nullableText(entry.Response),
		entry.IP, entry.IPRegion, entry.UserAgent, boolToTinyInt(entry.SuccessFlag), entry.FailReason)
	return err
}

func nullableText(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

const operateLogColumns = `operate_log_id, COALESCE(operate_user_id, 0), operate_user_type,
operate_user_name, COALESCE(module, ''), COALESCE(content, ''), COALESCE(url, ''),
COALESCE(method, ''), COALESCE(param, ''), COALESCE(response, ''), COALESCE(ip, ''),
COALESCE(ip_region, ''), COALESCE(user_agent, ''), success_flag, COALESCE(fail_reason, ''),
created_at, updated_at`

func (r *OperateLogRepository) ListOperateLogs(ctx context.Context, tenantID uint64, query app.OperateLogQuery) (app.OperateLogPage, error) {
	where := []string{"tenant_id = ?"}
	args := []any{tenantID}
	if query.OperateUserID != nil {
		where = append(where, "operate_user_id = ?")
		args = append(args, *query.OperateUserID)
	}
	if query.OperateUserType != nil {
		where = append(where, "operate_user_type = ?")
		args = append(args, *query.OperateUserType)
	}
	if value := strings.TrimSpace(query.UserName); value != "" {
		where = append(where, "operate_user_name LIKE ?")
		args = append(args, "%"+value+"%")
	}
	if value := strings.TrimSpace(query.Keywords); value != "" {
		like := "%" + value + "%"
		where = append(where, "(module LIKE ? OR content LIKE ?)")
		args = append(args, like, like)
	}
	if value := strings.TrimSpace(query.RequestKeywords); value != "" {
		like := "%" + value + "%"
		where = append(where, "(url LIKE ? OR method LIKE ? OR COALESCE(param, '') LIKE ? OR COALESCE(response, '') LIKE ?)")
		args = append(args, like, like, like, like)
	}
	if query.SuccessFlag != nil {
		where = append(where, "success_flag = ?")
		args = append(args, boolToTinyInt(*query.SuccessFlag))
	}
	if value := strings.TrimSpace(query.StartDate); value != "" {
		where = append(where, "created_at >= ?")
		args = append(args, value+" 00:00:00")
	}
	if value := strings.TrimSpace(query.EndDate); value != "" {
		where = append(where, "created_at <= ?")
		args = append(args, value+" 23:59:59")
	}
	filter := strings.Join(where, " AND ")

	var total int
	if err := r.store.QueryRow(ctx,
		`SELECT COUNT(*) FROM support_operate_logs WHERE `+filter, args...).Scan(&total); err != nil {
		return app.OperateLogPage{}, err
	}
	pageArgs := append(append([]any{}, args...), query.PageSize, (query.PageNum-1)*query.PageSize)
	rows, err := r.store.QueryRows(ctx,
		`SELECT `+operateLogColumns+` FROM support_operate_logs WHERE `+filter+
			` ORDER BY created_at DESC, operate_log_id DESC LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return app.OperateLogPage{}, err
	}
	defer func() { _ = rows.Close() }()
	items, err := scanOperateLogs(rows)
	if err != nil {
		return app.OperateLogPage{}, err
	}
	return app.OperateLogPage{List: items, Total: total}, nil
}

func (r *OperateLogRepository) GetOperateLog(ctx context.Context, tenantID, id uint64) (app.OperateLog, error) {
	row := r.store.QueryRow(ctx,
		`SELECT `+operateLogColumns+` FROM support_operate_logs
WHERE tenant_id = ? AND operate_log_id = ?`, tenantID, id)
	var entry app.OperateLog
	var userID uint64
	var successFlag int
	err := row.Scan(&entry.ID, &userID, &entry.OperateUserType, &entry.OperateUserName,
		&entry.Module, &entry.Content, &entry.URL, &entry.Method, &entry.Param, &entry.Response,
		&entry.IP, &entry.IPRegion, &entry.UserAgent, &successFlag, &entry.FailReason,
		&entry.CreatedAt, &entry.UpdatedAt)
	if err == sql.ErrNoRows {
		return app.OperateLog{}, app.ErrOperateLogNotFound
	}
	if err != nil {
		return app.OperateLog{}, err
	}
	if userID != 0 {
		entry.OperateUserID = &userID
	}
	entry.SuccessFlag = successFlag == 1
	return entry, nil
}

type operateLogScanner interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

func scanOperateLogs(rows operateLogScanner) ([]app.OperateLog, error) {
	items := make([]app.OperateLog, 0)
	for rows.Next() {
		var entry app.OperateLog
		var userID uint64
		var successFlag int
		if err := rows.Scan(&entry.ID, &userID, &entry.OperateUserType, &entry.OperateUserName,
			&entry.Module, &entry.Content, &entry.URL, &entry.Method, &entry.Param, &entry.Response,
			&entry.IP, &entry.IPRegion, &entry.UserAgent, &successFlag, &entry.FailReason,
			&entry.CreatedAt, &entry.UpdatedAt); err != nil {
			return nil, err
		}
		entry.OperateUserID = &userID
		entry.SuccessFlag = successFlag == 1
		items = append(items, entry)
	}
	return items, rows.Err()
}
