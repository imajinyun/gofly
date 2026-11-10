package repo

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/imajinyun/gofly/core/storage"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
)

var _ app.JobStore = (*JobRepository)(nil)

// JobRepository persists scheduled job definitions and execution logs.
type JobRepository struct {
	store *storage.SQLStore
}

// NewJobRepository constructs the job repository.
func NewJobRepository(store *storage.SQLStore) *JobRepository {
	return &JobRepository{store: store}
}

const jobSelectColumns = `job_id, job_name, job_class, trigger_type, trigger_value,
COALESCE(param, ''), enabled_flag, last_execute_time, last_execute_log_id,
COALESCE(remark, ''), sort, deleted_flag, COALESCE(update_name, ''), created_at, updated_at`

func scanJob(row interface {
	Scan(dest ...any) error
}) (app.Job, error) {
	var job app.Job
	var enabled, deleted int
	var lastTime sql.NullTime
	var lastLog sql.NullInt64
	if err := row.Scan(&job.ID, &job.JobName, &job.JobClass, &job.TriggerType, &job.TriggerValue,
		&job.Param, &enabled, &lastTime, &lastLog, &job.Remark, &job.Sort, &deleted,
		&job.UpdateName, &job.CreatedAt, &job.UpdatedAt); err != nil {
		return app.Job{}, err
	}
	job.EnabledFlag = enabled == 1
	job.DeletedFlag = deleted == 1
	if lastTime.Valid {
		t := lastTime.Time
		job.LastExecuteTime = &t
	}
	if lastLog.Valid {
		id := uint64(lastLog.Int64)
		job.LastExecuteLogID = &id
	}
	return job, nil
}

// ListJobs returns a filtered job page.
func (r *JobRepository) ListJobs(ctx context.Context, query app.JobQuery) (app.JobPage, error) {
	where := []string{"deleted_flag = ?"}
	args := []any{boolToTinyInt(query.DeletedFlag)}
	if value := strings.TrimSpace(query.SearchWord); value != "" {
		where = append(where, "(job_name LIKE ? OR job_class LIKE ?)")
		args = append(args, "%"+value+"%", "%"+value+"%")
	}
	if value := strings.TrimSpace(query.TriggerType); value != "" {
		where = append(where, "trigger_type = ?")
		args = append(args, value)
	}
	if query.EnabledFlag != nil {
		where = append(where, "enabled_flag = ?")
		args = append(args, boolToTinyInt(*query.EnabledFlag))
	}
	filter := strings.Join(where, " AND ")

	var total int
	if err := r.store.QueryRow(ctx,
		`SELECT COUNT(*) FROM support_jobs WHERE `+filter, args...).Scan(&total); err != nil {
		return app.JobPage{}, err
	}
	pageArgs := append(append([]any{}, args...), query.PageSize, (query.PageNum-1)*query.PageSize)
	rows, err := r.store.QueryRows(ctx,
		`SELECT `+jobSelectColumns+` FROM support_jobs WHERE `+filter+
			` ORDER BY sort ASC, job_id ASC LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return app.JobPage{}, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]app.Job, 0, query.PageSize)
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return app.JobPage{}, err
		}
		items = append(items, job)
	}
	return app.JobPage{List: items, Total: total}, rows.Err()
}

// GetJob loads one job; includeDeleted controls whether soft-deleted rows
// are visible.
func (r *JobRepository) GetJob(ctx context.Context, id uint64, includeDeleted bool) (app.Job, error) {
	filter := "deleted_flag = 0"
	if includeDeleted {
		filter = "1=1"
	}
	row := r.store.QueryRow(ctx,
		`SELECT `+jobSelectColumns+` FROM support_jobs WHERE job_id = ? AND `+filter, id)
	job, err := scanJob(row)
	if err == sql.ErrNoRows {
		return app.Job{}, app.ErrJobNotFound
	}
	return job, err
}

// GetActiveJobByClass looks for an active job with the same class.
func (r *JobRepository) GetActiveJobByClass(ctx context.Context, class string, excludeID uint64) (app.Job, bool, error) {
	query := `SELECT ` + jobSelectColumns + ` FROM support_jobs
WHERE job_class = ? AND deleted_flag = 0`
	args := []any{class}
	if excludeID != 0 {
		query += ` AND job_id <> ?`
		args = append(args, excludeID)
	}
	row := r.store.QueryRow(ctx, query, args...)
	job, err := scanJob(row)
	if err == sql.ErrNoRows {
		return app.Job{}, false, nil
	}
	if err != nil {
		return app.Job{}, false, err
	}
	return job, true, nil
}

// InsertJob creates a job and returns its id.
func (r *JobRepository) InsertJob(ctx context.Context, input app.JobInput) (uint64, error) {
	result, err := r.store.Exec(ctx,
		`INSERT INTO support_jobs
(job_name, job_class, trigger_type, trigger_value, param, enabled_flag, remark, sort, update_name)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		strings.TrimSpace(input.JobName), strings.TrimSpace(input.JobClass),
		input.TriggerType, strings.TrimSpace(input.TriggerValue), input.Param,
		boolToTinyInt(*input.EnabledFlag), input.Remark, *input.Sort, input.UpdateName)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	return uint64(id), err
}

// UpdateJob replaces all editable fields.
func (r *JobRepository) UpdateJob(ctx context.Context, input app.JobInput) error {
	_, err := r.store.Exec(ctx,
		`UPDATE support_jobs
SET job_name = ?, job_class = ?, trigger_type = ?, trigger_value = ?, param = ?,
enabled_flag = ?, remark = ?, sort = ?, update_name = ?
WHERE job_id = ? AND deleted_flag = 0`,
		strings.TrimSpace(input.JobName), strings.TrimSpace(input.JobClass),
		input.TriggerType, strings.TrimSpace(input.TriggerValue), input.Param,
		boolToTinyInt(*input.EnabledFlag), input.Remark, *input.Sort, input.UpdateName, input.ID)
	return err
}

