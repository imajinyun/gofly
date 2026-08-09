package command

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type apiCleanupStaleReport struct {
	Schema        string   `json:"schema"`
	StaleHandlers []string `json:"staleHandlers"`
	StaleLogics   []string `json:"staleLogics"`
}

type apiCleanupStaleResult struct {
	Schema      string   `json:"schema"`
	ReportPath  string   `json:"reportPath"`
	Execute     bool     `json:"execute"`
	Planned     []string `json:"planned"`
	Deleted     []string `json:"deleted,omitempty"`
	Skipped     []string `json:"skipped,omitempty"`
	StaleCount  int      `json:"staleCount"`
	NextActions []string `json:"nextActions"`
}

func apiCleanupCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly api cleanup stale --dir <project>`", errUsage)
	}
	switch args[0] {
	case "stale":
		return apiCleanupStaleCommand(args[1:])
	default:
		return fmt.Errorf("%w: expected `gofly api cleanup stale --dir <project>`", errUsage)
	}
}

func apiCleanupStaleCommand(args []string) error {
	fs := flag.NewFlagSet("api cleanup stale", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project root directory")
	execute := fs.Bool("execute", false, "delete stale files listed in .gofly/stale-api-files.json")
	jsonOut := registerCLIJSONOutputFlag(fs, "emit cleanup result as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	result, err := buildAPIStaleCleanupResult(*dir, *execute)
	if err != nil {
		return err
	}
	if *jsonOut || outputMode() == outputJSON {
		return printJSONEnvelope("api.cleanup.stale", result)
	}
	printAPIStaleCleanupText(result)
	return nil
}

func buildAPIStaleCleanupResult(dir string, execute bool) (apiCleanupStaleResult, error) {
	reportPath := filepath.Join(dir, ".gofly", "stale-api-files.json")
	data, err := os.ReadFile(reportPath)
	if err != nil {
		return apiCleanupStaleResult{}, fmt.Errorf("read stale api report: %w", err)
	}
	var report apiCleanupStaleReport
	if err := json.Unmarshal(data, &report); err != nil {
		return apiCleanupStaleResult{}, fmt.Errorf("decode stale api report: %w", err)
	}
	if report.Schema != "gofly.gozero_api_stale_files.v1" {
		return apiCleanupStaleResult{}, fmt.Errorf("unsupported stale api report schema %q", report.Schema)
	}
	files := append([]string{}, report.StaleHandlers...)
	files = append(files, report.StaleLogics...)
	planned, err := validateAPIStaleCleanupFiles(dir, files)
	if err != nil {
		return apiCleanupStaleResult{}, err
	}
	result := apiCleanupStaleResult{
		Schema:     "gofly.api_cleanup_stale.v1",
		ReportPath: reportPath,
		Execute:    execute,
		Planned:    planned,
		StaleCount: len(planned),
	}
	if !execute {
		result.NextActions = []string{"rerun with --execute to delete the planned stale files"}
		return result, nil
	}
	for _, rel := range planned {
		path, err := safeAPIStaleCleanupTarget(dir, rel)
		if err != nil {
			return apiCleanupStaleResult{}, err
		}
		if err := os.Remove(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				result.Skipped = append(result.Skipped, rel)
				continue
			}
			return apiCleanupStaleResult{}, fmt.Errorf("remove stale api file %s: %w", rel, err)
		}
		result.Deleted = append(result.Deleted, rel)
	}
	result.NextActions = []string{"review git diff", "rerun go test ./..."}
	return result, nil
}

func validateAPIStaleCleanupFiles(root string, files []string) ([]string, error) {
	seen := map[string]struct{}{}
	planned := make([]string, 0, len(files))
	for _, file := range files {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}
		rel, err := safeAPIStaleCleanupRel(file)
		if err != nil {
			return nil, err
		}
		if _, err := safeAPIStaleCleanupTarget(root, rel); err != nil {
			return nil, err
		}
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		planned = append(planned, rel)
	}
	return planned, nil
}

func safeAPIStaleCleanupRel(file string) (string, error) {
	if filepath.IsAbs(file) || !filepath.IsLocal(file) {
		return "", fmt.Errorf("unsafe stale api file %q", file)
	}
	rel := filepath.ToSlash(filepath.Clean(file))
	if !strings.HasSuffix(rel, ".go") {
		return "", fmt.Errorf("stale api file %q is not a .go file", file)
	}
	if !isAPIStaleCleanupAllowedRel(rel) {
		return "", fmt.Errorf("stale api file %q is outside internal/api/http, internal/app, or legacy handler/logic directories", file)
	}
	if filepath.Base(rel) == "routes.go" {
		return "", fmt.Errorf("stale api file %q must not remove routes.go", file)
	}
	return rel, nil
}

func isAPIStaleCleanupAllowedRel(rel string) bool {
	for _, prefix := range []string{
		"internal/api/http/",
		"internal/app/",
		"internal/handler/",
		"internal/logic/",
	} {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	return false
}

func safeAPIStaleCleanupTarget(root string, rel string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve stale cleanup root: %w", err)
	}
	target := filepath.Join(absRoot, filepath.FromSlash(rel))
	cleanTarget := filepath.Clean(target)
	relToRoot, err := filepath.Rel(absRoot, cleanTarget)
	if err != nil {
		return "", fmt.Errorf("resolve stale cleanup target: %w", err)
	}
	if relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("stale cleanup target %q escapes root", rel)
	}
	return cleanTarget, nil
}

func printAPIStaleCleanupText(result apiCleanupStaleResult) {
	mode := "report-only"
	if result.Execute {
		mode = "execute"
	}
	cliOutputf("api cleanup stale (%s): %d stale file(s)\n", mode, result.StaleCount)
	if len(result.Planned) > 0 {
		cliOutputln("planned:")
		for _, file := range result.Planned {
			cliOutputf("  %s\n", file)
		}
	}
	if len(result.Deleted) > 0 {
		cliOutputln("deleted:")
		for _, file := range result.Deleted {
			cliOutputf("  %s\n", file)
		}
	}
}
