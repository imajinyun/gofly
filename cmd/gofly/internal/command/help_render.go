package command

import (
	"fmt"
	"os"
	"strings"
)

func renderCommandHelp(topic commandHelp) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", topic.Short)
	fmt.Fprintf(&b, "%s\n  %s\n", helpBlue("Usage:"), helpColoredCommandLine(topic.Usage))
	if len(topic.Commands) > 0 {
		fmt.Fprintf(&b, "\n%s\n", helpBlue("Available Commands:"))
		for _, cmd := range topic.Commands {
			fmt.Fprintf(&b, "  %s %s\n", helpCommandName(cmd.Name, 20), cmd.Short)
		}
	}
	if len(topic.Flags) > 0 {
		fmt.Fprintf(&b, "\n%s\n", helpBlue("Flags:"))
		for _, flag := range topic.Flags {
			fmt.Fprintf(&b, "  %s\n", helpGreen(flag))
		}
	}
	if len(topic.Examples) > 0 {
		fmt.Fprintf(&b, "\n%s\n", helpBlue("Examples:"))
		for _, example := range topic.Examples {
			fmt.Fprintf(&b, "  %s\n", helpColoredCommandLine(example))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func colorizeHelpText(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		switch strings.TrimSpace(line) {
		case "Usage:", "Compatibility aliases:":
			lines[i] = helpBlue(line)
		default:
			if indent := leadingSpaces(line); indent >= 2 {
				commandLine := strings.TrimPrefix(strings.TrimSpace(line), "gofly ")
				lines[i] = strings.Repeat(" ", indent) + helpColoredCommandLine(commandLine)
			}
		}
	}
	return strings.Join(lines, "\n")
}

func leadingSpaces(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

func helpBlue(text string) string {
	return ansiColor("94", text)
}

func helpGreen(text string) string {
	return ansiColor("92", text)
}

func helpColoredCommandLine(line string) string {
	line = strings.TrimPrefix(strings.TrimSpace(line), "gofly ")
	for _, separator := range []string{" | ", " && ", " ; "} {
		line = strings.ReplaceAll(line, separator+"gofly ", separator)
	}
	return ansiColor(helpCommandColor(line), line)
}

func helpCommandName(name string, padding int) string {
	return ansiColor(helpCommandColor(name), rightPad(name, padding))
}

func helpCommandColor(text string) string {
	command := strings.Fields(strings.TrimSpace(text))
	if len(command) == 0 {
		return "97"
	}
	switch command[0] {
	case "api":
		return "92"
	case "rpc":
		return "95"
	case "model", "template", "upgrade":
		return "93"
	case "new", "kube", "quickstart", "feature":
		return "96"
	case "gen", "handler", "bug":
		return "91"
	case "docker", "config", "completion", "complete":
		return "94"
	case "env":
		return "92"
	case "migrate", "migration", "plugin":
		return "95"
	default:
		return "97"
	}
}

func ansiColor(code string, text string) string {
	if text == "" || os.Getenv("NO_COLOR") != "" || os.Getenv("GOFLY_NO_COLOR") != "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func rightPad(text string, padding int) string {
	if len(text) >= padding {
		return text
	}
	return text + strings.Repeat(" ", padding-len(text))
}
