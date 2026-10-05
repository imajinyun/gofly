package generator

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
)

const (
	MiddlewarePresetCatalogSchema = "gofly.middleware_presets.v1"
	MiddlewarePresetResultSchema  = "gofly.middleware_preset_result.v1"
)

//go:embed middleware_presets/*.go.tmpl
var middlewarePresetTemplates embed.FS

type MiddlewarePresetInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Kind        string   `json:"kind"`
	Files       []string `json:"files"`
}

type MiddlewarePresetOptions struct {
	Names  []string
	Dir    string
	DryRun bool
}

type MiddlewarePresetFileResult struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

type MiddlewarePresetResult struct {
	Schema  string                       `json:"schema"`
	Presets []string                     `json:"presets"`
	Files   []MiddlewarePresetFileResult `json:"files"`
	DryRun  bool                         `json:"dryRun"`
}

var middlewarePresetRegistry = []MiddlewarePresetInfo{
	{Name: "auth", Description: "API key, Basic Auth, RBAC, and principal helpers", Kind: "middleware", Files: []string{"auth.go"}},
	{Name: "cors", Description: "CORS policy middleware", Kind: "middleware", Files: []string{"cors.go"}},
	{Name: "csrf", Description: "CSRF protection middleware", Kind: "middleware", Files: []string{"csrf.go"}},
	{Name: "jwt", Description: "JWT validation and signing helpers", Kind: "middleware", Files: []string{"jwt.go"}},
	{Name: "observability", Description: "Request ID, access logging, and pprof registration", Kind: "middleware", Files: []string{"observability.go"}},
	{Name: "opentelemetry", Description: "OpenTelemetry request tracing middleware", Kind: "middleware", Files: []string{"opentelemetry.go"}},
	{Name: "prometheus", Description: "Prometheus metrics middleware and handler", Kind: "middleware", Files: []string{"prometheus.go"}},
	{Name: "session", Description: "Signed cookie session middleware", Kind: "middleware", Files: []string{"session.go"}},
	{Name: "sse", Description: "Server-sent event streaming helper", Kind: "helper", Files: []string{"sse.go"}},
	{Name: "stability", Description: "Recovery, rate limit, concurrency, breaker, and adaptive limit middleware", Kind: "middleware", Files: []string{"stability.go"}},
	{Name: "validation", Description: "JSON request validation helpers", Kind: "helper", Files: []string{"validation.go"}},
	{Name: "web-security", Description: "Security headers, request body limit, and timeout middleware", Kind: "middleware", Files: []string{"web_security.go"}},
	{Name: "websocket", Description: "Bounded WebSocket echo helper", Kind: "helper", Files: []string{"websocket.go"}},
}

func ListMiddlewarePresets() []MiddlewarePresetInfo {
	result := make([]MiddlewarePresetInfo, len(middlewarePresetRegistry))
	for i, preset := range middlewarePresetRegistry {
		result[i] = preset
		result[i].Files = append([]string(nil), preset.Files...)
	}
	return result
}

func GenerateMiddlewarePresets(opts MiddlewarePresetOptions) (MiddlewarePresetResult, error) {
	presets, err := resolveMiddlewarePresets(opts.Names)
	if err != nil {
		return MiddlewarePresetResult{}, err
	}
	root := strings.TrimSpace(opts.Dir)
	if root == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return MiddlewarePresetResult{}, fmt.Errorf("resolve middleware preset root: %w", err)
	}
	if err := validateMiddlewarePresetModule(absRoot); err != nil {
		return MiddlewarePresetResult{}, err
	}
	result := MiddlewarePresetResult{
		Schema: MiddlewarePresetResultSchema,
		DryRun: opts.DryRun,
		Files:  make([]MiddlewarePresetFileResult, 0, len(presets)),
	}
	prepared := make([]middlewarePresetPreparedFile, 0, len(presets))
	for _, preset := range presets {
		result.Presets = append(result.Presets, preset.Name)
		for _, fileName := range preset.Files {
			preparedFile, fileResult, err := prepareMiddlewarePresetFile(absRoot, fileName, opts.DryRun)
			if err != nil {
				return MiddlewarePresetResult{}, err
			}
			result.Files = append(result.Files, fileResult)
			if preparedFile.Path != "" {
				prepared = append(prepared, preparedFile)
			}
		}
	}
	if opts.DryRun {
		return result, nil
	}

	written := make([]string, 0, len(prepared))
	for _, file := range prepared {
		if err := writeNewGeneratedFileUnder(absRoot, file.Path, file.Content); err != nil {
			rollbackErr := rollbackMiddlewarePresetFiles(absRoot, written)
			return MiddlewarePresetResult{}, errors.Join(fmt.Errorf("write middleware preset %s: %w", file.Path, err), rollbackErr)
		}
		written = append(written, file.Path)
	}
	return result, nil
}

