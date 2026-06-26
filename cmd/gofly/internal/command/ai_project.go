package command

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

type aiProjectPlan struct {
	Prompt            string                    `json:"prompt"`
	ProjectType       string                    `json:"projectType"`
	Template          generator.ProjectTemplate `json:"template"`
	Features          []string                  `json:"features"`
	Command           string                    `json:"command"`
	RiskLevel         string                    `json:"riskLevel"`
	MutatesFilesystem bool                      `json:"mutatesFilesystem"`
	DryRun            bool                      `json:"dryRun"`
	Verify            []string                  `json:"verify"`
	Warnings          []string                  `json:"warnings,omitempty"`
	NextActions       []string                  `json:"nextActions"`
}

type aiProjectApplyResult struct {
	Plan              aiProjectPlan                    `json:"plan"`
	Applied           bool                             `json:"applied"`
	OutputDir         string                           `json:"outputDir"`
	ExecutedCommand   string                           `json:"executedCommand"`
	GeneratedFeatures []generator.ProjectFeatureResult `json:"generatedFeatures,omitempty"`
	Dependencies      []string                         `json:"dependencies,omitempty"`
	ConfigHints       []generator.ConfigHint           `json:"configHints,omitempty"`
	FeatureVerify     []string                         `json:"featureVerify,omitempty"`
	Verify            []string                         `json:"verify"`
	VerifyRan         bool                             `json:"verifyRan"`
	VerifyPassed      bool                             `json:"verifyPassed"`
	Verification      []aiProjectVerificationResult    `json:"verification,omitempty"`
	Warnings          []string                         `json:"warnings,omitempty"`
	NextActions       []string                         `json:"nextActions"`
	MutatesFilesystem bool                             `json:"mutatesFilesystem"`
}

