package command

import (
	"fmt"
	"os"

	"github.com/imajinyun/gofly/core/controlplane"
)

func loadAIControlPlaneBaseline(fromChecksum, fromSnapshotPath string) (aiControlPlaneBaseline, error) {
	if fromChecksum != "" && fromSnapshotPath != "" {
		return aiControlPlaneBaseline{}, fmt.Errorf("%w: --from-checksum and --from-snapshot are mutually exclusive", errUsage)
	}
	if fromSnapshotPath == "" {
		return aiControlPlaneBaseline{Checksum: fromChecksum}, nil
	}
	// #nosec G304 -- --from-snapshot reads an explicit local baseline file selected by the CLI caller.
	data, err := os.ReadFile(fromSnapshotPath)
	if err != nil {
		return aiControlPlaneBaseline{}, fmt.Errorf("read --from-snapshot %q: %w", fromSnapshotPath, err)
	}
	snapshot, err := controlplane.DecodeSnapshotJSON(data)
	if err != nil {
		return aiControlPlaneBaseline{}, fmt.Errorf("parse --from-snapshot %q: %w", fromSnapshotPath, err)
	}
	return aiControlPlaneBaseline{Checksum: snapshot.Checksum, Snapshot: snapshot, HasSnapshot: true}, nil
}

func (b aiControlPlaneBaseline) Diff(snapshot controlplane.Snapshot) controlplane.SnapshotDiff {
	if b.HasSnapshot {
		return controlplane.DiffSnapshots(b.Snapshot, snapshot)
	}
	return controlplane.DiffSnapshotChecksum(b.Checksum, snapshot)
}
