package command

import "flag"

type modelGenFlags struct {
	DDL           *string
	Src           *string
	ShortSrc      *string
	Dir           *string
	ShortDir      *string
	Package       *string
	Module        *string
	Table         *string
	Tables        *string
	ShortTable    *string
	Database      *string
	Strict        *bool
	IgnoreColumns *string
	ShortIgnore   *string
	Prefix        *string
	ShortPrefix   *string
	Style         *string
	Cache         *bool
	ShortCache    *bool
	JSON          *bool
	ConfigPath    *string
}

func newModelGenFlagSet() (*flag.FlagSet, modelGenFlags) {
	fs := flag.NewFlagSet("model gen", flag.ContinueOnError)
	flags := modelGenFlags{
		DDL:           fs.String("ddl", "", "SQL DDL file"),
		Src:           fs.String("src", "", "SQL DDL file"),
		ShortSrc:      fs.String("s", "", "SQL DDL file"),
		Dir:           fs.String("dir", ".", "output directory"),
		ShortDir:      fs.String("d", "", "output directory"),
		Package:       fs.String("package", "", "generated Go package name"),
		Module:        fs.String("module", "", "go module path, inferred from go.mod when empty"),
		Table:         fs.String("table", "", "comma-separated table names to generate"),
		Tables:        fs.String("tables", "", "comma-separated table names to generate, alias for --table"),
		ShortTable:    fs.String("t", "", "comma-separated table names to generate"),
		Database:      fs.String("database", "", "database name"),
		Strict:        fs.Bool("strict", false, "enable strict generation checks"),
		IgnoreColumns: fs.String("ignore-columns", "", "columns to ignore during generation"),
		ShortIgnore:   fs.String("i", "", "columns to ignore during generation"),
		Prefix:        fs.String("prefix", "", "table prefix to trim"),
		ShortPrefix:   fs.String("p", "", "table prefix to trim"),
		Style:         fs.String("style", "go_zero", "model style"),
		Cache:         fs.Bool("cache", false, "generate cache helpers"),
		ShortCache:    fs.Bool("c", false, "generate cache helpers"),
		JSON:          fs.Bool("json", false, "emit generation result as JSON"),
		ConfigPath:    fs.String("config", "", "gofly config file path"),
	}
	registerGoctlModelTemplateFlags(fs)
	return fs, flags
}

func (flags modelGenFlags) normalize(leadingDDL string, remaining []string) []string {
	if *flags.DDL == "" {
		*flags.DDL = *flags.Src
	}
	if *flags.DDL == "" {
		*flags.DDL = *flags.ShortSrc
	}
	if *flags.ShortDir != "" {
		*flags.Dir = *flags.ShortDir
	}
	if *flags.DDL == "" {
		*flags.DDL = leadingDDL
	}
	if *flags.DDL == "" && len(remaining) > 0 {
		*flags.DDL = remaining[0]
		remaining = remaining[1:]
	}
	if *flags.ShortDir == "" && *flags.Dir == "." && len(remaining) > 0 {
		*flags.Dir = remaining[0]
	}
	if *flags.Table == "" {
		*flags.Table = *flags.Tables
	}
	if *flags.Table == "" {
		*flags.Table = *flags.ShortTable
	}
	if *flags.IgnoreColumns == "" {
		*flags.IgnoreColumns = *flags.ShortIgnore
	}
	if *flags.Prefix == "" {
		*flags.Prefix = *flags.ShortPrefix
	}
	if *flags.ShortCache {
		*flags.Cache = true
	}
	return remaining
}
