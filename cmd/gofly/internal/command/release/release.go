package release

import (
	"errors"
	"flag"
	"fmt"
	"strings"
)

// Command implements `gofly release` and routes `--help` through hooks.
func Command(args []string, hooks Hooks) error {
	hooks = normalizeHooks(hooks)
	if hooks.PrintHelp("release", args) {
		return nil
	}
	return CheckCommand(args, hooks)
}

// CheckCommand implements `gofly release check`.
// It aggregates API breaking, RPC breaking, Go public API compatibility,
// CHANGELOG version consistency, and go mod tidiness into a single report.
func CheckCommand(args []string, hooks Hooks) error {
	hooks = normalizeHooks(hooks)
	fs := flag.NewFlagSet("release check", flag.ContinueOnError)
	apiBase := fs.String("api-base", "", "base .api file for breaking detection")
	apiTarget := fs.String("api-target", "", "target .api file for breaking detection")
	rpcBase := fs.String("rpc-base", "", "base .proto file for breaking detection")
	rpcTarget := fs.String("rpc-target", "", "target .proto file for breaking detection")
	changelog := fs.String("changelog", "CHANGELOG.md", "changelog file to parse for version")
	evidence := fs.String("evidence", "", "emit only one release check evidence by check name")
	jsonOut := fs.Bool("json", false, "emit report as JSON")
	strict := fs.Bool("strict", false, "treat warnings as blockers")
	_, err := hooks.ParseFlags(fs, args)
	if err != nil {
		return err
	}

	report := releaseCheckReport{Version: hooks.Version}
	var blockers, warnings []string
	selectedEvidence := strings.TrimSpace(*evidence)
	shouldRun := func(name string) bool {
		return selectedEvidence == "" || selectedEvidence == name
	}

	// 1. API breaking check (only if files provided).
	if shouldRun("api-breaking") && *apiBase != "" && *apiTarget != "" {
		item, checkBlockers, checkWarnings := releaseAPIBreakingCheck(*apiBase, *apiTarget)
		blockers = append(blockers, checkBlockers...)
		warnings = append(warnings, checkWarnings...)
		report.Checks = append(report.Checks, item)
	}

	// 2. RPC breaking check (only if files provided).
	if shouldRun("rpc-breaking") && *rpcBase != "" && *rpcTarget != "" {
		item, checkBlockers, checkWarnings := releaseRPCBreakingCheck(*rpcBase, *rpcTarget)
		blockers = append(blockers, checkBlockers...)
		warnings = append(warnings, checkWarnings...)
		report.Checks = append(report.Checks, item)
	}

	// 3. Go public API compatibility (apidiff).
	if shouldRun("go-api-compat") {
		apidiffItem, checkBlockers := releaseGoAPICompatCheck()
		blockers = append(blockers, checkBlockers...)
		report.Checks = append(report.Checks, apidiffItem)
	}

	// 4. CHANGELOG version consistency.
	if shouldRun("changelog-version") {
		changelogItem, checkBlockers := releaseChangelogVersionCheck(*changelog, hooks.Version)
		blockers = append(blockers, checkBlockers...)
		report.Checks = append(report.Checks, changelogItem)
	}

	// 5. go mod tidy check.
	if shouldRun("go-mod-tidy") {
		tidyItem, checkBlockers := releaseGoModTidyCheck()
		blockers = append(blockers, checkBlockers...)
		report.Checks = append(report.Checks, tidyItem)
	}

	// 6. Generated gateway transcode profile contract check.
	if shouldRun("gateway-profile-contract") {
		gatewayProfileItem, checkBlockers := releaseGatewayProfileContractCheck()
		blockers = append(blockers, checkBlockers...)
		report.Checks = append(report.Checks, gatewayProfileItem)
	}

	// 7. Generated gateway BFF aggregation contract check.
	if shouldRun("gateway-aggregation-contract") {
		gatewayAggregationItem, checkBlockers := releaseGatewayAggregationContractCheck()
		blockers = append(blockers, checkBlockers...)
		report.Checks = append(report.Checks, gatewayAggregationItem)
	}

	// 8. RPC mux adapter release-train evidence.
	if shouldRun("rpc-mux-adapter-evidence") {
		rpcMuxAdapterItem, checkBlockers := releaseRPCMuxAdapterEvidenceCheck()
		blockers = append(blockers, checkBlockers...)
		report.Checks = append(report.Checks, rpcMuxAdapterItem)
	}

	// 9. Generated RPC mux retry/open-boundary smoke evidence.
	if shouldRun("generated-rpc-mux-retry-smoke") {
		generatedRPCMuxRetryItem, checkBlockers := releaseGeneratedRPCMuxRetrySmokeCheck()
		blockers = append(blockers, checkBlockers...)
		report.Checks = append(report.Checks, generatedRPCMuxRetryItem)
	}

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

	if selectedEvidence != "" {
		report = filterReleaseCheckEvidence(report, selectedEvidence)
	}
	failed := len(report.Blocking) > 0 || (*strict && len(warnings) > 0)
	if *jsonOut || hooks.OutputJSON() {
		return printReleaseCheckJSON(hooks, report, failed)
	}
	printReleaseCheckText(hooks, report)

	if failed {
		return errors.New("release check failed")
	}
	return nil
}

func filterReleaseCheckEvidence(report releaseCheckReport, name string) releaseCheckReport {
	name = strings.TrimSpace(name)
	filtered := releaseCheckReport{Version: report.Version, Recommended: report.Recommended}
	for _, check := range report.Checks {
		if check.Name != name {
			continue
		}
		filtered.Checks = []releaseCheckItem{check}
		if check.Blocker {
			filtered.Blocking = []string{check.Detail}
			filtered.Summary = "BLOCKED: evidence check " + name
		} else {
			filtered.Summary = "PASS: evidence check " + name
		}
		return filtered
	}
	filtered.Blocking = []string{"release evidence check not found: " + name}
	filtered.Summary = "BLOCKED: release evidence check not found"
	return filtered
}