type aiProjectVerificationResult struct {
	Command string `json:"command"`
	Status  string `json:"status"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
}

type aiProjectApplyOptions struct {
	Verify        bool
	VerifyTimeout time.Duration
}

func aiPlanCommand(args []string) error {
	fs := flag.NewFlagSet("ai plan", flag.ContinueOnError)
	prompt := fs.String("prompt", "", "natural language project requirement")
	kind := fs.String("kind", "", "optional project kind hint, such as service, rpc, worker, cli, ai-agent, rag or gateway")
	name := fs.String("name", "", "project or service name used in the generated command")
	module := fs.String("module", "", "Go module path used in the generated command")
	dir := fs.String("dir", "", "output directory used in the generated command")
	formatName := fs.String("format", outputText, "output format: text or json")
	jsonOutput := fs.Bool("json", false, "output JSON envelope")
	dryRun := fs.Bool("dry-run", true, "plan only without writing files")
	plan := fs.Bool("plan", true, "alias for --dry-run")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *prompt == "" && len(remaining) > 0 {
		*prompt = strings.Join(remaining, " ")
	} else if len(remaining) > 0 {
		return fmt.Errorf("%w: ai plan accepts either --prompt or positional prompt text, not both", errUsage)
	}
	if strings.TrimSpace(*prompt) == "" {
		return fmt.Errorf("%w: --prompt or positional prompt text is required for `gofly ai plan`", errUsage)
	}
	format := strings.ToLower(strings.TrimSpace(*formatName))
	if format == "" {
		format = outputText
	}
	if format != outputText && format != outputJSON {
		return fmt.Errorf("%w: unsupported --format %q", errUsage, *formatName)
	}
	projectPlan := buildAIProjectPlan(*prompt, *kind, *name, *module, *dir, *dryRun || *plan)
	if *jsonOutput || outputMode() == outputJSON || format == outputJSON {
		return printJSONEnvelope("ai.plan", projectPlan)
	}
	cliOutputfIf("template=%s kind=%s risk=%s\n", projectPlan.Template.ID, projectPlan.ProjectType, projectPlan.RiskLevel)
	cliOutputfIf("features=%s\n", strings.Join(projectPlan.Features, ","))
	cliOutputfIf("command=%s\n", projectPlan.Command)
	for _, warning := range projectPlan.Warnings {
		cliOutputfIf("warning: %s\n", warning)
	}
	return nil
}

func aiNewCommand(args []string) error {
	fs := flag.NewFlagSet("ai new", flag.ContinueOnError)
	prompt := fs.String("prompt", "", "natural language project requirement")
	kind := fs.String("kind", "", "optional project kind hint, such as service, rpc, worker, cli, ai-agent, rag or gateway")
	templateID := fs.String("template", "", "explicit project template id; run `gofly template list --json` to inspect choices")
	name := fs.String("name", "", "project or service name")
	module := fs.String("module", "", "Go module path")
	dir := fs.String("dir", "", "output directory")
	formatName := fs.String("format", outputText, "output format: text or json")
	jsonOutput := fs.Bool("json", false, "output JSON envelope")
	dryRun := fs.Bool("dry-run", true, "print the scaffold plan without writing files")
	plan := fs.Bool("plan", false, "alias for --dry-run")
	apply := fs.Bool("apply", false, "apply the planned scaffold and write files")
	verify := fs.Bool("verify", false, "run supported post-generation verification commands after --apply")
	verifyTimeoutText := fs.String("verify-timeout", "2m", "timeout for each verification command")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *prompt == "" && len(remaining) > 0 {
		*prompt = strings.Join(remaining, " ")
	} else if len(remaining) > 0 {
		return fmt.Errorf("%w: ai new accepts either --prompt or positional prompt text, not both", errUsage)
	}
	if strings.TrimSpace(*prompt) == "" && strings.TrimSpace(*templateID) == "" {
		return fmt.Errorf("%w: --prompt, positional prompt text, or --template is required for `gofly ai new`", errUsage)
	}
	format := strings.ToLower(strings.TrimSpace(*formatName))
	if format == "" {
		format = outputText
	}
	if format != outputText && format != outputJSON {
		return fmt.Errorf("%w: unsupported --format %q", errUsage, *formatName)
	}
	if *apply && (*dryRun || *plan) && !flagWasProvided(fs, "dry-run") && !flagWasProvided(fs, "plan") {
		*dryRun = false
	}
	if *apply && (*dryRun || *plan) {
		return fmt.Errorf("%w: --apply cannot be combined with --dry-run or --plan", errUsage)
	}
	verifyTimeout, err := time.ParseDuration(strings.TrimSpace(*verifyTimeoutText))
	if err != nil || verifyTimeout <= 0 {
		return fmt.Errorf("%w: --verify-timeout must be a positive duration", errUsage)
	}
	projectPlan, err := buildAIProjectNewPlan(*prompt, *kind, *templateID, *name, *module, *dir, !*apply || *dryRun || *plan)
	if err != nil {
		return err
	}
	if !*apply {
		if *jsonOutput || outputMode() == outputJSON || format == outputJSON {
			return printJSONEnvelope("ai.new", projectPlan)
		}
		printAIProjectPlanText(projectPlan)
		return nil
	}
	result, err := applyAIProjectPlan(projectPlan, aiProjectApplyOptions{Verify: *verify, VerifyTimeout: verifyTimeout})
	if err != nil {
		return err
	}
	if *jsonOutput || outputMode() == outputJSON || format == outputJSON {
		return printJSONEnvelope("ai.new", result)
	}
	cliOutputfIf("applied template=%s kind=%s output=%s\n", result.Plan.Template.ID, result.Plan.ProjectType, result.OutputDir)
	cliOutputfIf("command=%s\n", result.ExecutedCommand)
	if len(result.GeneratedFeatures) > 0 {
		for _, feature := range result.GeneratedFeatures {
			cliOutputfIf("feature=%s files=%s\n", feature.Plugin, strings.Join(feature.Files, ","))
			if len(feature.Dependencies) > 0 {
				cliOutputfIf("  dependencies=%s\n", strings.Join(feature.Dependencies, ","))
			}
			for _, hint := range feature.ConfigHints {
				cliOutputfIf("  configHint=%s description=%q example=%q\n", hint.Key, hint.Description, hint.Example)
			}
			if len(feature.VerifyCommands) > 0 {
				cliOutputfIf("  verify=%s\n", strings.Join(feature.VerifyCommands, ","))
			}
		}
	}
	if result.VerifyRan {
		cliOutputfIf("verify=%t\n", result.VerifyPassed)
		for _, check := range result.Verification {
			cliOutputfIf("  - %s: %s\n", check.Command, check.Status)
		}
	}
	for _, warning := range result.Warnings {
		cliOutputfIf("warning: %s\n", warning)
	}
	for _, next := range result.NextActions {
		cliOutputfIf("next: %s\n", next)
	}
	return nil
}

func buildAIProjectPlan(prompt, kind, name, module, dir string, dryRun bool) aiProjectPlan {
	tmpl := generator.RecommendProjectTemplate(prompt, kind)
	return buildAIProjectPlanFromTemplate(prompt, tmpl, name, module, dir, dryRun)
}

func buildAIProjectNewPlan(prompt, kind, templateID, name, module, dir string, dryRun bool) (aiProjectPlan, error) {
	var tmpl generator.ProjectTemplate
	if strings.TrimSpace(templateID) != "" {
		var ok bool
		tmpl, ok = generator.GetProjectTemplate(templateID)
		if !ok {
			return aiProjectPlan{}, fmt.Errorf("%w: unknown project template %q", errUsage, templateID)
		}
	} else {
		tmpl = generator.RecommendProjectTemplate(prompt, kind)
	}
	if err := validateAIProjectTemplateCommand(tmpl); err != nil {
		return aiProjectPlan{}, err
	}
	projectPlan := buildAIProjectPlanFromTemplate(prompt, tmpl, name, module, dir, dryRun)
	if err := validateAIProjectApplyInputs(projectPlan); err != nil {
		return aiProjectPlan{}, err
	}
	return projectPlan, nil
}

func buildAIProjectPlanFromTemplate(prompt string, tmpl generator.ProjectTemplate, name, module, dir string, dryRun bool) aiProjectPlan {
	command := materializeTemplateCommand(tmpl.Command, name, module, dir)
	warnings := []string{
		"ai plan uses deterministic local template matching and does not call an external LLM provider",
		"rerun the proposed command with --dry-run first before applying filesystem mutations",
	}
	return aiProjectPlan{
		Prompt:            strings.TrimSpace(prompt),
		ProjectType:       tmpl.Kind,
		Template:          tmpl,
		Features:          append([]string(nil), tmpl.Features...),
		Command:           command,
		RiskLevel:         tmpl.RiskLevel,
		MutatesFilesystem: !dryRun,
		DryRun:            dryRun,
		Verify:            append([]string(nil), tmpl.Verify...),
		Warnings:          warnings,
		NextActions:       []string{"inspect the selected template with `gofly template inspect " + tmpl.ID + " --json`", "run the proposed scaffold command with --dry-run", "run generated project verification commands after applying the scaffold"},
	}
}

func validateAIProjectTemplateCommand(tmpl generator.ProjectTemplate) error {
	fields := strings.Fields(tmpl.Command)
	if len(fields) < 3 || fields[0] != "gofly" {
		return fmt.Errorf("%w: template %q has unsupported command %q", errUsage, tmpl.ID, tmpl.Command)
	}
	for _, field := range fields {
		if containsShellMetachar(field) {
			return fmt.Errorf("%w: template %q command %q contains unsupported shell metacharacter", errUsage, tmpl.ID, tmpl.Command)
		}
	}
	switch strings.Join(fields[:3], " ") {
	case "gofly new service", "gofly new api", "gofly new rpc", "gofly gen gateway":
		return nil
	default:
		return fmt.Errorf("%w: template %q command %q is not supported by `gofly ai new`", errUsage, tmpl.ID, tmpl.Command)
	}
}

func containsShellMetachar(value string) bool {
	return strings.ContainsAny(value, ";&|$`")
}

