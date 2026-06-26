package command

import (
	"strings"
)

func rpcCommand(args []string) error {
	if printCommandHelp("rpc", args) {
		return nil
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return rpcTemplateCommand(args)
	}
	return rpcCommands.dispatch(args, "gofly rpc idl|thrift|client|server|middleware|lint|deps|check|breaking|descriptor|gen|protoc|template|new")
}
