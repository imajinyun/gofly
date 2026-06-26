package command

import (
	"fmt"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

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
