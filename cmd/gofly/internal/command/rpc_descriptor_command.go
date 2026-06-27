package command

import (
	"flag"
	"fmt"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
	"github.com/imajinyun/gofly/rpc"
)

func rpcDescriptorCommand(args []string) error {
	fs := flag.NewFlagSet("rpc descriptor", flag.ContinueOnError)
	base := fs.String("base", "", "base descriptor json file")
	target := fs.String("target", "", "target descriptor json file")
	remoteURL := fs.String("url", "", "remote admin descriptor URL or admin base URL")
	service := fs.String("service", "", "service name when --url points at an admin base URL")
	formatName := registerCLIFormatFlag(fs, outputText, "output format: text or json")
	token := fs.String("token", "", "bearer token for descriptor URL sources")
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
	if *remoteURL != "" {
		if *base == "" {
			*base = *remoteURL
		} else if *target == "" {
			*target = *remoteURL
		}
	}
	if *base == "" || *target == "" {
		return fmt.Errorf("%w: base and target descriptor sources are required", errUsage)
	}
	baseDescriptor, err := readRPCDescriptorSource(*base, *token, *service)
	if err != nil {
		return fmt.Errorf("read base descriptor: %w", err)
	}
	targetDescriptor, err := readRPCDescriptorSource(*target, *token, *service)
	if err != nil {
		return fmt.Errorf("read target descriptor: %w", err)
	}
	report := rpc.CompareDescriptors(baseDescriptor, targetDescriptor)
	format, err := normalizeCLIFormat(formatName, outputText, outputText, outputJSON)
	if err != nil {
		return err
	}
	switch format {
	case outputText:
		cliOutput(formatRPCDescriptorCompatibilityText(report))
	case outputJSON:
		if err := printJSON(report); err != nil {
			return err
		}
	}
	if report.HasBreaking() {
		return generator.ErrBreakingChanges
	}
	return nil
}
