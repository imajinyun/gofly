package help

import "strings"

type Printer interface {
	Println(args ...any)
}

func IsArg(arg string) bool {
	return arg == "-h" || arg == "--help"
}

func PrintCommandHelp(printer Printer, command string, args []string) bool {
	topic, ok := CommandHelpTopic(command, args)
	if !ok {
		return false
	}
	Print(printer, topic)
	return true
}

func CommandHelpTopic(command string, args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	if args[0] == "help" {
		return joinHelpTopic(command, leadingHelpTopicArgs(args[1:])), true
	}
	for i, arg := range args {
		if IsArg(arg) {
			return joinHelpTopic(command, leadingHelpTopicArgs(args[:i])), true
		}
	}
	return "", false
}

func LeadingTopicArgs(args []string) []string {
	return leadingHelpTopicArgs(args)
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

func JoinTopic(command string, parts []string) string {
	return joinHelpTopic(command, parts)
}

func joinHelpTopic(command string, parts []string) string {
	if len(parts) == 0 {
		return command
	}
	return command + " " + strings.Join(parts, " ")
}

func Print(printer Printer, command string) {
	if command == "" {
		printer.Println(Usage())
		return
	}
	printer.Println(CommandUsage(command))
}
