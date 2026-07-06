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
	default:
		item.Detail = fmt.Sprintf("compatible aggregation diff with %d change(s)", len(report.Changes))
		return item, nil
	}
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
