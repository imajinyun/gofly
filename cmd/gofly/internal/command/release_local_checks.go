package command

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
	"github.com/imajinyun/gofly/gateway"
)

func releaseGoAPICompatCheck() (releaseCheckItem, []string) {
	item := releaseCheckItem{Name: "go-api-compat", Status: "pass"}
	if out, err := runAPIDiffCheck(); err != nil {
		item.Status = "fail"
		item.Detail = string(out)
		item.Blocker = true
		return item, []string{"Go public API incompatible changes detected"}
	} else {
		item.Detail = strings.TrimSpace(string(out))
		if item.Detail == "" {
			item.Detail = "no incompatible changes"
		}
	}
	return item, nil
}

func releaseChangelogVersionCheck(path string) (releaseCheckItem, []string) {
	item := releaseCheckItem{Name: "changelog-version", Status: "pass"}
	changelogVersion, err := parseChangelogVersion(path)
	if err != nil {
		item.Status = "skip"
		item.Detail = "changelog not found or unparsable"
	} else if changelogVersion != "" && changelogVersion != Version {
		item.Status = "fail"
		item.Detail = fmt.Sprintf("CHANGELOG version %q != gofly version %q", changelogVersion, Version)
		item.Blocker = true
		return item, []string{item.Detail}
	} else {
		item.Detail = fmt.Sprintf("version %q", changelogVersion)
	}
	return item, nil
}

func releaseGoModTidyCheck() (releaseCheckItem, []string) {
	item := releaseCheckItem{Name: "go-mod-tidy", Status: "pass"}
	if out, err := exec.Command("go", "mod", "tidy", "-diff").CombinedOutput(); err != nil {
		item.Status = "fail"
		item.Detail = strings.TrimSpace(string(out))
		item.Blocker = true
		return item, []string{"go mod tidy would change go.mod/go.sum"}
	}
	item.Detail = "clean"
	return item, nil
}

func releaseGatewayProfileContractCheck() (releaseCheckItem, []string) {
	item := releaseCheckItem{Name: "gateway-profile-contract", Status: "pass"}
	dir, err := os.MkdirTemp("", "gofly-release-gateway-profile-*")
	if err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"generated gateway profile contract check failed"}
	}
	defer os.RemoveAll(dir)

	projectDir := filepath.Join(dir, "edge")
	if err := generator.GenerateGateway(generator.GatewayOptions{Name: "edge", Module: "example.com/edge", Dir: projectDir}); err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"generated gateway profile contract check failed"}
	}
	profiles, err := readReleaseGatewayProfiles(filepath.Join(projectDir, "etc", "edge.json"))
	if err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"generated gateway profile contract check failed"}
	}
	candidate, err := readReleaseGatewayProfileCandidate(filepath.Join(projectDir, "etc", "edge-profile-candidate.json"))
	if err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"generated gateway profile contract check failed"}
	}
	gw, err := gateway.New([]gateway.Route{{PathPrefix: "/_release_profile_check", Targets: []string{"http://127.0.0.1:1"}}}, gateway.WithTranscodeProfiles(profiles...))
	if err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"generated gateway profile contract check failed"}
	}
	report := gw.ValidateTranscodeProfile(candidate)
	switch {
	case !report.OK:
		item.Status = "fail"
		item.Detail = strings.Join(report.Errors, "; ")
		item.Blocker = true
		return item, []string{"generated gateway profile candidate is invalid"}
	case !report.Compatible:
		item.Status = "fail"
		item.Detail = "generated gateway profile candidate contains breaking mapping changes"
		item.Blocker = true
		return item, []string{"generated gateway profile candidate has breaking mapping changes"}
	default:
		item.Detail = fmt.Sprintf("compatible profile diff with %d change(s)", len(report.Changes))
		return item, nil
	}
}