func validateAIProjectApplyInputs(plan aiProjectPlan) error {
	if strings.TrimSpace(plan.Template.ID) == "" {
		return fmt.Errorf("%w: project template is required", errUsage)
	}
	name, module, dir := aiProjectPlanValues(plan)
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: name is required", errUsage)
	}
	if strings.TrimSpace(module) == "" {
		return fmt.Errorf("%w: module is required", errUsage)
	}
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("%w: dir is required", errUsage)
	}
	if containsParentTraversalPath(dir) {
		return fmt.Errorf("%w: project directory must not contain parent traversal", errUsage)
	}
	return nil
}

func containsParentTraversalPath(path string) bool {
	for _, part := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return true
		}
	}
	return false
}

func applyAIProjectPlan(plan aiProjectPlan, opts aiProjectApplyOptions) (aiProjectApplyResult, error) {
	if err := validateAIProjectApplyInputs(plan); err != nil {
		return aiProjectApplyResult{}, err
	}
	name, module, dir := aiProjectPlanValues(plan)
	commandArgs, err := aiProjectApplyArgs(plan)
	if err != nil {
		return aiProjectApplyResult{}, err
	}
	if len(commandArgs) == 0 {
		return aiProjectApplyResult{}, fmt.Errorf("%w: no scaffold command generated", errUsage)
	}
	if err := withCommandIO(IOStreams{In: nil, Out: io.Discard, Err: currentErr()}, outputText, verbosityQuiet, func() error {
		return runAIProjectApplyCommand(commandArgs)
	}); err != nil {
		return aiProjectApplyResult{}, err
	}
	generatedFeatures, err := generator.ApplyProjectFeaturePlugins(generator.ProjectFeatureOptions{
		Dir:      dir,
		Name:     name,
		Module:   module,
		Features: plan.Features,
	})
	if err != nil {
		return aiProjectApplyResult{}, err
	}
	featureDependencies, featureConfigHints, featureVerify := aggregateProjectFeatureContract(generatedFeatures)
	verifyCommands := appendUniqueStrings(append([]string(nil), plan.Verify...), featureVerify...)
	warnings := append([]string(nil), plan.Warnings...)
	warnings = append(warnings, "ai new --apply writes scaffold files using built-in local generators only")
	verification := []aiProjectVerificationResult(nil)
	verifyPassed := false
	if opts.Verify {
		var err error
		verification, verifyPassed, err = runAIProjectVerification(dir, verifyCommands, opts.VerifyTimeout)
		if err != nil {
			return aiProjectApplyResult{}, err
		}
		controlPlaneResult := runAIProjectControlPlaneSnapshotAssertion(dir, opts.VerifyTimeout)
		if controlPlaneResult.Status != "skipped" {
			if controlPlaneResult.Status == "failed" {
				verifyPassed = false
			}
			verification = append(verification, controlPlaneResult)
		}
	} else {
		warnings = append(warnings, "generated verification commands are reported but not executed; pass --verify to run supported checks")
	}
	return aiProjectApplyResult{
		Plan:              plan,
		Applied:           true,
		OutputDir:         dir,
		ExecutedCommand:   "gofly " + strings.Join(commandArgs, " "),
		GeneratedFeatures: generatedFeatures,
		Dependencies:      featureDependencies,
		ConfigHints:       featureConfigHints,
		FeatureVerify:     featureVerify,
		Verify:            verifyCommands,
		VerifyRan:         opts.Verify,
		VerifyPassed:      verifyPassed,
		Verification:      verification,
		Warnings:          warnings,
		NextActions: aiProjectApplyNextActions(
			dir,
			verifyCommands,
			featureDependencies,
			featureConfigHints,
			opts.Verify,
			verifyPassed,
		),
		MutatesFilesystem: true,
	}, nil
}

