package app

import (
	"context"
	"errors"
	"strings"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
	"github.com/imajinyun/gofly/examples/gosky/internal/jobruntime"
)

// Job bounds.
const (
	jobParamRunes     = 1000
	executeParamRunes = 2000
	jobResultRunes    = 2000
	nextExecuteCount  = 5
	manualExecuteName = "manual"
)

// Job is one scheduled job definition.
type Job struct {
	ID               uint64     `json:"jobId"`
	JobName          string     `json:"jobName"`
	JobClass         string     `json:"jobClass"`
	TriggerType      string     `json:"triggerType"`
	TriggerValue     string     `json:"triggerValue"`
	Param            string     `json:"param"`
	EnabledFlag      bool       `json:"enabledFlag"`
	LastExecuteTime  *time.Time `json:"lastExecuteTime"`
	LastExecuteLogID *uint64    `json:"lastExecuteLogId"`
	Remark           string     `json:"remark"`
	Sort             int        `json:"sort"`
	UpdateName       string     `json:"updateName"`
	DeletedFlag      bool       `json:"-"`
	CreatedAt        time.Time  `json:"createTime"`
	UpdatedAt        time.Time  `json:"updateTime"`

	LastJobLog             *JobLog     `json:"lastJobLog,omitempty"`
	NextJobExecuteTimeList []time.Time `json:"nextJobExecuteTimeList,omitempty"`
}

// JobLog is one execution record.
type JobLog struct {
	LogID             uint64    `json:"logId"`
	JobID             uint64    `json:"jobId"`
	JobName           string    `json:"jobName"`
	Param             string    `json:"param"`
	SuccessFlag       bool      `json:"successFlag"`
	ExecuteStartTime  time.Time `json:"executeStartTime"`
	ExecuteTimeMillis int64     `json:"executeTimeMillis"`
	ExecuteEndTime    time.Time `json:"executeEndTime"`
	ExecuteResult     string    `json:"executeResult"`
	IP                string    `json:"ip"`
	ProcessID         string    `json:"processId"`
	ProgramPath       string    `json:"programPath"`
	CreateName        string    `json:"createName"`
	CreatedAt         time.Time `json:"createTime"`
}

// JobPage is the {list,total} envelope.
type JobPage struct {
	List  []Job `json:"list"`
	Total int   `json:"total"`
}

// JobLogPage is the {list,total} envelope for logs.
type JobLogPage struct {
	List  []JobLog `json:"list"`
	Total int      `json:"total"`
}

// JobInput is the add/update payload.
type JobInput struct {
	ID           uint64 `json:"jobId"`
	JobName      string `json:"jobName"`
	JobClass     string `json:"jobClass"`
	TriggerType  string `json:"triggerType"`
	TriggerValue string `json:"triggerValue"`
	Param        string `json:"param"`
	EnabledFlag  *bool  `json:"enabledFlag"`
	Remark       string `json:"remark"`
	Sort         *int   `json:"sort"`
	UpdateName   string `json:"-"`
}

// JobEnabledInput toggles a job.
type JobEnabledInput struct {
	ID          uint64 `json:"jobId"`
	EnabledFlag bool   `json:"enabledFlag"`
	UpdateName  string `json:"-"`
}

// JobExecuteInput triggers an immediate run.
type JobExecuteInput struct {
	ID         uint64 `json:"jobId"`
	Param      string `json:"param"`
	UpdateName string `json:"-"`
}

// JobQuery carries console filters.
type JobQuery struct {
	SearchWord  string
	TriggerType string
	EnabledFlag *bool
	DeletedFlag bool
	PageNum     uint64
	PageSize    uint64
}

// JobLogQuery carries log console filters.
type JobLogQuery struct {
	SearchWord  string
	JobID       *uint64
	SuccessFlag *bool
	StartDate   string
	EndDate     string
	PageNum     uint64
	PageSize    uint64
}

// JobStore is the persistence boundary.
type JobStore interface {
	ListJobs(ctx context.Context, query JobQuery) (JobPage, error)
	GetJob(ctx context.Context, id uint64, includeDeleted bool) (Job, error)
	GetActiveJobByClass(ctx context.Context, class string, excludeID uint64) (Job, bool, error)
	InsertJob(ctx context.Context, input JobInput) (uint64, error)
	UpdateJob(ctx context.Context, input JobInput) error
	UpdateJobEnabled(ctx context.Context, input JobEnabledInput) error
	SoftDeleteJob(ctx context.Context, id uint64, updateName string) error
	ListEnabledJobs(ctx context.Context) ([]Job, error)
	UpdateJobRunState(ctx context.Context, jobID, logID uint64, at time.Time) error

	InsertJobLog(ctx context.Context, log JobLog) (uint64, error)
	ListJobLogs(ctx context.Context, query JobLogQuery) (JobLogPage, error)
	GetJobLogsByIDs(ctx context.Context, ids []uint64) (map[uint64]JobLog, error)
}

