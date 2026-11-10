package repo

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/imajinyun/gofly/core/storage"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
)

// NoticeRepository persists the OA notice board.
type NoticeRepository struct {
	store *storage.SQLStore
}

// NewNoticeRepository constructs the notice repository.
func NewNoticeRepository(store *storage.SQLStore) *NoticeRepository {
	return &NoticeRepository{store: store}
}

// ---------------- types ----------------

// ListTypes returns all notice types ordered by id.
func (r *NoticeRepository) ListTypes(ctx context.Context) ([]app.NoticeType, error) {
	rows, err := r.store.QueryRows(ctx,
		`SELECT notice_type_id, notice_type_name, created_at, updated_at
FROM support_notice_types ORDER BY notice_type_id ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]app.NoticeType, 0)
	for rows.Next() {
		var item app.NoticeType
		if err := rows.Scan(&item.ID, &item.Name, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// AddType inserts a type; duplicate names fail on the unique key.
func (r *NoticeRepository) AddType(ctx context.Context, name string) (uint64, error) {
	result, err := r.store.Exec(ctx,
		`INSERT INTO support_notice_types (notice_type_name) VALUES (?)`, name)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	return uint64(id), err
}

// UpdateType renames a type.
func (r *NoticeRepository) UpdateType(ctx context.Context, id uint64, name string) (bool, error) {
	result, err := r.store.Exec(ctx,
		`UPDATE support_notice_types SET notice_type_name = ? WHERE notice_type_id = ?`, name, id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

// DeleteType removes a type.
func (r *NoticeRepository) DeleteType(ctx context.Context, id uint64) (bool, error) {
	result, err := r.store.Exec(ctx,
		`DELETE FROM support_notice_types WHERE notice_type_id = ?`, id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

// TypeInUse reports whether any active notice references the type.
func (r *NoticeRepository) TypeInUse(ctx context.Context, id uint64) (bool, error) {
	var count int
	err := r.store.QueryRow(ctx,
		`SELECT COUNT(*) FROM support_notices WHERE notice_type_id = ? AND deleted_flag = 0`, id).Scan(&count)
	return count > 0, err
}

// ---------------- notices ----------------

const noticeColumns = `n.notice_id, n.notice_type_id, COALESCE(t.notice_type_name, ''),
n.title, n.all_visible_flag, n.scheduled_publish_flag, n.publish_time,
COALESCE(n.author, ''), COALESCE(n.source, ''), COALESCE(n.document_number, ''),
n.page_view_count, n.user_view_count, n.deleted_flag, n.create_user_id,
COALESCE(n.create_user_name, ''), n.created_at, n.updated_at`

func scanNotice(scanner interface {
	Scan(dest ...any) error
}) (app.Notice, error) {
	var notice app.Notice
	var allVisible, scheduled, deleted int
	var createUser sql.NullInt64
	if err := scanner.Scan(&notice.ID, &notice.TypeID, &notice.TypeName, &notice.Title,
		&allVisible, &scheduled, &notice.PublishTime, &notice.Author, &notice.Source,
		&notice.DocumentNumber, &notice.PageViewCount, &notice.UserViewCount, &deleted,
		&createUser, &notice.CreateUserName, &notice.CreatedAt, &notice.UpdatedAt); err != nil {
		return app.Notice{}, err
	}
	notice.AllVisibleFlag = allVisible == 1
	notice.ScheduledPublishFlag = scheduled == 1
	notice.PublishFlag = notice.PublishTime.Before(time.Now())
	notice.DeletedFlag = deleted == 1
	if createUser.Valid {
		id := uint64(createUser.Int64)
		notice.CreateUserID = &id
	}
	return notice, nil
}

// List returns the admin notice page.
func (r *NoticeRepository) List(ctx context.Context, query app.NoticeQuery) (app.NoticePage, error) {
	where := []string{"n.deleted_flag = ?"}
	args := []any{boolToTinyInt(query.DeletedFlag)}
	if query.TypeID != nil {
		where = append(where, "n.notice_type_id = ?")
		args = append(args, *query.TypeID)
	}
	if value := strings.TrimSpace(query.Keywords); value != "" {
		where = append(where, "(n.title LIKE ? OR n.author LIKE ? OR n.source LIKE ?)")
		like := "%" + value + "%"
		args = append(args, like, like, like)
	}
	if value := strings.TrimSpace(query.DocumentNumber); value != "" {
		where = append(where, "n.document_number LIKE ?")
		args = append(args, "%"+value+"%")
	}
	if value := strings.TrimSpace(query.CreateUserName); value != "" {
		where = append(where, "n.create_user_name LIKE ?")
		args = append(args, "%"+value+"%")
	}
	if value := strings.TrimSpace(query.CreateTimeBegin); value != "" {
		where = append(where, "n.created_at >= ?")
		args = append(args, value+" 00:00:00")
	}
	if value := strings.TrimSpace(query.CreateTimeEnd); value != "" {
		where = append(where, "n.created_at <= ?")
		args = append(args, value+" 23:59:59")
	}
	if value := strings.TrimSpace(query.PublishBegin); value != "" {
		where = append(where, "n.publish_time >= ?")
		args = append(args, value+" 00:00:00")
	}
	if value := strings.TrimSpace(query.PublishEnd); value != "" {
		where = append(where, "n.publish_time <= ?")
		args = append(args, value+" 23:59:59")
	}
	filter := strings.Join(where, " AND ")

	var total int
	countArgs := append(append([]any{}, args...), nil)
	_ = countArgs
	countSQL := `SELECT COUNT(*) FROM support_notices n
LEFT JOIN support_notice_types t ON t.notice_type_id = n.notice_type_id WHERE ` + filter
	if err := r.store.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return app.NoticePage{}, err
	}
	pageArgs := append(append([]any{}, args...), query.PageSize, (query.PageNum-1)*query.PageSize)
	listSQL := `SELECT ` + noticeColumns + ` FROM support_notices n
LEFT JOIN support_notice_types t ON t.notice_type_id = n.notice_type_id
WHERE ` + filter + ` ORDER BY n.publish_time DESC, n.notice_id DESC LIMIT ? OFFSET ?`
	rows, err := r.store.QueryRows(ctx, listSQL, pageArgs...)
	if err != nil {
		return app.NoticePage{}, err
	}
	defer func() { _ = rows.Close() }()
	items, err := scanNoticeRows(rows)
	if err != nil {
		return app.NoticePage{}, err
	}
	return app.NoticePage{List: items, Total: total}, nil
}

// Get returns a notice with content and visibility ranges.
func (r *NoticeRepository) Get(ctx context.Context, id uint64) (app.NoticeDetail, error) {
	rows, err := r.store.QueryRows(ctx,
		`SELECT `+noticeColumns+`, COALESCE(n.content_text, ''), COALESCE(n.content_html, ''), COALESCE(n.attachment, '')
FROM support_notices n
LEFT JOIN support_notice_types t ON t.notice_type_id = n.notice_type_id
WHERE n.notice_id = ?`, id)
	if err != nil {
		return app.NoticeDetail{}, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return app.NoticeDetail{}, err
		}
		return app.NoticeDetail{}, app.ErrNoticeNotFound
	}
	var notice app.Notice
	var allVisible, scheduled, deleted int
	var createUser sql.NullInt64
	var contentText, contentHTML, attachment string
	if err := rows.Scan(&notice.ID, &notice.TypeID, &notice.TypeName, &notice.Title,
		&allVisible, &scheduled, &notice.PublishTime, &notice.Author, &notice.Source,
		&notice.DocumentNumber, &notice.PageViewCount, &notice.UserViewCount, &deleted,
		&createUser, &notice.CreateUserName, &notice.CreatedAt, &notice.UpdatedAt,
		&contentText, &contentHTML, &attachment); err != nil {
		return app.NoticeDetail{}, err
	}
	notice.AllVisibleFlag = allVisible == 1
	notice.ScheduledPublishFlag = scheduled == 1
	notice.PublishFlag = notice.PublishTime.Before(time.Now())
	notice.DeletedFlag = deleted == 1
	if createUser.Valid {
		id := uint64(createUser.Int64)
		notice.CreateUserID = &id
	}
	rangeRows, err := r.store.QueryRows(ctx,
		`SELECT data_type, data_id FROM support_notice_visible_ranges WHERE notice_id = ? ORDER BY range_id`, id)
	if err != nil {
		return app.NoticeDetail{}, err
	}
	defer func() { _ = rangeRows.Close() }()
	ranges := make([]app.NoticeVisibleRange, 0)
	for rangeRows.Next() {
		var entry app.NoticeVisibleRange
		if err := rangeRows.Scan(&entry.DataType, &entry.DataID); err != nil {
			return app.NoticeDetail{}, err
		}
		ranges = append(ranges, entry)
	}
	return app.NoticeDetail{
		Notice:           notice,
		ContentText:      contentText,
		ContentHTML:      contentHTML,
		Attachment:       splitFileKeys(attachment),
		VisibleRangeList: ranges,
	}, rangeRows.Err()
}

// Insert creates a notice and its visibility ranges in one transaction.
func (r *NoticeRepository) Insert(ctx context.Context, input app.NoticeInput, publishAt time.Time) (uint64, error) {
	var id uint64
	err := r.store.Transact(ctx, nil, func(txCtx context.Context, tx *sql.Tx) error {
		result, err := tx.ExecContext(txCtx,
			`INSERT INTO support_notices
(notice_type_id, title, all_visible_flag, scheduled_publish_flag, publish_time,
content_text, content_html, attachment, author, source, document_number,
create_user_id, create_user_name)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			input.TypeID, strings.TrimSpace(input.Title), boolToTinyInt(*input.AllVisibleFlag),
			boolToTinyInt(*input.ScheduledPublishFlag), publishAt,
			input.ContentText, input.ContentHTML, strings.Join(input.Attachment, ","),
			strings.TrimSpace(input.Author), strings.TrimSpace(input.Source),
			strings.TrimSpace(input.DocumentNumber), nullableUint64(uint64ValuePtr(input.CreateUserID)),
			input.CreateUserName)
		if err != nil {
			return err
		}
		id64, err := result.LastInsertId()
		if err != nil {
			return err
		}
		id = uint64(id64)
		return replaceRanges(txCtx, tx, id, input.VisibleRangeList)
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// Update replaces a notice and its visibility ranges.
func (r *NoticeRepository) Update(ctx context.Context, input app.NoticeInput, publishAt time.Time) (bool, error) {
	updated := false
	err := r.store.Transact(ctx, nil, func(txCtx context.Context, tx *sql.Tx) error {
		result, err := tx.ExecContext(txCtx,
			`UPDATE support_notices
SET notice_type_id = ?, title = ?, all_visible_flag = ?, scheduled_publish_flag = ?,
publish_time = ?, content_text = ?, content_html = ?, attachment = ?,
author = ?, source = ?, document_number = ?
WHERE notice_id = ? AND deleted_flag = 0`,
			input.TypeID, strings.TrimSpace(input.Title), boolToTinyInt(*input.AllVisibleFlag),
			boolToTinyInt(*input.ScheduledPublishFlag), publishAt,
			input.ContentText, input.ContentHTML, strings.Join(input.Attachment, ","),
			strings.TrimSpace(input.Author), strings.TrimSpace(input.Source),
			strings.TrimSpace(input.DocumentNumber), input.ID)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return nil
		}
		updated = true
		if _, err := tx.ExecContext(txCtx, `DELETE FROM support_notice_visible_ranges WHERE notice_id = ?`, input.ID); err != nil {
			return err
		}
		return replaceRanges(txCtx, tx, input.ID, input.VisibleRangeList)
	})
	if err != nil {
		return false, err
	}
	return updated, nil
}

