// Command plugin-ecosystem demonstrates gofly's copyable plugin governance contract.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/imajinyun/gofly/spi"
)

const (
	reportSchema     = "gofly.plugin_ecosystem.v1"
	registryPath     = "registry/plugins.json"
	templateContract = "templates/service/gofly.template.json"
)

type report struct {
	Schema        string              `json:"schema"`
	Protocol      string              `json:"protocol"`
	Registry      registrySummary     `json:"registry"`
	Publishing    publishingSummary   `json:"publishing"`
	Compatibility []compatibilityCase `json:"compatibility"`
	Conformance   []conformanceCase   `json:"conformance"`
	Examples      []exampleSummary    `json:"examples"`
	Security      []string            `json:"security"`
}

type registrySummary struct {
	Path    string   `json:"path"`
	Names   []string `json:"names"`
	Fields  []string `json:"fields"`
	Sources []string `json:"sources"`
}

type publishingSummary struct {
	ManifestFields []string `json:"manifestFields"`
	RegistryFields []string `json:"registryFields"`
	RequiredGates  []string `json:"requiredGates"`
	ReleaseNotes   []string `json:"releaseNotes"`
}

type compatibilityCase struct {
	Name               string   `json:"name"`
	CompatibleVersions []string `json:"compatibleVersions"`
	Accepted           bool     `json:"accepted"`
	Selected           string   `json:"selected,omitempty"`
}

type conformanceCase struct {
	Name     string `json:"name"`
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason"`
}

type exampleSummary struct {
	Name         string   `json:"name"`
	Capabilities []string `json:"capabilities"`
	Files        []string `json:"files,omitempty"`
	Patches      []string `json:"patches,omitempty"`
	Contract     string   `json:"contract,omitempty"`
}

type registryIndex struct {
	Version string          `json:"version"`
	Plugins []registryEntry `json:"plugins"`
}

type registryEntry struct {
	Name        string                `json:"name"`
	Remote      string                `json:"remote"`
	Version     string                `json:"version"`
	Protocol    string                `json:"protocol"`
	Checksum    string                `json:"checksum"`
	Source      string                `json:"source"`
	Description string                `json:"description,omitempty"`
	Tags        []string              `json:"tags,omitempty"`
	Manifest    spi.GeneratorManifest `json:"manifest"`
}

type fileGenerator struct{}

func (fileGenerator) Name() string { return "example-file-generator" }

func (fileGenerator) Manifest() spi.GeneratorManifest {
	return spi.GeneratorManifest{
		Name:               "example-file-generator",
		Version:            "v0.1.0",
		CompatibleVersions: []string{compatibleVersion()},
		Capabilities:       []string{"generate:file"},
		Permissions:        []string{"filesystem:write-relative"},
		RequiresDryRun:     true,
	}
}

func (fileGenerator) Generate(_ context.Context, req spi.GeneratorRequest) (spi.GeneratorResponse, error) {
	return spi.GeneratorResponse{
		ProtocolVersion: compatibleVersion(),
		Files: []spi.GeneratorFile{{
			Path:    filepath.ToSlash(filepath.Join("internal", "audit", "audit.go")),
			Content: "package audit\n\nconst Service = " + quote(req.Service) + "\n",
		}},
		Message: "generated audit file",
	}, nil
}

type patchGenerator struct{}

func (patchGenerator) Name() string { return "example-patch-generator" }

func (patchGenerator) Manifest() spi.GeneratorManifest {
	return spi.GeneratorManifest{
		Name:               "example-patch-generator",
		Version:            "v0.1.0",
		CompatibleVersions: []string{compatibleVersion()},
		Capabilities:       []string{"generate:patch"},
		Permissions:        []string{"filesystem:write-relative"},
		RequiresDryRun:     true,
	}
}

func (patchGenerator) Generate(_ context.Context, req spi.GeneratorRequest) (spi.GeneratorResponse, error) {
	return spi.GeneratorResponse{
		ProtocolVersion: compatibleVersion(),
		Patches: []spi.GeneratorPatch{{
			Path:        filepath.ToSlash(filepath.Join("cmd", req.Service, "main.go")),
			InsertAfter: "func main() {",
			Patch:       "\taudit.RecordStartup()",
		}},
		Message: "generated startup patch",
	}, nil
}

