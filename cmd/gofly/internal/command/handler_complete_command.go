package command

import (
	"flag"
	"fmt"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

// handlerCompleteCommand implements `gofly handler complete --file <handler.go> --method <name>`.
func handlerCompleteCommand(args []string) error {
	fs := flag.NewFlagSet("handler complete", flag.ContinueOnError)
	file := fs.String("file", "", "handler Go source file path")
	src := fs.String("src", "", "api/proto/thrift IDL file used to infer missing handlers")
	idl := fs.String("idl", "", "api/proto/thrift IDL file used to infer missing handlers")
	name := fs.String("method", "", "handler / method name")
	receiver := fs.String("receiver", "", "Go receiver name (optional, inferred from filename)")
	pkg := fs.String("package", "", "package name when creating a new file")
	body := fs.String("body", "", "method body Go statements; if empty renders a TODO placeholder")
	comment := fs.String("comment", "", "optional comment attached to the method")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *file == "" {
		return fmt.Errorf("%w: --file is required for `gofly handler complete`", errUsage)
	}
	if *src == "" {
		*src = *idl
	}
	if *src != "" {
		n, err := generator.CompleteHandlersFromIDL(generator.HandlerCompleteOptions{
			File:     *file,
			IDLFile:  *src,
			Receiver: *receiver,
			Package:  *pkg,
		})
		if err != nil {
			return err
		}
		cliOutputf("added %d method(s) to %s from %s\n", n, *file, *src)
		return nil
	}
	if *name == "" {
		*name = strings.Join(remaining, " ")
	}
	if *name == "" {
		return fmt.Errorf("%w: --method is required for `gofly handler complete`", errUsage)
	}
	completer := generator.NewHandlerCompleter(*file, *receiver, *pkg, nil)
	n, err := completer.Complete([]generator.Method{{
		Name:    *name,
		Body:    *body,
		Comment: *comment,
	}})
	if err != nil {
		return err
	}
	if n > 0 {
		cliOutputf("added %d method(s) to %s\n", n, *file)
	} else {
		cliOutputf("nothing to do: %s already contains %s\n", *file, *name)
	}
	return nil
}
