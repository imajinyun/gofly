package command

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
	if err := generateModelFromFlags(flags, typesMap); err != nil {
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
