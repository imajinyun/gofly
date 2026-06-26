package command

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

var runModelDatasource = generator.GenerateModelFromDatasource

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
		inputs := map[string]string{
			"ddl":   *ddl,
			"dir":   *dir,
			"style": *style,
		}
		if *pkg != "" {
			inputs["package"] = *pkg
		}
		if *module != "" {
			inputs["module"] = *module
		}
		if *table != "" {
			inputs["tables"] = *table
		}
		if *database != "" {
			inputs["database"] = *database
		}
		if *ignoreColumns != "" {
			inputs["ignoreColumns"] = *ignoreColumns
		}
		if *prefix != "" {
			inputs["prefix"] = *prefix
		}
		if *strict {
			inputs["strict"] = "true"
		}
		if *cache {
			inputs["cache"] = "true"
		}
		files := generatedModelFiles(filepath.Join(*dir, "model"))
		return printJSONEnvelope("model.gen", cliPlan{
			Command:           "model gen",
			DryRun:            false,
			MutatesFilesystem: true,
			Inputs:            inputs,
			Actions: []cliPlanAction{
				{Operation: "write-model-files", Target: filepath.Join(*dir, "model"), Description: "generate model entity and repository files", RiskLevel: "medium"},
			},
			GeneratedFiles: len(files),
			NextActions:    []string{"review generated model files", "go test ./..."},
		})
	}
	printModelGenerated(*dir)
	return nil
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

func modelMySQLDatasourceCommand(args []string) error {
	leadingURL, args := splitLeadingName(args)
	fs := flag.NewFlagSet("model mysql datasource", flag.ContinueOnError)
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
		Driver:        "mysql",
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

func modelTypesMapFromConfig(configPath, dir string) (map[string]string, error) {
	path := strings.TrimSpace(configPath)
	explicitPath := path != ""
	if path == "" {
		path = filepath.Join(dir, ".gofly", "config.json")
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) && !explicitPath {
			return nil, nil
		}
		return nil, err
	}
	cfg, err := generator.LoadConfig(path)
	if err != nil {
		return nil, err
	}
	if cfg.Model == nil || len(cfg.Model.TypesMap) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(cfg.Model.TypesMap))
	for key, value := range cfg.Model.TypesMap {
		out[key] = value
	}
	return out, nil
}

func registerGoctlModelTemplateFlags(fs *flag.FlagSet) {
	fs.String("home", "", "template home directory")
	fs.String("remote", "", "remote template repository")
	fs.String("branch", "", "remote template branch")
	fs.Bool("idea", false, "open generated project in IDE")
}

func modelMongoCommand(args []string) error {
	fs := flag.NewFlagSet("model mongo", flag.ContinueOnError)
	typeName := fs.String("type", "", "mongo model type name")
	t := fs.String("t", "", "mongo model type name")
	dir := fs.String("dir", ".", "output directory")
	d := fs.String("d", "", "output directory")
	pkg := fs.String("package", "model", "generated Go package name")
	cache := fs.Bool("cache", false, "generate cache helpers")
	c := fs.Bool("c", false, "generate cache helpers")
	prefix := fs.String("prefix", "", "model prefix to trim")
	p := fs.String("p", "", "model prefix to trim")
	easy := fs.Bool("easy", false, "use simplified mongo output")
	e := fs.Bool("e", false, "use simplified mongo output")
	style := fs.String("style", "go_zero", "model style")
	registerGoctlModelTemplateFlags(fs)
	_ = easy
	_ = e
	_ = style
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *typeName == "" {
		*typeName = *t
	}
	if *d != "" {
		*dir = *d
	}
	if *prefix == "" {
		*prefix = *p
	}
	if *c {
		*cache = true
	}
	fillNameFromArgs(typeName, remaining)
	return generator.GenerateMongoModel(generator.MongoModelOptions{Type: *typeName, Dir: *dir, Package: *pkg, Prefix: *prefix, Cache: *cache, Style: *style})
}
