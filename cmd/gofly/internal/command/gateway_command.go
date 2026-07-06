package command

import (
	"encoding/json"
	"flag"
	"fmt"
	"sort"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
	"github.com/imajinyun/gofly/gateway"
	"github.com/imajinyun/gofly/rest"
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
	case "aggregation":
		return gatewayAggregationCommand(args[1:])
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

func gatewayAggregationCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly gateway aggregation validate`", errUsage)
	}
	switch args[0] {
	case "validate", "diff":
		return gatewayAggregationValidateCommand(args[1:])
	default:
		return fmt.Errorf("%w: expected `gofly gateway aggregation validate`", errUsage)
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
	Gateway           gateway.Config             `json:"gateway"`
}

type gatewayAggregationCandidateConfig struct {
	Aggregation gateway.AggregationConfig `json:"aggregation"`
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

func gatewayAggregationValidateCommand(args []string) error {
	fs := flag.NewFlagSet("gateway aggregation validate", flag.ContinueOnError)
	configPath := fs.String("config", "", "gateway config json file")
	candidatePath := fs.String("candidate", "", "candidate aggregation json file")
	openAPIBasePath := fs.String("openapi-base", "", "base OpenAPI json file")
	openAPICandidatePath := fs.String("openapi-candidate", "", "candidate OpenAPI json file")
	routeName := fs.String("route", "", "route name or route key")
	formatName := registerCLIFormatFlag(fs, outputJSON, "output format: text, markdown, sarif, or json")
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
	if *openAPIBasePath != "" || *openAPICandidatePath != "" {
		if *openAPIBasePath == "" || *openAPICandidatePath == "" {
			return fmt.Errorf("%w: --openapi-base and --openapi-candidate are required together", errUsage)
		}
		return gatewayAggregationValidateOpenAPICommand(*openAPIBasePath, *openAPICandidatePath, *routeName, formatName, jsonFlag)
	}
	if *configPath == "" || *candidatePath == "" {
		return fmt.Errorf("%w: --config and --candidate are required", errUsage)
	}
	current, err := readGatewayAggregationConfig(*configPath)
	if err != nil {
		return err
	}
	candidate, err := readGatewayAggregationCandidate(*candidatePath)
	if err != nil {
		return err
	}
	gw, err := gateway.NewFromConfig(current, nil)
	if err != nil {
		return fmt.Errorf("load current gateway aggregation config: %w", err)
	}
	report := gw.ValidateAggregation(*routeName, candidate)
	format, err := normalizeCLIFormat(formatName, outputJSON, outputText, "markdown", "sarif", outputJSON)
	if err != nil {
		return err
	}
	if valueFromBoolFlag(jsonFlag) || outputMode() == outputJSON || format == outputJSON {
		return printJSONEnvelope("gateway.aggregation.validate", report)
	}
	if format == "markdown" {
		printGatewayAggregationValidationMarkdown(report)
		return nil
	}
	if format == "sarif" {
		return printGatewayAggregationValidationSARIF(report)
	}
	printGatewayAggregationValidationText(report)
	return nil
}

func gatewayAggregationValidateOpenAPICommand(basePath, candidatePath, routeName string, formatName *string, jsonFlag *bool) error {
	format, err := normalizeCLIFormat(formatName, outputJSON, outputText, "markdown", "sarif", outputJSON)
	if err != nil {
		return err
	}
	baseDoc, err := readGatewayOpenAPIDocument(basePath)
	if err != nil {
		return err
	}
	candidateDoc, err := readGatewayOpenAPIDocument(candidatePath)
	if err != nil {
		return err
	}
	importOptions := gateway.OpenAPIRouteOptions{GatewayPrefix: "/", Service: "openapi", Targets: []string{"http://127.0.0.1:1"}}
	baseRoutes, err := gateway.RouteConfigsFromOpenAPI(baseDoc, importOptions)
	if err != nil {
		err = fmt.Errorf("import base openapi aggregation routes: %w", err)
		if format == "sarif" {
			return printGatewayAggregationValidationSARIF(gatewayAggregationValidationReportFromError(err))
		}
		return err
	}
	candidateRoutes, err := gateway.RouteConfigsFromOpenAPI(candidateDoc, importOptions)
	if err != nil {
		err = fmt.Errorf("import candidate openapi aggregation routes: %w", err)
		if format == "sarif" {
			return printGatewayAggregationValidationSARIF(gatewayAggregationValidationReportFromError(err))
		}
		return err
	}
	current := gateway.Config{Routes: baseRoutes}
	candidate, err := gatewayAggregationFromRoutes(candidateRoutes, routeName)
	if err != nil {
		return err
	}
	gw, err := gateway.NewFromConfig(current, nil)
	if err != nil {
		return fmt.Errorf("load base openapi aggregation config: %w", err)
	}
	report := gw.ValidateAggregation(routeName, candidate)
	if valueFromBoolFlag(jsonFlag) || outputMode() == outputJSON || format == outputJSON {
		return printJSONEnvelope("gateway.aggregation.validate", report)
	}
	if format == "markdown" {
		printGatewayAggregationValidationMarkdown(report)
		return nil
	}
	if format == "sarif" {
		return printGatewayAggregationValidationSARIF(report)
	}
	printGatewayAggregationValidationText(report)
	return nil
}

func gatewayAggregationValidationReportFromError(err error) gateway.AggregationValidationReport {
	report := gateway.AggregationValidationReport{OK: false, Compatible: false}
	if err != nil {
		report.Errors = append(report.Errors, err.Error())
	}
	return report
}

func readGatewayOpenAPIDocument(path string) (rest.OpenAPIDocument, error) {
	data, err := readExplicitInputFile(path, "openapi document")
	if err != nil {
		return rest.OpenAPIDocument{}, err
	}
	var doc rest.OpenAPIDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return rest.OpenAPIDocument{}, fmt.Errorf("decode openapi document: %w", err)
	}
	return doc, nil
}

func gatewayAggregationFromRoutes(routes []gateway.RouteConfig, routeName string) (gateway.AggregationConfig, error) {
	routeName = strings.TrimSpace(routeName)
	for _, route := range routes {
		routeID := strings.TrimSpace(route.Method) + " " + strings.TrimSpace(route.PathPrefix)
		if routeName == "" || routeName == route.Name || routeName == routeID {
			if route.Aggregation.Enabled || len(route.Aggregation.Steps) > 0 || len(route.Aggregation.Shape.Mappings) > 0 || strings.TrimSpace(route.Aggregation.Shape.Mode) != "" {
				return route.Aggregation, nil
			}
		}
	}
	return gateway.AggregationConfig{}, fmt.Errorf("openapi aggregation route %q not found", routeName)
}

func readGatewayAggregationConfig(path string) (gateway.Config, error) {
	data, err := readExplicitInputFile(path, "gateway config")
	if err != nil {
		return gateway.Config{}, err
	}
	var config gatewayProfileValidateConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return gateway.Config{}, fmt.Errorf("decode gateway config: %w", err)
	}
	if len(config.Gateway.Routes) > 0 {
		return config.Gateway, nil
	}
	var direct gateway.Config
	if err := json.Unmarshal(data, &direct); err != nil {
		return gateway.Config{}, fmt.Errorf("decode gateway routes: %w", err)
	}
	return direct, nil
}

func readGatewayAggregationCandidate(path string) (gateway.AggregationConfig, error) {
	data, err := readExplicitInputFile(path, "candidate aggregation")
	if err != nil {
		return gateway.AggregationConfig{}, err
	}
	var wrapped gatewayAggregationCandidateConfig
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return gateway.AggregationConfig{}, fmt.Errorf("decode candidate aggregation: %w", err)
	}
	if wrapped.Aggregation.Enabled || len(wrapped.Aggregation.Steps) > 0 || len(wrapped.Aggregation.Shape.Mappings) > 0 || strings.TrimSpace(wrapped.Aggregation.Shape.Mode) != "" {
		return wrapped.Aggregation, nil
	}
	var candidate gateway.AggregationConfig
	if err := json.Unmarshal(data, &candidate); err != nil {
		return gateway.AggregationConfig{}, fmt.Errorf("decode candidate aggregation: %w", err)
	}
	return candidate, nil
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

func printGatewayAggregationValidationText(report gateway.AggregationValidationReport) {
	status := "compatible"
	if !report.OK {
		status = "invalid"
	} else if !report.Compatible {
		status = "breaking"
	}
	cliOutputf("gateway aggregation validation: %s\n", status)
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

func printGatewayAggregationValidationMarkdown(report gateway.AggregationValidationReport) {
	status := "compatible"
	if !report.OK {
		status = "invalid"
	} else if !report.Compatible {
		status = "breaking"
	}
	cliOutputf("# Gateway Aggregation Contract\n\n")
	cliOutputf("- Status: `%s`\n", status)
	cliOutputf("- Compatible: `%t`\n", report.Compatible)
	cliOutputf("- Changes: `%d`\n", len(report.Changes))
	if len(report.Errors) > 0 {
		cliOutput("\n## Errors\n\n")
		for _, errText := range report.Errors {
			cliOutputf("- %s\n", errText)
		}
	}
	if len(report.Changes) == 0 {
		cliOutputln("\nNo aggregation contract changes detected.")
		return
	}
	cliOutput("\n## Changes\n\n")
	cliOutputln("| Severity | Scope | Kind | Source | Target | Message |")
	cliOutputln("| --- | --- | --- | --- | --- | --- |")
	for _, change := range report.Changes {
		cliOutputf(
			"| %s | %s | %s | %s | %s | %s |\n",
			markdownCell(change.Severity),
			markdownCell(change.Scope),
			markdownCell(change.Kind),
			markdownCell(change.Source),
			markdownCell(change.Target),
			markdownCell(change.Message),
		)
	}
}

func markdownCell(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\n", " ")
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

type gatewayAggregationSARIF struct {
	Version string                  `json:"version"`
	Schema  string                  `json:"$schema"`
	Runs    []gatewayAggregationRun `json:"runs"`
}

type gatewayAggregationRun struct {
	Tool    gatewayAggregationTool     `json:"tool"`
	Results []gatewayAggregationResult `json:"results,omitempty"`
}

type gatewayAggregationTool struct {
	Driver gatewayAggregationDriver `json:"driver"`
}

type gatewayAggregationDriver struct {
	Name           string                   `json:"name"`
	InformationURI string                   `json:"informationUri,omitempty"`
	Rules          []gatewayAggregationRule `json:"rules,omitempty"`
}

type gatewayAggregationRule struct {
	ID               string                         `json:"id"`
	Name             string                         `json:"name,omitempty"`
	ShortDescription gatewayAggregationSARIFMessage `json:"shortDescription,omitempty"`
}

type gatewayAggregationResult struct {
	RuleID    string                         `json:"ruleId"`
	Level     string                         `json:"level"`
	Message   gatewayAggregationSARIFMessage `json:"message"`
	Locations []gatewayAggregationLocation   `json:"locations,omitempty"`
}

type gatewayAggregationSARIFMessage struct {
	Text string `json:"text"`
}

type gatewayAggregationLocation struct {
	PhysicalLocation gatewayAggregationPhysicalLocation `json:"physicalLocation"`
}

type gatewayAggregationPhysicalLocation struct {
	ArtifactLocation gatewayAggregationArtifactLocation `json:"artifactLocation"`
}

type gatewayAggregationArtifactLocation struct {
	URI string `json:"uri"`
}

func printGatewayAggregationValidationSARIF(report gateway.AggregationValidationReport) error {
	seenRules := map[string]gatewayAggregationRule{}
	results := make([]gatewayAggregationResult, 0, len(report.Changes)+len(report.Errors))
	for _, errText := range report.Errors {
		ruleID := "aggregation.invalid"
		seenRules[ruleID] = gatewayAggregationRule{ID: ruleID, Name: ruleID, ShortDescription: gatewayAggregationSARIFMessage{Text: "Invalid aggregation contract"}}
		results = append(results, gatewayAggregationResult{
			RuleID:  ruleID,
			Level:   "error",
			Message: gatewayAggregationSARIFMessage{Text: errText},
			Locations: []gatewayAggregationLocation{{
				PhysicalLocation: gatewayAggregationPhysicalLocation{ArtifactLocation: gatewayAggregationArtifactLocation{URI: "gateway-aggregation-contract"}},
			}},
		})
	}
	for _, change := range report.Changes {
		ruleID := "aggregation." + strings.TrimSpace(change.Kind)
		if ruleID == "aggregation." {
			ruleID = "aggregation.change"
		}
		seenRules[ruleID] = gatewayAggregationRule{ID: ruleID, Name: ruleID, ShortDescription: gatewayAggregationSARIFMessage{Text: change.Message}}
		level := "note"
		if change.Severity == "breaking" {
			level = "error"
		} else if change.Severity == "warning" {
			level = "warning"
		}
		results = append(results, gatewayAggregationResult{
			RuleID:  ruleID,
			Level:   level,
			Message: gatewayAggregationSARIFMessage{Text: gatewayAggregationChangeMessage(change)},
			Locations: []gatewayAggregationLocation{{
				PhysicalLocation: gatewayAggregationPhysicalLocation{ArtifactLocation: gatewayAggregationArtifactLocation{URI: "gateway-aggregation-contract"}},
			}},
		})
	}
	rules := make([]gatewayAggregationRule, 0, len(seenRules))
	for _, rule := range seenRules {
		rules = append(rules, rule)
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	payload := gatewayAggregationSARIF{
		Version: "2.1.0",
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Runs: []gatewayAggregationRun{{
			Tool:    gatewayAggregationTool{Driver: gatewayAggregationDriver{Name: "gofly gateway aggregation validate", InformationURI: "https://github.com/imajinyun/gofly", Rules: rules}},
			Results: results,
		}},
	}
	return printJSON(payload)
}

func gatewayAggregationChangeMessage(change gateway.TranscodeProfileChange) string {
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
	return strings.Join(parts, " ")
}
