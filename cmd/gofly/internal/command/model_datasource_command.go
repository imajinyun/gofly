package command

import (
	"flag"
	"fmt"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

var runModelDatasource = generator.GenerateModelFromDatasource

type modelDatasourceFlags struct {
	URL           *string
	DSN           *string
	Datasource    *string
	Table         *string
	Tables        *string
	ShortTable    *string
	Dir           *string
	ShortDir      *string
	Package       *string
	Module        *string
	Database      *string
	Schema        *string
	SchemaAlias   *string
	Strict        *bool
	IgnoreColumns *string
	ShortIgnore   *string
	Prefix        *string
	ShortPrefix   *string
	Style         *string
	Cache         *bool
	ShortCache    *bool
	ConfigPath    *string
}

func registerModelDatasourceFlags(fs *flag.FlagSet, schemaAlias bool) modelDatasourceFlags {
	flags := modelDatasourceFlags{
		URL:           fs.String("url", "", "database datasource url"),
		DSN:           fs.String("dsn", "", "database datasource url, alias for --url"),
		Datasource:    fs.String("datasource", "", "database datasource url"),
		Table:         fs.String("table", "", "table name filter"),
		Tables:        fs.String("tables", "", "table name filter, alias for --table"),
		ShortTable:    fs.String("t", "", "table name filter"),
		Dir:           fs.String("dir", ".", "output directory"),
		ShortDir:      fs.String("d", "", "output directory"),
		Package:       fs.String("package", "", "generated Go package name"),
		Module:        fs.String("module", "", "go module path, inferred from go.mod when empty"),
		Database:      fs.String("database", "", "database name"),
		Schema:        fs.String("schema", "", "schema name"),
		Strict:        fs.Bool("strict", false, "enable strict generation checks"),
		IgnoreColumns: fs.String("ignore-columns", "", "columns to ignore during generation"),
		ShortIgnore:   fs.String("i", "", "columns to ignore during generation"),
		Prefix:        fs.String("prefix", "", "table prefix to trim"),
		ShortPrefix:   fs.String("p", "", "table prefix to trim"),
		Style:         fs.String("style", "go_zero", "model style: go_zero/sql or gorm"),
		Cache:         fs.Bool("cache", false, "generate cache helpers"),
		ShortCache:    fs.Bool("c", false, "generate cache helpers"),
		ConfigPath:    fs.String("config", "", "gofly config file path"),
	}
	if schemaAlias {
		flags.SchemaAlias = fs.String("s", "", "schema name")
	}
	return flags
}

func (flags modelDatasourceFlags) normalize(leadingURL string) {
	if valueFromStringFlag(flags.ShortDir) != "" {
		setStringFlag(flags.Dir, valueFromStringFlag(flags.ShortDir))
	}
	if valueFromStringFlag(flags.URL) == "" {
		setStringFlag(flags.URL, valueFromStringFlag(flags.DSN))
	}
	if valueFromStringFlag(flags.URL) == "" {
		setStringFlag(flags.URL, valueFromStringFlag(flags.Datasource))
	}
	if valueFromStringFlag(flags.URL) == "" {
		setStringFlag(flags.URL, leadingURL)
	}
	if valueFromStringFlag(flags.Table) == "" {
		setStringFlag(flags.Table, valueFromStringFlag(flags.Tables))
	}
	if valueFromStringFlag(flags.Table) == "" {
		setStringFlag(flags.Table, valueFromStringFlag(flags.ShortTable))
	}
	if valueFromStringFlag(flags.Schema) == "" {
		setStringFlag(flags.Schema, valueFromStringFlag(flags.SchemaAlias))
	}
	if valueFromStringFlag(flags.IgnoreColumns) == "" {
		setStringFlag(flags.IgnoreColumns, valueFromStringFlag(flags.ShortIgnore))
	}
	if valueFromStringFlag(flags.Prefix) == "" {
		setStringFlag(flags.Prefix, valueFromStringFlag(flags.ShortPrefix))
	}
	if valueFromBoolFlag(flags.ShortCache) {
		setBoolFlag(flags.Cache, true)
	}
}

func modelMySQLCommand(args []string) error {
	if len(args) == 0 || args[0] == "ddl" {
		if len(args) > 0 {
			args = args[1:]
		}
		return modelGenCommand(args)
	}
	if args[0] == "datasource" {
		return modelMySQLDatasourceCommand(args[1:])
	}
	return fmt.Errorf("%w: expected `gofly model mysql ddl` or `gofly model mysql datasource`", errUsage)
}

func modelPostgresCommand(args []string) error {
	if len(args) == 0 || args[0] == "ddl" {
		if len(args) > 0 {
			args = args[1:]
		}
		return modelGenCommand(args)
	}
	if args[0] == "datasource" {
		return modelPostgresDatasourceCommand(args[1:])
	}
	return fmt.Errorf("%w: expected `gofly model pg ddl` or `gofly model pg datasource`", errUsage)
}