// SoftDelete marks a notice deleted.
func (r *NoticeRepository) SoftDelete(ctx context.Context, id uint64) error {
	_, err := r.store.Exec(ctx,
		`UPDATE support_notices SET deleted_flag = 1 WHERE notice_id = ?`, id)
	return err
}

// ---------------- employee board ----------------

// ResolveViewer loads the user id and department id behind a subject.
func (r *NoticeRepository) ResolveViewer(ctx context.Context, tenantID uint64, subject string) (app.IdentityScope, error) {
	var scope app.IdentityScope
	var department sql.NullInt64
	err := r.store.QueryRow(ctx,
		`SELECT u.user_id, tu.department_id, COALESCE(NULLIF(tu.display_name, ''), u.display_name, '')
FROM users AS u
JOIN tenant_users AS tu ON tu.user_id = u.user_id
WHERE tu.tenant_id = ? AND u.subject = ?
  AND u.user_state = 'active' AND tu.member_state = 'active'`,
		tenantID, subject).Scan(&scope.UserID, &department, &scope.DisplayName)
	if err == sql.ErrNoRows {
		return app.IdentityScope{}, nil
	}
	if err != nil {
		return app.IdentityScope{}, err
	}
	if department.Valid {
		scope.DepartmentID = uint64(department.Int64)
	}
	return scope, nil
}

