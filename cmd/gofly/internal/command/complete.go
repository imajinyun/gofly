package command

import (
	"fmt"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

// completeCommand 是 `gofly complete handler` 的快捷入口；也可以直接走 `gofly handler complete`。
func completeCommand(args []string) error {
	if printCommandHelp("complete", args) {
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly complete handler <%s>`", errUsage, completionShellUsage)
	}
	if args[0] != "handler" {
		return fmt.Errorf("%w: expected `gofly complete handler`", errUsage)
	}
	return completeShellCommand(args[1:])
}

// completeShellCommand 为 gofly 生成 shell 补全脚本。
// 用法：`gofly complete handler bash|zsh|fish|powershell|pwsh`。输出写入 stdout，
// 可以重定向到文件后由目标 shell 加载。
func completeShellCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly complete handler <%s>`", errUsage, completionShellUsage)
	}
	if len(args) > 1 {
		return fmt.Errorf("%w: complete handler accepts exactly one shell argument", errUsage)
	}
	shell := args[0]
	if !isCompletionShell(shell) {
		return fmt.Errorf("%w: expected %s, got %q", errUsage, completionShellUsage, shell)
	}
	script, err := generator.GenerateCompletion(shell)
	if err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	cliOutput(script)
	return nil
}
