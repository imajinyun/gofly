package callstats

import (
	"testing"
	"time"
)

func TestRegistryObserveSnapshotSortedAndAggregated(t *testing.T) {
	reg := NewRegistry()
	reg.Observe(PhaseSend, 10*time.Millisecond, false)
	reg.Observe(PhaseSend, 30*time.Millisecond, true)
	reg.Observe(PhaseResolve, 5*time.Millisecond, false)

	snapshot := reg.Snapshot()
	if len(snapshot.Phases) != 2 {
		t.Fatalf("phases = %#v, want two phases", snapshot.Phases)
	}
	if snapshot.Phases[0].Phase != PhaseResolve || snapshot.Phases[1].Phase != PhaseSend {
		t.Fatalf("phases = %#v, want sorted by phase", snapshot.Phases)
	}
	send := snapshot.Phases[1]
	if send.Calls != 2 || send.Errors != 1 || send.TotalDuration != 40*time.Millisecond || send.MaxDuration != 30*time.Millisecond || send.AvgDuration != 20*time.Millisecond {
		t.Fatalf("send snapshot = %#v, want aggregated durations and errors", send)
	}
}

func TestRegistryNilAndInvalidInputsAreSafe(t *testing.T) {
	var nilReg *Registry
	nilReg.Observe(PhaseSend, time.Millisecond, false)
	nilReg.ObserveSince(PhaseSend, time.Now(), true)
	if snapshot := nilReg.Snapshot(); len(snapshot.Phases) != 0 {
		t.Fatalf("nil snapshot = %#v, want empty", snapshot)
	}

	reg := NewRegistry()
	reg.Observe("", time.Millisecond, false)
	reg.Observe("  ", time.Millisecond, false)
	reg.Observe(PhaseRecv, -time.Millisecond, true)
	snapshot := reg.Snapshot()
	if len(snapshot.Phases) != 1 || snapshot.Phases[0].Phase != PhaseRecv || snapshot.Phases[0].TotalDuration != 0 || snapshot.Phases[0].Errors != 1 {
		t.Fatalf("snapshot = %#v, want only recv with clamped duration", snapshot)
	}
}
