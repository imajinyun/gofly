package command

import "path/filepath"

type modelGenJSONOptions struct {
	DDL           string
	Dir           string
	Package       string
	Module        string
	Tables        string
	Database      string
	IgnoreColumns string
	Prefix        string
	Style         string
	Strict        bool
	Cache         bool
}

func modelGenJSONOptionsFromFlags(flags modelGenFlags) modelGenJSONOptions {
	return modelGenJSONOptions{
		DDL:           *flags.DDL,
		Dir:           *flags.Dir,
		Package:       *flags.Package,
		Module:        *flags.Module,
		Tables:        *flags.Table,
		Database:      *flags.Database,
		IgnoreColumns: *flags.IgnoreColumns,
		Prefix:        *flags.Prefix,
		Style:         *flags.Style,
		Strict:        *flags.Strict,
		Cache:         *flags.Cache,
	}
}

func printModelGenJSON(opts modelGenJSONOptions) error {
	inputs := map[string]string{
		"ddl":   opts.DDL,
		"dir":   opts.Dir,
		"style": opts.Style,
	}
	if opts.Package != "" {
		inputs["package"] = opts.Package
	}
	if opts.Module != "" {
		inputs["module"] = opts.Module
	}
	if opts.Tables != "" {
		inputs["tables"] = opts.Tables
	}
	if opts.Database != "" {
		inputs["database"] = opts.Database
	}
	if opts.IgnoreColumns != "" {
		inputs["ignoreColumns"] = opts.IgnoreColumns
	}
	if opts.Prefix != "" {
		inputs["prefix"] = opts.Prefix
	}
	if opts.Strict {
		inputs["strict"] = "true"
	}
	if opts.Cache {
		inputs["cache"] = "true"
	}
	modelDir := filepath.Join(opts.Dir, "model")
	files := generatedModelFiles(modelDir)
	return printJSONEnvelope("model.gen", cliPlan{
		Command:           "model gen",
		DryRun:            false,
		MutatesFilesystem: true,
		Inputs:            inputs,
		Actions: []cliPlanAction{
			{
				Operation:   "write-model-files",
				Target:      modelDir,
				Description: "generate model entity and repository files",
				RiskLevel:   "medium",
			},
		},
		GeneratedFiles: len(files),
		NextActions:    []string{"review generated model files", "go test ./..."},
	})
}