func main() {
	out, err := buildReport(context.Background())
	if err != nil {
		panic(err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		panic(err)
	}
}

func buildReport(ctx context.Context) (report, error) {
	index, err := loadRegistry(registryPath)
	if err != nil {
		return report{}, err
	}
	if err := validateRegistry(index); err != nil {
		return report{}, err
	}

	req := spi.GeneratorRequest{
		ProtocolVersion: compatibleVersion(),
		Command:         "service",
		Service:         "orders",
		Module:          "example.com/orders",
		Style:           "production",
		Dir:             "orders",
	}
	filePlugin := fileGenerator{}
	fileResp, err := filePlugin.Generate(ctx, req)
	if err != nil {
		return report{}, err
	}
	patchPlugin := patchGenerator{}
	patchResp, err := patchPlugin.Generate(ctx, req)
	if err != nil {
		return report{}, err
	}

	names := make([]string, 0, len(index.Plugins))
	sources := make([]string, 0, len(index.Plugins))
	for _, plugin := range index.Plugins {
		names = append(names, plugin.Name)
		sources = append(sources, plugin.Source)
	}
	sort.Strings(names)
	sort.Strings(sources)

	return report{
		Schema:   reportSchema,
		Protocol: compatibleVersion(),
		Registry: registrySummary{
			Path:    registryPath,
			Names:   names,
			Fields:  []string{"name", "version", "protocol", "compatibleVersions", "capabilities", "permissions", "checksum", "source"},
			Sources: sources,
		},
		Publishing: publishingSummary{
			ManifestFields: []string{"name", "version", "compatibleVersions", "capabilities", "permissions", "requiresDryRun"},
			RegistryFields: []string{"name", "remote", "version", "protocol", "checksum", "source", "manifest"},
			RequiredGates:  []string{"make plugin-conformance-check", "go test -C examples/plugin-ecosystem ./...", "go run -C examples/plugin-ecosystem ."},
			ReleaseNotes:   []string{"protocol compatibility", "digest provenance", "signature provenance", "permission rationale", "template contract", "rollback and failure isolation behavior"},
		},
		Compatibility: []compatibilityCase{
			{Name: "old-protocol", CompatibleVersions: []string{"0"}, Accepted: false},
			{Name: "current-protocol", CompatibleVersions: []string{compatibleVersion()}, Accepted: true, Selected: compatibleVersion()},
			{Name: "future-plus-current", CompatibleVersions: []string{"2", compatibleVersion()}, Accepted: true, Selected: compatibleVersion()},
			{Name: "future-only", CompatibleVersions: []string{"2"}, Accepted: false},
		},
		Conformance: []conformanceCase{
			{Name: "digest-mismatch", Accepted: false, Reason: "registry checksum must match the downloaded plugin digest"},
			{Name: "malicious-path", Accepted: false, Reason: "plugin file and patch paths must remain relative to the project root"},
			{Name: "permission-escape", Accepted: false, Reason: "declared permissions must be the least privilege needed for plugin output"},
			{Name: "failure-isolation", Accepted: true, Reason: "plugin failures are reported as plugin effects without partial host writes"},
		},
		Examples: []exampleSummary{
			{Name: filePlugin.Manifest().Name, Capabilities: filePlugin.Manifest().Capabilities, Files: filePaths(fileResp.Files)},
			{Name: patchPlugin.Manifest().Name, Capabilities: patchPlugin.Manifest().Capabilities, Patches: patchPaths(patchResp.Patches)},
			{Name: "third-party-template-directory", Capabilities: []string{"template:directory"}, Contract: templateContract},
		},
		Security: []string{
			"remote plugins are version pinned",
			"registry entries publish sha256 checksums",
			"plugin and template outputs must stay relative",
			"third-party template directories publish source and protocol metadata",
		},
	}, nil
}

func loadRegistry(name string) (registryIndex, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return registryIndex{}, fmt.Errorf("read registry %s: %w", name, err)
	}
	var index registryIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return registryIndex{}, fmt.Errorf("decode registry %s: %w", name, err)
	}
	return index, nil
}

func validateRegistry(index registryIndex) error {
	if index.Version != "v1" {
		return fmt.Errorf("registry version = %q, want v1", index.Version)
	}
	for _, plugin := range index.Plugins {
		if plugin.Name == "" || plugin.Version == "" || plugin.Protocol != compatibleVersion() {
			return fmt.Errorf("plugin %q has incomplete identity", plugin.Name)
		}
		if plugin.Checksum == "" || plugin.Source == "" {
			return fmt.Errorf("plugin %q must publish checksum and source", plugin.Name)
		}
		if !contains(plugin.Manifest.CompatibleVersions, compatibleVersion()) {
			return fmt.Errorf("plugin %q is not compatible with protocol %s", plugin.Name, compatibleVersion())
		}
		if len(plugin.Manifest.Capabilities) == 0 || len(plugin.Manifest.Permissions) == 0 {
			return fmt.Errorf("plugin %q must declare capabilities and permissions", plugin.Name)
		}
	}
	return nil
}

func compatibleVersion() string {
	return "1"
}

func filePaths(files []spi.GeneratorFile) []string {
	out := make([]string, 0, len(files))
	for _, file := range files {
		out = append(out, file.Path)
	}
	return out
}

func patchPaths(patches []spi.GeneratorPatch) []string {
	out := make([]string, 0, len(patches))
	for _, patch := range patches {
		out = append(out, patch.Path)
	}
	return out
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func quote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
