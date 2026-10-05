package command

func middlewareManifestCommand() aiToolCommand {
	command := manifestCommand("api middleware", nil,
		"Generate named middleware skeletons or install maintained presets. List and preset dry-run are read-only; preset installation requires a gofly module and explicit runtime registration.",
		"gofly api middleware --list | --preset <names|all> --dir <project> [--dry-run] [--json]",
		map[string]aiInputProperty{
			"preset": stringProperty("Comma-separated maintained preset names, or all; exclusive with name/api. Use --list to discover the catalog."),
			"list":   boolProperty("List the embedded catalog without filesystem changes."),
			"dryRun": boolProperty("Plan preset files without writing; requires preset."),
			"dir":    stringProperty("Existing target Go module with a declared gofly dependency."),
			"json":   boolProperty("Emit preset/catalog results and command failures as one JSON envelope."),
			"name":   stringProperty("Legacy comma-separated skeleton names; exclusive with preset."),
			"api":    stringProperty("API file whose middleware declarations generate legacy skeletons."),
		}, []string{outputText, outputJSON},
		[]string{"writes source files under internal/middleware", "does not edit routes, secrets, configuration or module dependencies", "list and preset dry-run write no files"},
		"medium", true, true,
		[]string{"gofly api middleware --list --json", "gofly api middleware --preset auth,cors --dir . --dry-run --json"})
	command.OutputContract = &aiOutputContract{
		Mode:     "single JSON envelope for catalog/preset --json and errors; legacy skeleton success retains its existing output",
		Envelope: []string{"ok", "command", "version", "data", "error"},
		Semantics: map[string]string{
			"catalog": "api.middleware.list / gofly.middleware_presets.v1",
			"preset":  "api.middleware.preset / gofly.middleware_preset_result.v1",
			"files":   "actual relative file paths with planned, written or unchanged status",
			"errors":  "api.middleware; invalid usage exits 2 and operational failures exit 1",
		},
	}
	return command
}
