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
		return printModelGenJSON(modelGenJSONOptionsFromFlags(flags))
	}
	printModelGenerated(*flags.Dir)
	return nil
}
