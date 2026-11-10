package app

import (
	"context"
	"errors"
	"testing"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
	"github.com/imajinyun/gofly/examples/gosky/internal/jobruntime"
)

// Failure-mode inventory:
//
//	JB-01 add/update validate name, class registration, trigger syntax,
//	      required enabled/sort fields, and duplicate active class.
//	JB-02 execute records success/failure logs and updates last-run state.
//	JB-03 scheduler due() honors fixed delay, cron, and last-run timestamps.
//	JB-04 console queries require the matching capabilities.
type fakeJobStore struct {
	jobs   map[uint64]Job
	logs   []JobLog
	nextID uint64
	fail   error
}

func newFakeJobStore() *fakeJobStore {
	return &fakeJobStore{jobs: map[uint64]Job{}, nextID: 1}
}

func (s *fakeJobStore) ListJobs(_ context.Context, query JobQuery) (JobPage, error) {
	items := make([]Job, 0)
	for _, job := range s.jobs {
		if job.DeletedFlag != query.DeletedFlag {
			continue
		}
		items = append(items, job)
	}
	return JobPage{List: items, Total: len(items)}, s.fail
}

func (s *fakeJobStore) GetJob(_ context.Context, id uint64, includeDeleted bool) (Job, error) {
	job, ok := s.jobs[id]
	if !ok || (!includeDeleted && job.DeletedFlag) {
		return Job{}, ErrJobNotFound
	}
	return job, nil
}

func (s *fakeJobStore) GetActiveJobByClass(_ context.Context, class string, excludeID uint64) (Job, bool, error) {
	for _, job := range s.jobs {
		if !job.DeletedFlag && job.JobClass == class && job.ID != excludeID {
			return job, true, nil
		}
	}
	return Job{}, false, nil
}

func (s *fakeJobStore) InsertJob(_ context.Context, input JobInput) (uint64, error) {
	if s.fail != nil {
		return 0, s.fail
	}
	id := s.nextID
	s.nextID++
	enabled := false
	if input.EnabledFlag != nil {
		enabled = *input.EnabledFlag
	}
	sortValue := 0
	if input.Sort != nil {
		sortValue = *input.Sort
	}
	s.jobs[id] = Job{
		ID: id, JobName: input.JobName, JobClass: input.JobClass,
		TriggerType: input.TriggerType, TriggerValue: input.TriggerValue,
		Param: input.Param, EnabledFlag: enabled, Remark: input.Remark,
		Sort: sortValue,
	}
	return id, nil
}

func (s *fakeJobStore) UpdateJob(_ context.Context, input JobInput) error {
	job := s.jobs[input.ID]
	enabled := false
	if input.EnabledFlag != nil {
		enabled = *input.EnabledFlag
	}
	sortValue := 0
	if input.Sort != nil {
		sortValue = *input.Sort
	}
	job.JobName, job.JobClass = input.JobName, input.JobClass
	job.TriggerType, job.TriggerValue = input.TriggerType, input.TriggerValue
	job.Param, job.EnabledFlag, job.Remark, job.Sort = input.Param, enabled, input.Remark, sortValue
	s.jobs[input.ID] = job
	return s.fail
}

func (s *fakeJobStore) UpdateJobEnabled(_ context.Context, input JobEnabledInput) error {
	job := s.jobs[input.ID]
	job.EnabledFlag = input.EnabledFlag
	s.jobs[input.ID] = job
	return s.fail
}

func (s *fakeJobStore) SoftDeleteJob(_ context.Context, id uint64, _ string) error {
	job := s.jobs[id]
	job.DeletedFlag = true
	job.EnabledFlag = false
	s.jobs[id] = job
	return s.fail
}

func (s *fakeJobStore) ListEnabledJobs(_ context.Context) ([]Job, error) {
	items := make([]Job, 0)
	for _, job := range s.jobs {
		if job.EnabledFlag && !job.DeletedFlag {
			items = append(items, job)
		}
	}
	return items, s.fail
}

func (s *fakeJobStore) UpdateJobRunState(_ context.Context, jobID, logID uint64, at time.Time) error {
	job := s.jobs[jobID]
	job.LastExecuteTime = &at
	id := logID
	job.LastExecuteLogID = &id
	s.jobs[jobID] = job
	return s.fail
}

func (s *fakeJobStore) InsertJobLog(_ context.Context, log JobLog) (uint64, error) {
	if s.fail != nil {
		return 0, s.fail
	}
	log.LogID = uint64(len(s.logs) + 1)
	s.logs = append(s.logs, log)
	return log.LogID, nil
}

func (s *fakeJobStore) ListJobLogs(_ context.Context, _ JobLogQuery) (JobLogPage, error) {
	return JobLogPage{List: s.logs, Total: len(s.logs)}, s.fail
}

func (s *fakeJobStore) GetJobLogsByIDs(_ context.Context, ids []uint64) (map[uint64]JobLog, error) {
	result := make(map[uint64]JobLog, len(ids))
	for _, log := range s.logs {
		result[log.LogID] = log
	}
	return result, s.fail
}

func newJobTestService() (*JobService, *fakeJobStore, *jobruntime.Registry) {
	registry := jobruntime.NewRegistry()
	_ = registry.Register("test.ok", jobruntime.JobFunc(func(_ context.Context, param string) (string, error) {
		return "ran:" + param, nil
	}))
	_ = registry.Register("test.fail", jobruntime.JobFunc(func(_ context.Context, _ string) (string, error) {
		return "", errors.New("boom")
	}))
	store := newFakeJobStore()
	service := NewJobService(store, registry, dictCapAuthorizer{allowed: capSet([]authz.Capability{
		authz.CapabilitySupportJobRead,
		authz.CapabilitySupportJobManage,
		authz.CapabilitySupportJobExecute,
	})})
	service.now = func() time.Time { return time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC) }
	return service, store, registry
}