func aggregateProjectFeatureContract(features []generator.ProjectFeatureResult) ([]string, []generator.ConfigHint, []string) {
	dependencies := []string{}
	configHints := []generator.ConfigHint{}
	verifyCommands := []string{}
	seenConfigHints := map[string]struct{}{}
	for _, feature := range features {
		dependencies = appendUniqueStrings(dependencies, feature.Dependencies...)
		verifyCommands = appendUniqueStrings(verifyCommands, feature.VerifyCommands...)
		for _, hint := range feature.ConfigHints {
			key := strings.ToLower(strings.TrimSpace(hint.Key))
			if key == "" {
				continue
			}
			if _, ok := seenConfigHints[key]; ok {
				continue
			}
			seenConfigHints[key] = struct{}{}
			configHints = append(configHints, hint)
		}
	}
	return dependencies, configHints, verifyCommands
}

func appendUniqueStrings(values []string, more ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(more))
	unique := make([]string, 0, len(values)+len(more))
	for _, value := range append(values, more...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

func aiProjectApplyNextActions(
	dir string,
	verify []string,
	dependencies []string,
	configHints []generator.ConfigHint,
	verifyRan bool,
	verifyPassed bool,
) []string {
	next := []string{"cd " + dir}
	if len(dependencies) > 0 {
		next = append(next, "review feature dependencies: go get "+strings.Join(dependencies, " "))
	}
	for _, hint := range configHints {
		action := "configure " + hint.Key + ": " + hint.Description
		if hint.Example != "" {
			action += " (example: " + hint.Example + ")"
		}
		next = append(next, action)
	}
	if len(verify) == 0 {
		return next
	}
	if !verifyRan {
		return append(next, "run: "+strings.Join(verify, " && "))
	}
	if verifyPassed {
		return append(next, "review generated files and commit when ready")
	}
	return append(next, "fix failed verification output, then rerun: "+strings.Join(verify, " && "))
}

func runAIProjectVerification(dir string, verify []string, timeout time.Duration) ([]aiProjectVerificationResult, bool, error) {
	if timeout <= 0 {
		return nil, false, fmt.Errorf("%w: verification timeout must be positive", errUsage)
	}
	results := make([]aiProjectVerificationResult, 0, len(verify))
	passed := true
	for _, command := range verify {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		result := runAIProjectVerificationCommand(dir, command, timeout)
		if result.Status == "failed" {
			passed = false
		}
		results = append(results, result)
	}
	return results, passed, nil
}

func runAIProjectControlPlaneSnapshotAssertion(dir string, timeout time.Duration) aiProjectVerificationResult {
	const command = "control-plane snapshot"
	if timeout <= 0 {
		return aiProjectVerificationResult{Command: command, Status: "failed", Error: "verification timeout must be positive"}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return aiProjectVerificationResult{Command: command, Status: "failed", Error: err.Error()}
	}
	defer func() { _ = root.Close() }()
	testFile, err := root.Open(filepath.Join("internal", "config", "config_test.go"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return aiProjectVerificationResult{Command: command, Status: "skipped", Error: "generated project does not expose a control-plane snapshot contract test"}
		}
		return aiProjectVerificationResult{Command: command, Status: "failed", Error: err.Error()}
	}
	data, err := io.ReadAll(testFile)
	_ = testFile.Close()
	if err != nil {
		return aiProjectVerificationResult{Command: command, Status: "failed", Error: err.Error()}
	}
	if !strings.Contains(string(data), "TestControlPlaneSnapshotExposesGeneratedContract") {
		return aiProjectVerificationResult{Command: command, Status: "skipped", Error: "generated project does not expose a control-plane snapshot contract test"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "./internal/config", "-run", "TestControlPlaneSnapshotExposesGeneratedContract", "-count=1")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	result := aiProjectVerificationResult{Command: command, Status: "passed", Output: truncateVerificationOutput(string(out))}
	if ctx.Err() == context.DeadlineExceeded {
		result.Status = "failed"
		result.Error = "control-plane snapshot assertion timed out"
		return result
	}
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
	}
	return result
}

func runAIProjectVerificationCommand(dir, command string, timeout time.Duration) aiProjectVerificationResult {
	name, args, ok := aiProjectVerificationCommandArgs(command)
	if !ok {
		return aiProjectVerificationResult{Command: command, Status: "skipped", Error: "unsupported verification command"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// #nosec G204 -- verification commands are selected from aiProjectVerificationCommandArgs allow-list and never executed through a shell.
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if command == "gofly ai doctor --json" {
		if frameworkPath := strings.TrimSpace(os.Getenv("GOFLY_FRAMEWORK_PATH")); frameworkPath != "" {
			cmd.Dir = frameworkPath
		}
	}
	out, err := cmd.CombinedOutput()
	result := aiProjectVerificationResult{Command: command, Status: "passed", Output: truncateVerificationOutput(string(out))}
	if ctx.Err() == context.DeadlineExceeded {
		result.Status = "failed"
		result.Error = "verification command timed out"
		return result
	}
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
	}
	return result
}

func aiProjectVerificationCommandArgs(command string) (string, []string, bool) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "", nil, false
	}
	switch strings.Join(fields, " ") {
	case "gofmt":
		return "go", []string{"fmt", "./..."}, true
	case "go test ./...":
		return "go", []string{"test", "./..."}, true
	case "go mod tidy":
		return "go", []string{"mod", "tidy"}, true
	case "go vet ./...":
		return "go", []string{"vet", "./..."}, true
	case "gofly ai doctor --json":
		if frameworkPath := strings.TrimSpace(os.Getenv("GOFLY_FRAMEWORK_PATH")); frameworkPath != "" {
			return "go", []string{"run", "./cmd/gofly", "ai", "doctor", "--json"}, true
		}
		return "gofly", []string{"ai", "doctor", "--json"}, true
	default:
		return "", nil, false
	}
}

