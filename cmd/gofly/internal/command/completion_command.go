package command

import (
	"fmt"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func completionCommand(args []string) error {
	if printCommandHelp("completion", args) {
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly completion %s`", errUsage, completionShellUsage)
	}
	if len(args) > 1 {
		return fmt.Errorf("%w: completion accepts exactly one shell argument", errUsage)
	}
	shell := args[0]
	script, err := generator.GenerateCompletion(shell)
	if err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	cliOutput(script)
	return nil
}
