package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

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

func newAIControlPlaneProvider(source, token string) (controlplane.Provider, error) {
	if source == "" {
		return controlplane.StaticProvider{Name: "ai-manifest", Snapshot: defaultAIControlPlaneSnapshot()}, nil
	}
	parsed, err := url.Parse(source)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("%w: --source must be an absolute http(s) URL", errUsage)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("%w: --source supports only http and https URLs", errUsage)
	}
	if token == "" {
		token = os.Getenv("GOFLY_CONTROL_PLANE_TOKEN")
	}
	return httpControlPlaneProvider{
		URL:   parsed.String(),
		Token: token,
		Client: &http.Client{
			Timeout: 5 * time.Second,
		},
		WatchInterval: time.Second,
	}, nil
}

func aiControlPlaneProviderSource(provider controlplane.Provider) string {
	if sourceProvider, ok := provider.(controlplane.ProviderSource); ok {
		return sourceProvider.Source()
	}
	return "control-plane"
}

func (p httpControlPlaneProvider) Source() string {
	return p.URL
}

func (p httpControlPlaneProvider) Load(ctx context.Context) (controlplane.Snapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return controlplane.Snapshot{}, err
	}
	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL, nil)
	if err != nil {
		return controlplane.Snapshot{}, fmt.Errorf("create control-plane source request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if p.Token != "" {
		req.Header.Set("Authorization", "Bearer "+p.Token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return controlplane.Snapshot{}, fmt.Errorf("fetch control-plane source %s: %w", p.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return controlplane.Snapshot{}, fmt.Errorf("fetch control-plane source %s: status %d", p.URL, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return controlplane.Snapshot{}, fmt.Errorf("read control-plane source %s: %w", p.URL, err)
	}
	snapshot, err := controlplane.DecodeSnapshotJSON(data)
	if err != nil {
		return controlplane.Snapshot{}, fmt.Errorf("decode control-plane source %s: %w", p.URL, err)
	}
	return snapshot.WithChecksum(), nil
}

func (p httpControlPlaneProvider) Watch(ctx context.Context) (<-chan controlplane.SnapshotEvent, error) {
	if ctx == nil {
		return nil, errors.New("control-plane source watch context is nil")
	}
	interval := p.WatchInterval
	if interval <= 0 {
		interval = time.Second
	}
	out := make(chan controlplane.SnapshotEvent, 1)
	go func() {
		defer close(out)
		emit := func() bool {
			snapshot, err := p.Load(ctx)
			event := controlplane.SnapshotEvent{Snapshot: snapshot, Source: p.Source()}
			if err != nil {
				event = controlplane.SnapshotEvent{Source: p.Source(), Error: err.Error()}
			}
			select {
			case out <- event:
				return true
			case <-ctx.Done():
				return false
			}
		}
		if !emit() {
			return
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !emit() {
					return
				}
			}
		}
	}()
	return out, nil
}

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