func releaseGatewayAggregationContractCheck() (releaseCheckItem, []string) {
	item := releaseCheckItem{Name: "gateway-aggregation-contract", Status: "pass"}
	dir, err := os.MkdirTemp("", "gofly-release-gateway-aggregation-*")
	if err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"generated gateway aggregation contract check failed"}
	}
	defer os.RemoveAll(dir)

	projectDir := filepath.Join(dir, "edge")
	if err := generator.GenerateGateway(generator.GatewayOptions{Name: "edge", Module: "example.com/edge", Dir: projectDir}); err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"generated gateway aggregation contract check failed"}
	}
	current, err := readReleaseGatewayConfig(filepath.Join(projectDir, "etc", "edge.json"))
	if err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"generated gateway aggregation contract check failed"}
	}
	candidate, err := readReleaseGatewayAggregationCandidate(filepath.Join(projectDir, "etc", "edge-aggregation-candidate.json"))
	if err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"generated gateway aggregation contract check failed"}
	}
	gw, err := gateway.NewFromConfig(current, nil)
	if err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"generated gateway aggregation contract check failed"}
	}
	report := gw.ValidateAggregation("bff-home", candidate)
	item.Evidence = map[string]any{
		"aggregation-json-diff": releaseAggregationEvidence(report, gatewayAggregationSARIFContext{Route: "bff-home"}),
	}
	switch {
	case !report.OK:
		item.Status = "fail"
		item.Detail = strings.Join(report.Errors, "; ")
		item.Blocker = true
		return item, []string{"generated gateway aggregation candidate is invalid"}
	case !report.Compatible:
		item.Status = "fail"
		item.Detail = "generated gateway aggregation candidate contains breaking changes"
		item.Blocker = true
		return item, []string{"generated gateway aggregation candidate has breaking changes"}
	}
	openAPIReport, openAPIContext, err := releaseGatewayOpenAPIAggregationReport(projectDir)
	if err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"generated gateway OpenAPI aggregation contract check failed"}
	}
	item.Evidence["aggregation-openapi-diff"] = releaseAggregationEvidence(openAPIReport, openAPIContext)
	switch {
	case !openAPIReport.OK:
		item.Status = "fail"
		item.Detail = strings.Join(openAPIReport.Errors, "; ")
		item.Blocker = true
		return item, []string{"generated gateway OpenAPI aggregation candidate is invalid"}
	case !openAPIReport.Compatible:
		item.Status = "fail"
		item.Detail = "generated gateway OpenAPI aggregation candidate contains breaking changes"
		item.Blocker = true
		return item, []string{"generated gateway OpenAPI aggregation candidate has breaking changes"}
	default:
		item.Detail = fmt.Sprintf("compatible aggregation diff with %d change(s); compatible OpenAPI aggregation diff with %d change(s)", len(report.Changes), len(openAPIReport.Changes))
		return item, nil
	}
}

func releaseAggregationEvidence(report gateway.AggregationValidationReport, context gatewayAggregationSARIFContext) map[string]any {
	return map[string]any{
		"ok":            report.OK,
		"compatible":    report.Compatible,
		"changes":       len(report.Changes),
		"errors":        len(report.Errors),
		"changeDetails": gatewayAggregationValidationView(report, context).Changes,
	}
}

func releaseRPCMuxAdapterEvidenceCheck() (releaseCheckItem, []string) {
	item := releaseCheckItem{Name: "rpc-mux-adapter-evidence", Status: "pass"}
	evidence, err := readReleaseJSONFile(filepath.Join("bench", "rpc_mux_adapter_evidence.json"), "rpc mux adapter evidence")
	if err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"rpc mux adapter evidence is unavailable"}
	}
	schema, _ := evidence["schema"].(string)
	benchmark, _ := evidence["benchmark"].(string)
	status, _ := evidence["status"].(string)
	decision, _ := evidence["decision"].(map[string]any)
	allocationMode, _ := decision["allocationMode"].(string)
	latencyMode, _ := decision["latencyMode"].(string)
	promotionStatus, _ := decision["promotionStatus"].(string)
	baseline, _ := evidence["baseline"].(map[string]any)
	current, _ := evidence["current"].(map[string]any)
	if schema != "gofly.benchmark_rpc_mux_adapter_evidence.v1" ||
		benchmark != "BenchmarkRPCExperimentalMuxAdapterOpenSendReceiveClose" ||
		status != "report-only" ||
		allocationMode != "report-only" ||
		latencyMode != "report-only" ||
		promotionStatus != "blocked" {
		item.Status = "fail"
		item.Detail = "rpc mux adapter evidence contract drifted"
		item.Blocker = true
		return item, []string{"rpc mux adapter evidence contract drifted"}
	}
	item.Detail = "rpc mux adapter evidence attached as report-only"
	item.Evidence = map[string]any{
		"rpc-mux-adapter-evidence": map[string]any{
			"schema":          schema,
			"benchmark":       benchmark,
			"status":          status,
			"allocationMode":  allocationMode,
			"latencyMode":     latencyMode,
			"promotionStatus": promotionStatus,
			"baseline":        baseline,
			"current":         current,
			"decision":        decision,
		},
	}
	return item, nil
}

