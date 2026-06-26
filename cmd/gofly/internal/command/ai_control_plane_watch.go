package command

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/imajinyun/gofly/core/controlplane"
)

func runAIControlPlaneWatch(provider controlplane.Provider, manifest aiControlPlaneManifest, baseline aiControlPlaneBaseline, maxEvents int, timeoutValue string, jsonMode bool) error {
	if maxEvents <= 0 {
		return fmt.Errorf("%w: --max-events must be positive", errUsage)
	}
	timeout, err := time.ParseDuration(strings.TrimSpace(timeoutValue))
	if err != nil || timeout <= 0 {
		return fmt.Errorf("%w: --timeout must be a positive duration", errUsage)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	providerSource := aiControlPlaneProviderSource(provider)
	events, err := provider.Watch(ctx)
	if err != nil {
		return err
	}
	deduped := controlplane.DeduplicateSnapshotEvents(ctx, events)
	previous := baseline.Snapshot
	previousChecksum := baseline.Checksum
	hasPreviousSnapshot := baseline.HasSnapshot
	for index := 0; index < maxEvents; {
		select {
		case event, ok := <-deduped:
			if !ok {
				return nil
			}
			if event.Source == "" {
				event.Source = providerSource
			}
			diff := controlplane.DiffSnapshots(previous, event.Snapshot)
			if !hasPreviousSnapshot && previousChecksum != "" {
				diff = controlplane.DiffSnapshotChecksum(previousChecksum, event.Snapshot)
			}
			result := aiControlPlaneWatchEventResult{
				Index:          index,
				Source:         event.Source,
				Snapshot:       event.Snapshot,
				Diff:           diff,
				ConsumerAction: controlplane.ConsumerActionForDiff(diff),
				Error:          event.Error,
				SecretBoundary: manifest.SecretBoundary,
			}
			if jsonMode {
				if err := printJSONLine(jsonEnvelope{OK: true, Command: "ai.control_plane.event", Version: Version, Data: result}); err != nil {
					return err
				}
			} else if result.Error != "" {
				cliOutputfIf("event=%d source=%s error=%s\n", result.Index, result.Source, result.Error)
			} else {
				cliOutputfIf("event=%d source=%s version=%s checksum=%s action=%s\n", result.Index, result.Source, result.Snapshot.Version, result.Snapshot.Checksum, result.ConsumerAction.Action)
			}
			previous = event.Snapshot
			previousChecksum = event.Snapshot.WithChecksum().Checksum
			hasPreviousSnapshot = true
			index++
		case <-ctx.Done():
			return nil
		}
	}
	return nil
}
