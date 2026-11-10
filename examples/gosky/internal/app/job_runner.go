package app

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/jobruntime"
)

// run executes one job and records its log row, updating the job's last run
// state. Manual runs and scheduler runs share this path.
func (s *JobService) run(ctx context.Context, job Job, param, operator string) (JobLog, *coreerrors.Error) {
	executor, ok := s.registry.Get(job.JobClass)
	start := s.now()
	logRow := JobLog{
		JobID:            job.ID,
		JobName:          job.JobName,
		Param:            truncateRunes(param, executeParamRunes),
		ExecuteStartTime: start,
		IP:               "127.0.0.1",
		ProcessID:        strconv.Itoa(os.Getpid()),
		ProgramPath:      workingDirectory(),
		CreateName:       truncateRunes(operator, 255),
	}
	if !ok {
		// A deleted registration means the code no longer ships the executor.
		return s.finishFailedRun(ctx, logRow, "没有在代码中发现执行类："+job.JobClass)
	}
	result, runErr := safeExecute(ctx, executor, param)
	end := s.now()
	logRow.ExecuteEndTime = end
	logRow.ExecuteTimeMillis = end.Sub(start).Milliseconds()
	if runErr != nil {
		return s.finishFailedRun(ctx, logRow, truncateRunes(runErr.Error(), jobResultRunes))
	}
	logRow.SuccessFlag = true
	logRow.ExecuteResult = truncateRunes(result, jobResultRunes)
	return s.persistRun(ctx, logRow, nil)
}

// safeExecute guards a job panic so the scheduler goroutine survives a
// broken executor.
func safeExecute(ctx context.Context, executor jobruntime.Job, param string) (result string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = errors.New("job panic")
		}
	}()
	return executor.Execute(ctx, param)
}

func (s *JobService) finishFailedRun(ctx context.Context, logRow JobLog, reason string) (JobLog, *coreerrors.Error) {
	end := s.now()
	logRow.ExecuteEndTime = end
	logRow.ExecuteTimeMillis = end.Sub(logRow.ExecuteStartTime).Milliseconds()
	logRow.SuccessFlag = false
	logRow.ExecuteResult = reason
	persisted, persistErr := s.persistRun(ctx, logRow, nil)
	if persistErr != nil {
		return persisted, persistErr
	}
	return persisted, coreerrors.New(coreerrors.CodeInternal, reason)
}

func (s *JobService) persistRun(ctx context.Context, logRow JobLog, _ error) (JobLog, *coreerrors.Error) {
	logID, storeErr := s.store.InsertJobLog(ctx, logRow)
	if storeErr != nil {
		return logRow, coreerrors.Wrap(coreerrors.CodeUnavailable, "insert job log", storeErr)
	}
	logRow.LogID = logID
	if storeErr := s.store.UpdateJobRunState(ctx, logRow.JobID, logID, logRow.ExecuteStartTime); storeErr != nil {
		// Logs are durable even when the last-run pointer fails to update.
		return logRow, coreerrors.Wrap(coreerrors.CodeUnavailable, "update job run state", storeErr)
	}
	return logRow, nil
}

func workingDirectory() string {
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return ""
}

// JobScheduler polls enabled jobs on a one-second tick and executes jobs
// whose cron or fixed-delay trigger is due. It is the single-process
// equivalent of the Java SmartJobLauncher; overlap is prevented per job.
type JobScheduler struct {
	service *JobService
	store   JobStore
	now     func() time.Time
	tick    time.Duration

	mu      sync.Mutex
	running map[uint64]bool
}

// NewJobScheduler constructs the scheduler with a one-second tick.
func NewJobScheduler(service *JobService) *JobScheduler {
	return &JobScheduler{
		service: service,
		store:   service.store,
		now:     service.now,
		tick:    time.Second,
		running: make(map[uint64]bool),
	}
}

// Run polls until ctx is canceled.
func (s *JobScheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.poll(ctx)
		}
	}
}

func (s *JobScheduler) poll(ctx context.Context) {
	jobs, err := s.store.ListEnabledJobs(ctx)
	if err != nil {
		return
	}
	now := s.now()
	for _, job := range jobs {
		if !s.due(job, now) {
			continue
		}
		if !s.claim(job.ID) {
			continue
		}
		go func(job Job) {
			defer s.release(job.ID)
			if _, err := s.service.run(ctx, job, job.Param, "system"); err != nil {
				_ = err
			}
		}(job)
	}
}

func (s *JobScheduler) claim(id uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running[id] {
		return false
	}
	s.running[id] = true
	return true
}

func (s *JobScheduler) release(id uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.running, id)
}

// due reports whether the job should fire at now.
func (s *JobScheduler) due(job Job, now time.Time) bool {
	last := job.LastExecuteTime
	switch job.TriggerType {
	case jobruntime.TriggerFixedDelay:
		seconds, err := jobruntime.ParseFixedDelay(job.TriggerValue)
		if err != nil {
			return false
		}
		if last == nil {
			return true
		}
		return !now.Before(last.Add(time.Duration(seconds) * time.Second))
	case jobruntime.TriggerCron:
		from := now.Add(-time.Second)
		if last != nil {
			from = *last
		}
		next, err := jobruntime.NextCron(job.TriggerValue, from, 1)
		if err != nil || len(next) != 1 {
			return false
		}
		return !next[0].After(now)
	}
	return false
}
