package command

import (
	"flag"
	"fmt"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func modelPostgresDatasourceCommand(args []string) error {
	leadingURL, args := splitLeadingName(args)
	fs := flag.NewFlagSet("model pg datasource", flag.ContinueOnError)
	url := fs.String("url", "", "database datasource url")
	dsn := fs.String("dsn", "", "database datasource url, alias for --url")
	datasource := fs.String("datasource", "", "database datasource url")
	table := fs.String("table", "", "table name filter")
	tables := fs.String("tables", "", "table name filter, alias for --table")
	t := fs.String("t", "", "table name filter")
	dir := fs.String("dir", ".", "output directory")
	d := fs.String("d", "", "output directory")
	pkg := fs.String("package", "", "generated Go package name")
	module := fs.String("module", "", "go module path, inferred from go.mod when empty")
	database := fs.String("database", "", "database name")
	schema := fs.String("schema", "", "schema name")
	s := fs.String("s", "", "schema name")
	strict := fs.Bool("strict", false, "enable strict generation checks")
	ignoreColumns := fs.String("ignore-columns", "", "columns to ignore during generation")
	i := fs.String("i", "", "columns to ignore during generation")
	prefix := fs.String("prefix", "", "table prefix to trim")
	p := fs.String("p", "", "table prefix to trim")
	style := fs.String("style", "go_zero", "model style: go_zero/sql or gorm")
	cache := fs.Bool("cache", false, "generate cache helpers")
	c := fs.Bool("c", false, "generate cache helpers")
	configPath := fs.String("config", "", "gofly config file path")
	registerGoctlModelTemplateFlags(fs)
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *d != "" {
		*dir = *d
	}
	if *url == "" {
		*url = *dsn
	}
	if *url == "" {
		*url = *datasource
	}
	if *url == "" {
		*url = leadingURL
	}
	if *table == "" {
		*table = *tables
	}
	if *table == "" {
		*table = *t
	}
	if *schema == "" {
		*schema = *s
	}
	if *ignoreColumns == "" {
		*ignoreColumns = *i
	}
	if *prefix == "" {
		*prefix = *p
	}
	if *c {
		*cache = true
	}
	typesMap, err := modelTypesMapFromConfig(*configPath, *dir)
	if err != nil {
		return err
	}
	fillNameFromArgs(url, remaining)
	if *url == "" {
		return fmt.Errorf("%w: datasource url is required", errUsage)
	}
	return runModelDatasource(generator.ModelDatasourceOptions{
		Driver:        "postgres",
		DSN:           *url,
		Dir:           *dir,
		Package:       *pkg,
		Module:        *module,
		Tables:        splitCSV(*table),
		Style:         *style,
		Database:      *database,
		Schema:        *schema,
		IgnoreColumns: splitCSV(*ignoreColumns),
		Prefix:        *prefix,
		Strict:        *strict,
		Cache:         *cache,
		TypesMap:      typesMap,
	})
}
