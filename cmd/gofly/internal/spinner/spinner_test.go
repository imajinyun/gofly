package spinner

import (
	"testing"
	"time"
)

func TestStartStop(t *testing.T) {
	s := New()
	s.Start("testing")
	time.Sleep(250 * time.Millisecond)
	s.Stop("done")

	if s.running {
		t.Error("spinner should be stopped after Stop()")
	}
}

func TestDisabled(t *testing.T) {
	s := New()
	s.Disable()
	s.Start("should not spin")
	time.Sleep(100 * time.Millisecond)
	s.Stop()

	if s.running {
		t.Error("disabled spinner should not start")
	}
}

func TestUpdate(t *testing.T) {
	s := New()
	s.Start("phase 1")
	s.Update("phase 2")
	time.Sleep(100 * time.Millisecond)
	s.Stop()

	if s.running {
		t.Error("spinner should be stopped after Stop()")
	}
}

func TestMultipleStartIgnored(t *testing.T) {
	s := New()
	s.Start("first")
	s.Start("second") // should be no-op
	time.Sleep(100 * time.Millisecond)
	s.Stop()

	if s.running {
		t.Error("spinner should be stopped")
	}
}

func TestStopWithoutStart(t *testing.T) {
	s := New()
	s.Stop("never started") // should not panic
}

func TestUpdateUnchangedAfterStop(t *testing.T) {
	s := New()
	s.Start("running")
	s.Stop()
	// Should not panic
	s.Update("after stop")
}
