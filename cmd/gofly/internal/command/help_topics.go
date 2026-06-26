package command

import "strings"

var completionShells = []string{"bash", "zsh", "fish", "powershell", "pwsh"}

const completionShellUsage = "bash|zsh|fish|powershell|pwsh"

func commandUsage(command string) string {
	return renderCommandHelp(commandHelpFor(command))
}

func canonicalHelpTopic(command string) string {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return ""
	}
	if alias, ok := topLevelHelpAliases[parts[0]]; ok {
		parts[0] = alias
	}
	if len(parts) >= 2 {
		if aliases := nestedHelpAliases[parts[0]]; aliases != nil {
			if alias, ok := aliases[parts[1]]; ok {
				parts[1] = alias
			}
		}
		switch parts[0] {
		case "complete":
			if len(parts) >= 3 && parts[1] == "handler" && parts[2] == "pwsh" {
				parts[2] = "powershell"
			}
		}
	}
	parts = trimHelpTopicPositionals(parts)
	return strings.Join(parts, " ")
}

func trimHelpTopicPositionals(parts []string) []string {
	if len(parts) < 2 {
		return parts
	}
	switch parts[0] {
	case "api":
		if isAPIHelpSubcommand(parts[1]) {
			return parts[:2]
		}
	case "rpc":
		if isRPCHelpSubcommand(parts[1]) {
			return parts[:2]
		}
	case "new":
		if parts[1] == "service" || parts[1] == "api" || parts[1] == "rpc" {
			return parts[:2]
		}
	case "model":
		if len(parts) >= 3 && isModelDriverHelpSubcommand(parts[1], parts[2]) {
			return parts[:3]
		}
		if isModelHelpSubcommand(parts[1]) {
			return parts[:2]
		}
	case "gen":
		if isGenHelpSubcommand(parts[1]) {
			return parts[:2]
		}
	case "handler":
		if parts[1] == "gen" || parts[1] == "complete" {
			return parts[:2]
		}
	case "feature":
		if isFeatureHelpSubcommand(parts[1]) {
			return parts[:2]
		}
	case "plugin":
		if (parts[1] == "install" || parts[1] == "uninstall") && len(parts) >= 3 {
			return parts[:3]
		}
		if isPluginHelpSubcommand(parts[1]) {
			return parts[:2]
		}
	case "config":
		if isConfigHelpSubcommand(parts[1]) {
			return parts[:2]
		}
	case "env":
		if isEnvHelpSubcommand(parts[1]) {
			return parts[:2]
		}
	case "kube":
		if isKubeHelpSubcommand(parts[1]) {
			return parts[:2]
		}
	case "template":
		if isTemplateHelpSubcommand(parts[1]) {
			return parts[:2]
		}
	case "migrate", "migration":
		if parts[1] == "create" || parts[1] == "new" {
			return parts[:2]
		}
	case "complete":
		if parts[1] == "handler" {
			if len(parts) >= 3 && isCompleteHandlerShell(parts[2]) {
				return parts[:3]
			}
			return parts[:2]
		}
	case "quickstart", "docker":
		return parts[:1]
	case "version", "upgrade", "bug", "doctor":
		return parts[:1]
	case "ai":
		if len(parts) >= 2 && isAIHelpSubcommand(parts[1]) {
			return parts[:2]
		}
		return parts[:1]
	case "example", "examples":
		if parts[1] == "list" || parts[1] == "run" {
			return parts[:2]
		}
		return parts[:1]
	case "completion":
		if isCompletionHelpSubcommand(parts[1]) {
			return parts[:2]
		}
	}
	return parts
}

func isGenHelpSubcommand(command string) bool {
	switch command {
	case "handler", "rpc", "api", "rest", "middleware", "model", "gateway":
		return true
	default:
		return false
	}
}

func isAPIHelpSubcommand(command string) bool {
	switch command {
	case "go", "check", "format", "swagger", "doc", "route":
		return true
	case "import", "diff", "breaking", "types", "new":
		return true
	case "client", "ts", "js", "dart", "java", "kotlin":
		return true
	case "plugin", "middleware":
		return true
	default:
		return false
	}
}

func isRPCHelpSubcommand(command string) bool {
	switch command {
	case "idl", "thrift", "client", "server", "middleware", "lint", "deps":
		return true
	case "gen", "protoc", "check", "doc", "breaking", "descriptor", "plugin", "template", "new":
		return true
	default:
		return false
	}
}

func isModelDriverHelpSubcommand(driver string, command string) bool {
	switch driver {
	case "mysql", "pg":
		return command == "ddl" || command == "datasource"
	default:
		return false
	}
}

func isModelHelpSubcommand(command string) bool {
	switch command {
	case "gen", "mongo":
		return true
	default:
		return false
	}
}

func isConfigHelpSubcommand(command string) bool {
	switch command {
	case "init", "show", "get", "set", "clean":
		return true
	default:
		return false
	}
}

func isFeatureHelpSubcommand(command string) bool {
	switch command {
	case "list", "run":
		return true
	default:
		return false
	}
}

func isPluginHelpSubcommand(command string) bool {
	switch command {
	case "list", "search", "install", "uninstall", "run":
		return true
	default:
		return false
	}
}

func isKubeHelpSubcommand(command string) bool {
	switch command {
	case "deploy", "service", "ingress", "configmap", "job":
		return true
	default:
		return false
	}
}

func isTemplateHelpSubcommand(command string) bool {
	switch command {
	case "init", "list", "clean", "update", "revert":
		return true
	default:
		return false
	}
}

func isEnvHelpSubcommand(command string) bool {
	return command == "check" || command == "install"
}

func isCompletionHelpSubcommand(command string) bool {
	return isCompletionShell(command)
}

func isAIHelpSubcommand(command string) bool {
	return command == "manifest" || command == "plan" || command == "new" || command == "complete" || command == "stream" || command == "doctor" || command == "control-plane"
}

func isCompleteHandlerShell(command string) bool {
	return isCompletionShell(command)
}

func isCompletionShell(shell string) bool {
	shell = strings.ToLower(strings.TrimSpace(shell))
	for _, supported := range completionShells {
		if shell == supported {
			return true
		}
	}
	return false
}