// UpdateJobEnabled toggles only the enabled flag.
func (r *JobRepository) UpdateJobEnabled(ctx context.Context, input app.JobEnabledInput) error {
	_, err := r.store.Exec(ctx,
		`UPDATE support_jobs SET enabled_flag = ?, update_name = ?
WHERE job_id = ? AND deleted_flag = 0`,
		boolToTinyInt(input.EnabledFlag), input.UpdateName, input.ID)
	return err
}

// SoftDeleteJob marks a job deleted.
func (r *JobRepository) SoftDeleteJob(ctx context.Context, id uint64, updateName string) error {
	_, err := r.store.Exec(ctx,
		`UPDATE support_jobs SET deleted_flag = 1, enabled_flag = 0, update_name = ?
WHERE job_id = ?`, updateName, id)
	return err
}

// ListEnabledJobs returns all enabled, non-deleted jobs for the scheduler.
func (r *JobRepository) ListEnabledJobs(ctx context.Context) ([]app.Job, error) {
	rows, err := r.store.QueryRows(ctx,
		`SELECT `+jobSelectColumns+` FROM support_jobs
WHERE enabled_flag = 1 AND deleted_flag = 0 ORDER BY sort ASC, job_id ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]app.Job, 0)
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, job)
	}
	return items, rows.Err()
}

// UpdateJobRunState records last execution time/log after a run.
func (r *JobRepository) UpdateJobRunState(ctx context.Context, jobID, logID uint64, at time.Time) error {
	_, err := r.store.Exec(ctx,
		`UPDATE support_jobs SET last_execute_time = ?, last_execute_log_id = ? WHERE job_id = ?`,
		at, logID, jobID)
	return err
}

// ---------------- logs ----------------

const jobLogColumns = `log_id, job_id, job_name, COALESCE(param, ''), success_flag,
execute_start_time, execute_time_millis, execute_end_time, COALESCE(execute_result, ''),
COALESCE(ip, ''), COALESCE(process_id, ''), COALESCE(program_path, ''),
COALESCE(create_name, ''), created_at`

func scanJobLog(row interface {
	Scan(dest ...any) error
}) (app.JobLog, error) {
	var log app.JobLog
	var success int
	if err := row.Scan(&log.LogID, &log.JobID, &log.JobName, &log.Param, &success,
		&log.ExecuteStartTime, &log.ExecuteTimeMillis, &log.ExecuteEndTime, &log.ExecuteResult,
		&log.IP, &log.ProcessID, &log.ProgramPath, &log.CreateName, &log.CreatedAt); err != nil {
		return app.JobLog{}, err
	}
	log.SuccessFlag = success == 1
	return log, nil
}

// InsertJobLog stores an execution record and returns its id.
func (r *JobRepository) InsertJobLog(ctx context.Context, log app.JobLog) (uint64, error) {
	result, err := r.store.Exec(ctx,
		`INSERT INTO support_job_logs
(job_id, job_name, param, success_flag, execute_start_time, execute_time_millis,
execute_end_time, execute_result, ip, process_id, program_path, create_name)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		log.JobID, log.JobName, log.Param, boolToTinyInt(log.SuccessFlag),
		log.ExecuteStartTime, log.ExecuteTimeMillis, log.ExecuteEndTime, log.ExecuteResult,
		log.IP, log.ProcessID, log.ProgramPath, log.CreateName)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	return uint64(id), err
}

// ListJobLogs returns a filtered log page.
func (r *JobRepository) ListJobLogs(ctx context.Context, query app.JobLogQuery) (app.JobLogPage, error) {
	where := []string{"1=1"}
	args := []any{}
	if value := strings.TrimSpace(query.SearchWord); value != "" {
		where = append(where, "job_name LIKE ?")
		args = append(args, "%"+value+"%")
	}
	if query.JobID != nil {
		where = append(where, "job_id = ?")
		args = append(args, *query.JobID)
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
		`SELECT COUNT(*) FROM support_job_logs WHERE `+filter, args...).Scan(&total); err != nil {
		return app.JobLogPage{}, err
	}
	pageArgs := append(append([]any{}, args...), query.PageSize, (query.PageNum-1)*query.PageSize)
	rows, err := r.store.QueryRows(ctx,
		`SELECT `+jobLogColumns+` FROM support_job_logs WHERE `+filter+
			` ORDER BY created_at DESC, log_id DESC LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return app.JobLogPage{}, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]app.JobLog, 0, query.PageSize)
	for rows.Next() {
		log, err := scanJobLog(rows)
		if err != nil {
			return app.JobLogPage{}, err
		}
		items = append(items, log)
	}
	return app.JobLogPage{List: items, Total: total}, rows.Err()
}

// GetJobLogsByIDs loads logs for the last-log enrichment.
func (r *JobRepository) GetJobLogsByIDs(ctx context.Context, ids []uint64) (map[uint64]app.JobLog, error) {
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	rows, err := r.store.QueryRows(ctx,
		`SELECT `+jobLogColumns+` FROM support_job_logs WHERE log_id IN (`+
			strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make(map[uint64]app.JobLog, len(ids))
	for rows.Next() {
		log, err := scanJobLog(rows)
		if err != nil {
			return nil, err
		}
		result[log.LogID] = log
	}
	return result, rows.Err()
}
