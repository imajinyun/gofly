package command

import (
	"flag"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

// apiBreakingCommand implements `gofly api breaking --base old.api --target new.api`.
func apiBreakingCommand(args []string) error {
	fs := flag.NewFlagSet("api breaking", flag.ContinueOnError)
	base := fs.String("base", "", "base api file")
	target := fs.String("target", "", "target api file")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *base == "" && len(remaining) > 0 {
		*base = remaining[0]
		remaining = remaining[1:]
	}
	if *target == "" && len(remaining) > 0 {
		*target = remaining[0]
	}
	report, err := generator.DetectAPIChanges(generator.APIBreakingOptions{Base: *base, Target: *target})
	if err != nil {
		return err
	}
	cliOutput(string(generator.FormatBreakingText(report)))
	if report.HasBreaking() {
		return generator.ErrBreakingChanges
	}
	return nil
}

// rpcBreakingCommand implements `gofly rpc breaking --base old.proto --target new.proto`.
func rpcBreakingCommand(args []string) error {
	fs := flag.NewFlagSet("rpc breaking", flag.ContinueOnError)
	base := fs.String("base", "", "base proto file")
	target := fs.String("target", "", "target proto file")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *base == "" && len(remaining) > 0 {
		*base = remaining[0]
		remaining = remaining[1:]
	}
	if *target == "" && len(remaining) > 0 {
		*target = remaining[0]
	}
	report, err := generator.DetectProtoDescriptorChanges(generator.ProtoBreakingOptions{Base: *base, Target: *target})
	if err != nil {
		return err
	}
	cliOutput(formatRPCDescriptorCompatibilityText(report))
	if report.HasBreaking() {
		return generator.ErrBreakingChanges
	}
	return nil
}
