package command

import (
	"flag"
	"strings"

	commandfeature "github.com/imajinyun/gofly/cmd/gofly/internal/command/feature"
)

func featureCommand(args []string) error {
	return commandfeature.Command(args, featureHooks())
}

func featureHooks() commandfeature.Hooks {
	return commandfeature.Hooks{
		PrintHelp:           printCommandHelp,
		ParseFlags:          parseInterspersedFlags,
		RegisterOutputFlags: registerFeatureOutputFlags,
		UseJSON:             featureUsesJSON,
		PrintJSONEnvelope:   printJSONEnvelope,
		PrintJSONError:      printJSONError,
		PrintTextlnIf:       cliOutputlnIf,
		PrintTextfIf:        cliOutputfIf,
		Usage:               errUsage,
	}
}

func registerFeatureOutputFlags(fs *flag.FlagSet) commandfeature.OutputFlags {
	flags := registerCLIOutputFlags(fs, cliOutputFlagOptions{})
	return commandfeature.OutputFlags{Format: flags.Format, JSON: flags.JSON}
}

func featureUsesJSON(flags commandfeature.OutputFlags) bool {
	return valueFromBoolFlag(flags.JSON) || outputMode() == outputJSON || strings.EqualFold(strings.TrimSpace(valueFromStringFlag(flags.Format)), outputJSON)
}
