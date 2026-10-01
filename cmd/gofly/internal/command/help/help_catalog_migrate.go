package help

func migrateTopic(command string) Topic {
	common := []string{"--dir <dir>          migration directory (default migrations)", "--json               output JSON envelope"}
	switch command {
	case "migrate", "migration":
		return Topic{Name: "migrate", Short: "Create SQL migration files and manage versioned database migrations.", Usage: "gofly migrate create|gen|validate|up|down|status [flags]", Commands: []Command{{Name: "create <name>", Short: "create empty up/down SQL files"}, {Name: "gen <name>", Short: "generate initial migration from CREATE TABLE DDL"}, {Name: "validate", Short: "validate migration files offline"}, {Name: "up", Short: "apply pending migrations"}, {Name: "down", Short: "roll back an explicit number of migrations"}, {Name: "status", Short: "inspect database version and checksums"}}, Examples: []string{"gofly migrate create add_users --dir migrations", "gofly migrate gen init --from-ddl schema.sql --dialect mysql", "gofly migrate up --driver mysql --dir migrations"}}
	case "migrate create", "migrate new", "migration create", "migration new":
		return Topic{Name: "migrate create", Short: "Create SQL migration files.", Usage: "gofly migrate create <name> [--dir <dir>]", Flags: append(common, "--name <name>        migration name, also accepted as positional", "--version <number>   explicit unique version (default UTC timestamp)"), Examples: []string{"gofly migrate create add-users --dir migrations"}}
	case "migrate gen":
		return Topic{Name: command, Short: "Generate an initial SQL migration from reviewed CREATE TABLE statements.", Usage: "gofly migrate gen <name> --from-ddl <file> --dialect mysql|postgres [flags]", Flags: append(common, "--from-ddl <file>    initial schema SQL; preserves column and constraint definitions", "--dialect <name>     mysql or postgres", "--version <number>   explicit unique version", "--name <name>        migration name, also accepted as positional"), Examples: []string{"gofly migrate gen init_users --from-ddl schema/users.sql --dialect mysql --version 1"}}
	case "migrate validate":
		return Topic{Name: command, Short: "Validate file names, versions, pairs, sizes and symlink boundaries offline.", Usage: "gofly migrate validate [--dir <dir>] [--json]", Flags: common}
	default:
		flags := append(common, "--driver <name>      mysql or postgres", "--dsn-env <name>     DSN environment variable (default GOFLY_MIGRATE_DSN)", "--timeout <duration> operation timeout (default 30s, maximum 1h)")
		if command == "migrate up" || command == "migrate down" {
			flags = append(flags, "--steps <count>      positive count; required for down, up defaults to all")
		}
		return Topic{Name: command, Short: "Run an explicit database migration operation with checksum validation.", Usage: "gofly " + command + " --driver mysql|postgres [flags]", Flags: flags, Examples: []string{"gofly migrate status --driver mysql --json", "gofly migrate down --driver postgres --steps 1"}}
	}
}