// JobService owns validation, console queries, immediate execution and the
// enrichment of last-log / next-run projections.
type JobService struct {
	store      JobStore
	registry   *jobruntime.Registry
	authorizer RBACAuthorizer
	now        func() time.Time
}

// NewJobService constructs the job service.
func NewJobService(store JobStore, registry *jobruntime.Registry, authorizer RBACAuthorizer) *JobService {
	return &JobService{
		store:      store,
		registry:   registry,
		authorizer: authorizer,
		now:        time.Now,
	}
}

// mapJobError turns a not-found store error into a client error.
func mapJobError(action string, err error) *coreerrors.Error {
	if errors.Is(err, ErrJobNotFound) {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "数据不存在")
	}
	return coreerrors.Wrap(coreerrors.CodeUnavailable, action, err)
}

func (s *JobService) validateInput(input JobInput) *coreerrors.Error {
	name := strings.TrimSpace(input.JobName)
	class := strings.TrimSpace(input.JobClass)
	if name == "" || len([]rune(name)) > 100 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "任务名称不能为空且最多100字符")
	}
	if class == "" || len(class) > 200 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "任务执行类不能为空且最多200字符")
	}
	if _, ok := s.registry.Get(class); !ok {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "没有在代码中发现执行类："+class+"（或执行类未注册）")
	}
	value := strings.TrimSpace(input.TriggerValue)
	switch input.TriggerType {
	case jobruntime.TriggerCron:
		if value == "" || len(value) > 100 || !jobruntime.ValidateCron(value) {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "cron表达式错误")
		}
	case jobruntime.TriggerFixedDelay:
		if !jobruntime.ValidateFixedDelay(value) {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "固定间隔配置错误：必须是大于0的整数")
		}
	default:
		return coreerrors.New(coreerrors.CodeInvalidArgument, "触发类型错误")
	}
	if len([]rune(input.Param)) > jobParamRunes {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "定时任务参数最多1000字符")
	}
	if input.EnabledFlag == nil {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "是否开启不能为空")
	}
	if input.Sort == nil {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "排序不能为空")
	}
	if len([]rune(input.Remark)) > 250 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "任务备注最多250字符")
	}
	return nil
}

// List serves the job console (including the deleted-jobs modal).
func (s *JobService) List(ctx context.Context, query JobQuery) (JobPage, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportJobRead); err != nil {
		return JobPage{}, err
	}
	query.PageNum, query.PageSize = NormalizePage(query.PageNum, query.PageSize)
	page, storeErr := s.store.ListJobs(ctx, query)
	if storeErr != nil {
		return JobPage{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "list jobs", storeErr)
	}
	if enrichErr := s.enrich(ctx, page.List); enrichErr != nil {
		return JobPage{}, enrichErr
	}
	return page, nil
}

// Detail returns one job with last log and next runs.
func (s *JobService) Detail(ctx context.Context, id uint64) (Job, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportJobRead); err != nil {
		return Job{}, err
	}
	job, storeErr := s.store.GetJob(ctx, id, true)
	if storeErr != nil {
		return Job{}, mapJobError("get job", storeErr)
	}
	if err := s.enrich(ctx, []Job{job}); err != nil {
		return Job{}, err
	}
	return job, nil
}

func (s *JobService) enrich(ctx context.Context, jobs []Job) *coreerrors.Error {
	logIDs := make([]uint64, 0, len(jobs))
	for _, job := range jobs {
		if job.LastExecuteLogID != nil {
			logIDs = append(logIDs, *job.LastExecuteLogID)
		}
	}
	logMap := map[uint64]JobLog{}
	if len(logIDs) > 0 {
		var err error
		logMap, err = s.store.GetJobLogsByIDs(ctx, logIDs)
		if err != nil {
			return coreerrors.Wrap(coreerrors.CodeUnavailable, "load job logs", err)
		}
	}
	for i := range jobs {
		if jobs[i].LastExecuteLogID != nil {
			if log, ok := logMap[*jobs[i].LastExecuteLogID]; ok {
				lastLog := log
				jobs[i].LastJobLog = &lastLog
			}
		}
		if jobs[i].EnabledFlag && !jobs[i].DeletedFlag {
			jobs[i].NextJobExecuteTimeList = s.nextRuns(jobs[i])
		}
	}
	return nil
}

