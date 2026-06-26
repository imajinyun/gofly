package command

import (
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func aggregateProjectFeatureContract(features []generator.ProjectFeatureResult) ([]string, []generator.ConfigHint, []string) {
	dependencies := []string{}
	configHints := []generator.ConfigHint{}
	verifyCommands := []string{}
	seenConfigHints := map[string]struct{}{}
	for _, feature := range features {
		dependencies = appendUniqueStrings(dependencies, feature.Dependencies...)
		verifyCommands = appendUniqueStrings(verifyCommands, feature.VerifyCommands...)
		for _, hint := range feature.ConfigHints {
			key := strings.ToLower(strings.TrimSpace(hint.Key))
			if key == "" {
				continue
			}
			if _, ok := seenConfigHints[key]; ok {
				continue
			}
			seenConfigHints[key] = struct{}{}
			configHints = append(configHints, hint)
		}
	}
	return dependencies, configHints, verifyCommands
}

func appendUniqueStrings(values []string, more ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(more))
	unique := make([]string, 0, len(values)+len(more))
	for _, value := range append(values, more...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

func aiProjectApplyNextActions(
	dir string,
	verify []string,
	dependencies []string,
	configHints []generator.ConfigHint,
	verifyRan bool,
	verifyPassed bool,
) []string {
	next := []string{"cd " + dir}
	if len(dependencies) > 0 {
		next = append(next, "review feature dependencies: go get "+strings.Join(dependencies, " "))
	}
	for _, hint := range configHints {
		action := "configure " + hint.Key + ": " + hint.Description
		if hint.Example != "" {
			action += " (example: " + hint.Example + ")"
		}
		next = append(next, action)
	}
	if len(verify) == 0 {
		return next
	}
	if !verifyRan {
		return append(next, "run: "+strings.Join(verify, " && "))
	}
	if verifyPassed {
		return append(next, "review generated files and commit when ready")
	}
	return append(next, "fix failed verification output, then rerun: "+strings.Join(verify, " && "))
}
