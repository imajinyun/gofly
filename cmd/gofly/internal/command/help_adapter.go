package command

import commandhelp "github.com/imajinyun/gofly/cmd/gofly/internal/command/help"

type helpCommand = commandhelp.Command
type commandHelp = commandhelp.Topic

var (
	topLevelHelpAliases = commandhelp.TopLevelAliases()
	nestedHelpAliases   = commandhelp.NestedAliases()
	completionShells    = commandhelp.CompletionShells()
)

const completionShellUsage = commandhelp.CompletionShellUsage

type commandHelpPrinter struct{}

func (commandHelpPrinter) Println(args ...any) {
	cliOutputln(args...)
}

func isHelpArg(arg string) bool {
	return commandhelp.IsArg(arg)
}

func printCommandHelp(command string, args []string) bool {
	return commandhelp.PrintCommandHelp(commandHelpPrinter{}, command, args)
}

func commandHelpTopic(command string, args []string) (string, bool) {
	return commandhelp.CommandHelpTopic(command, args)
}

func leadingHelpTopicArgs(args []string) []string {
	return commandhelp.LeadingTopicArgs(args)
}

func joinHelpTopic(command string, parts []string) string {
	return commandhelp.JoinTopic(command, parts)
}

func printHelp(command string) {
	commandhelp.Print(commandHelpPrinter{}, command)
}

func usage() string {
	return commandhelp.Usage()
}

func commandUsage(command string) string {
	return commandhelp.CommandUsage(command)
}

func canonicalHelpTopic(command string) string {
	return commandhelp.CanonicalTopic(command)
}

func commandHelpFor(command string) commandHelp {
	return commandhelp.For(command)
}

func isGenHelpSubcommand(command string) bool {
	return commandhelp.IsGenHelpSubcommand(command)
}

func isAPIHelpSubcommand(command string) bool {
	return commandhelp.IsAPIHelpSubcommand(command)
}

func isRPCHelpSubcommand(command string) bool {
	return commandhelp.IsRPCHelpSubcommand(command)
}

func isModelDriverHelpSubcommand(driver string, command string) bool {
	return commandhelp.IsModelDriverHelpSubcommand(driver, command)
}

func isModelHelpSubcommand(command string) bool {
	return commandhelp.IsModelHelpSubcommand(command)
}

func isConfigHelpSubcommand(command string) bool {
	return commandhelp.IsConfigHelpSubcommand(command)
}

func isFeatureHelpSubcommand(command string) bool {
	return commandhelp.IsFeatureHelpSubcommand(command)
}

func isPluginHelpSubcommand(command string) bool {
	return commandhelp.IsPluginHelpSubcommand(command)
}

func isKubeHelpSubcommand(command string) bool {
	return commandhelp.IsKubeHelpSubcommand(command)
}

func isTemplateHelpSubcommand(command string) bool {
	return commandhelp.IsTemplateHelpSubcommand(command)
}

func isEnvHelpSubcommand(command string) bool {
	return commandhelp.IsEnvHelpSubcommand(command)
}

func isCompletionHelpSubcommand(command string) bool {
	return commandhelp.IsCompletionHelpSubcommand(command)
}

func isAIHelpSubcommand(command string) bool {
	return commandhelp.IsAIHelpSubcommand(command)
}

func isCompleteHandlerShell(command string) bool {
	return commandhelp.IsCompleteHandlerShell(command)
}

func isCompletionShell(shell string) bool {
	return commandhelp.IsCompletionShell(shell)
}

func ansiColor(code string, text string) string {
	return commandhelp.ANSIColor(code, text)
}

func rightPad(text string, padding int) string {
	return commandhelp.RightPad(text, padding)
}
