package command

import (
	"flag"
	"fmt"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func modelMySQLDatasourceCommand(args []string) error {
	leadingURL, args := splitLeadingName(args)
	fs := flag.NewFlagSet("model mysql datasource", flag.ContinueOnError)
	flags := registerModelDatasourceFlags(fs, false)
	registerGoctlModelTemplateFlags(fs)
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	flags.normalize(leadingURL)
	typesMap, err := modelTypesMapFromConfig(*flags.ConfigPath, *flags.Dir)
	if err != nil {
		return err
	}
	fillNameFromArgs(flags.URL, remaining)
	if *flags.URL == "" {
		return fmt.Errorf("%w: datasource url is required", errUsage)
	}
	return runModelDatasource(generator.ModelDatasourceOptions{
		Driver:        "mysql",
		DSN:           *flags.URL,
		Dir:           *flags.Dir,
		Package:       *flags.Package,
		Module:        *flags.Module,
		Tables:        splitCSV(*flags.Table),
		Style:         *flags.Style,
		Database:      *flags.Database,
		Schema:        *flags.Schema,
		IgnoreColumns: splitCSV(*flags.IgnoreColumns),
		Prefix:        *flags.Prefix,
		Strict:        *flags.Strict,
		Cache:         *flags.Cache,
		TypesMap:      typesMap,
	})
}
