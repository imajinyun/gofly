package saga

import (
	"context"
	"errors"
	"testing"
)

func TestExecuteSuccess(t *testing.T) {
	var order []string
	s := New().
		Step("a", func(context.Context) error { order = append(order, "a"); return nil }, nil).
		Step("b", func(context.Context) error { order = append(order, "b"); return nil }, nil)

	if err := s.Execute(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(order) != 2 || order[0] != "a" || order[1] != "b" {
		t.Fatalf("unexpected order: %v", order)
	}
}

func TestExecuteCompensatesInReverse(t *testing.T) {
	var order []string
	boom := errors.New("boom")
	s := New().
		Step("a",
			func(context.Context) error { order = append(order, "do-a"); return nil },
			func(context.Context) error { order = append(order, "undo-a"); return nil }).
		Step("b",
			func(context.Context) error { order = append(order, "do-b"); return nil },
			func(context.Context) error { order = append(order, "undo-b"); return nil }).
		Step("c",
			func(context.Context) error { order = append(order, "do-c"); return boom },
			func(context.Context) error { order = append(order, "undo-c"); return nil })

	err := s.Execute(context.Background())
	var ee *ExecutionError
	if !errors.As(err, &ee) {
		t.Fatalf("want *ExecutionError, got %v", err)
	}
	if ee.Step != "c" || ee.Index != 2 || !errors.Is(ee, boom) {
		t.Fatalf("unexpected execution error: %+v", ee)
	}
	// c's action failed so c is NOT compensated; b then a compensate in reverse.
	want := []string{"do-a", "do-b", "do-c", "undo-b", "undo-a"}
	if !equal(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestExecuteNilCompensationSkipped(t *testing.T) {
	var undoB bool
	fail := errors.New("fail")
	s := New().
		Step("a", func(context.Context) error { return nil }, nil).
		Step("b", func(context.Context) error { return nil }, func(context.Context) error { undoB = true; return nil }).
		Step("c", func(context.Context) error { return fail }, nil)

	err := s.Execute(context.Background())
	if err == nil {
		t.Fatal("want error")
	}
	if !undoB {
		t.Fatal("b should have been compensated")
	}
}

func TestCompensationErrorsCollected(t *testing.T) {
	actErr := errors.New("act")
	compErr := errors.New("comp")
	s := New().
		Step("a", func(context.Context) error { return nil }, func(context.Context) error { return compErr }).
		Step("b", func(context.Context) error { return actErr }, nil)

	err := s.Execute(context.Background())
	var ee *ExecutionError
	if !errors.As(err, &ee) {
		t.Fatalf("want *ExecutionError, got %v", err)
	}
	if len(ee.Compensations) != 1 {
		t.Fatalf("want 1 compensation failure, got %d", len(ee.Compensations))
	}
	if ee.Compensations[0].Step != "a" {
		t.Fatalf("unexpected comp step %q", ee.Compensations[0].Step)
	}
	if !errors.Is(ee.CompensationErr(), compErr) {
		t.Fatalf("CompensationErr should wrap compErr, got %v", ee.CompensationErr())
	}
	if ee.CompensationErr() == nil {
		t.Fatal("CompensationErr should be non-nil")
	}
}

func TestExecuteContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var undoA bool
	s := New().
		Step("a", func(context.Context) error { cancel(); return nil }, func(context.Context) error { undoA = true; return nil }).
		Step("b", func(context.Context) error { t.Fatal("b must not run"); return nil }, nil)

	err := s.Execute(ctx)
	var ee *ExecutionError
	if !errors.As(err, &ee) {
		t.Fatalf("want *ExecutionError, got %v", err)
	}
	if !errors.Is(ee, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", ee.Err)
	}
	if !undoA {
		t.Fatal("a should be compensated after cancellation")
	}
}

func TestExecuteNilAction(t *testing.T) {
	s := New().Add(Step{Name: "a"})
	err := s.Execute(context.Background())
	if err == nil {
		t.Fatal("want error for nil action")
	}
}

func TestExecuteEmpty(t *testing.T) {
	if err := New().Execute(context.Background()); err != nil {
		t.Fatalf("empty saga should succeed, got %v", err)
	}
}

func TestErrorStrings(t *testing.T) {
	ee := &ExecutionError{Step: "x", Err: errors.New("boom")}
	if ee.Error() == "" {
		t.Fatal("empty error string")
	}
	ee.Compensations = []CompensationError{{Step: "y", Err: errors.New("c")}}
	if ee.Error() == "" {
		t.Fatal("empty error string with comps")
	}
	ce := CompensationError{Step: "y", Err: errors.New("c")}
	if ce.Error() == "" || !errors.Is(ce, ce.Err) {
		t.Fatal("compensation error broken")
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