func truncateVerificationOutput(output string) string {
	const maxVerificationOutputBytes = 4096
	output = strings.TrimSpace(output)
	if len(output) <= maxVerificationOutputBytes {
		return output
	}
	return output[:maxVerificationOutputBytes] + "\n... truncated ..."
}

func runAIProjectApplyCommand(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("%w: scaffold command is incomplete", errUsage)
	}
	switch {
	case args[0] == "new" && args[1] == "service":
		return serviceNewCommand(args[2:])
	case args[0] == "new" && args[1] == "api":
		return apiNewCommand(args[2:])
	case args[0] == "new" && args[1] == "rpc":
		return rpcNewCommand(args[2:])
	case args[0] == "gen" && args[1] == "gateway":
		return gatewayGenCommand(args[2:])
	default:
		return fmt.Errorf("%w: unsupported scaffold command `gofly %s`", errUsage, strings.Join(args, " "))
	}
}

func aiProjectPlanValues(plan aiProjectPlan) (name, module, dir string) {
	fields := strings.Fields(plan.Command)
	if len(fields) > 3 && fields[0] == "gofly" && !strings.HasPrefix(fields[3], "-") {
		name = fields[3]
	}
	if v := templateInputValue(plan.Command, "--name"); v != "" {
		name = v
	}
	return name, templateInputValue(plan.Command, "--module"), templateInputValue(plan.Command, "--dir")
}

