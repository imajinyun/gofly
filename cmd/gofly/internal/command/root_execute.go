package command

import (
	"errors"
	"fmt"
	"strings"
)

func Execute(args []string) error {
	streams := defaultIOStreams()
	return ExecuteWithIO(args, streams)
}

func ExecuteWithIO(args []string, streams IOStreams) error {
	output, verbosity, remaining, err := parseGlobalControls(args)
	if err != nil {
		return err
	}
	return withCommandIO(streams, output, verbosity, func() error {
		err := execute(remaining)
		if err != nil && outputMode() == outputJSON && !errors.Is(err, errJSONAlreadyReported) {
			_ = printJSONError(commandName(remaining), err)
		}
		return err
	})
}

func parseGlobalControls(args []string) (string, int, []string, error) {
	output := outputText
	verbosity := verbosityNormal
	i := 0
	for i < len(args) {
		arg := args[i]
		switch {
		case arg == "--output":
			if i+1 >= len(args) {
				return "", 0, nil, fmt.Errorf("%w: --output requires text or json", errUsage)
			}
			value := args[i+1]
			if value != outputText && value != outputJSON {
				return "", 0, nil, fmt.Errorf("%w: unsupported --output %q", errUsage, value)
			}
			output = value
			i += 2
		case strings.HasPrefix(arg, "--output="):
			value := strings.TrimPrefix(arg, "--output=")
			if value != outputText && value != outputJSON {
				return "", 0, nil, fmt.Errorf("%w: unsupported --output %q", errUsage, value)
			}
			output = value
			i++
		case arg == "-v" || arg == "--verbose":
			if verbosity != verbosityQuiet {
				verbosity = verbosityVerbose
			}
			i++
		case arg == "-q" || arg == "--quiet":
			verbosity = verbosityQuiet
			i++
		default:
			return output, verbosity, args[i:], nil
		}
	}
	return output, verbosity, args[i:], nil
}

func execute(args []string) error {
	args = normalizeGoctlStyleFlags(args)
	if len(args) == 0 || isHelpArg(args[0]) {
		printHelp("")
		return nil
	}
	if args[0] == "help" {
		if len(args) > 1 {
			printHelp(strings.Join(args[1:], " "))
			return nil
		}
		printHelp("")
		return nil
	}
	return rootCommands.dispatch(args, rootCommands.expected())
}

func parseGlobalOutput(args []string) (string, []string, error) {
	output, _, remaining, err := parseGlobalControls(args)
	return output, remaining, err
}

func normalizeGoctlStyleFlags(args []string) []string {
	if len(args) == 0 {
		return args
	}
	out := make([]string, len(args))
	for i, arg := range args {
		out[i] = normalizeGoctlStyleFlag(arg)
	}
	return out
}

func normalizeGoctlStyleFlag(arg string) string {
	if len(arg) <= 2 || !strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "--") {
		return arg
	}
	if strings.HasPrefix(arg, "-=") || arg[1] == '-' {
		return arg
	}
	return "-" + arg
}
