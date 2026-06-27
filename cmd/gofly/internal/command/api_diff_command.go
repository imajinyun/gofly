package command

import (
	"flag"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func apiDiffCommand(args []string) error {
	leadingFiles, args := splitLeadingNames(args)
	fs := flag.NewFlagSet("api diff", flag.ContinueOnError)
	base := fs.String("base", "", "base api file")
	old := fs.String("old", "", "base api file, alias for --base")
	target := fs.String("target", "", "target api file")
	newFile := fs.String("new", "", "target api file, alias for --target")
	dir := fs.String("dir", ".", "output directory")
	output := registerOutputPathFlags(fs, "output diff file")
	format := registerCLIFormatFlag(fs, outputText, "diff format: text, markdown, or json")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *base == "" {
		*base = *old
	}
	if *target == "" {
		*target = *newFile
	}
	if *base == "" && len(leadingFiles) > 0 {
		*base = leadingFiles[0]
	}
	if *target == "" && len(leadingFiles) > 1 {
		*target = leadingFiles[1]
	}
	if *base == "" && len(remaining) > 0 {
		*base = remaining[0]
		remaining = remaining[1:]
	}
	if *target == "" && len(remaining) > 0 {
		*target = remaining[0]
	}
	return generator.GenerateAPIDiff(generator.APIDiffOptions{Base: *base, Target: *target, Dir: *dir, Output: output.resolve(), Format: *format})
}
