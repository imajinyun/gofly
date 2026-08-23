package command

import (
	"os/exec"

	commandrelease "github.com/imajinyun/gofly/cmd/gofly/internal/command/release"
	"github.com/imajinyun/gofly/gateway"
)

type releaseCheckReport = commandrelease.CheckReport
type releaseCheckItem = commandrelease.CheckItem

func releaseCommand(args []string) error {
	if printCommandHelp("release", args) {
		return nil
	}
	return releaseCommands.dispatch(args, "gofly release check")
}

var releaseCommands = newCommandRegistry(
	commandSpec{Name: "check", Run: releaseCheckCommand},
)

func releaseCheckCommand(args []string) error {
	return commandrelease.CheckCommand(args, releaseHooks())
}

func releaseHooks() commandrelease.Hooks {
	return commandrelease.Hooks{
		PrintHelp:       printCommandHelp,
		ParseFlags:      parseInterspersedFlags,
		PrintJSON:       printJSON,
		PrintText:       cliOutput,
		PrintTextf:      cliOutputf,
		PrintTextln:     cliOutputln,
		Version:         Version,
		OutputJSON:      func() bool { return outputMode() == outputJSON },
		AlreadyReported: errJSONAlreadyReported,
	}
}

func releaseGatewayProfileContractCheck() (releaseCheckItem, []string) {
	return commandrelease.GatewayProfileContractCheck()
}

func releaseGatewayAggregationContractCheck() (releaseCheckItem, []string) {
	return commandrelease.GatewayAggregationContractCheck()
}

func releaseRPCMuxAdapterEvidenceCheck() (releaseCheckItem, []string) {
	return commandrelease.RPCMuxAdapterEvidenceCheck()
}

func releaseGeneratedRPCMuxRetrySmokeCheck() (releaseCheckItem, []string) {
	return commandrelease.GeneratedRPCMuxRetrySmokeCheck()
}

func resolveReleaseEvidencePath(path string) (string, error) {
	return commandrelease.ResolveEvidencePath(path)
}

func readReleaseJSONFile(path, label string) (map[string]any, error) {
	return commandrelease.ReadJSONFile(path, label)
}

func readReleaseGatewayConfig(path string) (gateway.Config, error) {
	return commandrelease.ReadGatewayConfig(path)
}

func readReleaseGatewayAggregationCandidate(path string) (gateway.AggregationConfig, error) {
	return commandrelease.ReadGatewayAggregationCandidate(path)
}

func readReleaseGatewayProfiles(path string) ([]gateway.TranscodeProfile, error) {
	return commandrelease.ReadGatewayProfiles(path)
}

func readReleaseGatewayProfileCandidate(path string) (gateway.TranscodeProfile, error) {
	return commandrelease.ReadGatewayProfileCandidate(path)
}

func apidiffCommand(tool string, args ...string) *exec.Cmd {
	return commandrelease.APIDiffCommand(tool, args...)
}
