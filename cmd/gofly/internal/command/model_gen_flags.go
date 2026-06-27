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
	remaining = flags.normalizeDDL(leadingDDL, remaining)
	flags.normalizeDir(remaining)
	flags.normalizeTable()
	flags.normalizeIgnoreColumns()
	flags.normalizePrefix()
	flags.normalizeCache()
	return remaining
}

func (flags modelGenFlags) normalizeDDL(leadingDDL string, remaining []string) []string {
	if valueFromStringFlag(flags.DDL) == "" {
		setStringFlag(flags.DDL, valueFromStringFlag(flags.Src))
	}
	if valueFromStringFlag(flags.DDL) == "" {
		setStringFlag(flags.DDL, valueFromStringFlag(flags.ShortSrc))
	}
	if valueFromStringFlag(flags.DDL) == "" {
		setStringFlag(flags.DDL, leadingDDL)
	}
	if valueFromStringFlag(flags.DDL) == "" && len(remaining) > 0 {
		setStringFlag(flags.DDL, remaining[0])
		return remaining[1:]
	}
	return remaining
}

func (flags modelGenFlags) normalizeDir(remaining []string) {
	if valueFromStringFlag(flags.ShortDir) != "" {
		setStringFlag(flags.Dir, valueFromStringFlag(flags.ShortDir))
	}
	if valueFromStringFlag(flags.ShortDir) == "" && valueFromStringFlag(flags.Dir) == "." && len(remaining) > 0 {
		setStringFlag(flags.Dir, remaining[0])
	}
}

func (flags modelGenFlags) normalizeTable() {
	if valueFromStringFlag(flags.Table) == "" {
		setStringFlag(flags.Table, valueFromStringFlag(flags.Tables))
	}
	if valueFromStringFlag(flags.Table) == "" {
		setStringFlag(flags.Table, valueFromStringFlag(flags.ShortTable))
	}
}

func (flags modelGenFlags) normalizeIgnoreColumns() {
	if valueFromStringFlag(flags.IgnoreColumns) == "" {
		setStringFlag(flags.IgnoreColumns, valueFromStringFlag(flags.ShortIgnore))
	}
}

func (flags modelGenFlags) normalizePrefix() {
	if valueFromStringFlag(flags.Prefix) == "" {
		setStringFlag(flags.Prefix, valueFromStringFlag(flags.ShortPrefix))
	}
}

func (flags modelGenFlags) normalizeCache() {
	if valueFromBoolFlag(flags.ShortCache) {
		setBoolFlag(flags.Cache, true)
	}
}
