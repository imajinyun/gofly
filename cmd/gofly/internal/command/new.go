package command

import (
	"fmt"
)

func newCommand(args []string) error {
	if printCommandHelp("new", args) {
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly new service|api|rpc`", errUsage)
	}
	switch args[0] {
	case "service":
		return serviceNewCommand(args[1:])
	case "api":
		return apiNewCommand(args[1:])
	case "rpc":
		return rpcNewCommand(args[1:])
	default:
		return fmt.Errorf("%w: expected `gofly new service|api|rpc`", errUsage)
	}
}