func releaseGeneratedRPCMuxRetrySmokeCheck() (releaseCheckItem, []string) {
	item := releaseCheckItem{Name: "generated-rpc-mux-retry-smoke", Status: "pass"}
	path := filepath.Join("cmd", "gofly", "internal", "generator", "templates.go")
	resolved, err := resolveReleaseEvidencePath(path)
	if err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"generated RPC mux retry smoke source is unavailable"}
	}
	data, err := os.ReadFile(resolved) // #nosec G304 -- release check reads a generator template from the repository.
	if err != nil {
		item.Status = "fail"
		item.Detail = err.Error()
		item.Blocker = true
		return item, []string{"generated RPC mux retry smoke source is unavailable"}
	}
	source := string(data)
	openBeforeMarkers := []string{
		`badMuxEndpoint := "tcp://" + badMuxListener.Addr().String()`,
		`rpc.WithExperimentalMuxConnectionManagerMaxOpenRetries(cfg.RPC.Mux.MaxOpenRetries)`,
		`diagnosis.OpenRetries != 1`,
		`diagnosis.RetryReasons["dial_failure"] != 1`,
	}
	postOpenMarkers := []string{
		`"greeter/FailAfterOpen"`,
		`rpc.NewError(rpc.CodeUnavailable, "generated stream failed after open")`,
		`rpc.CodeOf(err) != rpc.CodeUnavailable`,
		`diagnosis.RetryReasons["open_stream"] != 0`,
	}
	missing := missingReleaseMarkers(source, append(openBeforeMarkers, postOpenMarkers...))
	if len(missing) > 0 {
		item.Status = "fail"
		item.Detail = "generated RPC mux retry smoke markers missing"
		item.Blocker = true
		item.Evidence = map[string]any{"generated-rpc-mux-retry-smoke": map[string]any{"missing": missing}}
		return item, []string{"generated RPC mux retry smoke markers missing"}
	}
	item.Detail = "generated RPC mux retry smoke covers open-before retry and post-open no replay"
	item.Evidence = map[string]any{
		"generated-rpc-mux-retry-smoke": map[string]any{
			"schema":            "gofly.generated_rpc_mux_retry_smoke.v1",
			"source":            path,
			"verifyCommand":     "go test ./...",
			"openBeforeRetry":   true,
			"postOpenNoReplay":  true,
			"openBeforeMarkers": openBeforeMarkers,
			"postOpenMarkers":   postOpenMarkers,
		},
	}
	return item, nil
}

func missingReleaseMarkers(source string, markers []string) []string {
	missing := make([]string, 0)
	for _, marker := range markers {
		if !strings.Contains(source, marker) {
			missing = append(missing, marker)
		}
	}
	return missing
}

func readReleaseJSONFile(path string, label string) (map[string]any, error) {
	resolved, err := resolveReleaseEvidencePath(path)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", label, err)
	}
	data, err := os.ReadFile(resolved) // #nosec G304 -- release check reads explicit repository evidence files.
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", label, err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode %s: %w", label, err)
	}
	return out, nil
}

