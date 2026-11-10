// Package jobruntime is the code-owned registry of scheduled job
// executors and the small cron parser used by the support/job console.
// Executor keys never come from arbitrary request payloads: CRUD validation
// only accepts names registered here at process startup.
package jobruntime

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Job executes one scheduled run. The result string is stored in the job log
// (bounded by the service layer).
type Job interface {
	Execute(ctx context.Context, param string) (string, error)
}

// JobFunc adapts a function to the Job interface.
type JobFunc func(ctx context.Context, param string) (string, error)

// Execute calls the wrapped function.
func (f JobFunc) Execute(ctx context.Context, param string) (string, error) {
	return f(ctx, param)
}

// Registry maps code-owned executor keys to jobs.
type Registry struct {
	mu   sync.RWMutex
	jobs map[string]Job
}

// NewRegistry returns an empty job registry.
func NewRegistry() *Registry {
	return &Registry{jobs: make(map[string]Job)}
}

// Register installs a job under a key. Duplicate registration replaces the
// previous job (wiring happens once at startup).
func (r *Registry) Register(key string, job Job) error {
	if r == nil {
		return fmt.Errorf("job registry is nil")
	}
	if len(key) == 0 || len(key) > 200 {
		return fmt.Errorf("job key is invalid")
	}
	if job == nil {
		return fmt.Errorf("job %q executor is nil", key)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobs[key] = job
	return nil
}

// MustRegister panics when registration fails; intended for startup wiring.
func (r *Registry) MustRegister(key string, job Job) {
	if err := r.Register(key, job); err != nil {
		panic(err)
	}
}

// Get resolves a registered job.
func (r *Registry) Get(key string) (Job, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	job, ok := r.jobs[key]
	return job, ok
}

// Names returns sorted registered executor keys.
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.jobs))
	for key := range r.jobs {
		names = append(names, key)
	}
	sort.Strings(names)
	return names
}
