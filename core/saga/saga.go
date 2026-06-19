// Package saga provides a small in-process orchestrator for the saga pattern:
// a sequence of steps executed in order, each with an optional compensation. If
// a step fails, the orchestrator runs the compensations of the already-completed
// steps in reverse order to unwind the partial work.
//
// It is dependency-free and synchronous; pair it with core/outbox or core/mq
// when steps cross service boundaries.
package saga

import (
	"context"
	"errors"
	"fmt"

	core "github.com/gofly/gofly/core"
)

// Action performs the forward work of a step.
type Action func(ctx context.Context) error

// Compensation undoes the work of a previously executed step. It is only invoked
// for steps whose Action completed successfully.
type Compensation func(ctx context.Context) error

// Step is a single unit of a saga. Name is used in error reporting. Compensate
// may be nil for steps that need no rollback.
type Step struct {
	Name       string
	Do         Action
	Compensate Compensation
}

// Saga is an ordered collection of steps. The zero value is ready to use; add
// steps with Add or Step, then call Execute.
type Saga struct {
	steps []Step
}

// New returns an empty Saga.
func New() *Saga { return &Saga{} }

// Add appends a fully-formed step and returns the saga for chaining.
func (s *Saga) Add(step Step) *Saga {
	s.steps = append(s.steps, step)
	return s
}

// Step appends a named step from its action and compensation and returns the
// saga for chaining. Pass a nil compensate for steps that need no rollback.
func (s *Saga) Step(name string, do Action, compensate Compensation) *Saga {
	return s.Add(Step{Name: name, Do: do, Compensate: compensate})
}

// Execute runs each step in order. On the first failing step it stops and
// compensates the previously completed steps in reverse order, then returns an
// *ExecutionError describing the failure and any compensation errors.
//
// If the context is cancelled before a step runs, execution stops and the
// completed steps are compensated. A nil return means every step succeeded.
func (s *Saga) Execute(ctx context.Context) error {
	ctx = core.Context(ctx)
	completed := make([]int, 0, len(s.steps))
	for i, step := range s.steps {
		if step.Do == nil {
			err := fmt.Errorf("saga: step %q has nil action", stepName(step, i))
			return &ExecutionError{Step: stepName(step, i), Index: i, Err: err, Compensations: s.compensate(ctx, completed)}
		}
		if cerr := ctx.Err(); cerr != nil {
			return &ExecutionError{Step: stepName(step, i), Index: i, Err: cerr, Compensations: s.compensate(ctx, completed)}
		}
		if err := step.Do(ctx); err != nil {
			return &ExecutionError{Step: stepName(step, i), Index: i, Err: err, Compensations: s.compensate(ctx, completed)}
		}
		completed = append(completed, i)
	}
	return nil
}

// compensate runs the compensation of each completed step index in reverse
// order, collecting any failures.
func (s *Saga) compensate(ctx context.Context, completed []int) []CompensationError {
	var failures []CompensationError
	for i := len(completed) - 1; i >= 0; i-- {
		idx := completed[i]
		step := s.steps[idx]
		if step.Compensate == nil {
			continue
		}
		if err := step.Compensate(ctx); err != nil {
			failures = append(failures, CompensationError{Step: stepName(step, idx), Index: idx, Err: err})
		}
	}
	return failures
}

func stepName(step Step, index int) string {
	if step.Name != "" {
		return step.Name
	}
	return fmt.Sprintf("step[%d]", index)
}

// CompensationError records the failure of a single step's compensation.
type CompensationError struct {
	Step  string
	Index int
	Err   error
}

// Error implements error.
func (e CompensationError) Error() string {
	return fmt.Sprintf("saga: compensation for step %q failed: %v", e.Step, e.Err)
}

// Unwrap exposes the underlying error.
func (e CompensationError) Unwrap() error { return e.Err }

// ExecutionError describes a saga that aborted at a step, along with any errors
// raised while compensating the completed steps.
type ExecutionError struct {
	// Step is the name of the step whose action failed (or was blocked).
	Step string
	// Index is the position of the failing step.
	Index int
	// Err is the action failure (or context error) that triggered the rollback.
	Err error
	// Compensations holds failures from unwinding completed steps, if any.
	Compensations []CompensationError
}

// Error implements error.
func (e *ExecutionError) Error() string {
	if len(e.Compensations) == 0 {
		return fmt.Sprintf("saga: step %q failed: %v", e.Step, e.Err)
	}
	return fmt.Sprintf("saga: step %q failed: %v; %d compensation(s) failed", e.Step, e.Err, len(e.Compensations))
}

// Unwrap returns the triggering error so errors.Is/As can inspect it.
func (e *ExecutionError) Unwrap() error { return e.Err }

// CompensationErr aggregates the compensation failures, or nil if none.
func (e *ExecutionError) CompensationErr() error {
	if len(e.Compensations) == 0 {
		return nil
	}
	errs := make([]error, len(e.Compensations))
	for i, c := range e.Compensations {
		errs[i] = c
	}
	return errors.Join(errs...)
}
