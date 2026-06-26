package command

import (
	"errors"
	"flag"
	"fmt"
)

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
		item, checkBlockers, checkWarnings := releaseAPIBreakingCheck(*apiBase, *apiTarget)
		blockers = append(blockers, checkBlockers...)
		warnings = append(warnings, checkWarnings...)
		report.Checks = append(report.Checks, item)
	}

	// 2. RPC breaking check (only if files provided).
	if *rpcBase != "" && *rpcTarget != "" {
		item, checkBlockers, checkWarnings := releaseRPCBreakingCheck(*rpcBase, *rpcTarget)
		blockers = append(blockers, checkBlockers...)
		warnings = append(warnings, checkWarnings...)
		report.Checks = append(report.Checks, item)
	}

	// 3. Go public API compatibility (apidiff).
	apidiffItem, checkBlockers := releaseGoAPICompatCheck()
	blockers = append(blockers, checkBlockers...)
	report.Checks = append(report.Checks, apidiffItem)

	// 4. CHANGELOG version consistency.
	changelogItem, checkBlockers := releaseChangelogVersionCheck(*changelog)
	blockers = append(blockers, checkBlockers...)
	report.Checks = append(report.Checks, changelogItem)

	// 5. go mod tidy check.
	tidyItem, checkBlockers := releaseGoModTidyCheck()
	blockers = append(blockers, checkBlockers...)
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
	}
	printReleaseCheckText(report)

	if failed {
		return errors.New("release check failed")
	}
	return nil
}
