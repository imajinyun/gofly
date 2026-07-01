package di

import (
	"errors"
	"strings"
	"testing"
)

type repo struct{ dsn string }

type service struct{ repo *repo }

func TestProvideResolveSingleton(t *testing.T) {
	c := New()
	calls := 0
	if err := Provide(c, func(*Container) (*repo, error) {
		calls++
		return &repo{dsn: "db"}, nil
	}); err != nil {
		t.Fatalf("Provide: %v", err)
	}

	r1, err := Resolve[*repo](c)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	r2, err := Resolve[*repo](c)
	if err != nil {
		t.Fatalf("Resolve(2): %v", err)
	}
	if r1 != r2 {
		t.Fatal("expected same singleton instance")
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d, want 1", calls)
	}
}

func TestResolveDependencyGraph(t *testing.T) {
	c := New()
	_ = Provide(c, func(*Container) (*repo, error) { return &repo{dsn: "db"}, nil })
	_ = Provide(c, func(c *Container) (*service, error) {
		r, err := Resolve[*repo](c)
		if err != nil {
			return nil, err
		}
		return &service{repo: r}, nil
	})

	svc, err := Resolve[*service](c)
	if err != nil {
		t.Fatalf("Resolve service: %v", err)
	}
	if svc.repo == nil || svc.repo.dsn != "db" {
		t.Fatalf("dependency not wired: %+v", svc)
	}
}

func TestResolveNotRegistered(t *testing.T) {
	c := New()
	if _, err := Resolve[*repo](c); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("err = %v, want ErrNotRegistered", err)
	}
}

func TestResolveCyclic(t *testing.T) {
	c := New()
	_ = Provide(c, func(c *Container) (*service, error) {
		return Resolve[*service](c)
	})
	if _, err := Resolve[*service](c); !errors.Is(err, ErrCyclic) {
		t.Fatalf("err = %v, want ErrCyclic", err)
	}
}

func TestSupply(t *testing.T) {
	c := New()
	want := &repo{dsn: "supplied"}
	if err := Supply(c, want); err != nil {
		t.Fatalf("Supply: %v", err)
	}
	got, err := Resolve[*repo](c)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != want {
		t.Fatal("Supply did not return the supplied value")
	}
}

type closableResource struct {
	name   string
	closed *[]string
}

func (r *closableResource) Close() error {
	*r.closed = append(*r.closed, r.name)
	return nil
}

func TestCloseReverseOrder(t *testing.T) {
	c := New()
	var order []string
	_ = Provide(c, func(*Container) (*closableResource, error) {
		return &closableResource{name: "a", closed: &order}, nil
	})
	if _, err := Resolve[*closableResource](c); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Resolve a second closer of a different type to verify reverse order.
	type other struct{ *closableResource }
	_ = Provide(c, func(*Container) (*other, error) {
		return &other{&closableResource{name: "b", closed: &order}}, nil
	})
	if _, err := Resolve[*other](c); err != nil {
		t.Fatalf("Resolve other: %v", err)
	}

	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(order) != 2 || order[0] != "b" || order[1] != "a" {
		t.Fatalf("close order = %v, want [b a]", order)
	}

	// Container is closed afterwards.
	if _, err := Resolve[*repo](c); !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
}

func TestProvideAfterInstantiation(t *testing.T) {
	c := New()
	_ = Provide(c, func(*Container) (*repo, error) { return &repo{}, nil })
	if _, err := Resolve[*repo](c); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := Provide(c, func(*Container) (*repo, error) { return &repo{}, nil }); err == nil {
		t.Fatal("expected error re-providing instantiated type")
	}
}

func TestProviderError(t *testing.T) {
	c := New()
	sentinel := errors.New("boom")
	_ = Provide(c, func(*Container) (*repo, error) { return nil, sentinel })
	if _, err := Resolve[*repo](c); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
}

type failingCloser struct{ err error }

func (c *failingCloser) Close() error { return c.err }

func TestContainerBoundaryErrors(t *testing.T) {
	c := New()
	if err := Provide[*repo](c, nil); err == nil || !strings.Contains(err.Error(), "provider is nil") {
		t.Fatalf("Provide nil error = %v, want provider nil", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close empty: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close twice: %v", err)
	}
	if err := Provide(c, func(*Container) (*repo, error) { return &repo{}, nil }); !errors.Is(err, ErrClosed) {
		t.Fatalf("Provide closed error = %v, want ErrClosed", err)
	}

	c = New()
	closeErr := errors.New("close failed")
	if err := Provide(c, func(*Container) (*failingCloser, error) { return &failingCloser{err: closeErr}, nil }); err != nil {
		t.Fatalf("Provide failingCloser: %v", err)
	}
	if _, err := Resolve[*failingCloser](c); err != nil {
		t.Fatalf("Resolve failingCloser: %v", err)
	}
	if err := c.Close(); !errors.Is(err, closeErr) {
		t.Fatalf("Close failingCloser error = %v, want closeErr", err)
	}
}

func TestMustResolveHappyAndPanic(t *testing.T) {
	c := New()
	_ = Provide(c, func(*Container) (*repo, error) { return &repo{dsn: "db"}, nil })
	got := MustResolve[*repo](c)
	if got.dsn != "db" {
		t.Fatalf("MustResolve = %+v, want dsn=db", got)
	}

	c2 := New()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("MustResolve on unregistered type should panic")
		}
	}()
	_ = MustResolve[*repo](c2)
}
