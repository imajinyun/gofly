package command

import (
	"errors"
	"flag"
	"fmt"
	"os/exec"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

// releaseCheckReport aggregates all release-governance signals into a single
// structured report that can be consumed by CI or release automation.
type releaseCheckReport struct {
	Version     string             `json:"version"`
	Recommended string             `json:"recommended_semver"`
	Blocking    []string           `json:"blocking"`
	Warnings    []string           `json:"warnings"`
	Checks      []releaseCheckItem `json:"checks"`
	Summary     string             `json:"summary"`
}

type releaseCheckItem struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // pass / fail / skip
	Detail  string `json:"detail,omitempty"`
	Blocker bool   `json:"blocker"`
}

func releaseCommand(args []string) error {
	if printCommandHelp("release", args) {
		return nil
	}
	return releaseCommands.dispatch(args, "gofly release check")
}

var releaseCommands = newCommandRegistry(
	commandSpec{Name: "check", Run: releaseCheckCommand},
)

// releaseCheckCommand implements `gofly release check`.
// It aggregates API breaking, RPC breaking, Go public API compatibility,
// CHANGELOG version consistency, and go mod tidiness into a single report.
func releaseCheckCommand(args []string) error {
	fs := flag.NewFlagSet("release check", flag.ContinueOnError)
	apiBase := fs.String("api-base", "", "base .api file for breaking detection")
	apiTarget := fs.String("api-target", "", "target .api file for breaking detection")
	rpcBase := fs.String("rpc-base", "", "base .proto file for breaking detection")
	rpcTarget := fs.String("rpc-target", "", "target .proto file for breaking detection")
	changelog := fs.String("changelog", "CHANGELOG.md", "changelog file to parse for version")
	jsonOut := fs.Bool("json", false, "emit report as JSON")
	strict := fs.Bool("strict", false, "treat warnings as blockers")
	_, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}

	report := releaseCheckReport{Version: Version}
	var blockers, warnings []string

	// 1. API breaking check (only if files provided).
	if *apiBase != "" && *apiTarget != "" {
		apiReport, err := generator.DetectAPIChanges(generator.APIBreakingOptions{Base: *apiBase, Target: *apiTarget})
		item := releaseCheckItem{Name: "api-breaking", Status: "pass"}
		if err != nil {
			item.Status = "fail"
			item.Detail = err.Error()
			item.Blocker = true
			blockers = append(blockers, "api breaking check error: "+err.Error())
		} else if apiReport.HasBreaking() {
			item.Status = "fail"
			item.Detail = fmt.Sprintf("%d breaking change(s) detected", apiReport.Breaking)
			item.Blocker = true
			blockers = append(blockers, fmt.Sprintf("API breaking: %d change(s)", apiReport.Breaking))
		} else if !apiReport.IsEmpty() {
			item.Status = "pass"
			item.Detail = fmt.Sprintf("%d warning(s), no breaking", apiReport.Warnings)
			if apiReport.Warnings > 0 {
				warnings = append(warnings, fmt.Sprintf("API warnings: %d", apiReport.Warnings))
			}
		} else {
			item.Detail = "no changes"
		}
		report.Checks = append(report.Checks, item)
	}

	// 2. RPC breaking check (only if files provided).
	if *rpcBase != "" && *rpcTarget != "" {
		rpcReport, err := generator.DetectProtoDescriptorChanges(generator.ProtoBreakingOptions{Base: *rpcBase, Target: *rpcTarget})
		item := releaseCheckItem{Name: "rpc-breaking", Status: "pass"}
		if err != nil {
			item.Status = "fail"
			item.Detail = err.Error()
			item.Blocker = true
			blockers = append(blockers, "rpc breaking check error: "+err.Error())
		} else if rpcReport.HasBreaking() {
			item.Status = "fail"
			item.Detail = fmt.Sprintf("%d breaking change(s) detected", rpcReport.Breaking)
			item.Blocker = true
			blockers = append(blockers, fmt.Sprintf("RPC breaking: %d change(s)", rpcReport.Breaking))
		} else if len(rpcReport.Changes) > 0 {
			item.Status = "pass"
			item.Detail = fmt.Sprintf("%d warning(s), no breaking", rpcReport.Warnings)
			if rpcReport.Warnings > 0 {
				warnings = append(warnings, fmt.Sprintf("RPC warnings: %d", rpcReport.Warnings))
			}
		} else {
			item.Detail = "no changes"
		}
		report.Checks = append(report.Checks, item)
	}

	// 3. Go public API compatibility (apidiff).
	apidiffItem := releaseCheckItem{Name: "go-api-compat", Status: "pass"}
	if out, err := runAPIDiffCheck(); err != nil {
		apidiffItem.Status = "fail"
		apidiffItem.Detail = string(out)
		apidiffItem.Blocker = true
		blockers = append(blockers, "Go public API incompatible changes detected")
	} else {
		apidiffItem.Detail = strings.TrimSpace(string(out))
		if apidiffItem.Detail == "" {
			apidiffItem.Detail = "no incompatible changes"
		}
	}
	report.Checks = append(report.Checks, apidiffItem)

	// 4. CHANGELOG version consistency.
	changelogItem := releaseCheckItem{Name: "changelog-version", Status: "pass"}
	changelogVersion, err := parseChangelogVersion(*changelog)
	if err != nil {
		changelogItem.Status = "skip"
		changelogItem.Detail = "changelog not found or unparsable"
	} else if changelogVersion != "" && changelogVersion != Version {
		changelogItem.Status = "fail"
		changelogItem.Detail = fmt.Sprintf("CHANGELOG version %q != gofly version %q", changelogVersion, Version)
		changelogItem.Blocker = true
		blockers = append(blockers, changelogItem.Detail)
	} else {
		changelogItem.Detail = fmt.Sprintf("version %q", changelogVersion)
	}
	report.Checks = append(report.Checks, changelogItem)

	// 5. go mod tidy check.
	tidyItem := releaseCheckItem{Name: "go-mod-tidy", Status: "pass"}
	if out, err := exec.Command("go", "mod", "tidy", "-diff").CombinedOutput(); err != nil {
		tidyItem.Status = "fail"
		tidyItem.Detail = strings.TrimSpace(string(out))
		tidyItem.Blocker = true
		blockers = append(blockers, "go mod tidy would change go.mod/go.sum")
	} else {
		tidyItem.Detail = "clean"
	}
	report.Checks = append(report.Checks, tidyItem)

	// Determine recommended SemVer bump.
	report.Recommended = recommendSemver(blockers, warnings)
	report.Blocking = blockers
	report.Warnings = warnings

	if len(blockers) > 0 {
		report.Summary = fmt.Sprintf("BLOCKED: %d blocker(s); recommended %s", len(blockers), report.Recommended)
	} else if len(warnings) > 0 {
		report.Summary = fmt.Sprintf("PASS with %d warning(s); recommended %s", len(warnings), report.Recommended)
	} else {
		report.Summary = "PASS; recommended " + report.Recommended
	}

	if *strict && len(warnings) > 0 {
		report.Summary = "BLOCKED (strict mode): warnings treated as blockers"
		report.Blocking = append(report.Blocking, warnings...)
	}

	failed := len(report.Blocking) > 0 || (*strict && len(warnings) > 0)
	if *jsonOut || outputMode() == outputJSON {
		return printReleaseCheckJSON(report, failed)
	} else {
		cliOutputf("gofly release check — %s\n\n", report.Summary)
		for _, c := range report.Checks {
			mark := "✓"
			if c.Status == "fail" {
				mark = "✗"
			} else if c.Status == "skip" {
				mark = "-"
			}
			cliOutputf("  %s %-20s %s", mark, c.Name, c.Status)
			if c.Detail != "" {
				cliOutputf(" — %s", c.Detail)
			}
			if c.Blocker {
				cliOutput(" [BLOCKER]")
			}
			cliOutputln()
		}
		if len(report.Blocking) > 0 {
			cliOutputln("\nBlocking:")
			for _, b := range report.Blocking {
				cliOutputf("  • %s\n", b)
			}
		}
	}

	if failed {
		return errors.New("release check failed")
	}
	return nil
}
