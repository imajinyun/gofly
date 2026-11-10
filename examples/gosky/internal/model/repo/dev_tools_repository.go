package repo

import (
	"context"
	"database/sql"
	"strings"

	"github.com/imajinyun/gofly/core/storage"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
)

// ChangeLogRepository persists product change logs.
type ChangeLogRepository struct {
	store *storage.SQLStore
}

// NewChangeLogRepository constructs the change log repository.
func NewChangeLogRepository(store *storage.SQLStore) *ChangeLogRepository {
	return &ChangeLogRepository{store: store}
}

const changeLogColumns = `change_log_id, update_version, type, COALESCE(publish_author, ''),
DATE_FORMAT(public_date, '%Y-%m-%d'), COALESCE(content, ''), COALESCE(link, ''), created_at, updated_at`

func scanChangeLog(row interface {
	Scan(dest ...any) error
}) (app.ChangeLog, error) {
	var log app.ChangeLog
	if err := row.Scan(&log.ID, &log.UpdateVersion, &log.Type, &log.PublishAuthor,
		&log.PublicDate, &log.Content, &log.Link, &log.CreatedAt, &log.UpdatedAt); err != nil {
		return app.ChangeLog{}, err
	}
	return log, nil
}

// List returns a filtered change-log page.
func (r *ChangeLogRepository) List(ctx context.Context, query app.ChangeLogQuery) (app.ChangeLogPage, error) {
	where := []string{"1=1"}
	args := []any{}
	if query.Type != nil {
		where = append(where, "type = ?")
		args = append(args, *query.Type)
	}
	if value := strings.TrimSpace(query.Keyword); value != "" {
		where = append(where, "(update_version LIKE ? OR content LIKE ?)")
		args = append(args, "%"+value+"%", "%"+value+"%")
	}
	if value := strings.TrimSpace(query.PublicDateBegin); value != "" {
		where = append(where, "public_date >= ?")
		args = append(args, value)
	}
	if value := strings.TrimSpace(query.PublicDateEnd); value != "" {
		where = append(where, "public_date <= ?")
		args = append(args, value)
	}
	filter := strings.Join(where, " AND ")

	var total int
	if err := r.store.QueryRow(ctx,
		`SELECT COUNT(*) FROM support_change_logs WHERE `+filter, args...).Scan(&total); err != nil {
		return app.ChangeLogPage{}, err
	}
	pageArgs := append(append([]any{}, args...), query.PageSize, (query.PageNum-1)*query.PageSize)
	rows, err := r.store.QueryRows(ctx,
		`SELECT `+changeLogColumns+` FROM support_change_logs WHERE `+filter+
			` ORDER BY public_date DESC, change_log_id DESC LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return app.ChangeLogPage{}, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]app.ChangeLog, 0, query.PageSize)
	for rows.Next() {
		log, err := scanChangeLog(rows)
		if err != nil {
			return app.ChangeLogPage{}, err
		}
		items = append(items, log)
	}
	return app.ChangeLogPage{List: items, Total: total}, rows.Err()
}

// Get loads one change log.
func (r *ChangeLogRepository) Get(ctx context.Context, id uint64) (app.ChangeLog, error) {
	row := r.store.QueryRow(ctx,
		`SELECT `+changeLogColumns+` FROM support_change_logs WHERE change_log_id = ?`, id)
	log, err := scanChangeLog(row)
	if err == sql.ErrNoRows {
		return app.ChangeLog{}, app.ErrChangeLogNotFound
	}
	return log, err
}

// Insert creates a change log.
func (r *ChangeLogRepository) Insert(ctx context.Context, input app.ChangeLogInput) (uint64, error) {
	result, err := r.store.Exec(ctx,
		`INSERT INTO support_change_logs
(update_version, type, publish_author, public_date, content, link)
VALUES (?, ?, ?, ?, ?, ?)`,
		strings.TrimSpace(input.UpdateVersion), input.Type,
		strings.TrimSpace(input.PublishAuthor), input.PublicDate,
		input.Content, strings.TrimSpace(input.Link))
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	return uint64(id), err
}

// Update replaces a change log and reports whether a row existed.
func (r *ChangeLogRepository) Update(ctx context.Context, input app.ChangeLogInput) (bool, error) {
	result, err := r.store.Exec(ctx,
		`UPDATE support_change_logs
SET update_version = ?, type = ?, publish_author = ?, public_date = ?, content = ?, link = ?
WHERE change_log_id = ?`,
		strings.TrimSpace(input.UpdateVersion), input.Type,
		strings.TrimSpace(input.PublishAuthor), input.PublicDate,
		input.Content, strings.TrimSpace(input.Link), input.ID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

// Delete removes one change log.
func (r *ChangeLogRepository) Delete(ctx context.Context, id uint64) error {
	_, err := r.store.Exec(ctx, `DELETE FROM support_change_logs WHERE change_log_id = ?`, id)
	return err
}

// DeleteBatch removes multiple change logs.
func (r *ChangeLogRepository) DeleteBatch(ctx context.Context, ids []uint64) error {
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	_, err := r.store.Exec(ctx,
		`DELETE FROM support_change_logs WHERE change_log_id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	return err
}

// HeartBeatRepository persists process heartbeats.
type HeartBeatRepository struct {
	store *storage.SQLStore
}

// NewHeartBeatRepository constructs the heartbeat repository.
func NewHeartBeatRepository(store *storage.SQLStore) *HeartBeatRepository {
	return &HeartBeatRepository{store: store}
}

// Upsert inserts or refreshes one process heartbeat.
func (r *HeartBeatRepository) Upsert(ctx context.Context, record app.HeartBeatRecord) error {
	_, err := r.store.Exec(ctx,
		`INSERT INTO support_heart_beat_records
(project_path, server_ip, process_no, process_start_time, heart_beat_time)
VALUES (?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE heart_beat_time = VALUES(heart_beat_time)`,
		record.ProjectPath, record.ServerIP, record.ProcessNo,
		record.ProcessStartTime, record.HeartBeatTime)
	return err
}

// List returns a filtered heartbeat page.
func (r *HeartBeatRepository) List(ctx context.Context, query app.HeartBeatQuery) (app.HeartBeatPage, error) {
	where := []string{"1=1"}
	args := []any{}
	if value := strings.TrimSpace(query.Keywords); value != "" {
		where = append(where, "(project_path LIKE ? OR server_ip LIKE ?)")
		args = append(args, "%"+value+"%", "%"+value+"%")
	}
	if value := strings.TrimSpace(query.StartDate); value != "" {
		where = append(where, "heart_beat_time >= ?")
		args = append(args, value+" 00:00:00")
	}
	if value := strings.TrimSpace(query.EndDate); value != "" {
		where = append(where, "heart_beat_time <= ?")
		args = append(args, value+" 23:59:59")
	}
	filter := strings.Join(where, " AND ")

	var total int
	if err := r.store.QueryRow(ctx,
		`SELECT COUNT(*) FROM support_heart_beat_records WHERE `+filter, args...).Scan(&total); err != nil {
		return app.HeartBeatPage{}, err
	}
	pageArgs := append(append([]any{}, args...), query.PageSize, (query.PageNum-1)*query.PageSize)
	rows, err := r.store.QueryRows(ctx,
		`SELECT heart_beat_record_id, project_path, COALESCE(server_ip, ''), process_no,
process_start_time, heart_beat_time
FROM support_heart_beat_records WHERE `+filter+
			` ORDER BY heart_beat_time DESC, heart_beat_record_id DESC LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return app.HeartBeatPage{}, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]app.HeartBeatRecord, 0, query.PageSize)
	for rows.Next() {
		var record app.HeartBeatRecord
		if err := rows.Scan(&record.ID, &record.ProjectPath, &record.ServerIP, &record.ProcessNo,
			&record.ProcessStartTime, &record.HeartBeatTime); err != nil {
			return app.HeartBeatPage{}, err
		}
		items = append(items, record)
	}
	return app.HeartBeatPage{List: items, Total: total}, rows.Err()
}

// DataTracerRepository persists business change trails.
type DataTracerRepository struct {
	store *storage.SQLStore
}

// NewDataTracerRepository constructs the data tracer repository.
func NewDataTracerRepository(store *storage.SQLStore) *DataTracerRepository {
	return &DataTracerRepository{store: store}
}

// List returns trails for one business row.
func (r *DataTracerRepository) List(ctx context.Context, query app.DataTracerQuery) (app.DataTracerPage, error) {
	where := []string{"data_id = ?"}
	args := []any{query.DataID}
	if query.Type != nil {
		where = append(where, "type = ?")
		args = append(args, *query.Type)
	}
	if value := strings.TrimSpace(query.Keywords); value != "" {
		where = append(where, "content LIKE ?")
		args = append(args, "%"+value+"%")
	}
	filter := strings.Join(where, " AND ")

	var total int
	if err := r.store.QueryRow(ctx,
		`SELECT COUNT(*) FROM support_data_tracer_logs WHERE `+filter, args...).Scan(&total); err != nil {
		return app.DataTracerPage{}, err
	}
	pageArgs := append(append([]any{}, args...), query.PageSize, (query.PageNum-1)*query.PageSize)
	rows, err := r.store.QueryRows(ctx,
		`SELECT data_tracer_id, data_id, type, COALESCE(content, ''), COALESCE(diff_old, ''),
COALESCE(diff_new, ''), COALESCE(extra_data, ''), user_id, user_type, COALESCE(user_name, ''),
COALESCE(user_agent, ''), COALESCE(ip, ''), COALESCE(ip_region, ''), created_at
FROM support_data_tracer_logs WHERE `+filter+
			` ORDER BY created_at DESC, data_tracer_id DESC LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return app.DataTracerPage{}, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]app.DataTracerRecord, 0, query.PageSize)
	for rows.Next() {
		var record app.DataTracerRecord
		var userID sql.NullInt64
		if err := rows.Scan(&record.ID, &record.DataID, &record.Type, &record.Content,
			&record.DiffOld, &record.DiffNew, &record.ExtraData, &userID, &record.UserType,
			&record.UserName, &record.UserAgent, &record.IP, &record.IPRegion, &record.CreatedAt); err != nil {
			return app.DataTracerPage{}, err
		}
		if userID.Valid {
			id := uint64(userID.Int64)
			record.UserID = &id
		}
		items = append(items, record)
	}
	return app.DataTracerPage{List: items, Total: total}, rows.Err()
}
