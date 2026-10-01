package command

func migrationManifestCommands() []aiToolCommand {
	var commands []aiToolCommand
	for _, action := range []string{"create", "gen", "validate", "up", "down", "status"} {
		properties := map[string]aiInputProperty{
			"dir":  stringProperty("Migration directory; must be writable for the transient snapshot lock."),
			"json": boolProperty("Print a JSON envelope."),
		}
		usage := "gofly migrate " + action + " [flags]"
		risk := "medium"
		effects := []string{"creates and removes a transient directory lock"}
		var aliases []string
		switch action {
		case "create", "gen":
			properties["name"] = stringProperty("Migration name.")
			properties["version"] = stringProperty("Explicit positive version; defaults to a UTC timestamp.")
			effects = append(effects, "writes SQL file pairs without overwriting existing versions")
			usage = "gofly migrate " + action + " <name> [flags]"
			if action == "gen" {
				properties["fromDdl"] = stringProperty("Reviewed initial CREATE TABLE SQL file (--from-ddl).")
				properties["dialect"] = enumStringProperty("SQL dialect.", "mysql", "postgres")
			} else {
				aliases = []string{"migration new", "migrate new", "migration create"}
			}
		case "validate":
			risk = "low"
		default:
			properties["driver"] = enumStringProperty("Database driver.", "mysql", "postgres")
			properties["dsnEnv"] = stringProperty("Environment variable containing the DSN; the secret value is never an input property.")
			properties["timeout"] = stringProperty("Positive timeout up to one hour; default 30s.")
			effects = append(effects, "may initialize database migration metadata")
			if action != "status" {
				risk = "high"
				properties["steps"] = intProperty("Positive migration count; required for down, up defaults to all pending.")
				effects = append(effects, "executes SQL and updates database migration history")
			}
		}
		commands = append(commands, manifestCommand("migrate "+action, aliases,
			"Versioned SQL migration "+action+"; inspect gofly migrate "+action+" --help for supported SQL and recovery boundaries.",
			usage, properties, []string{outputText, outputJSON}, effects, risk, false, true, nil))
	}
	return commands
}
