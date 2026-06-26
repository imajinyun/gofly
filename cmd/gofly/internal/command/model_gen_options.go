package command

import "github.com/imajinyun/gofly/cmd/gofly/internal/generator"

func generateModelFromFlags(flags modelGenFlags, typesMap map[string]string) error {
	return generator.GenerateModelFromDDL(generator.ModelOptions{
		DDLFile:       *flags.DDL,
		Dir:           *flags.Dir,
		Package:       *flags.Package,
		Module:        *flags.Module,
		Tables:        splitCSV(*flags.Table),
		Style:         *flags.Style,
		Database:      *flags.Database,
		IgnoreColumns: splitCSV(*flags.IgnoreColumns),
		Prefix:        *flags.Prefix,
		Strict:        *flags.Strict,
		Cache:         *flags.Cache,
		TypesMap:      typesMap,
	})
}
