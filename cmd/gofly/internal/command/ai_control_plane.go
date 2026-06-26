package command

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/imajinyun/gofly/core/controlplane"
)

func aiControlPlaneCommand(args []string) error {
	if printCommandHelp("ai control-plane", args) {
		return nil
	}
	fs := flag.NewFlagSet("ai control-plane", flag.ContinueOnError)
	formatName := fs.String("format", outputText, "output format: text or json")
	jsonOutput := fs.Bool("json", false, "output JSON envelope")
	schemaName := fs.String("schema", "", "output control-plane schema: jsonschema")
	watch := fs.Bool("watch", false, "emit bounded snapshot watch events")
	maxEvents := fs.Int("max-events", 1, "maximum watch events to emit")
	timeoutName := fs.String("timeout", "2s", "watch timeout boundary")
	source := fs.String("source", "", "runtime control-plane snapshot URL, for example http://127.0.0.1:8080/admin/control-plane")
	adminToken := fs.String("admin-token", "", "bearer token for --source runtime admin endpoint; defaults to GOFLY_CONTROL_PLANE_TOKEN")
	fromChecksum := fs.String("from-checksum", "", "compare current snapshot checksum with a previous checksum")
	fromSnapshot := fs.String("from-snapshot", "", "compare current snapshot with a previous control-plane snapshot JSON file")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if len(remaining) > 0 {
		return fmt.Errorf("%w: ai control-plane does not accept positional arguments: %s", errUsage, strings.Join(remaining, " "))
	}
	format := strings.ToLower(strings.TrimSpace(*formatName))
	if format == "" {
		format = outputText
	}
	if format != outputText && format != outputJSON {
		return fmt.Errorf("%w: unsupported --format %q", errUsage, *formatName)
	}
	schema := strings.ToLower(strings.TrimSpace(*schemaName))
	if schema != "" {
		if schema != "jsonschema" {
			return fmt.Errorf("%w: unsupported --schema %q", errUsage, *schemaName)
		}
		return printJSONEnvelope("ai.control_plane.schema", buildAIControlPlaneJSONSchema())
	}
	baseline, err := loadAIControlPlaneBaseline(strings.TrimSpace(*fromChecksum), strings.TrimSpace(*fromSnapshot))
	if err != nil {
		return err
	}
	manifest := buildAIControlPlaneManifest()
	provider, err := newAIControlPlaneProvider(strings.TrimSpace(*source), strings.TrimSpace(*adminToken))
	if err != nil {
		return err
	}
	jsonMode := *jsonOutput || outputMode() == outputJSON || format == outputJSON
	if *watch {
		return runAIControlPlaneWatch(provider, manifest, baseline, *maxEvents, *timeoutName, jsonMode)
	}
	snapshot, err := provider.Load(context.Background())
	if err != nil {
		return err
	}
	result := aiControlPlaneSnapshotResult{
		Source:         aiControlPlaneProviderSource(provider),
		Snapshot:       snapshot,
		Diff:           baseline.Diff(snapshot),
		AgentGuidance:  manifest.AgentGuidance,
		SecretBoundary: manifest.SecretBoundary,
	}
	result.ConsumerAction = controlplane.ConsumerActionForDiff(result.Diff)
	if jsonMode {
		return printJSONEnvelope("ai.control_plane", result)
	}
	cliOutputfIf("gofly AI control-plane snapshot\n")
	cliOutputfIf("source=%s version=%s checksum=%s\n", result.Source, result.Snapshot.Version, result.Snapshot.Checksum)
	if result.Diff.FromChecksum != "" {
		cliOutputfIf("diff changed=%t changeType=%s from=%s to=%s\n", result.Diff.Changed, result.Diff.ChangeType, result.Diff.FromChecksum, result.Diff.ToChecksum)
	}
	cliOutputfIf("consumerAction=%s fullReconcile=%t\n", result.ConsumerAction.Action, result.ConsumerAction.RequiresFullReconcile)
	metadataKeys := make([]string, 0, len(result.Snapshot.Metadata))
	for key := range result.Snapshot.Metadata {
		metadataKeys = append(metadataKeys, key)
	}
	sort.Strings(metadataKeys)
	for _, key := range metadataKeys {
		value := result.Snapshot.Metadata[key]
		cliOutputfIf("metadata.%s=%s\n", key, value)
	}
	for _, next := range result.AgentGuidance {
		cliOutputfIf("next: %s\n", next)
	}
	return nil
}

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
