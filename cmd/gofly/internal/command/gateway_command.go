package command

import (
	"encoding/json"
	"flag"
	"fmt"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
	"github.com/imajinyun/gofly/gateway"
)

func gatewayCommand(args []string) error {
	if printCommandHelp("gateway", args) {
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly gateway profile validate`", errUsage)
	}
	switch args[0] {
	case "profile":
		return gatewayProfileCommand(args[1:])
	default:
		return fmt.Errorf("%w: expected `gofly gateway profile validate`", errUsage)
	}
}

func gatewayProfileCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly gateway profile validate`", errUsage)
	}
	switch args[0] {
	case "validate", "diff":
		return gatewayProfileValidateCommand(args[1:])
	default:
		return fmt.Errorf("%w: expected `gofly gateway profile validate`", errUsage)
	}
}

func gatewayGenCommand(args []string) error {
	leadingName, args := splitLeadingName(args)
	fs := flag.NewFlagSet("gateway gen", flag.ContinueOnError)
	name := fs.String("name", "", "gateway service name")
	module := fs.String("module", "", "go module path")
	dir := fs.String("dir", "", "output directory")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *name == "" {
		*name = leadingName
	}
	fillNameFromArgs(name, remaining)
	if *name == "" {
		*name = "gateway"
	}
	return generator.GenerateGateway(generator.GatewayOptions{Name: *name, Module: *module, Dir: *dir})
}

type gatewayProfileValidateConfig struct {
	TranscodeProfiles []gateway.TranscodeProfile `json:"transcodeProfiles"`
}

func gatewayProfileValidateCommand(args []string) error {
	fs := flag.NewFlagSet("gateway profile validate", flag.ContinueOnError)
	configPath := fs.String("config", "", "gateway config json file")
	candidatePath := fs.String("candidate", "", "candidate transcode profile json file")
	formatName := registerCLIFormatFlag(fs, outputJSON, "output format: text or json")
	jsonFlag := registerCLIJSONOutputFlag(fs, "output JSON")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *configPath == "" && len(remaining) > 0 {
		*configPath = remaining[0]
		remaining = remaining[1:]
	}
	if *candidatePath == "" && len(remaining) > 0 {
		*candidatePath = remaining[0]
	}
	if *configPath == "" || *candidatePath == "" {
		return fmt.Errorf("%w: --config and --candidate are required", errUsage)
	}
	currentProfiles, err := readGatewayTranscodeProfiles(*configPath)
	if err != nil {
		return err
	}
	candidate, err := readGatewayTranscodeProfileCandidate(*candidatePath)
	if err != nil {
		return err
	}
	gw, err := gateway.New([]gateway.Route{{PathPrefix: "/_profile_validate", Targets: []string{"http://127.0.0.1:1"}}}, gateway.WithTranscodeProfiles(currentProfiles...))
	if err != nil {
		return fmt.Errorf("load current transcode profiles: %w", err)
	}
	report := gw.ValidateTranscodeProfile(candidate)
	format, err := normalizeCLIFormat(formatName, outputJSON, outputText, outputJSON)
	if err != nil {
		return err
	}
	if valueFromBoolFlag(jsonFlag) || outputMode() == outputJSON || format == outputJSON {
		return printJSONEnvelope("gateway.profile.validate", report)
	}
	printGatewayProfileValidationText(report)
	return nil
}

func readGatewayTranscodeProfiles(path string) ([]gateway.TranscodeProfile, error) {
	data, err := readExplicitInputFile(path, "gateway config")
	if err != nil {
		return nil, err
	}
	var config gatewayProfileValidateConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("decode gateway config: %w", err)
	}
	return config.TranscodeProfiles, nil
}

func readGatewayTranscodeProfileCandidate(path string) (gateway.TranscodeProfile, error) {
	data, err := readExplicitInputFile(path, "candidate profile")
	if err != nil {
		return gateway.TranscodeProfile{}, err
	}
	var candidate gateway.TranscodeProfile
	if err := json.Unmarshal(data, &candidate); err != nil {
		return gateway.TranscodeProfile{}, fmt.Errorf("decode candidate profile: %w", err)
	}
	return candidate, nil
}

func printGatewayProfileValidationText(report gateway.TranscodeProfileValidationReport) {
	status := "compatible"
	if !report.OK {
		status = "invalid"
	} else if !report.Compatible {
		status = "breaking"
	}
	cliOutputf("gateway profile validation: %s\n", status)
	for _, errText := range report.Errors {
		cliOutputf("error: %s\n", errText)
	}
	for _, change := range report.Changes {
		parts := []string{change.Severity, change.Scope, change.Kind}
		if change.Source != "" {
			parts = append(parts, "source="+change.Source)
		}
		if change.Target != "" {
			parts = append(parts, "target="+change.Target)
		}
		if change.Message != "" {
			parts = append(parts, change.Message)
		}
		cliOutputln(strings.Join(parts, " "))
	}
}