type middlewarePresetPreparedFile struct {
	Path    string
	Content []byte
}

func resolveMiddlewarePresets(names []string) ([]MiddlewarePresetInfo, error) {
	requested := make(map[string]struct{})
	all := false
	for _, value := range names {
		for _, name := range strings.Split(value, ",") {
			name = strings.ToLower(strings.TrimSpace(name))
			if name == "" {
				continue
			}
			if name == "all" {
				all = true
				continue
			}
			requested[name] = struct{}{}
		}
	}
	if len(requested) == 0 && !all {
		return nil, errors.New("middleware preset is required")
	}
	if all && len(requested) > 0 {
		return nil, errors.New("middleware preset all cannot be combined with named presets")
	}

	available := make([]string, 0, len(middlewarePresetRegistry))
	selected := make([]MiddlewarePresetInfo, 0, len(middlewarePresetRegistry))
	for _, preset := range middlewarePresetRegistry {
		available = append(available, preset.Name)
		if all {
			selected = append(selected, preset)
			continue
		}
		if _, ok := requested[preset.Name]; ok {
			selected = append(selected, preset)
			delete(requested, preset.Name)
		}
	}
	if len(requested) > 0 {
		unknown := make([]string, 0, len(requested))
		for name := range requested {
			unknown = append(unknown, name)
		}
		sort.Strings(unknown)
		return nil, fmt.Errorf("unknown middleware preset(s) %s; available: %s", strings.Join(unknown, ", "), strings.Join(available, ", "))
	}
	return selected, nil
}

func validateMiddlewarePresetModule(root string) error {
	data, err := ReadFileUnderRoot(root, "go.mod", "middleware preset module")
	if err != nil {
		return fmt.Errorf("read target go.mod: %w", err)
	}
	file, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return fmt.Errorf("parse target go.mod: %w", err)
	}
	if file.Module == nil || file.Module.Mod.Path == "" {
		return errors.New("target go.mod does not declare a module")
	}
	const frameworkModule = "github.com/imajinyun/gofly"
	if file.Module.Mod.Path == frameworkModule {
		return nil
	}
	for _, requirement := range file.Require {
		if requirement.Mod.Path == frameworkModule {
			return nil
		}
	}
	return errors.New("target go.mod must require github.com/imajinyun/gofly; add a compatible pinned dependency before installing presets (a replace directive alone is insufficient)")
}

func prepareMiddlewarePresetFile(root, fileName string, dryRun bool) (middlewarePresetPreparedFile, MiddlewarePresetFileResult, error) {
	templatePath := filepath.ToSlash(filepath.Join("middleware_presets", fileName+".tmpl"))
	content, err := middlewarePresetTemplates.ReadFile(templatePath)
	if err != nil {
		return middlewarePresetPreparedFile{}, MiddlewarePresetFileResult{}, fmt.Errorf("read middleware preset template %s: %w", fileName, err)
	}
	formatted, err := format.Source(content)
	if err != nil {
		return middlewarePresetPreparedFile{}, MiddlewarePresetFileResult{}, fmt.Errorf("format middleware preset %s: %w", fileName, err)
	}
	relativePath := filepath.Join("internal", "middleware", fileName)
	target, err := SafeTarget(root, relativePath, "middleware preset")
	if err != nil {
		return middlewarePresetPreparedFile{}, MiddlewarePresetFileResult{}, err
	}
	result := MiddlewarePresetFileResult{Path: filepath.ToSlash(relativePath), Status: "planned"}
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		if dryRun {
			return middlewarePresetPreparedFile{}, result, nil
		}
		result.Status = "written"
		return middlewarePresetPreparedFile{Path: relativePath, Content: formatted}, result, nil
	}
	if err != nil {
		return middlewarePresetPreparedFile{}, MiddlewarePresetFileResult{}, fmt.Errorf("inspect middleware preset target %s: %w", relativePath, err)
	}
	if !info.Mode().IsRegular() {
		return middlewarePresetPreparedFile{}, MiddlewarePresetFileResult{}, fmt.Errorf("middleware preset target %s must be a regular file", relativePath)
	}
	existing, err := ReadFileUnderRoot(root, relativePath, "middleware preset")
	if err != nil {
		return middlewarePresetPreparedFile{}, MiddlewarePresetFileResult{}, err
	}
	if !bytes.Equal(existing, formatted) {
		return middlewarePresetPreparedFile{}, MiddlewarePresetFileResult{}, fmt.Errorf("middleware preset target %s already exists with different content", relativePath)
	}
	result.Status = "unchanged"
	return middlewarePresetPreparedFile{}, result, nil
}

func rollbackMiddlewarePresetFiles(root string, paths []string) error {
	var result error
	for index := len(paths) - 1; index >= 0; index-- {
		if err := removeGeneratedFileUnder(root, paths[index]); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, fmt.Errorf("rollback middleware preset %s: %w", paths[index], err))
		}
	}
	return result
}
