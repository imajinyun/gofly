// Package saga provides saga orchestration with step execution, compensation
// and persistent state tracking.
package saga

import (
	"context"
	"errors"
	"sync"
	"time"
)

// StepStatus is the lifecycle state of a single saga step.
type StepStatus string

const (
	// StepPending means the step has not run yet.
	StepPending StepStatus = "pending"
	// StepCompleted means the step's action succeeded.
	StepCompleted StepStatus = "completed"
	// StepFailed means the step's action failed.
	StepFailed StepStatus = "failed"
	// StepCompensated means the step's compensation ran.
	StepCompensated StepStatus = "compensated"
)

// SagaStatus is the lifecycle state of a saga instance.
type SagaStatus string

const (
	// StatusRunning means the saga is executing its forward steps.
	StatusRunning SagaStatus = "running"
	// StatusCompleted means every step completed successfully.
	StatusCompleted SagaStatus = "completed"
	// StatusCompensating means a step failed and compensations are running.
	StatusCompensating SagaStatus = "compensating"
	// StatusCompensated means the saga aborted and rolled back.
	StatusCompensated SagaStatus = "compensated"
)

// StepRecord captures the persisted state of one step.
type StepRecord struct {
	Name   string     `json:"name"`
	Index  int        `json:"index"`
	Status StepStatus `json:"status"`
	Error  string     `json:"error,omitempty"`
}

// State is the persisted snapshot of a saga instance.
type State struct {
	ID        string       `json:"id"`
	Status    SagaStatus   `json:"status"`
	Steps     []StepRecord `json:"steps"`
	UpdatedAt time.Time    `json:"updatedAt"`
}

// Clone returns a deep copy of the state so callers cannot mutate stored data.
func (s State) Clone() State {
	cp := s
	if s.Steps != nil {
		cp.Steps = make([]StepRecord, len(s.Steps))
		copy(cp.Steps, s.Steps)
	}
	return cp
}

// ErrNotFound is returned by a Store when no state exists for an ID.
var ErrNotFound = errors.New("saga: state not found")

// Store persists saga state so an orchestration can be observed or recovered.
type Store interface {
	// Save writes the full state, overwriting any prior snapshot for state.ID.
	Save(ctx context.Context, state State) error
	// Load returns the state for id or ErrNotFound.
	Load(ctx context.Context, id string) (State, error)
	// Delete removes the state for id; missing ids are not an error.
	Delete(ctx context.Context, id string) error
}

// MemoryStore is an in-memory Store implementation, safe for concurrent use.
type MemoryStore struct {
	mu     sync.RWMutex
	states map[string]State
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{states: make(map[string]State)}
}

// Save implements Store.
func (m *MemoryStore) Save(ctx context.Context, state State) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if state.ID == "" {
		return errors.New("saga: state ID required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[state.ID] = state.Clone()
	return nil
}

// Load implements Store.
func (m *MemoryStore) Load(ctx context.Context, id string) (State, error) {
	if err := ctxErr(ctx); err != nil {
		return State{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, ok := m.states[id]
	if !ok {
		return State{}, ErrNotFound
	}
	return state.Clone(), nil
}

// Delete implements Store.
func (m *MemoryStore) Delete(ctx context.Context, id string) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.states, id)
	return nil
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

// ExecutePersist runs the saga like Execute but records progress to store under
// id, updating step and saga status as the orchestration advances. The persisted
// state can be inspected after the call (including failures and compensations).
func (s *Saga) ExecutePersist(ctx context.Context, store Store, id string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if store == nil {
		return s.Execute(ctx)
	}
	state := State{
		ID:     id,
		Status: StatusRunning,
		Steps:  make([]StepRecord, len(s.steps)),
	}
	for i, step := range s.steps {
		state.Steps[i] = StepRecord{Name: stepName(step, i), Index: i, Status: StepPending}
	}
	if err := s.persist(ctx, store, &state); err != nil {
		return err
	}

	completed := make([]int, 0, len(s.steps))
	for i, step := range s.steps {
		if step.Do == nil {
			err := errors.New("saga: step " + stepName(step, i) + " has nil action")
			return s.persistFailure(ctx, store, &state, completed, i, step, err)
		}
		if cerr := ctx.Err(); cerr != nil {
			return s.persistFailure(ctx, store, &state, completed, i, step, cerr)
		}
		if err := step.Do(ctx); err != nil {
			return s.persistFailure(ctx, store, &state, completed, i, step, err)
		}
		state.Steps[i].Status = StepCompleted
		completed = append(completed, i)
		if err := s.persist(ctx, store, &state); err != nil {
			return err
		}
	}
	state.Status = StatusCompleted
	if err := s.persist(ctx, store, &state); err != nil {
		return err
	}
	return nil
}

func (s *Saga) persistFailure(ctx context.Context, store Store, state *State, completed []int, idx int, step Step, cause error) error {
	state.Steps[idx].Status = StepFailed
	state.Steps[idx].Error = cause.Error()
	state.Status = StatusCompensating
	_ = s.persist(ctx, store, state)

	comps := s.compensate(ctx, completed)
	failed := make(map[int]string, len(comps))
	for _, c := range comps {
		failed[c.Index] = c.Err.Error()
	}
	for i := len(completed) - 1; i >= 0; i-- {
		cidx := completed[i]
		if s.steps[cidx].Compensate == nil {
			continue
		}
		if msg, ok := failed[cidx]; ok {
			state.Steps[cidx].Error = msg
		} else {
			state.Steps[cidx].Status = StepCompensated
		}
	}
	state.Status = StatusCompensated
	_ = s.persist(ctx, store, state)

	return &ExecutionError{Step: stepName(step, idx), Index: idx, Err: cause, Compensations: comps}
}

func (s *Saga) persist(ctx context.Context, store Store, state *State) error {
	state.UpdatedAt = time.Now()
	return store.Save(ctx, state.Clone())
}