func aiProjectApplyArgs(plan aiProjectPlan) ([]string, error) {
	name, module, dir := aiProjectPlanValues(plan)
	fields := strings.Fields(plan.Template.Command)
	if len(fields) < 3 || fields[0] != "gofly" {
		return nil, fmt.Errorf("%w: unsupported scaffold command %q", errUsage, plan.Template.Command)
	}
	args := make([]string, 0, len(fields)-1)
	for _, field := range fields[1:] {
		switch field {
		case "<name>":
			args = append(args, name)
		case "<module>":
			args = append(args, module)
		case "<dir>":
			args = append(args, dir)
		default:
			args = append(args, field)
		}
	}
	args = stripCommandFlags(args, "--dry-run", "--plan", "--json")
	return args, nil
}

func stripCommandFlags(args []string, names ...string) []string {
	remove := make(map[string]struct{}, len(names))
	for _, name := range names {
		remove[name] = struct{}{}
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, hasInlineValue := splitFlagName(arg)
		if _, ok := remove[name]; ok {
			if !hasInlineValue && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
			}
			continue
		}
		out = append(out, arg)
	}
	return out
}

func splitFlagName(arg string) (string, bool) {
	if !strings.HasPrefix(arg, "-") {
		return arg, false
	}
	name, _, ok := strings.Cut(arg, "=")
	if ok {
		return name, true
	}
	return arg, false
}

func templateInputValue(command, flagName string) string {
	fields := strings.Fields(command)
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if field == flagName && i+1 < len(fields) {
			return fields[i+1]
		}
		prefix := flagName + "="
		if strings.HasPrefix(field, prefix) {
			return strings.TrimPrefix(field, prefix)
		}
	}
	return ""
}

func flagWasProvided(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func printAIProjectPlanText(projectPlan aiProjectPlan) {
	cliOutputfIf("template=%s kind=%s risk=%s\n", projectPlan.Template.ID, projectPlan.ProjectType, projectPlan.RiskLevel)
	cliOutputfIf("features=%s\n", strings.Join(projectPlan.Features, ","))
	cliOutputfIf("command=%s\n", projectPlan.Command)
	for _, warning := range projectPlan.Warnings {
		cliOutputfIf("warning: %s\n", warning)
	}
}

func materializeTemplateCommand(command, name, module, dir string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "demo"
	}
	module = strings.TrimSpace(module)
	if module == "" {
		module = "example.com/" + name
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = name
	}
	replacer := strings.NewReplacer("<name>", name, "<module>", module, "<dir>", dir)
	return replacer.Replace(command)
}
