// gofly is the gofly framework CLI: code generation, scaffolding, governance
// and service tooling for Go microservices.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/imajinyun/gofly/cmd/gofly/internal/command"
)

func main() {
	os.Exit(runMain(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func runMain(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if command.IsCompilerPluginMode() {
		if err := command.ExecuteCompilerPluginMode(stdin, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	streams := command.IOStreams{In: stdin, Out: stdout, Err: stderr}
	if err := command.ExecuteWithIO(args, streams); err != nil {
		if !command.JSONOutputRequested(args) {
			fmt.Fprintln(stderr, err)
		}
		return command.ExitCode(err)
	}
	return 0
}
