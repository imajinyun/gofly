// Package schedule provides a simple in-process job scheduler with interval
// triggers, optional startup runs, no-overlap guards and panic recovery.
package schedule

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"
)

var (
	// ErrJobExists is returned when adding a job whose name is already registered.
	ErrJobExists = errors.New("scheduled job already exists")
	// ErrJobNotFound is returned when removing or querying an unknown job.
	ErrJobNotFound = errors.New("scheduled job not found")
	// ErrInvalidJob is returned when a Job is missing a name, interval or handler.
	ErrInvalidJob = errors.New("invalid scheduled job")
	// ErrSchedulerState is returned when an operation is incompatible with the
	// current scheduler state (e.g. removing while running).
	ErrSchedulerState = errors.New("scheduler state does not allow operation")
)

// Handler is the function executed for each scheduled job invocation.
type Handler func(context.Context) error

// Job describes a recurring task managed by a Scheduler.
type Job struct {
	Name       string
	Interval   time.Duration
	Delay      time.Duration
	Timeout    time.Duration
	RunOnStart bool
	NoOverlap  bool
	Handler    Handler
}

// JobSnapshot captures the runtime state of a single job.
type JobSnapshot struct {
	Name        string        `json:"name"`
	Interval    time.Duration `json:"interval"`
	Delay       time.Duration `json:"delay,omitempty"`
	Timeout     time.Duration `json:"timeout,omitempty"`
	RunOnStart  bool          `json:"runOnStart"`
	NoOverlap   bool          `json:"noOverlap"`
	Runs        int64         `json:"runs"`
	Errors      int64         `json:"errors"`
	Panics      int64         `json:"panics"`
	Skipped     int64         `json:"skipped"`
	Running     bool          `json:"running"`
	LastStarted time.Time     `json:"lastStarted,omitempty"`
	LastEnded   time.Time     `json:"lastEnded,omitempty"`
	LastError   string        `json:"lastError,omitempty"`
}

// Snapshot captures the overall scheduler state and all job snapshots.
type Snapshot struct {
	Started bool                   `json:"started"`
	Jobs    map[string]JobSnapshot `json:"jobs"`
}

type jobState struct {
	job         Job
	runs        int64
	errors      int64
	panics      int64
	skipped     int64
	running     bool
	lastStarted time.Time
	lastEnded   time.Time
	lastError   string
}

// Scheduler runs named jobs on a repeating interval with optional delay,
// timeout and no-overlap protection.
type Scheduler struct {
	mu      sync.Mutex
	jobs    map[string]*jobState
	started bool
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// New creates an unstarted Scheduler.
func New() *Scheduler {
	return &Scheduler{jobs: make(map[string]*jobState)}
}

// Add registers a new job. If the scheduler is already started the job begins
// executing immediately according to its configuration.
func (s *Scheduler) Add(job Job) error {
	if err := validate(job); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[job.Name]; ok {
		return ErrJobExists
	}
	state := &jobState{job: job}
	s.jobs[job.Name] = state
	if s.started {
		s.startJobLocked(state)
	}
	return nil
}

// Remove unregisters a job. The scheduler must be stopped.
func (s *Scheduler) Remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return ErrSchedulerState
	}
	if _, ok := s.jobs[name]; !ok {
		return ErrJobNotFound
	}
	delete(s.jobs, name)
	return nil
}

// Start begins executing all registered jobs. It is safe to call multiple
// times; subsequent calls are no-ops.
func (s *Scheduler) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.started = true
	for _, state := range s.jobs {
		s.startJobLocked(state)
	}
	return nil
}

// Stop signals all running jobs to finish and waits for them, or until ctx
// is cancelled. It is safe to call multiple times.
func (s *Scheduler) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	cancel := s.cancel
	s.started = false
	s.cancel = nil
	s.ctx = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

// Snapshot returns a point-in-time view of the scheduler and all jobs.
func (s *Scheduler) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := make(map[string]JobSnapshot, len(s.jobs))
	for name, state := range s.jobs {
		jobs[name] = state.snapshotLocked()
	}
	return Snapshot{Started: s.started, Jobs: jobs}
}

func (s *Scheduler) startJobLocked(state *jobState) {
	ctx := s.ctx
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.runLoop(ctx, state)
	}()
}

func (s *Scheduler) runLoop(ctx context.Context, state *jobState) {
	job := state.job
	if job.RunOnStart {
		s.dispatch(ctx, state)
	}
	if job.Delay > 0 {
		timer := time.NewTimer(job.Delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		}
	}
	ticker := time.NewTicker(job.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.dispatch(ctx, state)
		}
	}
}

func (s *Scheduler) dispatch(ctx context.Context, state *jobState) {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return
	}
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		s.runOnce(ctx, state)
	}()
}

func (s *Scheduler) runOnce(parent context.Context, state *jobState) {
	job := state.job
	s.mu.Lock()
	if job.NoOverlap && state.running {
		state.skipped++
		s.mu.Unlock()
		return
	}
	state.running = true
	state.lastStarted = time.Now()
	s.mu.Unlock()

	var cancel context.CancelFunc
	var ctx context.Context
	if job.Timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, job.Timeout)
	} else {
		ctx, cancel = context.WithCancel(parent)
	}
	defer cancel()

	err := runSafely(ctx, job.Handler)
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	state.running = false
	state.lastEnded = now
	state.runs++
	if err != nil {
		if errors.Is(err, errPanic) {
			state.panics++
		} else {
			state.errors++
		}
		state.lastError = err.Error()
		return
	}
	state.lastError = ""
}

func (s *jobState) snapshotLocked() JobSnapshot {
	return JobSnapshot{
		Name:        s.job.Name,
		Interval:    s.job.Interval,
		Delay:       s.job.Delay,
		Timeout:     s.job.Timeout,
		RunOnStart:  s.job.RunOnStart,
		NoOverlap:   s.job.NoOverlap,
		Runs:        s.runs,
		Errors:      s.errors,
		Panics:      s.panics,
		Skipped:     s.skipped,
		Running:     s.running,
		LastStarted: s.lastStarted,
		LastEnded:   s.lastEnded,
		LastError:   s.lastError,
	}
}

func validate(job Job) error {
	if job.Name == "" || job.Interval <= 0 || job.Handler == nil {
		return ErrInvalidJob
	}
	return nil
}

var errPanic = errors.New("scheduled job panic")

func runSafely(ctx context.Context, handler Handler) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("%w: %v\n%s", errPanic, v, debug.Stack())
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	return handler(ctx)
}
