package command

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"sort"
	"strings"
)

var (
	errUsage               = errors.New("invalid usage")
	errJSONAlreadyReported = errors.New("json error already reported")
)

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// ExitCode maps command errors to stable Unix-style process exit codes.
func ExitCode(err error) int {
	if err == nil {
		return exitOK
	}
	if errors.Is(err, errUsage) || errors.Is(err, flag.ErrHelp) || isFlagUsageError(err) {
		return exitUsage
	}
	return exitError
}

func isFlagUsageError(err error) bool {
	message := err.Error()
	return strings.Contains(message, "flag provided but not defined") ||
		strings.Contains(message, "invalid value") ||
		strings.Contains(message, "flag needs an argument")
}

func printJSON(value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	cliOutputln(string(data))
	return nil
}

func printJSONLine(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal json line: %w", err)
	}
	cliOutputln(string(data))
	return nil
}

func printCLIPlan(command string, plan cliPlan, forceJSON ...bool) error {
	if plan.Command == "" {
		plan.Command = command
	}
	jsonOutput := outputMode() == outputJSON
	for _, force := range forceJSON {
		jsonOutput = jsonOutput || force
	}
	if jsonOutput {
		return printJSONEnvelope(command, plan)
	}
	cliOutputfIf("%s plan (dry-run=%t, mutates-filesystem=%t)\n", command, plan.DryRun, plan.MutatesFilesystem)
	if len(plan.Inputs) > 0 {
		keys := make([]string, 0, len(plan.Inputs))
		for key := range plan.Inputs {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		cliOutputlnIf("inputs:")
		for _, key := range keys {
			cliOutputfIf("  %s: %s\n", key, plan.Inputs[key])
		}
	}
	if len(plan.Actions) > 0 {
		cliOutputlnIf("actions:")
		for _, action := range plan.Actions {
			cliOutputfIf("  - %s %s (%s): %s\n", action.Operation, action.Target, action.RiskLevel, action.Description)
		}
	}
	for _, warning := range plan.Warnings {
		cliOutputfIf("warning: %s\n", warning)
	}
	for _, next := range plan.NextActions {
		cliOutputfIf("next: %s\n", next)
	}
	return nil
}

type jsonEnvelope struct {
	OK          bool       `json:"ok"`
	Command     string     `json:"command"`
	Version     string     `json:"version"`
	Data        any        `json:"data,omitempty"`
	Error       *jsonError `json:"error,omitempty"`
	Diagnostics []string   `json:"diagnostics,omitempty"`
	Warnings    []string   `json:"warnings,omitempty"`
	NextActions []string   `json:"nextActions,omitempty"`
}

type jsonError struct {
	Code        string         `json:"code"`
	Message     string         `json:"message"`
	Retryable   bool           `json:"retryable"`
	Remediation string         `json:"remediation,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
	NextActions []string       `json:"nextActions,omitempty"`
}

func printJSONEnvelope(command string, data any) error {
	return printJSON(jsonEnvelope{OK: true, Command: command, Version: Version, Data: data})
}

func printJSONError(command string, err error) error {
	classified := classifyJSONError(err)
	var nextActions []string
	if classified != nil {
		nextActions = classified.NextActions
	}
	return printJSON(jsonEnvelope{OK: false, Command: command, Version: Version, Error: classified, NextActions: nextActions})
}

func commandName(args []string) string {
	if len(args) == 0 {
		return "root"
	}
	if len(args) == 1 {
		return args[0]
	}
	return args[0] + "." + args[1]
}
