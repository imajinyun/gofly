package command

import "github.com/imajinyun/gofly/cmd/gofly/internal/generator"

func modelCommand(args []string) error {
	if printCommandHelp("model", args) {
		return nil
	}
	return modelCommands.dispatch(args, "gofly model gen|mysql|pg|mongo")
}

func modelGenCommand(args []string) error {
	leadingDDL, args := splitLeadingName(args)
	fs, flags := newModelGenFlagSet()
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	remaining = flags.normalize(leadingDDL, remaining)
	typesMap, err := modelTypesMapFromConfig(*flags.ConfigPath, *flags.Dir)
	if err != nil {
		return err
	}
	fillNameFromArgs(flags.DDL, remaining)
	if err := generator.GenerateModelFromDDL(generator.ModelOptions{
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
	}); err != nil {
		return err
	}
	if *flags.JSON || outputMode() == outputJSON {
		return printModelGenJSON(modelGenJSONOptions{
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
		})
	}
	printModelGenerated(*flags.Dir)
	return nil
}