func resolveReleaseEvidencePath(path string) (string, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || filepath.IsAbs(path) {
		return "", fmt.Errorf("release evidence path must be relative")
	}
	current, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(current, path)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		} else if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return "", os.ErrNotExist
}

func releaseGatewayOpenAPIAggregationReport(projectDir string) (gateway.AggregationValidationReport, gatewayAggregationSARIFContext, error) {
	baseDoc, err := readGatewayOpenAPIDocument(filepath.Join(projectDir, "etc", "edge-openapi-base.json"))
	if err != nil {
		return gateway.AggregationValidationReport{}, gatewayAggregationSARIFContext{}, err
	}
	candidateDoc, err := readGatewayOpenAPIDocument(filepath.Join(projectDir, "etc", "edge-openapi-candidate.json"))
	if err != nil {
		return gateway.AggregationValidationReport{}, gatewayAggregationSARIFContext{}, err
	}
	context := gatewayOpenAPIAggregationSARIFContext(candidateDoc, "home")
	importOptions := gateway.OpenAPIRouteOptions{GatewayPrefix: "/", Service: "openapi", Targets: []string{"http://127.0.0.1:1"}}
	baseRoutes, err := gateway.RouteConfigsFromOpenAPI(baseDoc, importOptions)
	if err != nil {
		return gateway.AggregationValidationReport{}, context, fmt.Errorf("import base openapi aggregation routes: %w", err)
	}
	candidateRoutes, err := gateway.RouteConfigsFromOpenAPI(candidateDoc, importOptions)
	if err != nil {
		return gateway.AggregationValidationReport{}, context, fmt.Errorf("import candidate openapi aggregation routes: %w", err)
	}
	candidateAggregation, err := gatewayAggregationFromRoutes(candidateRoutes, "home")
	if err != nil {
		return gateway.AggregationValidationReport{}, context, err
	}
	gw, err := gateway.NewFromConfig(gateway.Config{Routes: baseRoutes}, nil)
	if err != nil {
		return gateway.AggregationValidationReport{}, context, fmt.Errorf("load base openapi aggregation config: %w", err)
	}
	return gw.ValidateAggregation("home", candidateAggregation), context, nil
}

func readReleaseGatewayConfig(path string) (gateway.Config, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- release check reads a generated file from a temporary project directory it just created.
	if err != nil {
		return gateway.Config{}, fmt.Errorf("read gateway config: %w", err)
	}
	var config struct {
		Gateway gateway.Config `json:"gateway"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return gateway.Config{}, fmt.Errorf("decode gateway config: %w", err)
	}
	return config.Gateway, nil
}

func readReleaseGatewayAggregationCandidate(path string) (gateway.AggregationConfig, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- release check reads a generated file from a temporary project directory it just created.
	if err != nil {
		return gateway.AggregationConfig{}, fmt.Errorf("read candidate aggregation: %w", err)
	}
	var candidate gateway.AggregationConfig
	if err := json.Unmarshal(data, &candidate); err != nil {
		return gateway.AggregationConfig{}, fmt.Errorf("decode candidate aggregation: %w", err)
	}
	return candidate, nil
}

func readReleaseGatewayProfiles(path string) ([]gateway.TranscodeProfile, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- release check reads a generated file from a temporary project directory it just created.
	if err != nil {
		return nil, fmt.Errorf("read gateway config: %w", err)
	}
	var config struct {
		TranscodeProfiles []gateway.TranscodeProfile `json:"transcodeProfiles"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("decode gateway config: %w", err)
	}
	return config.TranscodeProfiles, nil
}

func readReleaseGatewayProfileCandidate(path string) (gateway.TranscodeProfile, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- release check reads a generated file from a temporary project directory it just created.
	if err != nil {
		return gateway.TranscodeProfile{}, fmt.Errorf("read candidate profile: %w", err)
	}
	var candidate gateway.TranscodeProfile
	if err := json.Unmarshal(data, &candidate); err != nil {
		return gateway.TranscodeProfile{}, fmt.Errorf("decode candidate profile: %w", err)
	}
	return candidate, nil
}