func boolPtr(value bool) *bool { return &value }
func intPtr(value int) *int    { return &value }

func validJobInput() JobInput {
	return JobInput{
		JobName: "Nightly job", JobClass: "test.ok",
		TriggerType: "fixed_delay", TriggerValue: "60",
		EnabledFlag: boolPtr(true), Sort: intPtr(1),
	}
}

func TestJobAddValidation(t *testing.T) {
	service, _, _ := newJobTestService()
	ctx := fileCtx(authz.CapabilitySupportJobManage)

	cases := []struct {
		name   string
		mutate func(*JobInput)
	}{
		{"blank name", func(i *JobInput) { i.JobName = " " }},
		{"unknown class", func(i *JobInput) { i.JobClass = "ghost.Job" }},
		{"bad cron", func(i *JobInput) { i.TriggerType = "cron"; i.TriggerValue = "not cron" }},
		{"bad fixed delay", func(i *JobInput) { i.TriggerValue = "0" }},
		{"bad trigger type", func(i *JobInput) { i.TriggerType = "interval" }},
		{"missing enabled", func(i *JobInput) { i.EnabledFlag = nil }},
		{"missing sort", func(i *JobInput) { i.Sort = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := validJobInput()
			tc.mutate(&input)
			if err := service.Add(ctx, input); err == nil {
				t.Fatalf("%s accepted", tc.name)
			}
		})
	}

	if err := service.Add(ctx, validJobInput()); err != nil {
		t.Fatalf("valid add: %v", err)
	}
	duplicate := validJobInput()
	duplicate.JobName = "Other name"
	if err := service.Add(ctx, duplicate); err == nil {
		t.Fatal("duplicate class accepted")
	}
}

func TestJobExecuteRecordsLog(t *testing.T) {
	service, store, _ := newJobTestService()
	ctx := fileCtx(authz.CapabilitySupportJobManage, authz.CapabilitySupportJobExecute)

	id, err := store.InsertJob(ctx, validJobInput())
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	execErr := service.Execute(ctx, JobExecuteInput{ID: id, Param: "p1", UpdateName: "admin"})
	if execErr != nil {
		t.Fatalf("execute: %v", execErr)
	}
	if len(store.logs) != 1 || !store.logs[0].SuccessFlag || store.logs[0].ExecuteResult != "ran:p1" {
		t.Fatalf("success log = %#v", store.logs)
	}
	job := store.jobs[id]
	if job.LastExecuteTime == nil || job.LastExecuteLogID == nil {
		t.Fatal("last-run state not updated")
	}

	failInput := validJobInput()
	failInput.JobClass = "test.fail"
	failID, _ := store.InsertJob(ctx, failInput)
	if err := service.Execute(ctx, JobExecuteInput{ID: failID}); err == nil {
		t.Fatal("failing job should return an error")
	}
	if store.logs[1].SuccessFlag || store.logs[1].ExecuteResult != "boom" {
		t.Fatalf("failure log = %#v", store.logs[1])
	}
}

func TestJobSchedulerDue(t *testing.T) {
	service, _, _ := newJobTestService()
	now := service.now()
	scheduler := NewJobScheduler(service)

	// no last run: immediately due
	if !scheduler.due(Job{TriggerType: "fixed_delay", TriggerValue: "60"}, now) {
		t.Fatal("fixed delay without last run should be due")
	}
	last := now.Add(-30 * time.Second)
	early := Job{TriggerType: "fixed_delay", TriggerValue: "60", LastExecuteTime: &last}
	if scheduler.due(early, now) {
		t.Fatal("fixed delay not elapsed yet")
	}
	elapsed := now.Add(-60 * time.Second)
	dueJob := Job{TriggerType: "fixed_delay", TriggerValue: "60", LastExecuteTime: &elapsed}
	if !scheduler.due(dueJob, now) {
		t.Fatal("fixed delay elapsed should be due")
	}
	cronJob := Job{TriggerType: "cron", TriggerValue: "0 * * * * *", LastExecuteTime: &last}
	if !scheduler.due(cronJob, now) {
		t.Fatal("cron job due at 10:00 should fire")
	}
}

func TestJobCapabilityGates(t *testing.T) {
	service, _, _ := newJobTestService()
	denyRegistry := jobruntime.NewRegistry()
	denyStore := newFakeJobStore()
	denyService := NewJobService(denyStore, denyRegistry, dictCapAuthorizer{allowed: capSet(nil)})
	if _, err := denyService.List(fileCtx(), JobQuery{PageNum: 1, PageSize: 10}); err == nil || err.Code != coreerrors.CodePermissionDenied {
		t.Fatalf("list without cap = %v", err)
	}
	if _, err := denyService.Detail(fileCtx(), 1); err == nil || err.Code != coreerrors.CodePermissionDenied {
		t.Fatalf("detail without cap = %v", err)
	}
	page, err := service.List(fileCtx(authz.CapabilitySupportJobRead), JobQuery{PageNum: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list with cap: %v", err)
	}
	if page.Total != 0 {
		t.Fatalf("empty page total = %d", page.Total)
	}
}
