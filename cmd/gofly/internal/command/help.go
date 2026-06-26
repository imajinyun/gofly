package command

import "strings"

func isHelpArg(arg string) bool {
	return arg == "-h" || arg == "--help"
}

func printCommandHelp(command string, args []string) bool {
	topic, ok := commandHelpTopic(command, args)
	if !ok {
		return false
	}
	printHelp(topic)
	return true
}

func commandHelpTopic(command string, args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	if args[0] == "help" {
		return joinHelpTopic(command, leadingHelpTopicArgs(args[1:])), true
	}
	for i, arg := range args {
		if isHelpArg(arg) {
			return joinHelpTopic(command, leadingHelpTopicArgs(args[:i])), true
		}
	}
	return "", false
}

func leadingHelpTopicArgs(args []string) []string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "" || strings.HasPrefix(arg, "-") {
			break
		}
		parts = append(parts, arg)
	}
	return parts
}

func joinHelpTopic(command string, parts []string) string {
	if len(parts) == 0 {
		return command
	}
	return command + " " + strings.Join(parts, " ")
}

func printHelp(command string) {
	if command == "" {
		cliOutputln(usage())
		return
	}
	cliOutputln(commandUsage(command))
}
