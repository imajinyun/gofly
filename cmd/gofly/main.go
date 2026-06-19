// gofly is the gofly framework CLI: code generation, scaffolding, governance
// and service tooling for Go microservices.
package main

import (
	"fmt"
	"os"

	"github.com/gofly/gofly/cmd/gofly/internal/command"
)

func main() {
	if command.IsCompilerPluginMode() {
		if err := command.ExecuteCompilerPluginMode(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := command.Execute(os.Args[1:]); err != nil {
		if command.OutputMode() != "json" {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(command.ExitCode(err))
	}
}