// nextRuns projects the next 5 fire times from now (nil last run uses now).
func (s *JobService) nextRuns(job Job) []time.Time {
	now := s.now()
	switch job.TriggerType {
	case jobruntime.TriggerCron:
		times, err := jobruntime.NextCron(job.TriggerValue, now, nextExecuteCount)
		if err != nil {
			return nil
		}
		return times
	case jobruntime.TriggerFixedDelay:
		seconds, err := jobruntime.ParseFixedDelay(job.TriggerValue)
		if err != nil {
			return nil
		}
		start := now
		if job.LastExecuteTime != nil && job.LastExecuteTime.Add(time.Duration(seconds)*time.Second).After(now) {
			start = *job.LastExecuteTime
		}
		times := make([]time.Time, 0, nextExecuteCount)
		for range nextExecuteCount {
			start = start.Add(time.Duration(seconds) * time.Second)
			times = append(times, start)
		}
		return times
	}
	return nil
}

// Add creates a job; the class must be unique among active jobs.
func (s *JobService) Add(ctx context.Context, input JobInput) *coreerrors.Error {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportJobManage); err != nil {
		return err
	}
	if err := s.validateInput(input); err != nil {
		return err
	}
	if existing, ok, err := s.store.GetActiveJobByClass(ctx, strings.TrimSpace(input.JobClass), 0); err != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "check job class", err)
	} else if ok {
		_ = existing
		return coreerrors.New(coreerrors.CodeInvalidArgument, "已经存在相同的执行类")
	}
	if _, storeErr := s.store.InsertJob(ctx, input); storeErr != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "add job", storeErr)
	}
	return nil
}

// Update modifies a job.
func (s *JobService) Update(ctx context.Context, input JobInput) *coreerrors.Error {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportJobManage); err != nil {
		return err
	}
	if input.ID == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "任务id不能为空")
	}
	if _, storeErr := s.store.GetJob(ctx, input.ID, false); storeErr != nil {
		return mapJobError("get job", storeErr)
	}
	if err := s.validateInput(input); err != nil {
		return err
	}
	if existing, ok, err := s.store.GetActiveJobByClass(ctx, strings.TrimSpace(input.JobClass), input.ID); err != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "check job class", err)
	} else if ok {
		_ = existing
		return coreerrors.New(coreerrors.CodeInvalidArgument, "已经存在相同的执行类")
	}
	if storeErr := s.store.UpdateJob(ctx, input); storeErr != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "update job", storeErr)
	}
	return nil
}

// UpdateEnabled toggles a job; identical state is a no-op success.
func (s *JobService) UpdateEnabled(ctx context.Context, input JobEnabledInput) *coreerrors.Error {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportJobManage); err != nil {
		return err
	}
	if input.ID == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "任务id不能为空")
	}
	if _, storeErr := s.store.GetJob(ctx, input.ID, false); storeErr != nil {
		return mapJobError("get job", storeErr)
	}
	if storeErr := s.store.UpdateJobEnabled(ctx, input); storeErr != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "update job enabled", storeErr)
	}
	return nil
}

// Delete soft-deletes a job.
func (s *JobService) Delete(ctx context.Context, id uint64, operator string) *coreerrors.Error {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportJobManage); err != nil {
		return err
	}
	if id == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "任务id不能为空")
	}
	if storeErr := s.store.SoftDeleteJob(ctx, id, operator); storeErr != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "delete job", storeErr)
	}
	return nil
}

// Execute runs a job immediately and records the result.
func (s *JobService) Execute(ctx context.Context, input JobExecuteInput) *coreerrors.Error {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportJobExecute); err != nil {
		return err
	}
	if input.ID == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "任务id不能为空")
	}
	if len([]rune(input.Param)) > executeParamRunes {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "定时任务参数最多2000字符")
	}
	job, storeErr := s.store.GetJob(ctx, input.ID, false)
	if storeErr != nil {
		return mapJobError("get job", storeErr)
	}
	param := input.Param
	if strings.TrimSpace(param) == "" {
		param = job.Param
	}
	name := strings.TrimSpace(input.UpdateName)
	if name == "" {
		name = manualExecuteName
	}
	if _, err := s.run(ctx, job, param, name); err != nil {
		// The execution itself is recorded; return the failure so the UI
		// surfaces the error immediately for manual runs.
		return err
	}
	return nil
}

// ListLogs serves the execution-record modal.
func (s *JobService) ListLogs(ctx context.Context, query JobLogQuery) (JobLogPage, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportJobRead); err != nil {
		return JobLogPage{}, err
	}
	query.PageNum, query.PageSize = NormalizePage(query.PageNum, query.PageSize)
	page, storeErr := s.store.ListJobLogs(ctx, query)
	if storeErr != nil {
		return JobLogPage{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "list job logs", storeErr)
	}
	return page, nil
}
