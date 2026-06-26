package command

func modelCommandHelp(command string) (commandHelp, bool) {
	switch command {
	case "model gen", "model mysql ddl", "model pg ddl":
		return commandHelp{Name: command, Short: "Generate SQL model code from DDL.", Usage: "gofly " + command + " --ddl <schema.sql> [<dir>] [flags]", Flags: []string{"--ddl, --src, -s <file>        SQL DDL file", "--dir, -d <dir>                output directory", "--package <pkg>                generated package name", "--module <module>              module import path", "--table, --tables, -t <tables> table filter", "--style go_zero|gorm           model style", "--cache, -c                    cache option", "--home, --remote, --branch     template source options"}, Examples: []string{"gofly model gen -ddl schema.sql ./internal --style gorm", "gofly model mysql ddl -src schema.sql -dir . -style go_zero"}}, true
	case "gen model":
		return commandHelp{Name: "gen model", Short: "Generate SQL model code from DDL.", Usage: "gofly gen model --ddl <schema.sql> [<dir>] [flags]", Flags: []string{"--ddl, --src, -s <file>        SQL DDL file", "--dir, -d <dir>                output directory", "--package <pkg>                generated package name", "--module <module>              module import path", "--table, --tables, -t <tables> table filter", "--style go_zero|gorm           model style", "--cache, -c                    cache option", "--home, --remote, --branch     template source options"}, Examples: []string{"gofly gen model -ddl schema.sql ./internal --style gorm"}}, true
	case "model mysql datasource", "model pg datasource":
		return commandHelp{Name: command, Short: "Generate SQL model code by introspecting a database datasource.", Usage: "gofly " + command + " --url <dsn> --table <tables> --dir <dir> [flags]", Flags: []string{"--url, --dsn, --datasource <dsn> database datasource URL", "--table, --tables, -t <tables>   table filter", "--dir, -d <dir>                   output directory", "--package <pkg>                   generated package name", "--module <module>                 module import path", "--database <db>, --schema <name>  database/schema compatibility flags", "--style go_zero|gorm              model style"}, Examples: []string{"gofly model mysql datasource -datasource 'user:pass@tcp(localhost:3306)/app' -t users -d .", "gofly model pg datasource -url postgres://localhost/app -t accounts -d ."}}, true
	case "model mongo":
		return commandHelp{Name: "model mongo", Short: "Generate Mongo repository skeleton.", Usage: "gofly model mongo --type <name> --dir <dir> [--package <pkg>]", Flags: []string{"--type, -t <name>     model type name", "--dir, -d <dir>       output directory", "--package <pkg>       generated package name"}, Examples: []string{"gofly model mongo -t UserProfile -d internal/model"}}, true
	case "model":
		return commandHelp{
			Name:  "model",
			Short: "Generate SQL or Mongo models from DDL or database datasource.",
			Usage: "gofly model <command> [arguments]",
			Commands: []helpCommand{
				{Name: "gen", Short: "generate SQL model from DDL"},
				{Name: "mysql ddl", Short: "generate SQL model from MySQL DDL"},
				{Name: "mysql datasource", Short: "generate model by introspecting MySQL"},
				{Name: "pg ddl", Short: "generate SQL model from PostgreSQL DDL"},
				{Name: "pg datasource", Short: "generate model by introspecting PostgreSQL"},
				{Name: "mongo", Short: "generate Mongo repository skeleton"},
			},
			Flags: []string{"--style go_zero|gorm  choose model style", "-s, --src <ddl>       DDL source file", "-d, --dir <dir>       output directory"},
			Examples: []string{
				"gofly model mysql ddl -src schema.sql -dir . -style gorm",
				"gofly model mysql datasource -datasource 'user:pass@tcp(localhost:3306)/app' -t users -d .",
			},
		}, true
	default:
		return commandHelp{}, false
	}
}
