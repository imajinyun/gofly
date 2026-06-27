package command

import (
	"context"
	"flag"
	"fmt"
	"sort"
	"strings"

	"github.com/imajinyun/gofly/core/controlplane"
)

func aiControlPlaneCommand(args []string) error {
	if printCommandHelp("ai control-plane", args) {
		return nil
	}
	fs := flag.NewFlagSet("ai control-plane", flag.ContinueOnError)
	outputFlags := registerCLIOutputFlags(fs, cliOutputFlagOptions{JSONUsage: "output JSON envelope"})
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
	format, err := outputFlags.normalizedFormat(outputText)
	if err != nil {
		return err
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
	jsonMode := outputFlags.useJSON(format)
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
