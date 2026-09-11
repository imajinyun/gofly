package command

import "github.com/imajinyun/gofly/cmd/gofly/internal/generator"

func generateModelFromFlags(flags modelGenFlags, typesMap map[string]string, typeOverrides map[string]generator.ModelTypeOverride, templateSource modelTemplateSourceFlags) error {
	return generator.GenerateModelFromDDL(generator.ModelOptions{
		DDLFile:        *flags.DDL,
		Dir:            *flags.Dir,
		Package:        *flags.Package,
		Module:         *flags.Module,
		Tables:         splitCSV(*flags.Table),
		Style:          *flags.Style,
		Database:       *flags.Database,
		IgnoreColumns:  splitCSV(*flags.IgnoreColumns),
		Prefix:         *flags.Prefix,
		Strict:         *flags.Strict,
		Cache:          *flags.Cache,
		TypesMap:       typesMap,
		TypeOverrides:  typeOverrides,
		TemplateDir:    *templateSource.Home,
		TemplateRemote: *templateSource.Remote,
		TemplateBranch: *templateSource.Branch,
		TemplateSHA256: *templateSource.SHA256,
	})
}
