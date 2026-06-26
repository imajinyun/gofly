package command

import (
	"flag"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func modelCommand(args []string) error {
	if printCommandHelp("model", args) {
		return nil
	}
	return modelCommands.dispatch(args, "gofly model gen|mysql|pg|mongo")
}

func modelGenCommand(args []string) error {
	leadingDDL, args := splitLeadingName(args)
	fs := flag.NewFlagSet("model gen", flag.ContinueOnError)
	ddl := fs.String("ddl", "", "SQL DDL file")
	src := fs.String("src", "", "SQL DDL file")
	s := fs.String("s", "", "SQL DDL file")
	dir := fs.String("dir", ".", "output directory")
	d := fs.String("d", "", "output directory")
	pkg := fs.String("package", "", "generated Go package name")
	module := fs.String("module", "", "go module path, inferred from go.mod when empty")
	table := fs.String("table", "", "comma-separated table names to generate")
	tables := fs.String("tables", "", "comma-separated table names to generate, alias for --table")
	t := fs.String("t", "", "comma-separated table names to generate")
	database := fs.String("database", "", "database name")
	strict := fs.Bool("strict", false, "enable strict generation checks")
	ignoreColumns := fs.String("ignore-columns", "", "columns to ignore during generation")
	i := fs.String("i", "", "columns to ignore during generation")
	prefix := fs.String("prefix", "", "table prefix to trim")
	p := fs.String("p", "", "table prefix to trim")
	style := fs.String("style", "go_zero", "model style")
	cache := fs.Bool("cache", false, "generate cache helpers")
	c := fs.Bool("c", false, "generate cache helpers")
	jsonOut := fs.Bool("json", false, "emit generation result as JSON")
	configPath := fs.String("config", "", "gofly config file path")
	registerGoctlModelTemplateFlags(fs)
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *ddl == "" {
		*ddl = *src
	}
	if *ddl == "" {
		*ddl = *s
	}
	if *d != "" {
		*dir = *d
	}
	if *ddl == "" {
		*ddl = leadingDDL
	}
	if *ddl == "" && len(remaining) > 0 {
		*ddl = remaining[0]
		remaining = remaining[1:]
	}
	if *d == "" && *dir == "." && len(remaining) > 0 {
		*dir = remaining[0]
	}
	if *table == "" {
		*table = *tables
	}
	if *table == "" {
		*table = *t
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
	fillNameFromArgs(ddl, remaining)
	if err := generator.GenerateModelFromDDL(generator.ModelOptions{
		DDLFile:       *ddl,
		Dir:           *dir,
		Package:       *pkg,
		Module:        *module,
		Tables:        splitCSV(*table),
		Style:         *style,
		Database:      *database,
		IgnoreColumns: splitCSV(*ignoreColumns),
		Prefix:        *prefix,
		Strict:        *strict,
		Cache:         *cache,
		TypesMap:      typesMap,
	}); err != nil {
		return err
	}
	if *jsonOut || outputMode() == outputJSON {
		return printModelGenJSON(modelGenJSONOptions{
			DDL:           *ddl,
			Dir:           *dir,
			Package:       *pkg,
			Module:        *module,
			Tables:        *table,
			Database:      *database,
			IgnoreColumns: *ignoreColumns,
			Prefix:        *prefix,
			Style:         *style,
			Strict:        *strict,
			Cache:         *cache,
		})
	}
	printModelGenerated(*dir)
	return nil
}
