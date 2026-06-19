package saga

import (
	"context"
	"errors"
	"testing"
)

func TestExecutePersistSuccess(t *testing.T) {
	store := NewMemoryStore()
	s := New().
		Step("a", func(context.Context) error { return nil }, nil).
		Step("b", func(context.Context) error { return nil }, nil)
	if err := s.ExecutePersist(context.Background(), store, "saga-1"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	state, err := store.Load(context.Background(), "saga-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if state.Status != StatusCompleted {
		t.Fatalf("status = %s, want completed", state.Status)
	}
	for _, st := range state.Steps {
		if st.Status != StepCompleted {
			t.Fatalf("step %s status = %s, want completed", st.Name, st.Status)
		}
	}
}

func TestExecutePersistFailureCompensates(t *testing.T) {
	store := NewMemoryStore()
	var compensatedA bool
	boom := errors.New("boom")
	s := New().
		Step("a",
			func(context.Context) error { return nil },
			func(context.Context) error { compensatedA = true; return nil }).
		Step("b",
			func(context.Context) error { return boom },
			nil)
	err := s.ExecutePersist(context.Background(), store, "saga-2")
	if err == nil {
		t.Fatal("expected execution error")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want boom", err)
	}
	if !compensatedA {
		t.Fatal("step a was not compensated")
	}
	state, err := store.Load(context.Background(), "saga-2")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if state.Status != StatusCompensated {
		t.Fatalf("status = %s, want compensated", state.Status)
	}
	if state.Steps[0].Status != StepCompensated {
		t.Fatalf("step a status = %s, want compensated", state.Steps[0].Status)
	}
	if state.Steps[1].Status != StepFailed {
		t.Fatalf("step b status = %s, want failed", state.Steps[1].Status)
	}
	if state.Steps[1].Error == "" {
		t.Fatal("step b error not recorded")
	}
}

func TestMemoryStoreCRUD(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.Load(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	st := State{ID: "x", Status: StatusRunning, Steps: []StepRecord{{Name: "a", Index: 0, Status: StepPending}}}
	if err := store.Save(context.Background(), st); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Mutating the returned copy must not affect the store.
	loaded, _ := store.Load(context.Background(), "x")
	loaded.Steps[0].Status = StepCompleted
	again, _ := store.Load(context.Background(), "x")
	if again.Steps[0].Status != StepPending {
		t.Fatal("store state mutated via returned clone")
	}
	if err := store.Delete(context.Background(), "x"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.Load(context.Background(), "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestExecutePersistNilStoreFallback(t *testing.T) {
	s := New().Step("a", func(context.Context) error { return nil }, nil)
	if err := s.ExecutePersist(context.Background(), nil, "saga-3"); err != nil {
		t.Fatalf("execute with nil store: %v", err)
	}
}