// ListVisible returns published notices visible to the viewer, with a
// per-user view flag. Visibility: all-visible notices, notices with an
// EMPLOYEE range for the user, or a DEPARTMENT range matching the user's
// department.
func (r *NoticeRepository) ListVisible(ctx context.Context, viewer app.IdentityScope, query app.EmployeeNoticeQuery) (app.NoticePage, error) {
	where := []string{
		"n.deleted_flag = 0",
		"n.publish_time <= ?",
		`(n.all_visible_flag = 1
 OR EXISTS (SELECT 1 FROM support_notice_visible_ranges vr
            WHERE vr.notice_id = n.notice_id AND vr.data_type = ? AND vr.data_id = ?)
 OR EXISTS (SELECT 1 FROM support_notice_visible_ranges vr
            WHERE vr.notice_id = n.notice_id AND vr.data_type = ? AND vr.data_id = ?))`,
	}
	now := time.Now()
	args := []any{now, app.NoticeRangeEmployee, viewer.UserID, app.NoticeRangeDepartment, viewer.DepartmentID}
	if query.TypeID != nil {
		where = append(where, "n.notice_type_id = ?")
		args = append(args, *query.TypeID)
	}
	if value := strings.TrimSpace(query.Keywords); value != "" {
		where = append(where, "(n.title LIKE ? OR n.author LIKE ? OR n.source LIKE ? OR n.document_number LIKE ?)")
		like := "%" + value + "%"
		args = append(args, like, like, like, like)
	}
	if value := strings.TrimSpace(query.PublishBegin); value != "" {
		where = append(where, "n.publish_time >= ?")
		args = append(args, value+" 00:00:00")
	}
	if value := strings.TrimSpace(query.PublishEnd); value != "" {
		where = append(where, "n.publish_time <= ?")
		args = append(args, value+" 23:59:59")
	}
	if query.NotViewFlag != nil && *query.NotViewFlag {
		where = append(where, `NOT EXISTS (SELECT 1 FROM support_notice_view_records v
WHERE v.notice_id = n.notice_id AND v.user_id = ?)`)
		args = append(args, viewer.UserID)
	}
	filter := strings.Join(where, " AND ")

	var total int
	if err := r.store.QueryRow(ctx,
		`SELECT COUNT(*) FROM support_notices n WHERE `+filter, args...).Scan(&total); err != nil {
		return app.NoticePage{}, err
	}
	pageArgs := append(append([]any{}, args...), query.PageSize, (query.PageNum-1)*query.PageSize)
	rows, err := r.store.QueryRows(ctx,
		`SELECT `+noticeColumns+` FROM support_notices n
LEFT JOIN support_notice_types t ON t.notice_type_id = n.notice_type_id
WHERE `+filter+` ORDER BY n.publish_time DESC, n.notice_id DESC LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return app.NoticePage{}, err
	}
	defer func() { _ = rows.Close() }()
	items, err := scanNoticeRows(rows)
	if err != nil {
		return app.NoticePage{}, err
	}
	return app.NoticePage{List: items, Total: total}, nil
}

// ---------------- views ----------------

// RecordView inserts the first view or refreshes an existing one.
// pageDelta/userDelta match the Java counters (first view 1/1, repeat 1/0).
func (r *NoticeRepository) RecordView(ctx context.Context, noticeID, userID uint64, ip, userAgent string) (bool, int, int, error) {
	var existing int
	err := r.store.QueryRow(ctx,
		`SELECT COUNT(*) FROM support_notice_view_records WHERE notice_id = ? AND user_id = ?`,
		noticeID, userID).Scan(&existing)
	if err != nil {
		return false, 0, 0, err
	}
	firstView := existing == 0
	if firstView {
		if _, err := r.store.Exec(ctx,
			`INSERT INTO support_notice_view_records
(notice_id, user_id, page_view_count, first_ip, first_user_agent, last_ip, last_user_agent)
VALUES (?, ?, 1, ?, ?, ?, ?)`,
			noticeID, userID, ip, userAgent, ip, userAgent); err != nil {
			return false, 0, 0, err
		}
		if _, err := r.store.Exec(ctx,
			`UPDATE support_notices SET page_view_count = page_view_count + 1,
user_view_count = user_view_count + 1 WHERE notice_id = ?`, noticeID); err != nil {
			return false, 0, 0, err
		}
		return true, 1, 1, nil
	}
	if _, err := r.store.Exec(ctx,
		`UPDATE support_notice_view_records
SET page_view_count = page_view_count + 1, last_ip = ?, last_user_agent = ?
WHERE notice_id = ? AND user_id = ?`, ip, userAgent, noticeID, userID); err != nil {
		return false, 0, 0, err
	}
	if _, err := r.store.Exec(ctx,
		`UPDATE support_notices SET page_view_count = page_view_count + 1 WHERE notice_id = ?`,
		noticeID); err != nil {
		return false, 0, 0, err
	}
	return false, 1, 0, nil
}

// ListViewRecords returns per-employee view statistics for one notice.
func (r *NoticeRepository) ListViewRecords(ctx context.Context, query app.ViewRecordQuery) (app.NoticeViewRecordPage, error) {
	where := []string{"v.notice_id = ?"}
	args := []any{query.NoticeID}
	if query.DepartmentID != nil {
		where = append(where, "tu.department_id = ?")
		args = append(args, *query.DepartmentID)
	}
	if value := strings.TrimSpace(query.Keywords); value != "" {
		where = append(where, "(COALESCE(u.display_name, '') LIKE ? OR COALESCE(tu.display_name, '') LIKE ?)")
		args = append(args, "%"+value+"%", "%"+value+"%")
	}
	filter := strings.Join(where, " AND ")
	tenantJoin := `LEFT JOIN tenant_users tu ON tu.tenant_id = ? AND tu.user_id = v.user_id`

	var total int
	if err := r.store.QueryRow(ctx,
		`SELECT COUNT(*) FROM support_notice_view_records v
LEFT JOIN users u ON u.user_id = v.user_id
`+tenantJoin+` WHERE `+filter,
		append([]any{query.TenantID}, args...)...).Scan(&total); err != nil {
		return app.NoticeViewRecordPage{}, err
	}
	pageArgs := append(append([]any{}, args...), query.PageSize, (query.PageNum-1)*query.PageSize)
	rows, err := r.store.QueryRows(ctx,
		`SELECT v.user_id, COALESCE(NULLIF(tu.display_name, ''), u.display_name, ''), COALESCE(d.name, ''),
v.page_view_count, COALESCE(v.first_ip, ''), COALESCE(v.first_user_agent, ''),
v.created_at, COALESCE(v.last_ip, ''), COALESCE(v.last_user_agent, ''), v.updated_at
FROM support_notice_view_records v
LEFT JOIN users u ON u.user_id = v.user_id
`+tenantJoin+`
LEFT JOIN departments d ON d.tenant_id = tu.tenant_id AND d.department_id = tu.department_id
WHERE `+filter+` ORDER BY v.updated_at DESC, v.view_id DESC LIMIT ? OFFSET ?`,
		append([]any{query.TenantID}, pageArgs...)...)
	if err != nil {
		return app.NoticeViewRecordPage{}, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]app.NoticeViewRecord, 0, query.PageSize)
	for rows.Next() {
		var record app.NoticeViewRecord
		if err := rows.Scan(&record.UserID, &record.UserName, &record.DepartmentName,
			&record.PageViewCount, &record.FirstIP, &record.FirstUserAgent, &record.CreatedAt,
			&record.LastIP, &record.LastUserAgent, &record.UpdatedAt); err != nil {
			return app.NoticeViewRecordPage{}, err
		}
		items = append(items, record)
	}
	return app.NoticeViewRecordPage{List: items, Total: total}, rows.Err()
}

// ---------------- helpers ----------------

func scanNoticeRows(rows interface {
	Next() bool
	Err() error
	Scan(dest ...any) error
	Close() error
}) ([]app.Notice, error) {
	items := make([]app.Notice, 0)
	for rows.Next() {
		notice, err := scanNotice(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, notice)
	}
	return items, rows.Err()
}

func uint64ValuePtr(value uint64) *uint64 {
	if value == 0 {
		return nil
	}
	return &value
}

func splitFileKeys(value string) []string {
	if strings.TrimSpace(value) == "" {
		return []string{}
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if key := strings.TrimSpace(part); key != "" {
			result = append(result, key)
		}
	}
	return result
}

func replaceRanges(ctx context.Context, tx *sql.Tx, noticeID uint64, ranges []app.NoticeVisibleRange) error {
	for _, entry := range ranges {
		if _, err := tx.ExecContext(ctx,
			`INSERT IGNORE INTO support_notice_visible_ranges (notice_id, data_type, data_id)
VALUES (?, ?, ?)`, noticeID, entry.DataType, entry.DataID); err != nil {
			return err
		}
	}
	return nil
}
