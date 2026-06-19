package generator

import (
	"errors"
	"path/filepath"
	"strings"
)

type serviceScaffoldIR struct {
	Name    string
	Module  string
	Dir     string
	Style   string
	Kind    string
	Data    map[string]string
	Files   map[string]string
	Plugins []string
}

func buildServiceScaffoldIR(opts ServiceScaffoldOptions) (serviceScaffoldIR, error) {
	if opts.Name == "" {
		return serviceScaffoldIR{}, errors.New("name is required")
	}
	if opts.Module == "" {
		return serviceScaffoldIR{}, errors.New("module is required")
	}
	if opts.Dir == "" {
		opts.Dir = filepath.Join(".", opts.Name)
	}
	style, err := normalizeServiceStyle(opts.Style)
	if err != nil {
		return serviceScaffoldIR{}, err
	}
	if err := ValidateFeatureNames(opts.Features); err != nil {
		return serviceScaffoldIR{}, err
	}

	data := serviceScaffoldData(opts)
	files := serviceFiles(style, opts.Name)
	mergeServiceScaffoldExtras(files, opts)

	files, err = applyServiceTemplateSource(files, opts)
	if err != nil {
		return serviceScaffoldIR{}, err
	}

	if len(opts.Features) > 0 {
		scope := ExtensionScope{
			Name:   opts.Name,
			Module: opts.Module,
			Style:  style,
			Dir:    opts.Dir,
			Data:   data,
		}
		files, data, err = ApplyFeatureNames(opts.Features, scope, files, data)
		if err != nil {
			return serviceScaffoldIR{}, err
		}
	}

	return serviceScaffoldIR{
		Name:    opts.Name,
		Module:  opts.Module,
		Dir:     opts.Dir,
		Style:   style,
		Kind:    opts.Kind,
		Data:    data,
		Files:   files,
		Plugins: normalizedServicePlugins(opts.Plugins),
	}, nil
}

func serviceScaffoldData(opts ServiceScaffoldOptions) map[string]string {
	return map[string]string{
		"Name":         opts.Name,
		"Module":       opts.Module,
		"ReplaceBlock": frameworkReplaceBlock(opts.FrameworkPath),
		"GoFile":       "./cmd/" + opts.Name,
		"Exe":          opts.Name,
		"GoVersion":    "1.26",
		"BaseImage":    "gcr.io/distroless/static-debian12",
	}
}

func mergeServiceScaffoldExtras(files map[string]string, opts ServiceScaffoldOptions) {
	if strings.EqualFold(opts.Kind, "api") && !opts.SkipAPISpec {
		files[opts.Name+".api"] = apiNewTemplate
	}
	if strings.EqualFold(opts.Kind, "rpc") {
		files[opts.Name+".proto"] = rpcNewTemplate
	}
	for path, content := range opts.ExtraFiles {
		files[path] = content
	}
}

func applyServiceTemplateSource(files map[string]string, opts ServiceScaffoldOptions) (map[string]string, error) {
	templateDir := opts.TemplateDir
	if opts.TemplateRemote != "" && templateDir == "" {
		templateDir = filepath.Join(opts.Dir, ".gofly", "templates")
	}
	if templateDir != "" || opts.TemplateRemote != "" {
		var err error
		templateDir, err = ResolveTemplateSource(
			templateDir,
			opts.TemplateRemote,
			opts.TemplateBranch,
			opts.StrictTemplateRemote,
		)
		if err != nil {
			return nil, err
		}
	}
	if templateDir == "" {
		return files, nil
	}
	return ApplyTemplateExtension(templateDir, files)
}

func normalizedServicePlugins(plugins []string) []string {
	if len(plugins) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(plugins))
	for _, plugin := range plugins {
		plugin = strings.TrimSpace(plugin)
		if plugin == "" {
			continue
		}
		normalized = append(normalized, plugin)
	}
	return normalized
}
