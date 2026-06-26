package command

import "strings"

func apiCommand(args []string) error {
	if printCommandHelp("api", args) {
		return nil
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return apiTemplateCommand(args)
	}
	return apiCommands.dispatch(args, "gofly api check|breaking|gen|go|types|route|import|diff|plugin|middleware|format|doc|client|new")
}
