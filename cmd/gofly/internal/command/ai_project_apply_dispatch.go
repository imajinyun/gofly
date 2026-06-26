package command

import (
	"fmt"
	"strings"
)

func runAIProjectApplyCommand(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("%w: scaffold command is incomplete", errUsage)
	}
	switch {
	case args[0] == "new" && args[1] == "service":
		return serviceNewCommand(args[2:])
	case args[0] == "new" && args[1] == "api":
		return apiNewCommand(args[2:])
	case args[0] == "new" && args[1] == "rpc":
		return rpcNewCommand(args[2:])
	case args[0] == "gen" && args[1] == "gateway":
		return gatewayGenCommand(args[2:])
	default:
		return fmt.Errorf("%w: unsupported scaffold command `gofly %s`", errUsage, strings.Join(args, " "))
	}
}
