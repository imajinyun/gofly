package generator

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/imajinyun/gofly/core/storage"
)

type ModelOptions struct {
	DDLFile       string
	Dir           string
	Package       string
	Module        string
	Tables        []string
	Style         string
	Database      string
	Schema        string
	IgnoreColumns []string
	Prefix        string
	Strict        bool
	Cache         bool
	TypesMap      map[string]string
}

type MongoModelOptions struct {
	Type    string
	Dir     string
	Package string
	Prefix  string
	Cache   bool
	Style   string
}

type ModelDatasourceOptions struct {
	Driver        string
	DSN           string
	Dir           string
	Package       string
	Module        string
	Tables        []string
	Timeout       time.Duration
	Style         string
	Database      string
	Schema        string
	IgnoreColumns []string
	Prefix        string
	Strict        bool
	Cache         bool
	TypesMap      map[string]string
}

const (
	modelStyleSQL         = "sql"
	modelStyleGORM        = "gorm"
	modelStyleMongoDriver = "driver"
	gormModulePath        = "gorm.io/gorm"
	gormModuleVersion     = "v1.31.1"
	mongoModulePath       = "go.mongodb.org/mongo-driver"
	mongoModuleVersion    = "v1.17.4"
)

type SQLTable struct {
	Name             string
	Columns          []SQLColumn
	PrimaryKey       string
	SoftDeleteColumn string
	UniqueIndexes    []SQLUniqueIndex
	Indexes          []SQLIndex
}

type SQLColumn struct {
	Name       string
	Type       string
	PrimaryKey bool
	Nullable   bool
	Unique     bool
	GoType     string
}

type SQLUniqueIndex struct {
	Columns []string
}

type SQLIndex struct {
	Columns []string
}

type modelUniqueIndex struct {
	Columns []SQLColumn
}

type modelIndexPrefix struct {
	Columns      []SQLColumn
	OrderColumns []SQLColumn
}

var createTableStartRE = regexp.MustCompile(
	`(?is)create\s+table\s+(?:if\s+not\s+exists\s+)?` +
		`(?:[\x60"]?[A-Za-z_][A-Za-z0-9_]*[\x60"]?\.)?` +
		`[\x60"]?([A-Za-z_][A-Za-z0-9_]*)[\x60"]?\s*\(`)

func GenerateModelFromDDL(opts ModelOptions) error {
	if opts.DDLFile == "" {
		return errors.New("ddl file is required")
	}
	if opts.Dir == "" {
		opts.Dir = "."
	}
	content, err := os.ReadFile(opts.DDLFile)
	if err != nil {
		return fmt.Errorf("read ddl file: %w", err)
	}
	tables, err := ParseSQLModels(string(content))
	if err != nil {
		return err
	}
	tables, err = prepareModelTables(tables, modelGenerationOptions{
		Tables:        opts.Tables,
		IgnoreColumns: opts.IgnoreColumns,
		Prefix:        opts.Prefix,
		Strict:        opts.Strict,
	})
	if err != nil {
		return err
	}
	applyModelTypesMap(tables, opts.TypesMap)
	if opts.Strict {
		if err := validateKnownModelColumnTypes(tables); err != nil {
			return err
		}
	}
	pkg := opts.Package
	if pkg == "" {
		pkg = "model"
	}
	module := strings.TrimSpace(opts.Module)
	if module == "" {
		var inferErr error
		module, inferErr = inferModelModule(opts.Dir)
		if inferErr != nil {
			module = "github.com/imajinyun/gofly"
		}
	}
	style := normalizeModelStyle(opts.Style)
	if err := writeModelFiles(tables, opts.Dir, pkg, module, style, opts.Cache, storage.DialectQuestion); err != nil {
		return err
	}
	return ensureModelGoModDependencies(opts.Dir, style)
}

func GenerateModelFromDatasource(opts ModelDatasourceOptions) error {
	if strings.TrimSpace(opts.Driver) == "" {
		return errors.New("datasource driver is required")
	}
	if strings.TrimSpace(opts.DSN) == "" {
		return errors.New("datasource dsn is required")
	}
	if opts.Dir == "" {
		opts.Dir = "."
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	db, err := sql.Open(datasourceDriverName(opts.Driver), opts.DSN)
	if err != nil {
		return fmt.Errorf("open datasource: %w", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping datasource: %w", err)
	}
	tables, err := introspectSQLTables(ctx, db, datasourceIntrospectionOptions{
		Driver:   opts.Driver,
		Tables:   opts.Tables,
		Database: opts.Database,
		Schema:   opts.Schema,
	})
	if err != nil {
		return err
	}
	tables, err = prepareModelTables(tables, modelGenerationOptions{
		Tables:        opts.Tables,
		IgnoreColumns: opts.IgnoreColumns,
		Prefix:        opts.Prefix,
		Strict:        opts.Strict,
	})
	if err != nil {
		return err
	}
	applyModelTypesMap(tables, opts.TypesMap)
	if opts.Strict {
		if err := validateKnownModelColumnTypes(tables); err != nil {
			return err
		}
	}
	pkg := opts.Package
	if pkg == "" {
		pkg = "model"
	}
	module := strings.TrimSpace(opts.Module)
	if module == "" {
		var inferErr error
		module, inferErr = inferModelModule(opts.Dir)
		if inferErr != nil {
			module = "github.com/imajinyun/gofly"
		}
	}
	style := normalizeModelStyle(opts.Style)
	if err := writeModelFiles(tables, opts.Dir, pkg, module, style, opts.Cache, modelDefaultDialect(opts.Driver)); err != nil {
		return err
	}
	return ensureModelGoModDependencies(opts.Dir, style)
}

func modelDefaultDialect(driver string) storage.Dialect {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "pg", "postgres", "postgresql":
		return storage.DialectPostgres
	case "mysql", "mariadb":
		return storage.DialectMySQL
	case "sqlite", "sqlite3":
		return storage.DialectSQLite
	default:
		return storage.DialectQuestion
	}
}

func modelDialectConstName(dialect storage.Dialect) string {
	switch storage.NormalizeDialect(dialect) {
	case storage.DialectPostgres:
		return "DialectPostgres"
	case storage.DialectMySQL:
		return "DialectMySQL"
	case storage.DialectSQLite:
		return "DialectSQLite"
	default:
		return "DialectQuestion"
	}
}

func normalizeModelStyle(style string) string {
	switch strings.ToLower(strings.TrimSpace(style)) {
	case modelStyleGORM:
		return modelStyleGORM
	default:
		return modelStyleSQL
	}
}

func ensureModelGoModDependencies(dir string, style string) error {
	if style != modelStyleGORM {
		return nil
	}
	return ensureGoModDependencyIfPresent(dir, gormModulePath, gormModuleVersion)
}

func ensureGoModDependencyIfPresent(dir string, module string, version string) error {
	path, err := findNearestGoMod(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if isGoflyRootModule(path) {
		return nil
	}
	return ensureGoModRequire(path, module, version)
}

func isGoflyRootModule(path string) bool {
	module, err := readGoModModule(path)
	return err == nil && module == "github.com/imajinyun/gofly"
}

func inferModelModule(dir string) (string, error) {
	path, err := findNearestGoMod(dir)
	if err != nil {
		return "", err
	}
	module, err := readGoModModule(path)
	if err != nil {
		return "", err
	}
	root := filepath.Dir(path)
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve module root: %w", err)
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve model directory: %w", err)
	}
	rel, err := filepath.Rel(absRoot, absDir)
	if err != nil {
		return "", fmt.Errorf("resolve model module path: %w", err)
	}
	if rel == "." {
		return module, nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return module, nil
	}
	return strings.TrimRight(module, "/") + "/" + filepath.ToSlash(rel), nil
}

func findNearestGoMod(dir string) (string, error) {
	current, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve go.mod directory: %w", err)
	}
	for {
		path := filepath.Join(current, "go.mod")
		_, err := os.Stat(path)
		if err == nil {
			return path, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat go.mod: %w", err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", os.ErrNotExist
		}
		current = parent
	}
}

func ensureGoModRequire(path string, module string, version string) error {
	if filepath.Base(path) != "go.mod" {
		return fmt.Errorf("go.mod path %q must end with go.mod", path)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve go.mod path: %w", err)
	}
	resolvedDir, err := filepath.EvalSymlinks(filepath.Dir(absPath))
	if err != nil {
		return fmt.Errorf("resolve go.mod directory symlinks: %w", err)
	}
	path = filepath.Join(resolvedDir, "go.mod")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read go.mod: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat go.mod: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("go.mod path %q is a directory", path)
	}
	if goModHasRequire(data, module) {
		return nil
	}
	updated := addGoModRequire(data, module, version)
	// #nosec G306 G703 -- path is normalized to an existing nearest go.mod; preserve the file's existing permissions.
	if err := os.WriteFile(path, updated, info.Mode().Perm()); err != nil {
		return fmt.Errorf("write go.mod: %w", err)
	}
	return nil
}

func readGoModModule(path string) (string, error) {
	// #nosec G304 -- go.mod is read from an explicit generated project path to infer module metadata.
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			module := strings.TrimSpace(strings.TrimPrefix(line, "module "))
			if module != "" {
				return module, nil
			}
		}
	}
	return "", errors.New("module is required")
}

func goModHasRequire(data []byte, module string) bool {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "require ")
		if strings.HasPrefix(line, module+" ") || line == module {
			return true
		}
	}
	return false
}

func addGoModRequire(data []byte, module string, version string) []byte {
	text := strings.TrimRight(string(data), "\n")
	requireLine := "require " + module + " " + version
	if strings.HasPrefix(text, "require (\n") {
		return []byte(strings.Replace(text, "require (\n", "require (\n\t"+module+" "+version+"\n", 1) + "\n")
	}
	if strings.Contains(text, "\nrequire (\n") {
		return []byte(strings.Replace(text, "\nrequire (\n", "\nrequire (\n\t"+module+" "+version+"\n", 1) + "\n")
	}
	if strings.TrimSpace(text) == "" {
		return []byte(requireLine + "\n")
	}
	return []byte(text + "\n\n" + requireLine + "\n")
}

func datasourceDriverName(driver string) string {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "postgres", "postgresql", "pg":
		return "pgx"
	default:
		return strings.ToLower(strings.TrimSpace(driver))
	}
}

type datasourceIntrospectionOptions struct {
	Driver   string
	Tables   []string
	Database string
	Schema   string
}

type modelGenerationOptions struct {
	Tables        []string
	IgnoreColumns []string
	Prefix        string
	Strict        bool
}

func introspectSQLTables(ctx context.Context, db *sql.DB, opts datasourceIntrospectionOptions) ([]SQLTable, error) {
	query, args, err := datasourceColumnsQueryWithScope(opts)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query datasource schema: %w", err)
	}
	defer rows.Close()
	byName := map[string]*SQLTable{}
	var order []string
	for rows.Next() {
		var tableName, columnName, dataType, columnKey, nullable string
		var ordinal int
		if err := rows.Scan(&tableName, &columnName, &dataType, &columnKey, &nullable, &ordinal); err != nil {
			return nil, fmt.Errorf("scan datasource column: %w", err)
		}
		table := byName[tableName]
		if table == nil {
			table = &SQLTable{Name: tableName}
			byName[tableName] = table
			order = append(order, tableName)
		}
		column := SQLColumn{
			Name:       columnName,
			Type:       normalizeDatasourceType(dataType),
			PrimaryKey: strings.EqualFold(columnKey, "PRI"),
			Nullable:   strings.EqualFold(nullable, "YES"),
		}
		if column.PrimaryKey && table.PrimaryKey == "" {
			table.PrimaryKey = column.Name
		}
		table.Columns = append(table.Columns, column)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate datasource schema: %w", err)
	}
	if err := introspectSQLIndexes(ctx, db, opts, byName); err != nil {
		return nil, err
	}
	tables := make([]SQLTable, 0, len(order))
	for _, name := range order {
		table := byName[name]
		if len(table.Columns) == 0 {
			continue
		}
		if table.PrimaryKey == "" {
			table.PrimaryKey = table.Columns[0].Name
			table.Columns[0].PrimaryKey = true
		}
		tables = append(tables, *table)
	}
	if len(tables) == 0 {
		return nil, errors.New("model table is required")
	}
	return tables, nil
}

func introspectSQLIndexes(ctx context.Context, db *sql.DB, opts datasourceIntrospectionOptions, byName map[string]*SQLTable) error {
	query, args, err := datasourceIndexesQueryWithScope(opts)
	if err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("query datasource indexes: %w", err)
	}
	defer rows.Close()
	type indexKey struct {
		table string
		name  string
	}
	type indexState struct {
		columns []string
		invalid bool
		unique  bool
	}
	indexes := make(map[indexKey]*indexState)
	var order []indexKey
	for rows.Next() {
		var tableName, indexName string
		var columnName sql.NullString
		var nonUnique, seq int
		if err := rows.Scan(&tableName, &indexName, &columnName, &nonUnique, &seq); err != nil {
			return fmt.Errorf("scan datasource index: %w", err)
		}
		if _, ok := byName[tableName]; !ok {
			continue
		}
		key := indexKey{table: tableName, name: indexName}
		state := indexes[key]
		if state == nil {
			state = &indexState{unique: nonUnique == 0}
			indexes[key] = state
			order = append(order, key)
		}
		column := strings.TrimSpace(columnName.String)
		if seq <= 0 || !columnName.Valid || column == "" {
			state.invalid = true
			continue
		}
		state.columns = append(state.columns, column)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate datasource indexes: %w", err)
	}
	for _, key := range order {
		table := byName[key.table]
		state := indexes[key]
		if table == nil || state == nil || state.invalid || len(state.columns) == 0 {
			continue
		}
		if state.unique {
			if len(state.columns) == 1 {
				markSQLColumnUnique(table, state.columns[0])
			} else {
				table.UniqueIndexes = append(table.UniqueIndexes, SQLUniqueIndex{Columns: append([]string(nil), state.columns...)})
			}
			continue
		}
		table.Indexes = append(table.Indexes, SQLIndex{Columns: append([]string(nil), state.columns...)})
	}
	return nil
}

func markSQLColumnUnique(table *SQLTable, name string) {
	if table == nil {
		return
	}
	for i := range table.Columns {
		if table.Columns[i].Name == name && !table.Columns[i].PrimaryKey {
			table.Columns[i].Unique = true
			return
		}
	}
}

func datasourceColumnsQuery(driver string, tables []string) (string, []any, error) {
	return datasourceColumnsQueryWithScope(datasourceIntrospectionOptions{Driver: driver, Tables: tables})
}

func datasourceColumnsQueryWithScope(opts datasourceIntrospectionOptions) (string, []any, error) {
	tables := cleanTableNames(opts.Tables)
	database := strings.TrimSpace(opts.Database)
	schema := strings.TrimSpace(opts.Schema)
	switch strings.ToLower(strings.TrimSpace(opts.Driver)) {
	case "mysql":
		query := `SELECT table_name, column_name, data_type, column_key, is_nullable, ordinal_position
FROM information_schema.columns
WHERE table_schema = DATABASE()`
		args := make([]any, 0, len(tables)+1)
		if database != "" {
			query = strings.Replace(query, "table_schema = DATABASE()", "table_schema = ?", 1)
			args = append(args, database)
		}
		if len(tables) > 0 {
			query += " AND table_name IN (" + strings.TrimRight(strings.Repeat("?,", len(tables)), ",") + ")"
			for _, table := range tables {
				args = append(args, table)
			}
		}
		query += " ORDER BY table_name, ordinal_position"
		return query, args, nil
	case "pg", "postgres", "postgresql":
		query := `SELECT c.table_name, c.column_name, c.data_type,
       CASE WHEN tc.constraint_type = 'PRIMARY KEY' THEN 'PRI' ELSE '' END AS column_key,
       c.is_nullable, c.ordinal_position
FROM information_schema.columns c
LEFT JOIN information_schema.key_column_usage kcu
  ON c.table_schema = kcu.table_schema
 AND c.table_name = kcu.table_name
 AND c.column_name = kcu.column_name
LEFT JOIN information_schema.table_constraints tc
  ON kcu.constraint_schema = tc.constraint_schema
 AND kcu.constraint_name = tc.constraint_name
 AND tc.constraint_type = 'PRIMARY KEY'
WHERE c.table_schema = current_schema()`
		args := make([]any, 0, len(tables)+1)
		placeholderOffset := 0
		if schema != "" {
			query = strings.Replace(query, "c.table_schema = current_schema()", "c.table_schema = $1", 1)
			args = append(args, schema)
			placeholderOffset = 1
		}
		if len(tables) > 0 {
			placeholders := make([]string, 0, len(tables))
			for i, table := range tables {
				placeholders = append(placeholders, fmt.Sprintf("$%d", i+1+placeholderOffset))
				args = append(args, table)
			}
			query += " AND c.table_name IN (" + strings.Join(placeholders, ",") + ")"
		}
		query += " ORDER BY c.table_name, c.ordinal_position"
		return query, args, nil
	default:
		return "", nil, fmt.Errorf("unsupported datasource driver %q", opts.Driver)
	}
}

func datasourceIndexesQueryWithScope(opts datasourceIntrospectionOptions) (string, []any, error) {
	tables := cleanTableNames(opts.Tables)
	database := strings.TrimSpace(opts.Database)
	schema := strings.TrimSpace(opts.Schema)
	switch strings.ToLower(strings.TrimSpace(opts.Driver)) {
	case "mysql":
		query := `SELECT table_name, index_name, column_name, non_unique, seq_in_index
FROM information_schema.statistics
WHERE table_schema = DATABASE() AND index_name <> 'PRIMARY'`
		args := make([]any, 0, len(tables)+1)
		if database != "" {
			query = strings.Replace(query, "table_schema = DATABASE()", "table_schema = ?", 1)
			args = append(args, database)
		}
		if len(tables) > 0 {
			query += " AND table_name IN (" + strings.TrimRight(strings.Repeat("?,", len(tables)), ",") + ")"
			for _, table := range tables {
				args = append(args, table)
			}
		}
		query += " ORDER BY table_name, index_name, seq_in_index"
		return query, args, nil
	case "pg", "postgres", "postgresql":
		query := `SELECT t.relname AS table_name, i.relname AS index_name, a.attname AS column_name,
       CASE WHEN ix.indisunique THEN 0 ELSE 1 END AS non_unique,
       k.ordinality AS seq_in_index
FROM pg_class t
JOIN pg_namespace ns ON ns.oid = t.relnamespace
JOIN pg_index ix ON t.oid = ix.indrelid
JOIN pg_class i ON i.oid = ix.indexrelid
JOIN LATERAL unnest(ix.indkey) WITH ORDINALITY AS k(attnum, ordinality) ON true
JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum
WHERE ns.nspname = current_schema()
  AND t.relkind IN ('r', 'p')
  AND NOT ix.indisprimary
  AND ix.indexprs IS NULL
  AND ix.indpred IS NULL
  AND k.ordinality <= ix.indnkeyatts`
		args := make([]any, 0, len(tables)+1)
		placeholderOffset := 0
		if schema != "" {
			query = strings.Replace(query, "ns.nspname = current_schema()", "ns.nspname = $1", 1)
			args = append(args, schema)
			placeholderOffset = 1
		}
		if len(tables) > 0 {
			placeholders := make([]string, 0, len(tables))
			for i, table := range tables {
				placeholders = append(placeholders, fmt.Sprintf("$%d", i+1+placeholderOffset))
				args = append(args, table)
			}
			query += " AND t.relname IN (" + strings.Join(placeholders, ",") + ")"
		}
		query += " ORDER BY t.relname, i.relname, k.ordinality"
		return query, args, nil
	default:
		return "", nil, fmt.Errorf("unsupported datasource driver %q", opts.Driver)
	}
}

func cleanTableNames(names []string) []string {
	out := make([]string, 0, len(names))
	seen := map[string]struct{}{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func normalizeDatasourceType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "character varying":
		return "varchar"
	case "timestamp with time zone":
		return "timestamptz"
	case "timestamp without time zone":
		return "timestamp"
	case "double precision":
		return "double"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func filterSQLTables(tables []SQLTable, names []string) []SQLTable {
	if len(names) == 0 {
		return tables
	}
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			wanted[name] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return tables
	}
	out := make([]SQLTable, 0, len(tables))
	for _, table := range tables {
		if _, ok := wanted[table.Name]; ok {
			out = append(out, table)
		}
	}
	return out
}

func prepareModelTables(tables []SQLTable, opts modelGenerationOptions) ([]SQLTable, error) {
	tables = filterSQLTables(tables, opts.Tables)
	if opts.Strict && len(cleanTableNames(opts.Tables)) > 0 && len(tables) != len(cleanTableNames(opts.Tables)) {
		return nil, fmt.Errorf("strict model generation: requested table not found")
	}
	ignored := cleanNameSet(opts.IgnoreColumns)
	prefix := strings.TrimSpace(opts.Prefix)
	out := make([]SQLTable, 0, len(tables))
	for _, table := range tables {
		prepared := table
		if prefix != "" && strings.HasPrefix(prepared.Name, prefix) {
			prepared.Name = strings.TrimPrefix(prepared.Name, prefix)
			if opts.Strict && prepared.Name == "" {
				return nil, fmt.Errorf("strict model generation: table %q becomes empty after trimming prefix %q", table.Name, prefix)
			}
		}
		if len(ignored) > 0 {
			columns := make([]SQLColumn, 0, len(prepared.Columns))
			primaryIgnored := false
			for _, column := range prepared.Columns {
				if _, ok := ignored[strings.ToLower(column.Name)]; ok {
					if column.Name == prepared.PrimaryKey || column.PrimaryKey {
						primaryIgnored = true
					}
					continue
				}
				columns = append(columns, column)
			}
			if opts.Strict && primaryIgnored {
				return nil, fmt.Errorf("strict model generation: primary key column %q cannot be ignored", prepared.PrimaryKey)
			}
			prepared.Columns = columns
			if primaryIgnored {
				prepared.PrimaryKey = ""
			}
		}
		if len(prepared.Columns) == 0 {
			return nil, fmt.Errorf("model table %q has no columns after applying filters", table.Name)
		}
		if prepared.PrimaryKey == "" {
			prepared.PrimaryKey = prepared.Columns[0].Name
			prepared.Columns[0].PrimaryKey = true
		}
		prepared.UniqueIndexes = filterUniqueIndexes(prepared.UniqueIndexes, prepared.Columns)
		prepared.Indexes = filterSQLIndexes(prepared.Indexes, prepared.Columns, prepared.PrimaryKey)
		prepared.SoftDeleteColumn = detectSoftDeleteColumn(prepared.Columns)
		out = append(out, prepared)
	}
	return out, nil
}

func filterUniqueIndexes(indexes []SQLUniqueIndex, columns []SQLColumn) []SQLUniqueIndex {
	if len(indexes) == 0 {
		return nil
	}
	available := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		available[column.Name] = struct{}{}
	}
	out := make([]SQLUniqueIndex, 0, len(indexes))
	seen := make(map[string]struct{}, len(indexes))
	for _, index := range indexes {
		if len(index.Columns) < 2 {
			continue
		}
		filtered := make([]string, 0, len(index.Columns))
		valid := true
		for _, column := range index.Columns {
			if _, ok := available[column]; !ok {
				valid = false
				break
			}
			filtered = append(filtered, column)
		}
		if !valid {
			continue
		}
		key := strings.Join(filtered, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, SQLUniqueIndex{Columns: filtered})
	}
	return out
}

func filterSQLIndexes(indexes []SQLIndex, columns []SQLColumn, primaryKey string) []SQLIndex {
	if len(indexes) == 0 {
		return nil
	}
	available := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		available[column.Name] = struct{}{}
	}
	out := make([]SQLIndex, 0, len(indexes))
	seen := make(map[string]struct{}, len(indexes))
	for _, index := range indexes {
		filtered := make([]string, 0, len(index.Columns))
		valid := true
		for _, column := range index.Columns {
			column = strings.TrimSpace(column)
			if column == "" {
				continue
			}
			if _, ok := available[column]; !ok {
				valid = false
				break
			}
			filtered = append(filtered, column)
		}
		if !valid || len(filtered) == 0 || filtered[0] == primaryKey {
			continue
		}
		key := strings.Join(filtered, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, SQLIndex{Columns: filtered})
	}
	return out
}

func detectSoftDeleteColumn(columns []SQLColumn) string {
	for _, name := range []string{"deleted_at", "delete_time"} {
		for _, column := range columns {
			if strings.EqualFold(column.Name, name) {
				return column.Name
			}
		}
	}
	return ""
}

func validateKnownModelColumnTypes(tables []SQLTable) error {
	for _, table := range tables {
		for _, column := range table.Columns {
			if strings.TrimSpace(column.GoType) != "" {
				continue
			}
			if _, ok := sqlGoTypeKnown(column.Type); !ok {
				return fmt.Errorf("strict model generation: unknown column type %q for %s.%s; configure types_map or disable --strict", column.Type, table.Name, column.Name)
			}
		}
	}
	return nil
}

func cleanNameSet(names []string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		out[strings.ToLower(name)] = struct{}{}
	}
	return out
}

func GenerateMongoModel(opts MongoModelOptions) error {
	if opts.Type == "" {
		return errors.New("mongo model type is required")
	}
	if opts.Dir == "" {
		opts.Dir = "."
	}
	pkg := opts.Package
	if pkg == "" {
		pkg = "model"
	}
	if strings.EqualFold(strings.TrimSpace(opts.Style), modelStyleMongoDriver) {
		return generateMongoDriverModel(opts, pkg)
	}
	typeName := exportName(strings.TrimPrefix(opts.Type, opts.Prefix))
	var b bytes.Buffer
	fprintf(&b, "package %s\n\n", lowerName(pkg))
	fprintf(&b, "import (\n")
	fprintf(&b, "\t\"context\"\n")
	fprintf(&b, "\t\"errors\"\n")
	if opts.Cache {
		fprintf(&b, "\n\t\"github.com/imajinyun/gofly/cache\"\n")
	}
	fprintf(&b, ")\n\n")
	fprintf(&b, "var Err%sNotFound = errors.New(%q)\n\n", typeName, strings.ToLower(typeName)+" not found")
	fprintf(&b, "type %s struct {\n", typeName)
	fprintf(&b, "\tID string `bson:%q json:%q`\n", "_id,omitempty", "id,omitempty")
	fprintf(&b, "}\n\n")
	fprintf(&b, "type %sRepo struct {\n\tcollection MongoCollection[%s]\n}\n\n", typeName, typeName)
	fprintf(&b, "type MongoCollection[T any] interface {\n")
	fprintf(&b, "\tInsert(ctx context.Context, value T) error\n")
	fprintf(&b, "\tFindOne(ctx context.Context, id string) (T, error)\n")
	fprintf(&b, "\tFindMany(ctx context.Context, filter any, limit int, offset int) ([]T, error)\n")
	fprintf(&b, "\tCount(ctx context.Context, filter any) (int64, error)\n")
	fprintf(&b, "\tUpdate(ctx context.Context, id string, value T) error\n")
	fprintf(&b, "\tDelete(ctx context.Context, id string) error\n")
	fprintf(&b, "}\n\n")
	fprintf(&b, "func New%sRepo(collection MongoCollection[%s]) *%sRepo {\n", typeName, typeName, typeName)
	fprintf(&b, "\treturn &%sRepo{collection: collection}\n", typeName)
	fprintf(&b, "}\n\n")
	if opts.Cache {
		fprintf(&b, "func NewCached%sRepo(repo *%sRepo, opts ...cache.ModelOption[%s, string]) *cache.ModelCache[%s, string] {\n", typeName, typeName, typeName, typeName)
		fprintf(&b, "\treturn cache.NewModel(repo.FindOne, opts...)\n}\n\n")
	}
	fprintf(&b, "func (r *%sRepo) Insert(ctx context.Context, value %s) error {\n", typeName, typeName)
	fprintf(&b, "\treturn r.collection.Insert(ctx, value)\n")
	fprintf(&b, "}\n\n")
	fprintf(&b, "func (r *%sRepo) FindOne(ctx context.Context, id string) (%s, error) {\n", typeName, typeName)
	fprintf(&b, "\treturn r.collection.FindOne(ctx, id)\n")
	fprintf(&b, "}\n\n")
	fprintf(&b, "func (r *%sRepo) FindMany(ctx context.Context, filter any, limit int, offset int) ([]%s, error) {\n", typeName, typeName)
	fprintf(&b, "\treturn r.collection.FindMany(ctx, filter, limit, offset)\n")
	fprintf(&b, "}\n\n")
	fprintf(&b, "func (r *%sRepo) Count(ctx context.Context, filter any) (int64, error) {\n", typeName)
	fprintf(&b, "\treturn r.collection.Count(ctx, filter)\n")
	fprintf(&b, "}\n\n")
	fprintf(&b, "func (r *%sRepo) Update(ctx context.Context, id string, value %s) error {\n", typeName, typeName)
	fprintf(&b, "\treturn r.collection.Update(ctx, id, value)\n")
	fprintf(&b, "}\n\n")
	fprintf(&b, "func (r *%sRepo) Delete(ctx context.Context, id string) error {\n", typeName)
	fprintf(&b, "\treturn r.collection.Delete(ctx, id)\n")
	fprintf(&b, "}\n")
	formatted, err := format.Source(b.Bytes())
	if err != nil {
		return fmt.Errorf("format mongo model file: %w", err)
	}
	path := filepath.Join(opts.Dir, lowerSnake(typeName)+".go")
	if err := writeGeneratedFile(path, formatted); err != nil {
		return err
	}
	return nil
}

func generateMongoDriverModel(opts MongoModelOptions, pkg string) error {
	typeName := exportName(strings.TrimPrefix(opts.Type, opts.Prefix))
	var b bytes.Buffer
	fprintf(&b, "package %s\n\n", lowerName(pkg))
	fprintf(&b, "import (\n")
	fprintf(&b, "\t\"context\"\n")
	fprintf(&b, "\t\"errors\"\n")
	fprintf(&b, "\n")
	if opts.Cache {
		fprintf(&b, "\t\"github.com/imajinyun/gofly/cache\"\n")
	}
	fprintf(&b, "\t\"go.mongodb.org/mongo-driver/bson\"\n")
	fprintf(&b, "\t\"go.mongodb.org/mongo-driver/bson/primitive\"\n")
	fprintf(&b, "\t\"go.mongodb.org/mongo-driver/mongo\"\n")
	fprintf(&b, "\t\"go.mongodb.org/mongo-driver/mongo/options\"\n")
	fprintf(&b, ")\n\n")
	fprintf(&b, "var Err%sNotFound = mongo.ErrNoDocuments\n\n", typeName)
	fprintf(&b, "type %s struct {\n", typeName)
	fprintf(&b, "\tID primitive.ObjectID `bson:%q json:%q`\n", "_id,omitempty", "id,omitempty")
	fprintf(&b, "}\n\n")
	fprintf(&b, "type %sRepo struct {\n\tcollection *mongo.Collection\n}\n\n", typeName)
	fprintf(&b, "func New%sRepo(collection *mongo.Collection) *%sRepo {\n", typeName, typeName)
	fprintf(&b, "\treturn &%sRepo{collection: collection}\n", typeName)
	fprintf(&b, "}\n\n")
	if opts.Cache {
		fprintf(&b, "func NewCached%sRepo(repo *%sRepo, opts ...cache.ModelOption[*%s, string]) *cache.ModelCache[*%s, string] {\n", typeName, typeName, typeName, typeName)
		fprintf(&b, "\treturn cache.NewModel(repo.FindByHexID, opts...)\n}\n\n")
	}
	fprintf(&b, "func (r *%sRepo) Collection() *mongo.Collection {\n", typeName)
	fprintf(&b, "\tif r == nil {\n\t\treturn nil\n\t}\n\treturn r.collection\n}\n\n")
	fprintf(&b, "func (r *%sRepo) collectionOrError() (*mongo.Collection, error) {\n", typeName)
	fprintf(&b, "\tif r == nil || r.collection == nil {\n\t\treturn nil, errors.New(%q)\n\t}\n\treturn r.collection, nil\n}\n\n", lowerCamel(typeName)+" repo collection is nil")
	fprintf(&b, "func (r *%sRepo) Insert(ctx context.Context, value *%s) error {\n", typeName, typeName)
	fprintf(&b, "\tif value == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" is nil")
	fprintf(&b, "\tcollection, err := r.collectionOrError()\n\tif err != nil {\n\t\treturn err\n\t}\n")
	fprintf(&b, "\t_, err = collection.InsertOne(ctx, value)\n\treturn err\n}\n\n")
	fprintf(&b, "func (r *%sRepo) FindOne(ctx context.Context, id primitive.ObjectID) (*%s, error) {\n", typeName, typeName)
	fprintf(&b, "\tcollection, err := r.collectionOrError()\n\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	fprintf(&b, "\tvar out %s\n", typeName)
	fprintf(&b, "\tif err := collection.FindOne(ctx, bson.M{\"_id\": id}).Decode(&out); err != nil {\n\t\treturn nil, err\n\t}\n")
	fprintf(&b, "\treturn &out, nil\n}\n\n")
	fprintf(&b, "func (r *%sRepo) FindByHexID(ctx context.Context, id string) (*%s, error) {\n", typeName, typeName)
	fprintf(&b, "\tobjectID, err := primitive.ObjectIDFromHex(id)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	fprintf(&b, "\treturn r.FindOne(ctx, objectID)\n}\n\n")
	fprintf(&b, "func (r *%sRepo) FindMany(ctx context.Context, filter any, limit int64, offset int64) ([]%s, error) {\n", typeName, typeName)
	fprintf(&b, "\tcollection, err := r.collectionOrError()\n\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	fprintf(&b, "\tif filter == nil {\n\t\tfilter = bson.M{}\n\t}\n")
	fprintf(&b, "\tfindOpts := options.Find().SetLimit(limit).SetSkip(offset)\n")
	fprintf(&b, "\tcursor, err := collection.Find(ctx, filter, findOpts)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n\tdefer cursor.Close(ctx)\n")
	fprintf(&b, "\tout := make([]%s, 0)\n", typeName)
	fprintf(&b, "\tif err := cursor.All(ctx, &out); err != nil {\n\t\treturn nil, err\n\t}\n\treturn out, nil\n}\n\n")
	fprintf(&b, "func (r *%sRepo) Count(ctx context.Context, filter any) (int64, error) {\n", typeName)
	fprintf(&b, "\tcollection, err := r.collectionOrError()\n\tif err != nil {\n\t\treturn 0, err\n\t}\n")
	fprintf(&b, "\tif filter == nil {\n\t\tfilter = bson.M{}\n\t}\n\treturn collection.CountDocuments(ctx, filter)\n}\n\n")
	fprintf(&b, "func (r *%sRepo) Update(ctx context.Context, id primitive.ObjectID, value *%s) error {\n", typeName, typeName)
	fprintf(&b, "\tif value == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" is nil")
	fprintf(&b, "\tcollection, err := r.collectionOrError()\n\tif err != nil {\n\t\treturn err\n\t}\n")
	fprintf(&b, "\t_, err = collection.UpdateOne(ctx, bson.M{\"_id\": id}, bson.M{\"$set\": value})\n\treturn err\n}\n\n")
	fprintf(&b, "func (r *%sRepo) Delete(ctx context.Context, id primitive.ObjectID) error {\n", typeName)
	fprintf(&b, "\tcollection, err := r.collectionOrError()\n\tif err != nil {\n\t\treturn err\n\t}\n")
	fprintf(&b, "\t_, err = collection.DeleteOne(ctx, bson.M{\"_id\": id})\n\treturn err\n}\n")
	formatted, err := format.Source(b.Bytes())
	if err != nil {
		return fmt.Errorf("format mongo driver model file: %w", err)
	}
	path := filepath.Join(opts.Dir, lowerSnake(typeName)+".go")
	if err := writeGeneratedFile(path, formatted); err != nil {
		return err
	}
	return ensureGoModDependencyIfPresent(opts.Dir, mongoModulePath, mongoModuleVersion)
}

func writeModelFiles(tables []SQLTable, dir string, pkg string, module string, style string, cacheEnabled bool, defaultDialect storage.Dialect) error {
	if len(tables) == 0 {
		return errors.New("model table is required")
	}
	entityDir := filepath.Join(dir, "model", "entity")
	repoDir := filepath.Join(dir, "model", "repo")
	if err := ensureGeneratedDir(entityDir); err != nil {
		return fmt.Errorf("create entity directory: %w", err)
	}
	if err := ensureGeneratedDir(repoDir); err != nil {
		return fmt.Errorf("create repo directory: %w", err)
	}
	if err := writeEntityTablerFile(entityDir); err != nil {
		return err
	}
	for _, table := range tables {
		if err := writeEntityFile(entityDir, table, pkg, style); err != nil {
			return err
		}
		if err := writeRepoFile(repoDir, table, pkg, module, style, cacheEnabled, defaultDialect); err != nil {
			return err
		}
	}
	return nil
}

func writeEntityTablerFile(dir string) error {
	var b bytes.Buffer
	fprintf(&b, "package entity\n\n")
	fprintf(&b, "type Tabler interface {\n")
	fprintf(&b, "\tTableName() string\n")
	fprintf(&b, "}\n")
	formatted, err := format.Source(b.Bytes())
	if err != nil {
		return fmt.Errorf("format entity tabler file: %w", err)
	}
	return writeGeneratedFile(filepath.Join(dir, "tabler_gen.go"), formatted)
}

func writeEntityFile(dir string, table SQLTable, pkg string, style string) error {
	typeName := exportName(singularize(table.Name))
	var b bytes.Buffer
	fprintf(&b, "package entity\n\n")
	if modelsNeedTime([]SQLTable{table}) {
		fprintf(&b, "import \"time\"\n\n")
	}
	fprintf(&b, "const %sTable = %q\n\n", typeName, table.Name)
	fprintf(&b, "var %sColumns = []string{%s}\n\n", typeName, quotedColumnList(table.Columns))
	fprintf(&b, "type %s struct {\n", typeName)
	for _, column := range table.Columns {
		fieldName := modelFieldName(column.Name)
		if style == modelStyleGORM {
			fprintf(&b, "\t%s %s `db:%q json:%q gorm:%q`\n", fieldName, columnGoType(column), column.Name, lowerCamel(column.Name), gormColumnTag(column))
			continue
		}
		fprintf(&b, "\t%s %s `db:%q json:%q`\n", fieldName, columnGoType(column), column.Name, lowerCamel(column.Name))
	}
	fprintf(&b, "}\n")
	fprintf(&b, "\nvar _ Tabler = (*%s)(nil)\n\n", typeName)
	fprintf(&b, "func (%s) TableName() string { return %sTable }\n", typeName, typeName)
	formatted, err := format.Source(b.Bytes())
	if err != nil {
		return fmt.Errorf("format entity file: %w", err)
	}
	filename := lowerSnake(singularize(table.Name)) + "_gen.go"
	return writeGeneratedFile(filepath.Join(dir, filename), formatted)
}

func writeRepoFile(dir string, table SQLTable, pkg string, module string, style string, cacheEnabled bool, defaultDialect storage.Dialect) error {
	if style == modelStyleGORM {
		return writeGORMRepoFile(dir, table, module, cacheEnabled)
	}
	defaultDialect = storage.NormalizeDialect(defaultDialect)
	typeName := exportName(singularize(table.Name))
	repoName := typeName + "Repo"
	uniqueIndexes := cacheableUniqueIndexes(table)
	indexPrefixes := modelIndexPrefixes(table)
	needsCacheKeyHelpers := cacheEnabled && (len(uniqueIndexes) > 0 || len(indexPrefixes) > 0)
	var b bytes.Buffer
	fprintf(&b, "package repo\n\n")
	fprintf(&b, "import (\n")
	fprintf(&b, "\t\"context\"\n")
	fprintf(&b, "\t\"database/sql\"\n")
	fprintf(&b, "\t\"errors\"\n")
	if needsCacheKeyHelpers {
		fprintf(&b, "\t\"fmt\"\n")
	}
	fprintf(&b, "\t\"sort\"\n")
	if needsCacheKeyHelpers {
		fprintf(&b, "\t\"strconv\"\n")
	}
	fprintf(&b, "\t\"strings\"\n")
	if hasSoftDelete(table) || (cacheEnabled && len(indexPrefixes) > 0) {
		fprintf(&b, "\t\"time\"\n")
	}
	fprintf(&b, "\n")
	if cacheEnabled {
		fprintf(&b, "\t\"github.com/imajinyun/gofly/cache\"\n")
		fprintf(&b, "\t\"github.com/imajinyun/gofly/core/kv/redis\"\n")
	}
	fprintf(&b, "\t\"github.com/imajinyun/gofly/core/storage\"\n")
	fprintf(&b, "\t%q\n", modelEntityImport(module))
	fprintf(&b, ")\n\n")
	fprintf(&b, "type %s struct {\n\tstore   *storage.SQLStore\n\tcluster *storage.Cluster\n\ttx      *sql.Tx\n\tdialect storage.Dialect\n}\n\n", repoName)
	fprintf(&b, "func New%s(store *storage.SQLStore, dialect ...storage.Dialect) *%s {\n", repoName, repoName)
	fprintf(&b, "\td := storage.%s\n\tif len(dialect) > 0 {\n\t\td = dialect[0]\n\t}\n\treturn &%s{store: store, dialect: d}\n}\n\n", modelDialectConstName(defaultDialect), repoName)
	fprintf(&b, "func New%sWithCluster(cluster *storage.Cluster, dialect ...storage.Dialect) *%s {\n", repoName, repoName)
	fprintf(&b, "\td := storage.%s\n\tif len(dialect) > 0 {\n\t\td = dialect[0]\n\t}\n", modelDialectConstName(defaultDialect))
	fprintf(&b, "\tvar store *storage.SQLStore\n\tif cluster != nil {\n\t\tstore = cluster.Writer()\n\t}\n")
	fprintf(&b, "\treturn &%s{store: store, cluster: cluster, dialect: d}\n}\n\n", repoName)
	writeSQLRepoRuntimeHelpers(&b, repoName)
	if cacheEnabled {
		fprintf(&b, "func NewCached%s(repo *%s, opts ...cache.ModelOption[*entity.%s, %s]) *cache.ModelCache[*entity.%s, %s] {\n", repoName, repoName, typeName, primaryKeyType(table), typeName, primaryKeyType(table))
		fprintf(&b, "\treturn cache.NewModel(repo.FindOne, opts...)\n}\n\n")
	}
	fprintf(&b, "func (r *%s) TableName() string { return entity.%sTable }\n\n", repoName, typeName)
	fprintf(&b, "func (r *%s) Columns() []string { return append([]string(nil), entity.%sColumns...) }\n\n", repoName, typeName)
	writeFindOne(&b, table, typeName, repoName)
	writeInsert(&b, table, typeName, repoName)
	writeUpdate(&b, table, typeName, repoName)
	writeDelete(&b, table, typeName, repoName)
	writeList(&b, table, typeName, repoName)
	writeCount(&b, table, typeName, repoName)
	writeWhereMethods(&b, table, typeName, repoName)
	writeAdvancedSQLRepoMethods(&b, table, typeName, repoName)
	if cacheEnabled {
		writeConsistentCachedRepo(&b, table, typeName, repoName, modelStyleSQL)
		writeRedisCachedRepo(&b, table, typeName, repoName, modelStyleSQL)
		writeUniqueCacheKeyFuncs(&b, uniqueIndexes)
		writeIndexListCacheKeyFuncs(&b, indexPrefixes, typeName)
	}
	filename := lowerSnake(singularize(table.Name)) + ".go"
	formatted, err := format.Source(b.Bytes())
	if err != nil {
		return fmt.Errorf("format repo file %s: %w", filename, err)
	}
	return writeGeneratedFile(filepath.Join(dir, filename), formatted)
}

func writeGORMRepoFile(dir string, table SQLTable, module string, cacheEnabled bool) error {
	typeName := exportName(singularize(table.Name))
	repoName := typeName + "Repo"
	uniqueIndexes := cacheableUniqueIndexes(table)
	indexPrefixes := modelIndexPrefixes(table)
	needsCacheKeyHelpers := cacheEnabled && (len(uniqueIndexes) > 0 || len(indexPrefixes) > 0)
	var b bytes.Buffer
	fprintf(&b, "package repo\n\n")
	fprintf(&b, "import (\n")
	fprintf(&b, "\t\"context\"\n")
	fprintf(&b, "\t\"errors\"\n")
	if needsCacheKeyHelpers {
		fprintf(&b, "\t\"fmt\"\n")
		fprintf(&b, "\t\"strconv\"\n")
		fprintf(&b, "\t\"strings\"\n")
	}
	if hasSoftDelete(table) || (cacheEnabled && len(indexPrefixes) > 0) {
		fprintf(&b, "\t\"time\"\n")
	}
	fprintf(&b, "\n")
	if cacheEnabled {
		fprintf(&b, "\t\"github.com/imajinyun/gofly/cache\"\n")
		fprintf(&b, "\t\"github.com/imajinyun/gofly/core/kv/redis\"\n")
	}
	fprintf(&b, "\t\"github.com/imajinyun/gofly/core/storage\"\n")
	fprintf(&b, "\t%q\n", modelEntityImport(module))
	fprintf(&b, "\t\"gorm.io/gorm\"\n")
	fprintf(&b, ")\n\n")
	fprintf(&b, "type %s struct {\n\tdb *gorm.DB\n}\n\n", repoName)
	fprintf(&b, "func New%s(db *gorm.DB) *%s {\n", repoName, repoName)
	fprintf(&b, "\treturn &%s{db: db}\n}\n\n", repoName)
	if cacheEnabled {
		fprintf(&b, "func NewCached%s(repo *%s, opts ...cache.ModelOption[*entity.%s, %s]) *cache.ModelCache[*entity.%s, %s] {\n", repoName, repoName, typeName, primaryKeyType(table), typeName, primaryKeyType(table))
		fprintf(&b, "\treturn cache.NewModel(repo.FindOne, opts...)\n}\n\n")
	}
	fprintf(&b, "func (r *%s) DB() *gorm.DB {\n", repoName)
	fprintf(&b, "\tif r == nil {\n\t\treturn nil\n\t}\n\treturn r.db\n}\n\n")
	fprintf(&b, "func (r *%s) WithDB(db *gorm.DB) *%s {\n", repoName, repoName)
	fprintf(&b, "\tif r == nil {\n\t\treturn nil\n\t}\n\tclone := *r\n\tclone.db = db\n\treturn &clone\n}\n\n")
	fprintf(&b, "func (r *%s) Transact(ctx context.Context, fn func(context.Context, *%s) error) error {\n", repoName, repoName)
	fprintf(&b, "\tdb, err := r.dbWithContext(ctx)\n\tif err != nil {\n\t\treturn err\n\t}\n")
	fprintf(&b, "\tif fn == nil {\n\t\treturn errors.New(\"transaction function is required\")\n\t}\n")
	fprintf(&b, "\treturn db.Transaction(func(tx *gorm.DB) error {\n\t\treturn fn(ctx, r.WithDB(tx))\n\t})\n}\n\n")
	fprintf(&b, "func (r *%s) TableName() string { return entity.%sTable }\n\n", repoName, typeName)
	fprintf(&b, "func (r *%s) Columns() []string { return append([]string(nil), entity.%sColumns...) }\n\n", repoName, typeName)
	fprintf(&b, "func (r *%s) dbWithContext(ctx context.Context) (*gorm.DB, error) {\n", repoName)
	fprintf(&b, "\tif r == nil || r.db == nil {\n\t\treturn nil, errors.New(%q)\n\t}\n", lowerCamel(typeName)+" repo db is nil")
	fprintf(&b, "\treturn r.db.WithContext(ctx).Table(entity.%sTable), nil\n}\n\n", typeName)
	writeGORMFindOne(&b, table, typeName, repoName)
	writeGORMInsert(&b, table, typeName, repoName)
	writeGORMUpdate(&b, table, typeName, repoName)
	writeGORMDelete(&b, table, typeName, repoName)
	writeGORMList(&b, table, typeName, repoName)
	writeGORMCount(&b, table, typeName, repoName)
	writeGORMWhereMethods(&b, table, typeName, repoName)
	writeAdvancedGORMRepoMethods(&b, table, typeName, repoName)
	if cacheEnabled {
		writeConsistentCachedRepo(&b, table, typeName, repoName, modelStyleGORM)
		writeRedisCachedRepo(&b, table, typeName, repoName, modelStyleGORM)
		writeUniqueCacheKeyFuncs(&b, uniqueIndexes)
		writeIndexListCacheKeyFuncs(&b, indexPrefixes, typeName)
	}
	filename := lowerSnake(singularize(table.Name)) + ".go"
	formatted, err := format.Source(b.Bytes())
	if err != nil {
		return fmt.Errorf("format gorm repo file %s: %w", filename, err)
	}
	return writeGeneratedFile(filepath.Join(dir, filename), formatted)
}

func writeSQLRepoRuntimeHelpers(b *bytes.Buffer, repoName string) {
	fprintf(b, "func (r *%s) WithTx(tx *sql.Tx) *%s {\n", repoName, repoName)
	fprintf(b, "\tif r == nil {\n\t\treturn nil\n\t}\n")
	fprintf(b, "\tclone := *r\n\tclone.tx = tx\n\treturn &clone\n}\n\n")
	fprintf(b, "func (r *%s) Transact(ctx context.Context, opts *sql.TxOptions, fn func(context.Context, *%s) error) error {\n", repoName, repoName)
	fprintf(b, "\tif r == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(strings.TrimSuffix(repoName, "Repo"))+" repo is nil")
	fprintf(b, "\tif fn == nil {\n\t\treturn errors.New(\"transaction function is required\")\n\t}\n")
	fprintf(b, "\tif r.cluster != nil {\n\t\treturn r.cluster.Transact(ctx, opts, func(ctx context.Context, tx *sql.Tx) error {\n\t\t\treturn fn(ctx, r.WithTx(tx))\n\t\t})\n\t}\n")
	fprintf(b, "\tif r.store == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(strings.TrimSuffix(repoName, "Repo"))+" repo store is nil")
	fprintf(b, "\treturn r.store.Transact(ctx, opts, func(ctx context.Context, tx *sql.Tx) error {\n\t\treturn fn(ctx, r.WithTx(tx))\n\t})\n}\n\n")
	fprintf(b, "func (r *%s) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {\n", repoName)
	fprintf(b, "\tif r == nil {\n\t\treturn nil, errors.New(%q)\n\t}\n", lowerCamel(strings.TrimSuffix(repoName, "Repo"))+" repo is nil")
	fprintf(b, "\tif r.tx != nil {\n\t\treturn r.tx.ExecContext(ctx, query, args...)\n\t}\n")
	fprintf(b, "\tif r.cluster != nil {\n\t\tstore := r.cluster.Writer()\n\t\tif store == nil {\n\t\t\treturn nil, errors.New(%q)\n\t\t}\n\t\treturn store.Exec(ctx, query, args...)\n\t}\n", lowerCamel(strings.TrimSuffix(repoName, "Repo"))+" repo cluster writer is nil")
	fprintf(b, "\tif r.store == nil {\n\t\treturn nil, errors.New(%q)\n\t}\n", lowerCamel(strings.TrimSuffix(repoName, "Repo"))+" repo store is nil")
	fprintf(b, "\treturn r.store.Exec(ctx, query, args...)\n}\n\n")
	fprintf(b, "func (r *%s) queryOne(ctx context.Context, query string, scan func(*sql.Row) error, args ...any) error {\n", repoName)
	fprintf(b, "\tif r == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(strings.TrimSuffix(repoName, "Repo"))+" repo is nil")
	fprintf(b, "\tif scan == nil {\n\t\treturn errors.New(\"scan function is required\")\n\t}\n")
	fprintf(b, "\tif r.tx != nil {\n\t\treturn scan(r.tx.QueryRowContext(ctx, query, args...))\n\t}\n")
	fprintf(b, "\tif r.cluster != nil {\n\t\tstore := r.cluster.ForQuery(query)\n\t\tif store == nil {\n\t\t\treturn errors.New(%q)\n\t\t}\n\t\treturn store.QueryOne(ctx, query, scan, args...)\n\t}\n", lowerCamel(strings.TrimSuffix(repoName, "Repo"))+" repo cluster reader is nil")
	fprintf(b, "\tif r.store == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(strings.TrimSuffix(repoName, "Repo"))+" repo store is nil")
	fprintf(b, "\treturn r.store.QueryOne(ctx, query, scan, args...)\n}\n\n")
	fprintf(b, "func (r *%s) queryAll(ctx context.Context, query string, scan func(*sql.Rows) error, args ...any) error {\n", repoName)
	fprintf(b, "\tif r == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(strings.TrimSuffix(repoName, "Repo"))+" repo is nil")
	fprintf(b, "\tif scan == nil {\n\t\treturn errors.New(\"scan function is required\")\n\t}\n")
	fprintf(b, "\tif r.tx != nil {\n")
	fprintf(b, "\t\trows, err := r.tx.QueryContext(ctx, query, args...)\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n\t\tdefer rows.Close()\n")
	fprintf(b, "\t\tif err := scan(rows); err != nil {\n\t\t\treturn err\n\t\t}\n\t\treturn rows.Err()\n\t}\n")
	fprintf(b, "\tif r.cluster != nil {\n\t\tstore := r.cluster.ForQuery(query)\n\t\tif store == nil {\n\t\t\treturn errors.New(%q)\n\t\t}\n\t\treturn store.QueryAll(ctx, query, scan, args...)\n\t}\n", lowerCamel(strings.TrimSuffix(repoName, "Repo"))+" repo cluster reader is nil")
	fprintf(b, "\tif r.store == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(strings.TrimSuffix(repoName, "Repo"))+" repo store is nil")
	fprintf(b, "\treturn r.store.QueryAll(ctx, query, scan, args...)\n}\n\n")
}

func writeWhereMethods(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	fprintf(b, "func (r *%s) FindWhere(ctx context.Context, where *storage.Where) ([]entity.%s, error) {\n", receiverName, typeName)
	if hasSoftDelete(table) {
		fprintf(b, "\twhere = where.IsNull(%q)\n", table.SoftDeleteColumn)
	}
	fprintf(b, "\tquery, args, err := storage.SelectWhere(entity.%sTable, entity.%sColumns, where, r.dialect)\n", typeName, typeName)
	fprintf(b, "\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	fprintf(b, "\tout := make([]entity.%s, 0)\n", typeName)
	fprintf(b, "\tif err := r.queryAll(ctx, query, func(rows *sql.Rows) error {\n")
	fprintf(b, "\t\tfor rows.Next() {\n\t\t\tvar item entity.%s\n\t\t\tif err := rows.Scan(%s); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t\tout = append(out, item)\n\t\t}\n\t\treturn nil\n\t}, args...); err != nil {\n\t\treturn nil, err\n\t}\n", typeName, scanArgs("item", table.Columns))
	fprintf(b, "\treturn out, nil\n}\n\n")
	fprintf(b, "func (r *%s) CountWhere(ctx context.Context, where *storage.Where) (int64, error) {\n", receiverName)
	if hasSoftDelete(table) {
		fprintf(b, "\twhere = where.IsNull(%q)\n", table.SoftDeleteColumn)
	}
	fprintf(b, "\tquery, args, err := storage.CountWhere(entity.%sTable, where, r.dialect)\n", typeName)
	fprintf(b, "\tif err != nil {\n\t\treturn 0, err\n\t}\n")
	fprintf(b, "\tvar count int64\n")
	fprintf(b, "\tif err := r.queryOne(ctx, query, func(row *sql.Row) error {\n\t\treturn row.Scan(&count)\n\t}, args...); err != nil {\n\t\treturn 0, err\n\t}\n")
	fprintf(b, "\treturn count, nil\n}\n\n")
}

func writeAdvancedSQLRepoMethods(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	pk := primaryColumn(table)
	writeUniqueFinders(b, table, typeName, receiverName)
	writeIndexListFinders(b, table, typeName, receiverName)
	writeSQLClaimUpdateHelpers(b, table, typeName, receiverName)
	writeSQLFindByIDs(b, table, typeName, receiverName)
	writeSQLUpsertMethods(b, table, typeName, receiverName)
	writeSQLForUpdateMethods(b, table, typeName, receiverName)
	fprintf(b, "func (r *%s) InsertMany(ctx context.Context, items []*entity.%s) error {\n", receiverName, typeName)
	fprintf(b, "\tif len(items) == 0 {\n\t\treturn nil\n\t}\n")
	fprintf(b, "\targs := make([]any, 0, len(items)*len(entity.%sColumns))\n", typeName)
	fprintf(b, "\trows := 0\n")
	fprintf(b, "\tfor _, item := range items {\n")
	fprintf(b, "\t\tif item == nil {\n\t\t\tcontinue\n\t\t}\n")
	fprintf(b, "\t\targs = append(args, %s)\n", valueArgs("item", table.Columns))
	fprintf(b, "\t\trows++\n")
	fprintf(b, "\t}\n")
	fprintf(b, "\tif rows == 0 {\n\t\treturn nil\n\t}\n")
	fprintf(b, "\tquery, err := storage.BatchInsert(entity.%sTable, entity.%sColumns, rows, r.dialect)\n", typeName, typeName)
	fprintf(b, "\tif err != nil {\n\t\treturn err\n\t}\n")
	fprintf(b, "\t_, err = r.exec(ctx, query, args...)\n\treturn err\n}\n\n")
	fprintf(b, "func (r *%s) UpdateMany(ctx context.Context, items []*entity.%s) error {\n", receiverName, typeName)
	fprintf(b, "\tfor _, item := range items {\n\t\tif err := r.Update(ctx, item); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n\treturn nil\n}\n\n")
	fprintf(b, "func (r *%s) DeleteMany(ctx context.Context, ids ...%s) error {\n", receiverName, columnGoType(pk))
	fprintf(b, "\tfor _, id := range ids {\n\t\tif err := r.Delete(ctx, id); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n\treturn nil\n}\n\n")
	writeSQLUpdateFields(b, table, typeName, receiverName)
	writeSQLOptimisticLock(b, table, typeName, receiverName)
	writeSQLCursorPage(b, table, typeName, receiverName)
}

func writeSQLFindByIDs(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	pk := primaryColumn(table)
	pkArg := "ids"
	pkField := modelFieldName(pk.Name)
	fprintf(b, "func (r *%s) FindByIDs(ctx context.Context, %s []%s) ([]entity.%s, error) {\n", receiverName, pkArg, columnGoType(pk), typeName)
	fprintf(b, "\tif len(%s) == 0 {\n\t\treturn []entity.%s{}, nil\n\t}\n", pkArg, typeName)
	fprintf(b, "\tvalues := make([]any, 0, len(%s))\n", pkArg)
	fprintf(b, "\tfor _, id := range %s {\n\t\tvalues = append(values, id)\n\t}\n", pkArg)
	fprintf(b, "\twhere := storage.NewWhere().In(%q, values...).OrderBy(%q)\n", pk.Name, pk.Name)
	if hasSoftDelete(table) {
		fprintf(b, "\twhere = where.IsNull(%q)\n", table.SoftDeleteColumn)
	}
	fprintf(b, "\tquery, args, err := storage.SelectWhere(entity.%sTable, entity.%sColumns, where, r.dialect)\n", typeName, typeName)
	fprintf(b, "\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	fprintf(b, "\tfound := make(map[%s]entity.%s, len(%s))\n", columnGoType(pk), typeName, pkArg)
	fprintf(b, "\tif err := r.queryAll(ctx, query, func(rows *sql.Rows) error {\n")
	fprintf(b, "\t\tfor rows.Next() {\n\t\t\tvar item entity.%s\n\t\t\tif err := rows.Scan(%s); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t\tfound[item.%s] = item\n\t\t}\n\t\treturn nil\n\t}, args...); err != nil {\n\t\treturn nil, err\n\t}\n", typeName, scanArgs("item", table.Columns), pkField)
	fprintf(b, "\tout := make([]entity.%s, 0, len(found))\n", typeName)
	fprintf(b, "\tfor _, id := range %s {\n\t\tif item, ok := found[id]; ok {\n\t\t\tout = append(out, item)\n\t\t}\n\t}\n", pkArg)
	fprintf(b, "\treturn out, nil\n}\n\n")
}

func writeSQLUpsertMethods(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	for _, index := range modelUpsertIndexes(table) {
		updateColumns := upsertUpdateColumns(table, index.Columns)
		if len(updateColumns) == 0 {
			continue
		}
		name := uniqueFinderName(index.Columns)
		fprintf(b, "func (r *%s) UpsertBy%s(ctx context.Context, in *entity.%s) error {\n", receiverName, name, typeName)
		fprintf(b, "\tif in == nil {\n\t\treturn errors.New(\"%s is nil\")\n\t}\n", lowerCamel(typeName))
		fprintf(b, "\tquery, err := storage.Upsert(entity.%sTable, entity.%sColumns, []string{%s}, []string{%s}, r.dialect)\n", typeName, typeName, quotedColumnList(index.Columns), quotedColumnList(updateColumns))
		fprintf(b, "\tif err != nil {\n\t\treturn err\n\t}\n")
		fprintf(b, "\t_, err = r.exec(ctx, query, %s)\n\treturn err\n}\n\n", valueArgs("in", table.Columns))
	}
}

func writeSQLForUpdateMethods(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	pk := primaryColumn(table)
	pkArg := modelArgName(pk.Name)
	for _, method := range []struct {
		name       string
		skipLocked bool
	}{
		{name: "FindOneForUpdate"},
		{name: "FindOneForUpdateSkipLocked", skipLocked: true},
	} {
		fprintf(b, "func (r *%s) %s(ctx context.Context, %s %s) (*entity.%s, error) {\n", receiverName, method.name, pkArg, columnGoType(pk), typeName)
		fprintf(b, "\tquery, err := storage.SelectForUpdate(entity.%sTable, entity.%sColumns, %q, r.dialect, %t)\n", typeName, typeName, pk.Name, method.skipLocked)
		fprintf(b, "\tif err != nil {\n\t\treturn nil, err\n\t}\n")
		fprintf(b, "\tvar out entity.%s\n", typeName)
		fprintf(b, "\tif err := r.queryOne(ctx, query, func(row *sql.Row) error {\n\t\treturn row.Scan(%s)\n\t}, %s); err != nil {\n", scanArgs("out", table.Columns), pkArg)
		fprintf(b, "\t\tif errors.Is(err, sql.ErrNoRows) {\n\t\t\treturn nil, storage.ErrNotFound\n\t\t}\n\t\treturn nil, err\n\t}\n")
		fprintf(b, "\treturn &out, nil\n}\n\n")
	}
	writeUniqueForUpdateFinders(b, table, typeName, receiverName)
}

func writeUniqueFinders(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	for _, column := range table.Columns {
		if !column.Unique || column.PrimaryKey {
			continue
		}
		fprintf(b, "func (r *%s) FindBy%s(ctx context.Context, %s %s) (*entity.%s, error) {\n", receiverName, modelFieldName(column.Name), modelArgName(column.Name), columnGoType(column), typeName)
		fprintf(b, "\twhere := storage.NewWhere().Eq(%q, %s)", column.Name, modelArgName(column.Name))
		if hasSoftDelete(table) {
			fprintf(b, ".IsNull(%q)", table.SoftDeleteColumn)
		}
		fprintf(b, ".Limit(1)\n")
		fprintf(b, "\tquery, args, err := storage.SelectWhere(entity.%sTable, entity.%sColumns, where, r.dialect)\n", typeName, typeName)
		fprintf(b, "\tif err != nil {\n\t\treturn nil, err\n\t}\n")
		fprintf(b, "\tvar out entity.%s\n", typeName)
		fprintf(b, "\tif err := r.queryOne(ctx, query, func(row *sql.Row) error {\n\t\treturn row.Scan(%s)\n\t}, args...); err != nil {\n", scanArgs("out", table.Columns))
		fprintf(b, "\t\tif errors.Is(err, sql.ErrNoRows) {\n\t\t\treturn nil, storage.ErrNotFound\n\t\t}\n\t\treturn nil, err\n\t}\n")
		fprintf(b, "\treturn &out, nil\n}\n\n")
	}
	writeCompositeUniqueFinders(b, table, typeName, receiverName)
}

func writeUniqueForUpdateFinders(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	for _, column := range table.Columns {
		if !column.Unique || column.PrimaryKey {
			continue
		}
		writeUniqueForUpdateFinder(b, table, typeName, receiverName, []SQLColumn{column})
	}
	for _, index := range table.UniqueIndexes {
		columns, ok := uniqueIndexColumns(table, index)
		if !ok {
			continue
		}
		writeUniqueForUpdateFinder(b, table, typeName, receiverName, columns)
	}
}

func writeUniqueForUpdateFinder(b *bytes.Buffer, table SQLTable, typeName, receiverName string, columns []SQLColumn) {
	name := uniqueFinderName(columns)
	for _, method := range []struct {
		suffix     string
		skipLocked bool
	}{
		{suffix: "ForUpdate"},
		{suffix: "ForUpdateSkipLocked", skipLocked: true},
	} {
		fprintf(b, "func (r *%s) FindBy%s%s(ctx context.Context, %s) (*entity.%s, error) {\n", receiverName, name, method.suffix, uniqueFinderParams(columns), typeName)
		fprintf(b, "\twhere := storage.NewWhere()\n")
		writeSQLIndexWhereFilters(b, columns)
		if hasSoftDelete(table) {
			fprintf(b, "\twhere = where.IsNull(%q)\n", table.SoftDeleteColumn)
		}
		fprintf(b, "\tquery, args, err := storage.SelectWhere(entity.%sTable, entity.%sColumns, where, r.dialect)\n", typeName, typeName)
		fprintf(b, "\tif err != nil {\n\t\treturn nil, err\n\t}\n")
		fprintf(b, "\tquery += \" FOR UPDATE\"\n")
		if method.skipLocked {
			fprintf(b, "\tquery += \" SKIP LOCKED\"\n")
		}
		fprintf(b, "\tvar out entity.%s\n", typeName)
		fprintf(b, "\tif err := r.queryOne(ctx, query, func(row *sql.Row) error {\n\t\treturn row.Scan(%s)\n\t}, args...); err != nil {\n", scanArgs("out", table.Columns))
		fprintf(b, "\t\tif errors.Is(err, sql.ErrNoRows) {\n\t\t\treturn nil, storage.ErrNotFound\n\t\t}\n\t\treturn nil, err\n\t}\n")
		fprintf(b, "\treturn &out, nil\n}\n\n")
	}
}

func writeCompositeUniqueFinders(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	for _, index := range table.UniqueIndexes {
		columns, ok := uniqueIndexColumns(table, index)
		if !ok {
			continue
		}
		fprintf(b, "func (r *%s) FindBy%s(ctx context.Context, %s) (*entity.%s, error) {\n", receiverName, uniqueFinderName(columns), uniqueFinderParams(columns), typeName)
		fprintf(b, "\twhere := storage.NewWhere()\n")
		writeSQLIndexWhereFilters(b, columns)
		if hasSoftDelete(table) {
			fprintf(b, "\twhere = where.IsNull(%q)\n", table.SoftDeleteColumn)
		}
		fprintf(b, "\twhere = where.Limit(1)\n")
		fprintf(b, "\tquery, args, err := storage.SelectWhere(entity.%sTable, entity.%sColumns, where, r.dialect)\n", typeName, typeName)
		fprintf(b, "\tif err != nil {\n\t\treturn nil, err\n\t}\n")
		fprintf(b, "\tvar out entity.%s\n", typeName)
		fprintf(b, "\tif err := r.queryOne(ctx, query, func(row *sql.Row) error {\n\t\treturn row.Scan(%s)\n\t}, args...); err != nil {\n", scanArgs("out", table.Columns))
		fprintf(b, "\t\tif errors.Is(err, sql.ErrNoRows) {\n\t\t\treturn nil, storage.ErrNotFound\n\t\t}\n\t\treturn nil, err\n\t}\n")
		fprintf(b, "\treturn &out, nil\n}\n\n")
	}
}

func writeIndexListFinders(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	for _, index := range modelIndexPrefixes(table) {
		name := uniqueFinderName(index.Columns)
		fprintf(b, "func (r *%s) FindBy%s(ctx context.Context, %s, limit int, offset int) ([]entity.%s, error) {\n", receiverName, name, uniqueFinderParams(index.Columns), typeName)
		fprintf(b, "\twhere := storage.NewWhere()\n")
		writeSQLIndexWhereFilters(b, index.Columns)
		if hasSoftDelete(table) {
			fprintf(b, "\twhere = where.IsNull(%q)\n", table.SoftDeleteColumn)
		}
		for _, column := range index.OrderColumns {
			fprintf(b, "\twhere = where.OrderBy(%q)\n", column.Name)
		}
		fprintf(b, "\twhere = where.Limit(limit).Offset(offset)\n")
		fprintf(b, "\tquery, args, err := storage.SelectWhere(entity.%sTable, entity.%sColumns, where, r.dialect)\n", typeName, typeName)
		fprintf(b, "\tif err != nil {\n\t\treturn nil, err\n\t}\n")
		fprintf(b, "\tout := make([]entity.%s, 0)\n", typeName)
		fprintf(b, "\tif err := r.queryAll(ctx, query, func(rows *sql.Rows) error {\n")
		fprintf(b, "\t\tfor rows.Next() {\n\t\t\tvar item entity.%s\n\t\t\tif err := rows.Scan(%s); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t\tout = append(out, item)\n\t\t}\n\t\treturn nil\n\t}, args...); err != nil {\n\t\treturn nil, err\n\t}\n", typeName, scanArgs("item", table.Columns))
		fprintf(b, "\treturn out, nil\n}\n\n")
		writeIndexListForUpdateFinder(b, table, index, typeName, receiverName, false)
		writeIndexListForUpdateFinder(b, table, index, typeName, receiverName, true)
		writeIndexListClaimFinder(b, table, index, typeName, receiverName)
		fprintf(b, "func (r *%s) CountBy%s(ctx context.Context, %s) (int64, error) {\n", receiverName, name, uniqueFinderParams(index.Columns))
		fprintf(b, "\twhere := storage.NewWhere()\n")
		writeSQLIndexWhereFilters(b, index.Columns)
		if hasSoftDelete(table) {
			fprintf(b, "\twhere = where.IsNull(%q)\n", table.SoftDeleteColumn)
		}
		fprintf(b, "\tquery, args, err := storage.CountWhere(entity.%sTable, where, r.dialect)\n", typeName)
		fprintf(b, "\tif err != nil {\n\t\treturn 0, err\n\t}\n")
		fprintf(b, "\tvar count int64\n")
		fprintf(b, "\tif err := r.queryOne(ctx, query, func(row *sql.Row) error {\n\t\treturn row.Scan(&count)\n\t}, args...); err != nil {\n\t\treturn 0, err\n\t}\n")
		fprintf(b, "\treturn count, nil\n}\n\n")
	}
}

func writeIndexListForUpdateFinder(b *bytes.Buffer, table SQLTable, index modelIndexPrefix, typeName, receiverName string, skipLocked bool) {
	name := uniqueFinderName(index.Columns)
	suffix := "ForUpdate"
	if skipLocked {
		suffix = "ForUpdateSkipLocked"
	}
	fprintf(b, "func (r *%s) FindBy%s%s(ctx context.Context, %s, limit int, offset int) ([]entity.%s, error) {\n", receiverName, name, suffix, uniqueFinderParams(index.Columns), typeName)
	fprintf(b, "\twhere := storage.NewWhere()\n")
	writeSQLIndexWhereFilters(b, index.Columns)
	if hasSoftDelete(table) {
		fprintf(b, "\twhere = where.IsNull(%q)\n", table.SoftDeleteColumn)
	}
	for _, column := range index.OrderColumns {
		fprintf(b, "\twhere = where.OrderBy(%q)\n", column.Name)
	}
	fprintf(b, "\twhere = where.Limit(limit).Offset(offset)\n")
	fprintf(b, "\tquery, args, err := storage.SelectWhere(entity.%sTable, entity.%sColumns, where, r.dialect)\n", typeName, typeName)
	fprintf(b, "\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	fprintf(b, "\tquery += \" FOR UPDATE\"\n")
	if skipLocked {
		fprintf(b, "\tquery += \" SKIP LOCKED\"\n")
	}
	fprintf(b, "\tout := make([]entity.%s, 0)\n", typeName)
	fprintf(b, "\tif err := r.queryAll(ctx, query, func(rows *sql.Rows) error {\n")
	fprintf(b, "\t\tfor rows.Next() {\n\t\t\tvar item entity.%s\n\t\t\tif err := rows.Scan(%s); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t\tout = append(out, item)\n\t\t}\n\t\treturn nil\n\t}, args...); err != nil {\n\t\treturn nil, err\n\t}\n", typeName, scanArgs("item", table.Columns))
	fprintf(b, "\treturn out, nil\n}\n\n")
}

func writeIndexListClaimFinder(b *bytes.Buffer, table SQLTable, index modelIndexPrefix, typeName, receiverName string) {
	claimColumn, ok := claimableStatusColumn(index.Columns)
	if !ok {
		return
	}
	pk := primaryColumn(table)
	pkField := modelFieldName(pk.Name)
	pkType := columnGoType(pk)
	name := uniqueFinderName(index.Columns)
	nextArg := "next" + modelFieldName(claimColumn.Name)
	fprintf(b, "func (r *%s) ClaimBy%sSkipLocked(ctx context.Context, %s, %s %s, limit int) ([]entity.%s, error) {\n", receiverName, name, uniqueFinderParams(index.Columns), nextArg, columnGoType(claimColumn), typeName)
	fprintf(b, "\tif limit <= 0 {\n\t\treturn []entity.%s{}, nil\n\t}\n", typeName)
	fprintf(b, "\tclaimed := make([]entity.%s, 0)\n", typeName)
	fprintf(b, "\tif err := r.Transact(ctx, nil, func(ctx context.Context, txRepo *%s) error {\n", receiverName)
	fprintf(b, "\t\titems, err := txRepo.FindBy%sForUpdateSkipLocked(ctx, %s, limit, 0)\n", name, uniqueFinderArgs(index.Columns))
	fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
	fprintf(b, "\t\tids := make([]%s, 0, len(items))\n", pkType)
	fprintf(b, "\t\tfor i := range items {\n")
	fprintf(b, "\t\t\tids = append(ids, items[i].%s)\n", pkField)
	fprintf(b, "\t\t}\n")
	fprintf(b, "\t\tif err := txRepo.%s(ctx, ids, %s); err != nil {\n\t\t\treturn err\n\t\t}\n", claimUpdateMethodName(claimColumn, pk), nextArg)
	fprintf(b, "\t\tfor i := range items {\n")
	fprintf(b, "\t\t\titems[i].%s = %s\n", modelFieldName(claimColumn.Name), nextArg)
	fprintf(b, "\t\t}\n")
	fprintf(b, "\t\tclaimed = items\n\t\treturn nil\n\t}); err != nil {\n\t\treturn nil, err\n\t}\n")
	fprintf(b, "\treturn claimed, nil\n}\n\n")
}

func writeSQLClaimUpdateHelpers(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	pk := primaryColumn(table)
	seen := make(map[string]struct{})
	for _, index := range modelIndexPrefixes(table) {
		claimColumn, ok := claimableStatusColumn(index.Columns)
		if !ok {
			continue
		}
		if _, ok := seen[claimColumn.Name]; ok {
			continue
		}
		seen[claimColumn.Name] = struct{}{}
		nextArg := "next" + modelFieldName(claimColumn.Name)
		fprintf(b, "func (r *%s) %s(ctx context.Context, ids []%s, %s %s) error {\n", receiverName, claimUpdateMethodName(claimColumn, pk), columnGoType(pk), nextArg, columnGoType(claimColumn))
		fprintf(b, "\tif len(ids) == 0 {\n\t\treturn nil\n\t}\n")
		fprintf(b, "\targs := make([]any, 0, len(ids)+1)\n")
		fprintf(b, "\targs = append(args, %s)\n", nextArg)
		fprintf(b, "\tplaceholders := make([]string, 0, len(ids))\n")
		fprintf(b, "\tfor i, id := range ids {\n")
		fprintf(b, "\t\tplaceholders = append(placeholders, storage.Placeholder(r.dialect, i+2))\n")
		fprintf(b, "\t\targs = append(args, id)\n")
		fprintf(b, "\t}\n")
		fprintf(b, "\tquery := \"UPDATE \" + entity.%sTable + \" SET %s = \" + storage.Placeholder(r.dialect, 1) + \" WHERE %s IN (\" + strings.Join(placeholders, \", \") + \")\"\n", typeName, claimColumn.Name, pk.Name)
		if hasSoftDelete(table) {
			fprintf(b, "\tquery += \" AND %s IS NULL\"\n", table.SoftDeleteColumn)
		}
		fprintf(b, "\t_, err := r.exec(ctx, query, args...)\n")
		fprintf(b, "\treturn err\n}\n\n")
	}
}

func claimUpdateMethodName(claimColumn SQLColumn, pk SQLColumn) string {
	return "updateClaimed" + modelFieldName(claimColumn.Name) + "By" + modelFieldName(pk.Name)
}

func writeSQLIndexWhereFilters(b *bytes.Buffer, columns []SQLColumn) {
	for _, column := range columns {
		fprintf(b, "\twhere = where.Eq(%q, %s)\n", column.Name, modelArgName(column.Name))
	}
}

func claimableStatusColumn(columns []SQLColumn) (SQLColumn, bool) {
	for _, column := range columns {
		switch strings.ToLower(column.Name) {
		case "status", "state":
			return column, true
		}
	}
	return SQLColumn{}, false
}

func writeSQLUpdateFields(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	pk := primaryColumn(table)
	columns := updateColumns(table)
	fprintf(b, "func (r *%s) UpdateFields(ctx context.Context, %s %s, fields map[string]any) error {\n", receiverName, modelArgName(pk.Name), columnGoType(pk))
	fprintf(b, "\tif len(fields) == 0 {\n\t\treturn nil\n\t}\n")
	fprintf(b, "\tallowed := map[string]struct{}{%s}\n", columnSetLiteral(columns))
	fprintf(b, "\tfieldNames := make([]string, 0, len(fields))\n")
	fprintf(b, "\tfor column := range fields {\n\t\tif _, ok := allowed[column]; !ok {\n\t\t\treturn errors.New(\"field is not updatable: \" + column)\n\t\t}\n\t\tfieldNames = append(fieldNames, column)\n\t}\n")
	fprintf(b, "\tsort.Strings(fieldNames)\n")
	fprintf(b, "\tsetParts := make([]string, 0, len(fieldNames))\n\targs := make([]any, 0, len(fieldNames)+1)\n\tidx := 1\n")
	fprintf(b, "\tfor _, column := range fieldNames {\n\t\tsetParts = append(setParts, column+\" = \"+storage.Placeholder(r.dialect, idx))\n\t\targs = append(args, fields[column])\n\t\tidx++\n\t}\n")
	fprintf(b, "\targs = append(args, %s)\n", modelArgName(pk.Name))
	fprintf(b, "\tquery := \"UPDATE \" + entity.%sTable + \" SET \" + strings.Join(setParts, \", \") + \" WHERE %s = \" + storage.Placeholder(r.dialect, idx)\n", typeName, pk.Name)
	if hasSoftDelete(table) {
		fprintf(b, "\tquery += \" AND %s IS NULL\"\n", table.SoftDeleteColumn)
	}
	fprintf(b, "\t_, err := r.exec(ctx, query, args...)\n\treturn err\n}\n\n")
}

func writeSQLOptimisticLock(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	version, ok := versionColumn(table)
	if !ok {
		return
	}
	pk := primaryColumn(table)
	columns := updateColumnsExcept(table, version.Name)
	fprintf(b, "func (r *%s) UpdateWithVersion(ctx context.Context, in *entity.%s, expectedVersion %s) error {\n", receiverName, typeName, columnGoType(version))
	fprintf(b, "\tif in == nil {\n\t\treturn errors.New(\"%s is nil\")\n\t}\n", lowerCamel(typeName))
	fprintf(b, "\tquery, err := storage.UpdateByID(entity.%sTable, []string{%s}, %q, r.dialect)\n\tif err != nil {\n\t\treturn err\n\t}\n", typeName, quotedColumnList(append(columns, version)), pk.Name)
	fprintf(b, "\tquery += \" AND %s = \" + storage.Placeholder(r.dialect, %d)\n", version.Name, len(columns)+3)
	if hasSoftDelete(table) {
		fprintf(b, "\tquery += \" AND %s IS NULL\"\n", table.SoftDeleteColumn)
	}
	args := valueArgs("in", columns)
	if args != "" {
		args += ", "
	}
	fprintf(b, "\tresult, err := r.exec(ctx, query, %sexpectedVersion+1, in.%s, expectedVersion)\n\tif err != nil {\n\t\treturn err\n\t}\n", args, modelFieldName(pk.Name))
	fprintf(b, "\trows, err := result.RowsAffected()\n\tif err == nil && rows == 0 {\n\t\treturn storage.ErrNotFound\n\t}\n\treturn nil\n}\n\n")
}

func writeSQLCursorPage(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	pk := primaryColumn(table)
	fprintf(b, "func (r *%s) ListAfter(ctx context.Context, after %s, limit int) ([]entity.%s, error) {\n", receiverName, columnGoType(pk), typeName)
	fprintf(b, "\tcolumns, err := storage.JoinIdentifiers(entity.%sColumns)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n", typeName)
	fprintf(b, "\tquery := \"SELECT \" + columns + \" FROM \" + entity.%sTable + \" WHERE %s > \" + storage.Placeholder(r.dialect, 1)", typeName, pk.Name)
	if hasSoftDelete(table) {
		fprintf(b, " + \" AND %s IS NULL\"", table.SoftDeleteColumn)
	}
	fprintf(b, " + \" ORDER BY %s ASC LIMIT \" + storage.Placeholder(r.dialect, 2)\n", pk.Name)
	fprintf(b, "\tout := make([]entity.%s, 0)\n", typeName)
	fprintf(b, "\tif err := r.queryAll(ctx, query, func(rows *sql.Rows) error {\n")
	fprintf(b, "\t\tfor rows.Next() {\n\t\t\tvar item entity.%s\n\t\t\tif err := rows.Scan(%s); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t\tout = append(out, item)\n\t\t}\n\t\treturn nil\n\t}, after, limit); err != nil {\n\t\treturn nil, err\n\t}\n", typeName, scanArgs("item", table.Columns))
	fprintf(b, "\treturn out, nil\n}\n\n")
}

func writeConsistentCachedRepo(b *bytes.Buffer, table SQLTable, typeName, repoName string, style string) {
	pk := primaryColumn(table)
	cachedName := "Cached" + repoName
	pkArg := modelArgName(pk.Name)
	pkField := modelFieldName(pk.Name)
	uniqueIndexes := cacheableUniqueIndexes(table)
	indexPrefixes := modelIndexPrefixes(table)
	fprintf(b, "type %s struct {\n\trepo *%s\n\tcache *cache.ModelCache[*entity.%s, %s]\n\tafterCommit *[]func(context.Context) error\n", cachedName, repoName, typeName, columnGoType(pk))
	for _, index := range uniqueIndexes {
		fprintf(b, "\t%s *cache.ModelCache[*entity.%s, string]\n", uniqueCacheFieldName(index.Columns), typeName)
	}
	for _, index := range indexPrefixes {
		fprintf(b, "\t%s *cache.Cache[[]entity.%s]\n", indexListCacheFieldName(index.Columns), typeName)
		fprintf(b, "\t%s *cache.Cache[int64]\n", indexCountCacheFieldName(index.Columns))
	}
	fprintf(b, "}\n\n")
	fprintf(b, "func NewConsistentCached%s(repo *%s, opts ...cache.ModelOption[*entity.%s, %s]) *%s {\n", repoName, repoName, typeName, columnGoType(pk), cachedName)
	fprintf(b, "\tloader := func(ctx context.Context, id %s) (*entity.%s, error) {\n\t\tif repo == nil {\n\t\t\treturn nil, errors.New(%q)\n\t\t}\n\t\treturn repo.FindOne(ctx, id)\n\t}\n", columnGoType(pk), typeName, lowerCamel(typeName)+" repo is nil")
	fprintf(b, "\tout := &%s{repo: repo, cache: cache.NewModel(loader, opts...)}\n", cachedName)
	writeUniqueModelCacheInitializers(b, uniqueIndexes, typeName, "repo", false)
	writeIndexListCacheInitializers(b, indexPrefixes, typeName, "repo")
	fprintf(b, "\treturn out\n}\n\n")
	writeConsistentCachedTxHelpers(b, typeName, repoName, cachedName, style)
	fprintf(b, "func (c *%s) FindOne(ctx context.Context, %s %s) (*entity.%s, error) {\n", cachedName, modelArgName(pk.Name), columnGoType(pk), typeName)
	fprintf(b, "\treturn c.FindByIDCached(ctx, %s)\n}\n\n", pkArg)
	fprintf(b, "func (c *%s) FindByIDCached(ctx context.Context, %s %s) (*entity.%s, error) {\n", cachedName, pkArg, columnGoType(pk), typeName)
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn nil, errors.New(%q)\n\t}\n", lowerCamel(typeName)+" cached repo is nil")
	fprintf(b, "\tif c.cache == nil {\n\t\treturn c.repo.FindOne(ctx, %s)\n\t}\n\treturn c.cache.Get(ctx, %s)\n}\n\n", pkArg, pkArg)
	writeConsistentCachedFindByIDs(b, table, typeName, cachedName)
	writeUniqueCachedFinders(b, uniqueIndexes, typeName, cachedName, false)
	writeIndexListCachedFinders(b, indexPrefixes, typeName, cachedName)
	if style == modelStyleSQL {
		writeCachedForUpdateMethods(b, table, typeName, cachedName, lowerCamel(typeName)+" cached repo is nil", false)
	}
	writeConsistentCachedUpsertMethods(b, table, typeName, cachedName)
	fprintf(b, "func (c *%s) Insert(ctx context.Context, in *entity.%s) error {\n", cachedName, typeName)
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" cached repo is nil")
	fprintf(b, "\tif err := c.repo.Insert(ctx, in); err != nil {\n\t\treturn err\n\t}\n")
	fprintf(b, "\treturn c.deferOrRunAfterCommit(ctx, func(ctx context.Context) error {\n\t\treturn c.afterInsertCommit(ctx, in)\n\t})\n}\n\n")
	writeConsistentCachedBatchMutations(b, table, typeName, cachedName, uniqueIndexes)
	fprintf(b, "func (c *%s) Update(ctx context.Context, in *entity.%s) error {\n", cachedName, typeName)
	fprintf(b, "\treturn c.UpdateWithInvalidate(ctx, in)\n}\n\n")
	fprintf(b, "func (c *%s) UpdateWithInvalidate(ctx context.Context, in *entity.%s) error {\n", cachedName, typeName)
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" cached repo is nil")
	fprintf(b, "\tvar old *entity.%s\n", typeName)
	if len(uniqueIndexes) > 0 {
		fprintf(b, "\tif in != nil {\n\t\told, _ = c.repo.FindOne(ctx, in.%s)\n\t}\n", pkField)
	}
	fprintf(b, "\tif err := c.repo.Update(ctx, in); err != nil {\n\t\treturn err\n\t}\n")
	fprintf(b, "\treturn c.deferOrRunAfterCommit(ctx, func(ctx context.Context) error {\n\t\treturn c.afterUpdateCommit(ctx, in, old)\n\t})\n}\n\n")
	writeConsistentCachedUpdateFields(b, table, typeName, cachedName, uniqueIndexes)
	writeConsistentCachedUpdateWithVersion(b, table, typeName, cachedName, uniqueIndexes)
	fprintf(b, "func (c *%s) Delete(ctx context.Context, %s %s) error {\n", cachedName, modelArgName(pk.Name), columnGoType(pk))
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" cached repo is nil")
	fprintf(b, "\tvar old *entity.%s\n", typeName)
	if len(uniqueIndexes) > 0 {
		fprintf(b, "\told, _ = c.repo.FindOne(ctx, %s)\n", pkArg)
	}
	fprintf(b, "\tif err := c.repo.Delete(ctx, %s); err != nil {\n\t\treturn err\n\t}\n", pkArg)
	fprintf(b, "\treturn c.deferOrRunAfterCommit(ctx, func(ctx context.Context) error {\n\t\treturn c.afterDeleteCommit(ctx, %s, old)\n\t})\n}\n\n", pkArg)
	writeConsistentCachedAfterCommitMethods(b, table, typeName, cachedName, uniqueIndexes)
}

func writeRedisCachedRepo(b *bytes.Buffer, table SQLTable, typeName, repoName string, style string) {
	pk := primaryColumn(table)
	cachedName := "RedisCached" + repoName
	pkType := columnGoType(pk)
	pkArg := modelArgName(pk.Name)
	indexPrefixes := modelIndexPrefixes(table)
	fprintf(b, "type %s struct {\n\trepo *%s\n\tcache *cache.RedisModelCache[*entity.%s, %s]\n\tafterCommit *[]func(context.Context) error\n", cachedName, repoName, typeName, pkType)
	for _, index := range indexPrefixes {
		fprintf(b, "\t%s *cache.RedisModelCache[[]entity.%s, string]\n", indexListCacheFieldName(index.Columns), typeName)
		fprintf(b, "\t%s *cache.RedisModelCache[int64, string]\n", indexCountCacheFieldName(index.Columns))
		fprintf(b, "\t%s *cache.RedisModelCache[string, string]\n", indexListVersionFieldName(index.Columns))
	}
	fprintf(b, "}\n\n")
	fprintf(b, "func NewRedisCached%s(repo *%s, client *redis.Client, opts ...cache.RedisModelOption[*entity.%s, %s]) *%s {\n", repoName, repoName, typeName, pkType, cachedName)
	fprintf(b, "\tloader := func(ctx context.Context, id %s) (*entity.%s, error) {\n\t\tif repo == nil {\n\t\t\treturn nil, errors.New(%q)\n\t\t}\n\t\treturn repo.FindOne(ctx, id)\n\t}\n", pkType, typeName, lowerCamel(typeName)+" repo is nil")
	fprintf(b, "\toptions := append([]cache.RedisModelOption[*entity.%s, %s]{cache.WithRedisModelNotFound[*entity.%s, %s](redis.ErrNil), cache.WithRedisModelKeyPrefix[*entity.%s, %s](entity.%sTable)}, opts...)\n", typeName, pkType, typeName, pkType, typeName, pkType, typeName)
	fprintf(b, "\tout := &%s{repo: repo, cache: cache.NewRedisModel(loader, client, options...)}\n", cachedName)
	writeRedisIndexListCacheInitializers(b, indexPrefixes, typeName, "repo")
	fprintf(b, "\treturn out\n}\n\n")
	writeRedisCachedTxHelpers(b, typeName, repoName, cachedName, style)
	fprintf(b, "func (c *%s) FindOne(ctx context.Context, %s %s) (*entity.%s, error) {\n", cachedName, pkArg, pkType, typeName)
	fprintf(b, "\treturn c.FindByIDCached(ctx, %s)\n}\n\n", pkArg)
	fprintf(b, "func (c *%s) FindByIDCached(ctx context.Context, %s %s) (*entity.%s, error) {\n", cachedName, pkArg, pkType, typeName)
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn nil, errors.New(%q)\n\t}\n", lowerCamel(typeName)+" redis cached repo is nil")
	fprintf(b, "\tif c.cache == nil {\n\t\treturn c.repo.FindOne(ctx, %s)\n\t}\n\treturn c.cache.Get(ctx, %s)\n}\n\n", pkArg, pkArg)
	writeRedisCachedFindByIDs(b, table, typeName, cachedName)
	writeRedisIndexListCachedFinders(b, indexPrefixes, typeName, cachedName)
	if style == modelStyleSQL {
		writeCachedForUpdateMethods(b, table, typeName, cachedName, lowerCamel(typeName)+" redis cached repo is nil", true)
	}
	writeRedisCachedUpsertMethods(b, table, typeName, cachedName)
	fprintf(b, "func (c *%s) Insert(ctx context.Context, in *entity.%s) error {\n", cachedName, typeName)
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" redis cached repo is nil")
	fprintf(b, "\tif err := c.repo.Insert(ctx, in); err != nil {\n\t\treturn err\n\t}\n")
	fprintf(b, "\treturn c.deferOrRunAfterCommit(ctx, func(ctx context.Context) error {\n\t\treturn c.afterInsertCommit(ctx, in)\n\t})\n}\n\n")
	writeRedisCachedBatchMutations(b, table, typeName, cachedName)
	fprintf(b, "func (c *%s) Update(ctx context.Context, in *entity.%s) error {\n", cachedName, typeName)
	fprintf(b, "\treturn c.UpdateWithInvalidate(ctx, in)\n}\n\n")
	fprintf(b, "func (c *%s) UpdateWithInvalidate(ctx context.Context, in *entity.%s) error {\n", cachedName, typeName)
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" redis cached repo is nil")
	fprintf(b, "\tif err := c.repo.Update(ctx, in); err != nil {\n\t\treturn err\n\t}\n")
	fprintf(b, "\treturn c.deferOrRunAfterCommit(ctx, func(ctx context.Context) error {\n\t\treturn c.afterUpdateCommit(ctx, in)\n\t})\n}\n\n")
	writeRedisCachedUpdateFields(b, table, typeName, cachedName)
	writeRedisCachedUpdateWithVersion(b, table, typeName, cachedName)
	fprintf(b, "func (c *%s) Delete(ctx context.Context, %s %s) error {\n", cachedName, pkArg, pkType)
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" redis cached repo is nil")
	fprintf(b, "\tif err := c.repo.Delete(ctx, %s); err != nil {\n\t\treturn err\n\t}\n", pkArg)
	fprintf(b, "\treturn c.deferOrRunAfterCommit(ctx, func(ctx context.Context) error {\n\t\treturn c.afterDeleteCommit(ctx, %s)\n\t})\n}\n\n", pkArg)
	writeRedisCachedAfterCommitMethods(b, table, typeName, cachedName)
}

func writeConsistentCachedTxHelpers(b *bytes.Buffer, typeName, repoName, cachedName string, style string) {
	fprintf(b, "func (c *%s) cloneWithRepo(repo *%s, afterCommit *[]func(context.Context) error) *%s {\n", cachedName, repoName, cachedName)
	fprintf(b, "\tif c == nil {\n\t\treturn nil\n\t}\n")
	fprintf(b, "\tclone := *c\n\tclone.repo = repo\n\tclone.afterCommit = afterCommit\n\treturn &clone\n}\n\n")
	if style == modelStyleGORM {
		fprintf(b, "func (c *%s) WithDB(db *gorm.DB) *%s {\n", cachedName, cachedName)
		fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn nil\n\t}\n")
		fprintf(b, "\tafterCommit := make([]func(context.Context) error, 0)\n")
		fprintf(b, "\treturn c.cloneWithRepo(c.repo.WithDB(db), &afterCommit)\n}\n\n")
		fprintf(b, "func (c *%s) Transact(ctx context.Context, fn func(context.Context, *%s) error) error {\n", cachedName, cachedName)
		fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" cached repo is nil")
		fprintf(b, "\tif fn == nil {\n\t\treturn errors.New(\"transaction function is required\")\n\t}\n")
		fprintf(b, "\tafterCommit := make([]func(context.Context) error, 0)\n")
		fprintf(b, "\tif err := c.repo.Transact(ctx, func(ctx context.Context, txRepo *%s) error {\n\t\treturn fn(ctx, c.cloneWithRepo(txRepo, &afterCommit))\n\t}); err != nil {\n\t\treturn err\n\t}\n", repoName)
		fprintf(b, "\ttxCached := c.cloneWithRepo(c.repo, &afterCommit)\n")
		fprintf(b, "\treturn txCached.FlushAfterCommit(ctx)\n}\n\n")
	} else {
		fprintf(b, "func (c *%s) WithTx(tx *sql.Tx) *%s {\n", cachedName, cachedName)
		fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn nil\n\t}\n")
		fprintf(b, "\tafterCommit := make([]func(context.Context) error, 0)\n")
		fprintf(b, "\treturn c.cloneWithRepo(c.repo.WithTx(tx), &afterCommit)\n}\n\n")
		fprintf(b, "func (c *%s) Transact(ctx context.Context, opts *sql.TxOptions, fn func(context.Context, *%s) error) error {\n", cachedName, cachedName)
		fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" cached repo is nil")
		fprintf(b, "\tif fn == nil {\n\t\treturn errors.New(\"transaction function is required\")\n\t}\n")
		fprintf(b, "\tafterCommit := make([]func(context.Context) error, 0)\n")
		fprintf(b, "\tif err := c.repo.Transact(ctx, opts, func(ctx context.Context, txRepo *%s) error {\n\t\treturn fn(ctx, c.cloneWithRepo(txRepo, &afterCommit))\n\t}); err != nil {\n\t\treturn err\n\t}\n", repoName)
		fprintf(b, "\ttxCached := c.cloneWithRepo(c.repo, &afterCommit)\n")
		fprintf(b, "\treturn txCached.FlushAfterCommit(ctx)\n}\n\n")
	}
	fprintf(b, "func (c *%s) FlushAfterCommit(ctx context.Context) error {\n", cachedName)
	fprintf(b, "\tif c == nil || c.afterCommit == nil {\n\t\treturn nil\n\t}\n")
	fprintf(b, "\tfor _, flush := range *c.afterCommit {\n\t\tif err := flush(ctx); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n")
	fprintf(b, "\t*c.afterCommit = nil\n\treturn nil\n}\n\n")
	fprintf(b, "func (c *%s) DiscardAfterCommit() {\n", cachedName)
	fprintf(b, "\tif c == nil || c.afterCommit == nil {\n\t\treturn\n\t}\n")
	fprintf(b, "\t*c.afterCommit = nil\n}\n\n")
	fprintf(b, "func (c *%s) deferOrRunAfterCommit(ctx context.Context, fn func(context.Context) error) error {\n", cachedName)
	fprintf(b, "\tif fn == nil {\n\t\treturn nil\n\t}\n")
	fprintf(b, "\tif c != nil && c.afterCommit != nil {\n\t\t*c.afterCommit = append(*c.afterCommit, fn)\n\t\treturn nil\n\t}\n")
	fprintf(b, "\treturn fn(ctx)\n}\n\n")
}

func writeRedisCachedTxHelpers(b *bytes.Buffer, typeName, repoName, cachedName string, style string) {
	fprintf(b, "func (c *%s) cloneWithRepo(repo *%s, afterCommit *[]func(context.Context) error) *%s {\n", cachedName, repoName, cachedName)
	fprintf(b, "\tif c == nil {\n\t\treturn nil\n\t}\n")
	fprintf(b, "\tclone := *c\n\tclone.repo = repo\n\tclone.afterCommit = afterCommit\n\treturn &clone\n}\n\n")
	if style == modelStyleGORM {
		fprintf(b, "func (c *%s) WithDB(db *gorm.DB) *%s {\n", cachedName, cachedName)
		fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn nil\n\t}\n")
		fprintf(b, "\tafterCommit := make([]func(context.Context) error, 0)\n")
		fprintf(b, "\treturn c.cloneWithRepo(c.repo.WithDB(db), &afterCommit)\n}\n\n")
		fprintf(b, "func (c *%s) Transact(ctx context.Context, fn func(context.Context, *%s) error) error {\n", cachedName, cachedName)
		fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" redis cached repo is nil")
		fprintf(b, "\tif fn == nil {\n\t\treturn errors.New(\"transaction function is required\")\n\t}\n")
		fprintf(b, "\tafterCommit := make([]func(context.Context) error, 0)\n")
		fprintf(b, "\tif err := c.repo.Transact(ctx, func(ctx context.Context, txRepo *%s) error {\n\t\treturn fn(ctx, c.cloneWithRepo(txRepo, &afterCommit))\n\t}); err != nil {\n\t\treturn err\n\t}\n", repoName)
		fprintf(b, "\ttxCached := c.cloneWithRepo(c.repo, &afterCommit)\n")
		fprintf(b, "\treturn txCached.FlushAfterCommit(ctx)\n}\n\n")
	} else {
		fprintf(b, "func (c *%s) WithTx(tx *sql.Tx) *%s {\n", cachedName, cachedName)
		fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn nil\n\t}\n")
		fprintf(b, "\tafterCommit := make([]func(context.Context) error, 0)\n")
		fprintf(b, "\treturn c.cloneWithRepo(c.repo.WithTx(tx), &afterCommit)\n}\n\n")
		fprintf(b, "func (c *%s) Transact(ctx context.Context, opts *sql.TxOptions, fn func(context.Context, *%s) error) error {\n", cachedName, cachedName)
		fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" redis cached repo is nil")
		fprintf(b, "\tif fn == nil {\n\t\treturn errors.New(\"transaction function is required\")\n\t}\n")
		fprintf(b, "\tafterCommit := make([]func(context.Context) error, 0)\n")
		fprintf(b, "\tif err := c.repo.Transact(ctx, opts, func(ctx context.Context, txRepo *%s) error {\n\t\treturn fn(ctx, c.cloneWithRepo(txRepo, &afterCommit))\n\t}); err != nil {\n\t\treturn err\n\t}\n", repoName)
		fprintf(b, "\ttxCached := c.cloneWithRepo(c.repo, &afterCommit)\n")
		fprintf(b, "\treturn txCached.FlushAfterCommit(ctx)\n}\n\n")
	}
	fprintf(b, "func (c *%s) FlushAfterCommit(ctx context.Context) error {\n", cachedName)
	fprintf(b, "\tif c == nil || c.afterCommit == nil {\n\t\treturn nil\n\t}\n")
	fprintf(b, "\tfor _, flush := range *c.afterCommit {\n\t\tif err := flush(ctx); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n")
	fprintf(b, "\t*c.afterCommit = nil\n\treturn nil\n}\n\n")
	fprintf(b, "func (c *%s) DiscardAfterCommit() {\n", cachedName)
	fprintf(b, "\tif c == nil || c.afterCommit == nil {\n\t\treturn\n\t}\n")
	fprintf(b, "\t*c.afterCommit = nil\n}\n\n")
	fprintf(b, "func (c *%s) deferOrRunAfterCommit(ctx context.Context, fn func(context.Context) error) error {\n", cachedName)
	fprintf(b, "\tif fn == nil {\n\t\treturn nil\n\t}\n")
	fprintf(b, "\tif c != nil && c.afterCommit != nil {\n\t\t*c.afterCommit = append(*c.afterCommit, fn)\n\t\treturn nil\n\t}\n")
	fprintf(b, "\treturn fn(ctx)\n}\n\n")
}

func writeConsistentCachedFindByIDs(b *bytes.Buffer, table SQLTable, typeName, cachedName string) {
	pk := primaryColumn(table)
	pkArg := "ids"
	pkField := modelFieldName(pk.Name)
	pkType := columnGoType(pk)
	fprintf(b, "func (c *%s) FindByIDsCached(ctx context.Context, %s []%s) ([]entity.%s, error) {\n", cachedName, pkArg, pkType, typeName)
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn nil, errors.New(%q)\n\t}\n", lowerCamel(typeName)+" cached repo is nil")
	fprintf(b, "\tif len(%s) == 0 {\n\t\treturn []entity.%s{}, nil\n\t}\n", pkArg, typeName)
	fprintf(b, "\tif c.cache == nil {\n\t\treturn c.repo.FindByIDs(ctx, %s)\n\t}\n", pkArg)
	fprintf(b, "\tfound := make(map[%s]*entity.%s, len(%s))\n", pkType, typeName, pkArg)
	fprintf(b, "\tmissing := make([]%s, 0)\n", pkType)
	fprintf(b, "\tseenMissing := make(map[%s]struct{})\n", pkType)
	fprintf(b, "\tfor _, id := range %s {\n", pkArg)
	fprintf(b, "\t\tif _, ok := found[id]; ok {\n\t\t\tcontinue\n\t\t}\n")
	fprintf(b, "\t\tif item, ok := c.cache.Peek(id); ok {\n\t\t\tfound[id] = item\n\t\t\tcontinue\n\t\t}\n")
	fprintf(b, "\t\tif _, ok := seenMissing[id]; !ok {\n\t\t\tmissing = append(missing, id)\n\t\t\tseenMissing[id] = struct{}{}\n\t\t}\n\t}\n")
	fprintf(b, "\tif len(missing) > 0 {\n")
	fprintf(b, "\t\titems, err := c.repo.FindByIDs(ctx, missing)\n\t\tif err != nil {\n\t\t\treturn nil, err\n\t\t}\n")
	fprintf(b, "\t\tfor i := range items {\n\t\t\titem := items[i]\n\t\t\tfound[item.%s] = &item\n\t\t\tc.cache.Set(item.%s, &item)\n\t\t}\n\t}\n", pkField, pkField)
	fprintf(b, "\tout := make([]entity.%s, 0, len(found))\n", typeName)
	fprintf(b, "\tfor _, id := range %s {\n\t\tif item, ok := found[id]; ok && item != nil {\n\t\t\tout = append(out, *item)\n\t\t}\n\t}\n", pkArg)
	fprintf(b, "\treturn out, nil\n}\n\n")
}

func writeRedisCachedFindByIDs(b *bytes.Buffer, table SQLTable, typeName, cachedName string) {
	pk := primaryColumn(table)
	pkArg := "ids"
	pkField := modelFieldName(pk.Name)
	pkType := columnGoType(pk)
	fprintf(b, "func (c *%s) FindByIDsCached(ctx context.Context, %s []%s) ([]entity.%s, error) {\n", cachedName, pkArg, pkType, typeName)
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn nil, errors.New(%q)\n\t}\n", lowerCamel(typeName)+" redis cached repo is nil")
	fprintf(b, "\tif len(%s) == 0 {\n\t\treturn []entity.%s{}, nil\n\t}\n", pkArg, typeName)
	fprintf(b, "\tif c.cache == nil {\n\t\treturn c.repo.FindByIDs(ctx, %s)\n\t}\n", pkArg)
	fprintf(b, "\tfound := make(map[%s]*entity.%s, len(%s))\n", pkType, typeName, pkArg)
	fprintf(b, "\tmissing := make([]%s, 0)\n", pkType)
	fprintf(b, "\tseenMissing := make(map[%s]struct{})\n", pkType)
	fprintf(b, "\tfor _, id := range %s {\n", pkArg)
	fprintf(b, "\t\tif _, ok := found[id]; ok {\n\t\t\tcontinue\n\t\t}\n")
	fprintf(b, "\t\titem, ok, err := c.cache.Peek(ctx, id)\n")
	fprintf(b, "\t\tif err != nil {\n\t\t\treturn nil, err\n\t\t}\n")
	fprintf(b, "\t\tif ok {\n\t\t\tfound[id] = item\n\t\t\tcontinue\n\t\t}\n")
	fprintf(b, "\t\tif _, ok := seenMissing[id]; !ok {\n\t\t\tmissing = append(missing, id)\n\t\t\tseenMissing[id] = struct{}{}\n\t\t}\n\t}\n")
	fprintf(b, "\tif len(missing) > 0 {\n")
	fprintf(b, "\t\titems, err := c.repo.FindByIDs(ctx, missing)\n\t\tif err != nil {\n\t\t\treturn nil, err\n\t\t}\n")
	fprintf(b, "\t\tfor i := range items {\n\t\t\titem := items[i]\n\t\t\tfound[item.%s] = &item\n\t\t\tif err := c.cache.Set(ctx, item.%s, &item); err != nil {\n\t\t\t\treturn nil, err\n\t\t\t}\n\t\t}\n\t}\n", pkField, pkField)
	fprintf(b, "\tout := make([]entity.%s, 0, len(found))\n", typeName)
	fprintf(b, "\tfor _, id := range %s {\n\t\tif item, ok := found[id]; ok && item != nil {\n\t\t\tout = append(out, *item)\n\t\t}\n\t}\n", pkArg)
	fprintf(b, "\treturn out, nil\n}\n\n")
}

func writeCachedForUpdateMethods(b *bytes.Buffer, table SQLTable, typeName, cachedName, nilMessage string, redis bool) {
	pk := primaryColumn(table)
	pkArg := modelArgName(pk.Name)
	pkType := columnGoType(pk)
	for _, method := range []string{"FindOneForUpdate", "FindOneForUpdateSkipLocked"} {
		fprintf(b, "func (c *%s) %s(ctx context.Context, %s %s) (*entity.%s, error) {\n", cachedName, method, pkArg, pkType, typeName)
		fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn nil, errors.New(%q)\n\t}\n", nilMessage)
		fprintf(b, "\treturn c.repo.%s(ctx, %s)\n}\n\n", method, pkArg)
	}
	writeCachedUniqueForUpdateFinders(b, table, typeName, cachedName, nilMessage)
	writeCachedIndexListForUpdateFinders(b, modelIndexPrefixes(table), typeName, cachedName, nilMessage)
	writeCachedIndexListClaimFinders(b, table, typeName, cachedName, nilMessage, redis)
}

func writeCachedUniqueForUpdateFinders(b *bytes.Buffer, table SQLTable, typeName, cachedName, nilMessage string) {
	for _, column := range table.Columns {
		if !column.Unique || column.PrimaryKey {
			continue
		}
		writeCachedUniqueForUpdateFinder(b, typeName, cachedName, nilMessage, []SQLColumn{column})
	}
	for _, index := range table.UniqueIndexes {
		columns, ok := uniqueIndexColumns(table, index)
		if !ok {
			continue
		}
		writeCachedUniqueForUpdateFinder(b, typeName, cachedName, nilMessage, columns)
	}
}

func writeCachedUniqueForUpdateFinder(b *bytes.Buffer, typeName, cachedName, nilMessage string, columns []SQLColumn) {
	name := uniqueFinderName(columns)
	params := uniqueFinderParams(columns)
	args := uniqueFinderArgs(columns)
	for _, suffix := range []string{"ForUpdate", "ForUpdateSkipLocked"} {
		fprintf(b, "func (c *%s) FindBy%s%s(ctx context.Context, %s) (*entity.%s, error) {\n", cachedName, name, suffix, params, typeName)
		fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn nil, errors.New(%q)\n\t}\n", nilMessage)
		fprintf(b, "\treturn c.repo.FindBy%s%s(ctx, %s)\n}\n\n", name, suffix, args)
	}
}

func writeCachedIndexListForUpdateFinders(b *bytes.Buffer, indexes []modelIndexPrefix, typeName, cachedName, nilMessage string) {
	for _, index := range indexes {
		name := uniqueFinderName(index.Columns)
		params := uniqueFinderParams(index.Columns)
		args := uniqueFinderArgs(index.Columns)
		if args != "" {
			args += ", "
		}
		for _, suffix := range []string{"ForUpdate", "ForUpdateSkipLocked"} {
			fprintf(b, "func (c *%s) FindBy%s%s(ctx context.Context, %s, limit int, offset int) ([]entity.%s, error) {\n", cachedName, name, suffix, params, typeName)
			fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn nil, errors.New(%q)\n\t}\n", nilMessage)
			fprintf(b, "\treturn c.repo.FindBy%s%s(ctx, %slimit, offset)\n}\n\n", name, suffix, args)
		}
	}
}

func writeCachedIndexListClaimFinders(b *bytes.Buffer, table SQLTable, typeName, cachedName, nilMessage string, redis bool) {
	pk := primaryColumn(table)
	pkField := modelFieldName(pk.Name)
	pkType := columnGoType(pk)
	for _, index := range modelIndexPrefixes(table) {
		claimColumn, ok := claimableStatusColumn(index.Columns)
		if !ok {
			continue
		}
		name := uniqueFinderName(index.Columns)
		params := uniqueFinderParams(index.Columns)
		args := uniqueFinderArgs(index.Columns)
		nextArg := "next" + modelFieldName(claimColumn.Name)
		nextType := columnGoType(claimColumn)
		if args != "" {
			args += ", "
		}
		fprintf(b, "func (c *%s) ClaimBy%sSkipLocked(ctx context.Context, %s, %s %s, limit int) ([]entity.%s, error) {\n", cachedName, name, params, nextArg, nextType, typeName)
		fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn nil, errors.New(%q)\n\t}\n", nilMessage)
		fprintf(b, "\tif limit <= 0 {\n\t\treturn []entity.%s{}, nil\n\t}\n", typeName)
		fprintf(b, "\tif c.afterCommit != nil {\n\t\treturn c.claimBy%sSkipLocked(ctx, %s%s, limit)\n\t}\n", name, args, nextArg)
		fprintf(b, "\tclaimed := make([]entity.%s, 0)\n", typeName)
		fprintf(b, "\tif err := c.Transact(ctx, nil, func(ctx context.Context, txRepo *%s) error {\n", cachedName)
		fprintf(b, "\t\titems, err := txRepo.claimBy%sSkipLocked(ctx, %s%s, limit)\n", name, args, nextArg)
		fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		fprintf(b, "\t\tclaimed = items\n\t\treturn nil\n\t}); err != nil {\n\t\treturn nil, err\n\t}\n")
		fprintf(b, "\treturn claimed, nil\n}\n\n")
		fprintf(b, "func (c *%s) claimBy%sSkipLocked(ctx context.Context, %s, %s %s, limit int) ([]entity.%s, error) {\n", cachedName, name, params, nextArg, nextType, typeName)
		fprintf(b, "\titems, err := c.repo.FindBy%sForUpdateSkipLocked(ctx, %slimit, 0)\n", name, args)
		fprintf(b, "\tif err != nil {\n\t\treturn nil, err\n\t}\n")
		if !redis {
			fprintf(b, "\toldItems := make([]entity.%s, len(items))\n", typeName)
		}
		fprintf(b, "\tids := make([]%s, 0, len(items))\n", pkType)
		fprintf(b, "\tfor i := range items {\n")
		if !redis {
			fprintf(b, "\t\toldItems[i] = items[i]\n")
		}
		fprintf(b, "\t\tids = append(ids, items[i].%s)\n", pkField)
		fprintf(b, "\t}\n")
		fprintf(b, "\tif err := c.repo.%s(ctx, ids, %s); err != nil {\n\t\treturn nil, err\n\t}\n", claimUpdateMethodName(claimColumn, pk), nextArg)
		fprintf(b, "\tfor i := range items {\n")
		fprintf(b, "\t\titems[i].%s = %s\n", modelFieldName(claimColumn.Name), nextArg)
		fprintf(b, "\t}\n")
		fprintf(b, "\tupdatedItems := append([]entity.%s(nil), items...)\n", typeName)
		fprintf(b, "\tif err := c.deferOrRunAfterCommit(ctx, func(ctx context.Context) error {\n")
		fprintf(b, "\t\tfor i := range updatedItems {\n")
		if redis {
			fprintf(b, "\t\t\tif err := c.afterUpdateCommit(ctx, &updatedItems[i]); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n")
		} else {
			fprintf(b, "\t\t\tif err := c.afterUpdateCommit(ctx, &updatedItems[i], &oldItems[i]); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n")
		}
		fprintf(b, "\t\t}\n\t\treturn nil\n\t}); err != nil {\n\t\treturn nil, err\n\t}\n")
		fprintf(b, "\treturn items, nil\n}\n\n")
	}
}

func writeConsistentCachedUpsertMethods(b *bytes.Buffer, table SQLTable, typeName, cachedName string) {
	indexes := cacheableUniqueIndexes(table)
	for _, index := range modelUpsertIndexes(table) {
		if len(upsertUpdateColumns(table, index.Columns)) == 0 {
			continue
		}
		name := uniqueFinderName(index.Columns)
		fprintf(b, "func (c *%s) UpsertBy%s(ctx context.Context, in *entity.%s) error {\n", cachedName, name, typeName)
		fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" cached repo is nil")
		fprintf(b, "\tvar old *entity.%s\n", typeName)
		if len(indexes) > 0 {
			fprintf(b, "\tif in != nil {\n\t\told, _ = c.repo.FindBy%s(ctx, %s)\n\t}\n", name, uniqueFinderEntityArgs(index.Columns, "in"))
		}
		fprintf(b, "\tif err := c.repo.UpsertBy%s(ctx, in); err != nil {\n\t\treturn err\n\t}\n", name)
		fprintf(b, "\treturn c.deferOrRunAfterCommit(ctx, func(ctx context.Context) error {\n\t\treturn c.afterUpdateCommit(ctx, in, old)\n\t})\n}\n\n")
	}
}

func writeRedisCachedUpsertMethods(b *bytes.Buffer, table SQLTable, typeName, cachedName string) {
	for _, index := range modelUpsertIndexes(table) {
		if len(upsertUpdateColumns(table, index.Columns)) == 0 {
			continue
		}
		name := uniqueFinderName(index.Columns)
		fprintf(b, "func (c *%s) UpsertBy%s(ctx context.Context, in *entity.%s) error {\n", cachedName, name, typeName)
		fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" redis cached repo is nil")
		fprintf(b, "\tif err := c.repo.UpsertBy%s(ctx, in); err != nil {\n\t\treturn err\n\t}\n", name)
		fprintf(b, "\treturn c.deferOrRunAfterCommit(ctx, func(ctx context.Context) error {\n\t\treturn c.afterUpdateCommit(ctx, in)\n\t})\n}\n\n")
	}
}

func writeConsistentCachedBatchMutations(b *bytes.Buffer, table SQLTable, typeName, cachedName string, indexes []modelUniqueIndex) {
	pk := primaryColumn(table)
	pkField := modelFieldName(pk.Name)
	pkType := columnGoType(pk)
	fprintf(b, "func (c *%s) InsertMany(ctx context.Context, items []*entity.%s) error {\n", cachedName, typeName)
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" cached repo is nil")
	fprintf(b, "\tif len(items) == 0 {\n\t\treturn nil\n\t}\n")
	fprintf(b, "\tif err := c.repo.InsertMany(ctx, items); err != nil {\n\t\treturn err\n\t}\n")
	fprintf(b, "\treturn c.deferOrRunAfterCommit(ctx, func(ctx context.Context) error {\n")
	fprintf(b, "\t\tfor _, item := range items {\n\t\t\tif item == nil {\n\t\t\t\tcontinue\n\t\t\t}\n\t\t\tif err := c.afterInsertCommit(ctx, item); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t}\n")
	fprintf(b, "\t\treturn nil\n\t})\n}\n\n")
	fprintf(b, "func (c *%s) UpdateMany(ctx context.Context, items []*entity.%s) error {\n", cachedName, typeName)
	fprintf(b, "\treturn c.UpdateManyWithInvalidate(ctx, items)\n}\n\n")
	fprintf(b, "func (c *%s) UpdateManyWithInvalidate(ctx context.Context, items []*entity.%s) error {\n", cachedName, typeName)
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" cached repo is nil")
	fprintf(b, "\tif len(items) == 0 {\n\t\treturn nil\n\t}\n")
	fprintf(b, "\tvar oldByID map[%s]*entity.%s\n", pkType, typeName)
	if len(indexes) > 0 {
		fprintf(b, "\toldByID = make(map[%s]*entity.%s, len(items))\n", pkType, typeName)
		fprintf(b, "\tfor _, item := range items {\n\t\tif item == nil {\n\t\t\tcontinue\n\t\t}\n\t\told, _ := c.repo.FindOne(ctx, item.%s)\n\t\toldByID[item.%s] = old\n\t}\n", pkField, pkField)
	}
	fprintf(b, "\tif err := c.repo.UpdateMany(ctx, items); err != nil {\n\t\treturn err\n\t}\n")
	fprintf(b, "\treturn c.deferOrRunAfterCommit(ctx, func(ctx context.Context) error {\n")
	fprintf(b, "\t\tfor _, item := range items {\n\t\t\tif item == nil {\n\t\t\t\tcontinue\n\t\t\t}\n\t\t\tvar old *entity.%s\n\t\t\tif oldByID != nil {\n\t\t\t\told = oldByID[item.%s]\n\t\t\t}\n\t\t\tif err := c.afterUpdateCommit(ctx, item, old); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t}\n", typeName, pkField)
	fprintf(b, "\t\treturn nil\n\t})\n}\n\n")
	fprintf(b, "func (c *%s) DeleteMany(ctx context.Context, ids ...%s) error {\n", cachedName, pkType)
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" cached repo is nil")
	fprintf(b, "\tif len(ids) == 0 {\n\t\treturn nil\n\t}\n")
	fprintf(b, "\tvar oldByID map[%s]*entity.%s\n", pkType, typeName)
	if len(indexes) > 0 {
		fprintf(b, "\toldByID = make(map[%s]*entity.%s, len(ids))\n", pkType, typeName)
		fprintf(b, "\tfor _, id := range ids {\n\t\told, _ := c.repo.FindOne(ctx, id)\n\t\toldByID[id] = old\n\t}\n")
	}
	fprintf(b, "\tif err := c.repo.DeleteMany(ctx, ids...); err != nil {\n\t\treturn err\n\t}\n")
	fprintf(b, "\treturn c.deferOrRunAfterCommit(ctx, func(ctx context.Context) error {\n")
	fprintf(b, "\t\tfor _, id := range ids {\n\t\t\tvar old *entity.%s\n\t\t\tif oldByID != nil {\n\t\t\t\told = oldByID[id]\n\t\t\t}\n\t\t\tif err := c.afterDeleteCommit(ctx, id, old); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t}\n", typeName)
	fprintf(b, "\t\treturn nil\n\t})\n}\n\n")
}

func writeRedisCachedBatchMutations(b *bytes.Buffer, table SQLTable, typeName, cachedName string) {
	pk := primaryColumn(table)
	pkType := columnGoType(pk)
	fprintf(b, "func (c *%s) InsertMany(ctx context.Context, items []*entity.%s) error {\n", cachedName, typeName)
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" redis cached repo is nil")
	fprintf(b, "\tif len(items) == 0 {\n\t\treturn nil\n\t}\n")
	fprintf(b, "\tif err := c.repo.InsertMany(ctx, items); err != nil {\n\t\treturn err\n\t}\n")
	fprintf(b, "\treturn c.deferOrRunAfterCommit(ctx, func(ctx context.Context) error {\n")
	fprintf(b, "\t\tfor _, item := range items {\n\t\t\tif item == nil {\n\t\t\t\tcontinue\n\t\t\t}\n\t\t\tif err := c.afterInsertCommit(ctx, item); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t}\n")
	fprintf(b, "\t\treturn nil\n\t})\n}\n\n")
	fprintf(b, "func (c *%s) UpdateMany(ctx context.Context, items []*entity.%s) error {\n", cachedName, typeName)
	fprintf(b, "\treturn c.UpdateManyWithInvalidate(ctx, items)\n}\n\n")
	fprintf(b, "func (c *%s) UpdateManyWithInvalidate(ctx context.Context, items []*entity.%s) error {\n", cachedName, typeName)
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" redis cached repo is nil")
	fprintf(b, "\tif len(items) == 0 {\n\t\treturn nil\n\t}\n")
	fprintf(b, "\tif err := c.repo.UpdateMany(ctx, items); err != nil {\n\t\treturn err\n\t}\n")
	fprintf(b, "\treturn c.deferOrRunAfterCommit(ctx, func(ctx context.Context) error {\n")
	fprintf(b, "\t\tfor _, item := range items {\n\t\t\tif item == nil {\n\t\t\t\tcontinue\n\t\t\t}\n\t\t\tif err := c.afterUpdateCommit(ctx, item); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t}\n")
	fprintf(b, "\t\treturn nil\n\t})\n}\n\n")
	fprintf(b, "func (c *%s) DeleteMany(ctx context.Context, ids ...%s) error {\n", cachedName, pkType)
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" redis cached repo is nil")
	fprintf(b, "\tif len(ids) == 0 {\n\t\treturn nil\n\t}\n")
	fprintf(b, "\tif err := c.repo.DeleteMany(ctx, ids...); err != nil {\n\t\treturn err\n\t}\n")
	fprintf(b, "\treturn c.deferOrRunAfterCommit(ctx, func(ctx context.Context) error {\n")
	fprintf(b, "\t\tfor _, id := range ids {\n\t\t\tif err := c.afterDeleteCommit(ctx, id); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t}\n")
	fprintf(b, "\t\treturn nil\n\t})\n}\n\n")
}

func writeConsistentCachedAfterCommitMethods(b *bytes.Buffer, table SQLTable, typeName, cachedName string, indexes []modelUniqueIndex) {
	pk := primaryColumn(table)
	pkField := modelFieldName(pk.Name)
	indexPrefixes := modelIndexPrefixes(table)
	fprintf(b, "func (c *%s) afterInsertCommit(ctx context.Context, in *entity.%s) error {\n", cachedName, typeName)
	fprintf(b, "\tif c.cache != nil && in != nil {\n\t\tc.cache.Set(in.%s, in)\n\t}\n", pkField)
	writeUniqueCacheSetAfterMutation(b, indexes, false)
	writeIndexListCacheClearAfterMutation(b, indexPrefixes)
	fprintf(b, "\treturn nil\n}\n\n")
	fprintf(b, "func (c *%s) afterUpdateCommit(ctx context.Context, in *entity.%s, old *entity.%s) error {\n", cachedName, typeName, typeName)
	fprintf(b, "\tif c.cache != nil && in != nil {\n\t\tc.cache.Invalidate(in.%s)\n\t}\n", pkField)
	writeUniqueCacheInvalidateAfterMutation(b, indexes, "old", false)
	writeUniqueCacheSetAfterMutation(b, indexes, false)
	writeIndexListCacheClearAfterMutation(b, indexPrefixes)
	fprintf(b, "\treturn nil\n}\n\n")
	fprintf(b, "func (c *%s) afterUpdateFieldsCommit(ctx context.Context, %s %s, old *entity.%s) error {\n", cachedName, modelArgName(pk.Name), columnGoType(pk), typeName)
	fprintf(b, "\tif c.cache != nil {\n\t\tc.cache.Invalidate(%s)\n\t}\n", modelArgName(pk.Name))
	writeUniqueCacheInvalidateAfterMutation(b, indexes, "old", false)
	writeIndexListCacheClearAfterMutation(b, indexPrefixes)
	fprintf(b, "\treturn nil\n}\n\n")
	fprintf(b, "func (c *%s) afterDeleteCommit(ctx context.Context, %s %s, old *entity.%s) error {\n", cachedName, modelArgName(pk.Name), columnGoType(pk), typeName)
	fprintf(b, "\tif c.cache != nil {\n\t\tc.cache.Invalidate(%s)\n\t}\n", modelArgName(pk.Name))
	writeUniqueCacheInvalidateAfterMutation(b, indexes, "old", false)
	writeIndexListCacheClearAfterMutation(b, indexPrefixes)
	fprintf(b, "\treturn nil\n}\n\n")
}

func writeRedisCachedAfterCommitMethods(b *bytes.Buffer, table SQLTable, typeName, cachedName string) {
	pk := primaryColumn(table)
	pkArg := modelArgName(pk.Name)
	pkField := modelFieldName(pk.Name)
	indexPrefixes := modelIndexPrefixes(table)
	fprintf(b, "func (c *%s) afterInsertCommit(ctx context.Context, in *entity.%s) error {\n", cachedName, typeName)
	fprintf(b, "\tif c.cache != nil && in != nil {\n\t\tif err := c.cache.Set(ctx, in.%s, in); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n", pkField)
	writeRedisIndexListCacheBumpAfterMutation(b, indexPrefixes, typeName)
	fprintf(b, "\treturn nil\n}\n\n")
	fprintf(b, "func (c *%s) afterUpdateCommit(ctx context.Context, in *entity.%s) error {\n", cachedName, typeName)
	fprintf(b, "\tif c.cache != nil && in != nil {\n\t\tif err := c.cache.Invalidate(ctx, in.%s); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n", pkField)
	writeRedisIndexListCacheBumpAfterMutation(b, indexPrefixes, typeName)
	fprintf(b, "\treturn nil\n}\n\n")
	fprintf(b, "func (c *%s) afterUpdateFieldsCommit(ctx context.Context, %s %s) error {\n", cachedName, pkArg, columnGoType(pk))
	fprintf(b, "\tif c.cache != nil {\n\t\tif err := c.cache.Invalidate(ctx, %s); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n", pkArg)
	writeRedisIndexListCacheBumpAfterMutation(b, indexPrefixes, typeName)
	fprintf(b, "\treturn nil\n}\n\n")
	fprintf(b, "func (c *%s) afterDeleteCommit(ctx context.Context, %s %s) error {\n", cachedName, pkArg, columnGoType(pk))
	fprintf(b, "\tif c.cache != nil {\n\t\tif err := c.cache.Invalidate(ctx, %s); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n", pkArg)
	writeRedisIndexListCacheBumpAfterMutation(b, indexPrefixes, typeName)
	fprintf(b, "\treturn nil\n}\n\n")
}

func writeConsistentCachedUpdateFields(b *bytes.Buffer, table SQLTable, typeName, cachedName string, indexes []modelUniqueIndex) {
	pk := primaryColumn(table)
	pkArg := modelArgName(pk.Name)
	fprintf(b, "func (c *%s) UpdateFields(ctx context.Context, %s %s, fields map[string]any) error {\n", cachedName, pkArg, columnGoType(pk))
	fprintf(b, "\treturn c.UpdateFieldsWithInvalidate(ctx, %s, fields)\n}\n\n", pkArg)
	fprintf(b, "func (c *%s) UpdateFieldsWithInvalidate(ctx context.Context, %s %s, fields map[string]any) error {\n", cachedName, pkArg, columnGoType(pk))
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" cached repo is nil")
	fprintf(b, "\tif len(fields) == 0 {\n\t\treturn nil\n\t}\n")
	fprintf(b, "\tvar old *entity.%s\n", typeName)
	if len(indexes) > 0 {
		fprintf(b, "\told, _ = c.repo.FindOne(ctx, %s)\n", pkArg)
	}
	fprintf(b, "\tif err := c.repo.UpdateFields(ctx, %s, fields); err != nil {\n\t\treturn err\n\t}\n", pkArg)
	fprintf(b, "\treturn c.deferOrRunAfterCommit(ctx, func(ctx context.Context) error {\n\t\treturn c.afterUpdateFieldsCommit(ctx, %s, old)\n\t})\n}\n\n", pkArg)
}

func writeConsistentCachedUpdateWithVersion(b *bytes.Buffer, table SQLTable, typeName, cachedName string, indexes []modelUniqueIndex) {
	version, ok := versionColumn(table)
	if !ok {
		return
	}
	pk := primaryColumn(table)
	pkField := modelFieldName(pk.Name)
	fprintf(b, "func (c *%s) UpdateWithVersion(ctx context.Context, in *entity.%s, expectedVersion %s) error {\n", cachedName, typeName, columnGoType(version))
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" cached repo is nil")
	fprintf(b, "\tvar old *entity.%s\n", typeName)
	if len(indexes) > 0 {
		fprintf(b, "\tif in != nil {\n\t\told, _ = c.repo.FindOne(ctx, in.%s)\n\t}\n", pkField)
	}
	fprintf(b, "\tif err := c.repo.UpdateWithVersion(ctx, in, expectedVersion); err != nil {\n\t\treturn err\n\t}\n")
	fprintf(b, "\tif in != nil {\n\t\tin.%s = expectedVersion + 1\n\t}\n", modelFieldName(version.Name))
	fprintf(b, "\treturn c.deferOrRunAfterCommit(ctx, func(ctx context.Context) error {\n\t\treturn c.afterUpdateCommit(ctx, in, old)\n\t})\n}\n\n")
}

func writeRedisCachedUpdateFields(b *bytes.Buffer, table SQLTable, typeName, cachedName string) {
	pk := primaryColumn(table)
	pkArg := modelArgName(pk.Name)
	fprintf(b, "func (c *%s) UpdateFields(ctx context.Context, %s %s, fields map[string]any) error {\n", cachedName, pkArg, columnGoType(pk))
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" redis cached repo is nil")
	fprintf(b, "\tif len(fields) == 0 {\n\t\treturn nil\n\t}\n")
	fprintf(b, "\tif err := c.repo.UpdateFields(ctx, %s, fields); err != nil {\n\t\treturn err\n\t}\n", pkArg)
	fprintf(b, "\treturn c.deferOrRunAfterCommit(ctx, func(ctx context.Context) error {\n\t\treturn c.afterUpdateFieldsCommit(ctx, %s)\n\t})\n}\n\n", pkArg)
}

func writeRedisCachedUpdateWithVersion(b *bytes.Buffer, table SQLTable, typeName, cachedName string) {
	version, ok := versionColumn(table)
	if !ok {
		return
	}
	fprintf(b, "func (c *%s) UpdateWithVersion(ctx context.Context, in *entity.%s, expectedVersion %s) error {\n", cachedName, typeName, columnGoType(version))
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" redis cached repo is nil")
	fprintf(b, "\tif err := c.repo.UpdateWithVersion(ctx, in, expectedVersion); err != nil {\n\t\treturn err\n\t}\n")
	fprintf(b, "\treturn c.deferOrRunAfterCommit(ctx, func(ctx context.Context) error {\n\t\treturn c.afterUpdateCommit(ctx, in)\n\t})\n}\n\n")
}

func writeUniqueModelCacheInitializers(b *bytes.Buffer, indexes []modelUniqueIndex, typeName, repoValue string, redis bool) {
	if redis {
		return
	}
	for _, index := range indexes {
		fieldName := uniqueCacheFieldName(index.Columns)
		finderName := uniqueFinderName(index.Columns)
		keyPrefix := uniqueCachePrefix(index.Columns)
		fprintf(b, "\tout.%s = cache.NewModel(func(ctx context.Context, key string) (*entity.%s, error) {\n", fieldName, typeName)
		fprintf(b, "\t\treturn nil, cache.ErrNotFound\n")
		fprintf(b, "\t}, cache.WithModelKeyPrefix[*entity.%s, string](%q))\n", typeName, keyPrefix)
		fprintf(b, "\t_ = %s.FindBy%s\n", repoValue, finderName)
	}
}

func writeUniqueCachedFinders(b *bytes.Buffer, indexes []modelUniqueIndex, typeName, cachedName string, redis bool) {
	if redis {
		return
	}
	for _, index := range indexes {
		columns := index.Columns
		fieldName := uniqueCacheFieldName(columns)
		finderName := uniqueFinderName(columns)
		params := uniqueFinderParams(columns)
		args := uniqueFinderArgs(columns)
		keyExpr := uniqueCacheKeyCall(columns, args)
		fprintf(b, "func (c *%s) FindBy%sCached(ctx context.Context, %s) (*entity.%s, error) {\n", cachedName, finderName, params, typeName)
		fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn nil, errors.New(%q)\n\t}\n", lowerCamel(typeName)+" cached repo is nil")
		fprintf(b, "\tkey := %s\n", keyExpr)
		fprintf(b, "\tif c.%s == nil || c.%s.Cache() == nil {\n\t\treturn c.repo.FindBy%s(ctx, %s)\n\t}\n", fieldName, fieldName, finderName, args)
		fprintf(b, "\tif cached, ok := c.%s.Cache().Get(key); ok {\n\t\treturn cached, nil\n\t}\n", fieldName)
		fprintf(b, "\tout, err := c.repo.FindBy%s(ctx, %s)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n", finderName, args)
		fprintf(b, "\tc.%s.Cache().Set(key, out)\n\treturn out, nil\n}\n\n", fieldName)
	}
}

func writeIndexListCacheInitializers(b *bytes.Buffer, indexes []modelIndexPrefix, typeName, repoValue string) {
	for _, index := range indexes {
		fieldName := indexListCacheFieldName(index.Columns)
		countFieldName := indexCountCacheFieldName(index.Columns)
		finderName := uniqueFinderName(index.Columns)
		cachePrefix := indexListCachePrefix(index.Columns)
		fprintf(b, "\tout.%s = cache.New[[]entity.%s](cache.WithName[[]entity.%s](%q))\n", fieldName, typeName, typeName, "list:"+cachePrefix)
		fprintf(b, "\tout.%s = cache.New[int64](cache.WithName[int64](%q))\n", countFieldName, "count:"+cachePrefix)
		fprintf(b, "\t_ = %s.FindBy%s\n", repoValue, finderName)
		fprintf(b, "\t_ = %s.CountBy%s\n", repoValue, finderName)
	}
}

func writeRedisIndexListCacheInitializers(b *bytes.Buffer, indexes []modelIndexPrefix, typeName, repoValue string) {
	versionFunc := redisIndexListVersionValueFuncName(typeName)
	for _, index := range indexes {
		fieldName := indexListCacheFieldName(index.Columns)
		countFieldName := indexCountCacheFieldName(index.Columns)
		versionFieldName := indexListVersionFieldName(index.Columns)
		finderName := uniqueFinderName(index.Columns)
		cachePrefix := indexListCachePrefix(index.Columns)
		fprintf(b, "\tout.%s = cache.NewRedisModel(func(ctx context.Context, key string) ([]entity.%s, error) {\n", fieldName, typeName)
		fprintf(b, "\t\treturn nil, cache.ErrNotFound\n")
		fprintf(b, "\t}, client, cache.WithRedisModelNotFound[[]entity.%s, string](redis.ErrNil), cache.WithRedisModelKeyPrefix[[]entity.%s, string](%q))\n", typeName, typeName, "list:"+cachePrefix)
		fprintf(b, "\tout.%s = cache.NewRedisModel(func(ctx context.Context, key string) (int64, error) {\n", countFieldName)
		fprintf(b, "\t\treturn 0, cache.ErrNotFound\n")
		fprintf(b, "\t}, client, cache.WithRedisModelNotFound[int64, string](redis.ErrNil), cache.WithRedisModelKeyPrefix[int64, string](%q))\n", "count:"+cachePrefix)
		fprintf(b, "\tout.%s = cache.NewRedisModel(func(ctx context.Context, key string) (string, error) {\n", versionFieldName)
		fprintf(b, "\t\tversion := %s()\n", versionFunc)
		fprintf(b, "\t\treturn version, out.%s.Set(ctx, key, version)\n", versionFieldName)
		fprintf(b, "\t}, client, cache.WithRedisModelNotFound[string, string](redis.ErrNil), cache.WithRedisModelKeyPrefix[string, string](%q))\n", "list-version:"+cachePrefix)
		fprintf(b, "\t_ = %s.FindBy%s\n", repoValue, finderName)
		fprintf(b, "\t_ = %s.CountBy%s\n", repoValue, finderName)
	}
}

func writeIndexListCachedFinders(b *bytes.Buffer, indexes []modelIndexPrefix, typeName, cachedName string) {
	for _, index := range indexes {
		columns := index.Columns
		fieldName := indexListCacheFieldName(columns)
		countFieldName := indexCountCacheFieldName(columns)
		finderName := uniqueFinderName(columns)
		params := uniqueFinderParams(columns)
		args := uniqueFinderArgs(columns)
		if args != "" {
			args += ", "
		}
		keyExpr := indexListCacheKeyCall(columns, args+"limit, offset")
		countKeyExpr := indexCountCacheKeyCall(columns, strings.TrimSuffix(args, ", "))
		finderArgs := strings.TrimSuffix(args, ", ")
		fprintf(b, "func (c *%s) FindBy%sCached(ctx context.Context, %s, limit int, offset int) ([]entity.%s, error) {\n", cachedName, finderName, params, typeName)
		fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn nil, errors.New(%q)\n\t}\n", lowerCamel(typeName)+" cached repo is nil")
		fprintf(b, "\tkey := %s\n", keyExpr)
		fprintf(b, "\tif c.%s == nil {\n\t\treturn c.repo.FindBy%s(ctx, %s, limit, offset)\n\t}\n", fieldName, finderName, finderArgs)
		fprintf(b, "\tif cached, ok := c.%s.Get(key); ok {\n\t\treturn cached, nil\n\t}\n", fieldName)
		fprintf(b, "\tout, err := c.repo.FindBy%s(ctx, %s, limit, offset)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n", finderName, finderArgs)
		fprintf(b, "\tc.%s.Set(key, out)\n\treturn out, nil\n}\n\n", fieldName)
		fprintf(b, "func (c *%s) CountBy%sCached(ctx context.Context, %s) (int64, error) {\n", cachedName, finderName, params)
		fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn 0, errors.New(%q)\n\t}\n", lowerCamel(typeName)+" cached repo is nil")
		fprintf(b, "\tkey := %s\n", countKeyExpr)
		fprintf(b, "\tif c.%s == nil {\n\t\treturn c.repo.CountBy%s(ctx, %s)\n\t}\n", countFieldName, finderName, finderArgs)
		fprintf(b, "\tif cached, ok := c.%s.Get(key); ok {\n\t\treturn cached, nil\n\t}\n", countFieldName)
		fprintf(b, "\ttotal, err := c.repo.CountBy%s(ctx, %s)\n\tif err != nil {\n\t\treturn 0, err\n\t}\n", finderName, finderArgs)
		fprintf(b, "\tc.%s.Set(key, total)\n\treturn total, nil\n}\n\n", countFieldName)
		fprintf(b, "func (c *%s) PageBy%sCached(ctx context.Context, %s, limit int, offset int) ([]entity.%s, int64, error) {\n", cachedName, finderName, params, typeName)
		fprintf(b, "\titems, err := c.FindBy%sCached(ctx, %s, limit, offset)\n\tif err != nil {\n\t\treturn nil, 0, err\n\t}\n", finderName, finderArgs)
		fprintf(b, "\ttotal, err := c.CountBy%sCached(ctx, %s)\n\tif err != nil {\n\t\treturn nil, 0, err\n\t}\n", finderName, finderArgs)
		fprintf(b, "\treturn items, total, nil\n}\n\n")
	}
}

func writeRedisIndexListCachedFinders(b *bytes.Buffer, indexes []modelIndexPrefix, typeName, cachedName string) {
	cacheKeyFunc := redisIndexListCacheKeyFuncName(typeName)
	for _, index := range indexes {
		columns := index.Columns
		fieldName := indexListCacheFieldName(columns)
		countFieldName := indexCountCacheFieldName(columns)
		versionFieldName := indexListVersionFieldName(columns)
		finderName := uniqueFinderName(columns)
		params := uniqueFinderParams(columns)
		args := uniqueFinderArgs(columns)
		if args != "" {
			args += ", "
		}
		baseKeyExpr := indexListCacheKeyCall(columns, args+"limit, offset")
		baseCountKeyExpr := indexCountCacheKeyCall(columns, strings.TrimSuffix(args, ", "))
		finderArgs := strings.TrimSuffix(args, ", ")
		fprintf(b, "func (c *%s) FindBy%sCached(ctx context.Context, %s, limit int, offset int) ([]entity.%s, error) {\n", cachedName, finderName, params, typeName)
		fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn nil, errors.New(%q)\n\t}\n", lowerCamel(typeName)+" redis cached repo is nil")
		fprintf(b, "\tif c.%s == nil || c.%s == nil {\n\t\treturn c.repo.FindBy%s(ctx, %s, limit, offset)\n\t}\n", fieldName, versionFieldName, finderName, finderArgs)
		fprintf(b, "\tversion, err := c.%s.Get(ctx, \"current\")\n\tif err != nil {\n\t\treturn nil, err\n\t}\n", versionFieldName)
		fprintf(b, "\tkey := %s(version, %s)\n", cacheKeyFunc, baseKeyExpr)
		fprintf(b, "\tout, err := c.%s.Get(ctx, key)\n\tif err == nil {\n\t\treturn out, nil\n\t}\n", fieldName)
		fprintf(b, "\tif !errors.Is(err, cache.ErrNotFound) {\n\t\treturn nil, err\n\t}\n")
		fprintf(b, "\tout, err = c.repo.FindBy%s(ctx, %s, limit, offset)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n", finderName, finderArgs)
		fprintf(b, "\tif err := c.%s.Set(ctx, key, out); err != nil {\n\t\treturn nil, err\n\t}\n", fieldName)
		fprintf(b, "\treturn out, nil\n}\n\n")
		fprintf(b, "func (c *%s) CountBy%sCached(ctx context.Context, %s) (int64, error) {\n", cachedName, finderName, params)
		fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn 0, errors.New(%q)\n\t}\n", lowerCamel(typeName)+" redis cached repo is nil")
		fprintf(b, "\tif c.%s == nil || c.%s == nil {\n\t\treturn c.repo.CountBy%s(ctx, %s)\n\t}\n", countFieldName, versionFieldName, finderName, finderArgs)
		fprintf(b, "\tversion, err := c.%s.Get(ctx, \"current\")\n\tif err != nil {\n\t\treturn 0, err\n\t}\n", versionFieldName)
		fprintf(b, "\tkey := %s(version, %s)\n", cacheKeyFunc, baseCountKeyExpr)
		fprintf(b, "\ttotal, err := c.%s.Get(ctx, key)\n\tif err == nil {\n\t\treturn total, nil\n\t}\n", countFieldName)
		fprintf(b, "\tif !errors.Is(err, cache.ErrNotFound) {\n\t\treturn 0, err\n\t}\n")
		fprintf(b, "\ttotal, err = c.repo.CountBy%s(ctx, %s)\n\tif err != nil {\n\t\treturn 0, err\n\t}\n", finderName, finderArgs)
		fprintf(b, "\tif err := c.%s.Set(ctx, key, total); err != nil {\n\t\treturn 0, err\n\t}\n", countFieldName)
		fprintf(b, "\treturn total, nil\n}\n\n")
		fprintf(b, "func (c *%s) PageBy%sCached(ctx context.Context, %s, limit int, offset int) ([]entity.%s, int64, error) {\n", cachedName, finderName, params, typeName)
		fprintf(b, "\titems, err := c.FindBy%sCached(ctx, %s, limit, offset)\n\tif err != nil {\n\t\treturn nil, 0, err\n\t}\n", finderName, finderArgs)
		fprintf(b, "\ttotal, err := c.CountBy%sCached(ctx, %s)\n\tif err != nil {\n\t\treturn nil, 0, err\n\t}\n", finderName, finderArgs)
		fprintf(b, "\treturn items, total, nil\n}\n\n")
	}
}

func writeUniqueCacheSetAfterMutation(b *bytes.Buffer, indexes []modelUniqueIndex, redis bool) {
	if redis {
		return
	}
	for _, index := range indexes {
		fieldName := uniqueCacheFieldName(index.Columns)
		keyExpr := uniqueCacheKeyFromEntityCall(index.Columns, "in")
		fprintf(b, "\tif c.%s != nil && c.%s.Cache() != nil && in != nil {\n\t\tc.%s.Cache().Set(%s, in)\n\t}\n", fieldName, fieldName, fieldName, keyExpr)
	}
}

func writeUniqueCacheInvalidateAfterMutation(b *bytes.Buffer, indexes []modelUniqueIndex, valueName string, redis bool) {
	if redis {
		return
	}
	for _, index := range indexes {
		fieldName := uniqueCacheFieldName(index.Columns)
		keyExpr := uniqueCacheKeyFromEntityCall(index.Columns, valueName)
		fprintf(b, "\tif c.%s != nil && c.%s.Cache() != nil && %s != nil {\n\t\tc.%s.Cache().Delete(%s)\n\t}\n", fieldName, fieldName, valueName, fieldName, keyExpr)
	}
}

func writeIndexListCacheClearAfterMutation(b *bytes.Buffer, indexes []modelIndexPrefix) {
	for _, index := range indexes {
		fieldName := indexListCacheFieldName(index.Columns)
		countFieldName := indexCountCacheFieldName(index.Columns)
		fprintf(b, "\tif c.%s != nil {\n\t\tc.%s.Clear()\n\t}\n", fieldName, fieldName)
		fprintf(b, "\tif c.%s != nil {\n\t\tc.%s.Clear()\n\t}\n", countFieldName, countFieldName)
	}
}

func writeRedisIndexListCacheBumpAfterMutation(b *bytes.Buffer, indexes []modelIndexPrefix, typeName string) {
	versionFunc := redisIndexListVersionValueFuncName(typeName)
	for _, index := range indexes {
		fieldName := indexListVersionFieldName(index.Columns)
		fprintf(b, "\tif c.%s != nil {\n\t\tif err := c.%s.Set(ctx, \"current\", %s()); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n", fieldName, fieldName, versionFunc)
	}
}

func writeGORMWhereMethods(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	fprintf(b, "func (r *%s) FindWhere(ctx context.Context, where any, args ...any) ([]entity.%s, error) {\n", receiverName, typeName)
	fprintf(b, "\tdb, err := r.dbWithContext(ctx)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	fprintf(b, "\tout := make([]entity.%s, 0)\n", typeName)
	if hasSoftDelete(table) {
		fprintf(b, "\tdb = db.Where(%q)\n", table.SoftDeleteColumn+" IS NULL")
	}
	fprintf(b, "\tif where != nil {\n\t\tdb = db.Where(where, args...)\n\t}\n")
	fprintf(b, "\tif err := db.Find(&out).Error; err != nil {\n\t\treturn nil, err\n\t}\n")
	fprintf(b, "\treturn out, nil\n}\n\n")
	fprintf(b, "func (r *%s) CountWhere(ctx context.Context, where any, args ...any) (int64, error) {\n", receiverName)
	fprintf(b, "\tdb, err := r.dbWithContext(ctx)\n\tif err != nil {\n\t\treturn 0, err\n\t}\n")
	if hasSoftDelete(table) {
		fprintf(b, "\tdb = db.Where(%q)\n", table.SoftDeleteColumn+" IS NULL")
	}
	fprintf(b, "\tif where != nil {\n\t\tdb = db.Where(where, args...)\n\t}\n")
	fprintf(b, "\tvar count int64\n")
	fprintf(b, "\tif err := db.Model(&entity.%s{}).Count(&count).Error; err != nil {\n\t\treturn 0, err\n\t}\n", typeName)
	fprintf(b, "\treturn count, nil\n}\n\n")
}

func modelEntityImport(module string) string {
	module = strings.Trim(strings.TrimSpace(module), "/")
	if module == "" {
		module = "github.com/imajinyun/gofly"
	}
	return module + "/model/entity"
}

func ParseSQLModels(content string) ([]SQLTable, error) {
	content = stripSQLComments(content)
	matches := createTableStartRE.FindAllStringSubmatchIndex(content, -1)
	if len(matches) == 0 {
		return nil, errors.New("no create table statement found")
	}
	tables := make([]SQLTable, 0, len(matches))
	for _, match := range matches {
		name := content[match[2]:match[3]]
		bodyStart := match[1]
		body, err := readBalancedSQLBody(content, bodyStart)
		if err != nil {
			return nil, err
		}
		table, err := parseSQLTable(name, body)
		if err != nil {
			return nil, err
		}
		tables = append(tables, table)
	}
	return tables, nil
}

func readBalancedSQLBody(content string, start int) (string, error) {
	depth := 1
	for i := start; i < len(content); i++ {
		switch content[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return content[start:i], nil
			}
		}
	}
	return "", errors.New("create table statement is not closed")
}

// kept for backward compatibility with existing tests; generates a single-file blob
func GenerateModelCode(tables []SQLTable, packageName string) ([]byte, error) {
	if len(tables) == 0 {
		return nil, errors.New("model table is required")
	}
	if packageName == "" {
		packageName = "model"
	}
	var b bytes.Buffer
	fprintf(&b, "package %s\n\n", packageName)
	fprintf(&b, "import (\n")
	fprintf(&b, "\t\"context\"\n")
	fprintf(&b, "\t\"database/sql\"\n")
	fprintf(&b, "\t\"errors\"\n")
	if modelsNeedTime(tables) || tablesHaveSoftDelete(tables) {
		fprintf(&b, "\t\"time\"\n")
	}
	fprintf(&b, "\n\t\"github.com/imajinyun/gofly/cache\"\n")
	fprintf(&b, "\t\"github.com/imajinyun/gofly/core/storage\"\n")
	fprintf(&b, ")\n\n")
	fprintf(&b, "type Tabler interface {\n\tTableName() string\n}\n\n")
	for _, table := range tables {
		writeSQLModel(&b, table)
	}
	out, err := format.Source(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format generated model code: %w", err)
	}
	return out, nil
}

func parseSQLTable(name string, body string) (SQLTable, error) {
	table := SQLTable{Name: name}
	parts := splitSQLDefinitions(body)
	uniqueColumns := make(map[string]struct{})
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lower := strings.ToLower(part)
		if strings.HasPrefix(lower, "primary key") {
			table.PrimaryKey = parseTablePrimaryKey(part)
			continue
		}
		if strings.HasPrefix(lower, "unique ") || (strings.HasPrefix(lower, "constraint ") && strings.Contains(lower, " unique ")) {
			columns := parseUniqueIndexColumns(part)
			if len(columns) == 1 {
				uniqueColumns[columns[0]] = struct{}{}
			} else if len(columns) > 1 {
				table.UniqueIndexes = append(table.UniqueIndexes, SQLUniqueIndex{Columns: columns})
			}
			continue
		}
		if strings.HasPrefix(lower, "key ") || strings.HasPrefix(lower, "index ") {
			if columns := parseSQLIndexColumns(part); len(columns) > 0 {
				table.Indexes = append(table.Indexes, SQLIndex{Columns: columns})
			}
			continue
		}
		if strings.HasPrefix(lower, "constraint ") {
			continue
		}
		column := parseSQLColumn(part)
		if column.Name == "" || column.Type == "" {
			continue
		}
		if column.PrimaryKey {
			table.PrimaryKey = column.Name
		}
		table.Columns = append(table.Columns, column)
	}
	if len(table.Columns) == 0 {
		return SQLTable{}, fmt.Errorf("table %s has no columns", name)
	}
	if table.PrimaryKey == "" {
		table.PrimaryKey = table.Columns[0].Name
	}
	for i := range table.Columns {
		if table.Columns[i].Name == table.PrimaryKey {
			table.Columns[i].PrimaryKey = true
		}
		if _, ok := uniqueColumns[table.Columns[i].Name]; ok {
			table.Columns[i].Unique = true
		}
	}
	table.UniqueIndexes = filterUniqueIndexes(table.UniqueIndexes, table.Columns)
	table.SoftDeleteColumn = detectSoftDeleteColumn(table.Columns)
	table.Indexes = filterSQLIndexes(table.Indexes, table.Columns, table.PrimaryKey)
	return table, nil
}

func parseSQLColumn(def string) SQLColumn {
	fields := strings.Fields(def)
	if len(fields) < 2 {
		return SQLColumn{}
	}
	name := cleanSQLIdent(fields[0])
	typeName := strings.ToLower(fields[1])
	lower := strings.ToLower(def)
	return SQLColumn{
		Name:       name,
		Type:       typeName,
		PrimaryKey: strings.Contains(lower, "primary key"),
		Nullable:   !strings.Contains(lower, "not null") && !strings.Contains(lower, "primary key"),
		Unique:     strings.Contains(lower, " unique"),
	}
}

func parseUniqueIndexColumns(def string) []string {
	return parseSQLIndexColumns(def)
}

func parseSQLIndexColumns(def string) []string {
	start := strings.Index(def, "(")
	end := strings.LastIndex(def, ")")
	if start < 0 || end <= start+1 {
		return nil
	}
	rawColumns := strings.Split(def[start+1:end], ",")
	columns := make([]string, 0, len(rawColumns))
	for _, raw := range rawColumns {
		fields := strings.Fields(strings.TrimSpace(raw))
		if len(fields) == 0 {
			continue
		}
		column := cleanSQLIndexColumnIdent(fields[0])
		if column != "" {
			columns = append(columns, column)
		}
	}
	return columns
}

func cleanSQLIndexColumnIdent(value string) string {
	value = cleanSQLIdent(value)
	if idx := strings.Index(value, "("); idx >= 0 {
		value = value[:idx]
	}
	return cleanSQLIdent(value)
}

func parseTablePrimaryKey(def string) string {
	start := strings.Index(def, "(")
	end := strings.Index(def, ")")
	if start < 0 || end <= start+1 {
		return ""
	}
	return cleanSQLIdent(strings.Split(def[start+1:end], ",")[0])
}

func splitSQLDefinitions(body string) []string {
	var parts []string
	var b strings.Builder
	depth := 0
	for _, r := range body {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				parts = append(parts, b.String())
				b.Reset()
				continue
			}
		}
		b.WriteRune(r)
	}
	if strings.TrimSpace(b.String()) != "" {
		parts = append(parts, b.String())
	}
	return parts
}

func stripSQLComments(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

func cleanSQLIdent(value string) string {
	return strings.Trim(strings.TrimSpace(value), "`\"")
}

func writeSQLModel(b *bytes.Buffer, table SQLTable) {
	typeName := exportName(singularize(table.Name))
	modelName := typeName + "Model"
	fprintf(b, "const %sTable = %q\n\n", lowerCamel(typeName), table.Name)
	fprintf(b, "var %sColumns = []string{%s}\n\n", lowerCamel(typeName), quotedColumnList(table.Columns))
	fprintf(b, "type %s struct {\n", typeName)
	for _, column := range table.Columns {
		fieldName := modelFieldName(column.Name)
		fprintf(b, "\t%s %s `db:%q json:%q`\n", fieldName, columnGoType(column), column.Name, lowerCamel(column.Name))
	}
	fprintf(b, "}\n\n")
	fprintf(b, "var _ Tabler = (*%s)(nil)\n\n", typeName)
	fprintf(b, "func (%s) TableName() string { return %sTable }\n\n", typeName, lowerCamel(typeName))
	fprintf(b, "type %s struct {\n\tstore *storage.SQLStore\n\tdialect storage.Dialect\n}\n\n", modelName)
	fprintf(b, "func New%s(store *storage.SQLStore, dialect ...storage.Dialect) *%s {\n", modelName, modelName)
	fprintf(b, "\td := storage.DialectQuestion\n\tif len(dialect) > 0 {\n\t\td = dialect[0]\n\t}\n\treturn &%s{store: store, dialect: d}\n}\n\n", modelName)
	fprintf(b, "func NewCached%s(model *%s, opts ...cache.ModelOption[*%s, %s]) *cache.ModelCache[*%s, %s] {\n", modelName, modelName, typeName, primaryKeyType(table), typeName, primaryKeyType(table))
	fprintf(b, "\treturn cache.NewModel(model.FindOne, opts...)\n}\n\n")
	fprintf(b, "func (m *%s) TableName() string { return %sTable }\n\n", modelName, lowerCamel(typeName))
	fprintf(b, "func (m *%s) Columns() []string { return append([]string(nil), %sColumns...) }\n\n", modelName, lowerCamel(typeName))
	writeLegacyFindOne(b, table, typeName, modelName)
	writeLegacyInsert(b, table, typeName, modelName)
	writeLegacyUpdate(b, table, typeName, modelName)
	writeLegacyDelete(b, table, typeName, modelName)
	writeLegacyList(b, table, typeName, modelName)
	writeLegacyCount(b, table, typeName, modelName)
}

func writeLegacyFindOne(b *bytes.Buffer, table SQLTable, typeName, modelName string) {
	pk := primaryColumn(table)
	fprintf(b, "func (m *%s) FindOne(ctx context.Context, %s %s) (*%s, error) {\n", modelName, modelArgName(pk.Name), columnGoType(pk), typeName)
	if hasSoftDelete(table) {
		fprintf(b, "\tcolumns, err := storage.JoinIdentifiers(%sColumns)\n", lowerCamel(typeName))
		fprintf(b, "\tif err != nil {\n\t\treturn nil, err\n\t}\n")
		fprintf(b, "\tquery := \"SELECT \" + columns + \" FROM \" + %sTable + \" WHERE %s = \" + storage.Placeholder(m.dialect, 1) + \" AND %s IS NULL LIMIT 1\"\n", lowerCamel(typeName), pk.Name, table.SoftDeleteColumn)
	} else {
		fprintf(b, "\tquery, err := storage.SelectByID(%sTable, %sColumns, %q, m.dialect)\n", lowerCamel(typeName), lowerCamel(typeName), pk.Name)
		fprintf(b, "\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	}
	fprintf(b, "\tvar out %s\n", typeName)
	fprintf(b, "\tif err := m.store.QueryOne(ctx, query, func(row *sql.Row) error {\n\t\treturn row.Scan(%s)\n\t}, %s); err != nil {\n", scanArgs("out", table.Columns), modelArgName(pk.Name))
	fprintf(b, "\t\tif errors.Is(err, sql.ErrNoRows) {\n\t\t\treturn nil, storage.ErrNotFound\n\t\t}\n\t\treturn nil, err\n\t}\n")
	fprintf(b, "\treturn &out, nil\n}\n\n")
}

func writeLegacyInsert(b *bytes.Buffer, table SQLTable, typeName, modelName string) {
	fprintf(b, "func (m *%s) Insert(ctx context.Context, in *%s) error {\n", modelName, typeName)
	fprintf(b, "\tif in == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" is nil")
	fprintf(b, "\tquery, err := storage.Insert(%sTable, %sColumns, m.dialect)\n", lowerCamel(typeName), lowerCamel(typeName))
	fprintf(b, "\tif err != nil {\n\t\treturn err\n\t}\n")
	fprintf(b, "\tif _, err := m.store.Exec(ctx, query, %s); err != nil {\n\t\treturn err\n\t}\n", valueArgs("in", table.Columns))
	fprintf(b, "\treturn nil\n}\n\n")
}

func writeLegacyUpdate(b *bytes.Buffer, table SQLTable, typeName, modelName string) {
	pk := primaryColumn(table)
	columns := updateColumns(table)
	fprintf(b, "func (m *%s) Update(ctx context.Context, in *%s) error {\n", modelName, typeName)
	fprintf(b, "\tif in == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" is nil")
	fprintf(b, "\tquery, err := storage.UpdateByID(%sTable, []string{%s}, %q, m.dialect)\n", lowerCamel(typeName), quotedColumnList(columns), pk.Name)
	fprintf(b, "\tif err != nil {\n\t\treturn err\n\t}\n")
	if hasSoftDelete(table) {
		fprintf(b, "\tquery += \" AND %s IS NULL\"\n", table.SoftDeleteColumn)
	}
	fprintf(b, "\tif _, err := m.store.Exec(ctx, query, %s, in.%s); err != nil {\n\t\treturn err\n\t}\n", valueArgs("in", columns), modelFieldName(pk.Name))
	fprintf(b, "\treturn nil\n}\n\n")
}

func writeLegacyDelete(b *bytes.Buffer, table SQLTable, typeName, modelName string) {
	pk := primaryColumn(table)
	fprintf(b, "func (m *%s) Delete(ctx context.Context, %s %s) error {\n", modelName, modelArgName(pk.Name), columnGoType(pk))
	if hasSoftDelete(table) {
		fprintf(b, "\tquery := \"UPDATE \" + %sTable + \" SET %s = \" + storage.Placeholder(m.dialect, 1) + \" WHERE %s = \" + storage.Placeholder(m.dialect, 2) + \" AND %s IS NULL\"\n", lowerCamel(typeName), table.SoftDeleteColumn, pk.Name, table.SoftDeleteColumn)
		fprintf(b, "\tif _, err := m.store.Exec(ctx, query, %s, %s); err != nil {\n\t\treturn err\n\t}\n", softDeleteValueExpr(table), modelArgName(pk.Name))
	} else {
		fprintf(b, "\tquery, err := storage.DeleteByID(%sTable, %q, m.dialect)\n", lowerCamel(typeName), pk.Name)
		fprintf(b, "\tif err != nil {\n\t\treturn err\n\t}\n")
		fprintf(b, "\tif _, err := m.store.Exec(ctx, query, %s); err != nil {\n\t\treturn err\n\t}\n", modelArgName(pk.Name))
	}
	fprintf(b, "\treturn nil\n}\n\n")
}

func writeLegacyList(b *bytes.Buffer, table SQLTable, typeName, modelName string) {
	pk := primaryColumn(table)
	fprintf(b, "func (m *%s) List(ctx context.Context, limit int, offset int) ([]%s, error) {\n", modelName, typeName)
	if hasSoftDelete(table) {
		fprintf(b, "\tcolumns, err := storage.JoinIdentifiers(%sColumns)\n", lowerCamel(typeName))
		fprintf(b, "\tif err != nil {\n\t\treturn nil, err\n\t}\n")
		fprintf(b, "\tquery := \"SELECT \" + columns + \" FROM \" + %sTable + \" WHERE %s IS NULL ORDER BY %s LIMIT \" + storage.Placeholder(m.dialect, 1) + \" OFFSET \" + storage.Placeholder(m.dialect, 2)\n", lowerCamel(typeName), table.SoftDeleteColumn, pk.Name)
	} else {
		fprintf(b, "\tquery, err := storage.SelectPage(%sTable, %sColumns, %q, m.dialect)\n", lowerCamel(typeName), lowerCamel(typeName), pk.Name)
		fprintf(b, "\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	}
	fprintf(b, "\tout := make([]%s, 0)\n", typeName)
	fprintf(b, "\tif err := m.store.QueryAll(ctx, query, func(rows *sql.Rows) error {\n")
	fprintf(b, "\t\tfor rows.Next() {\n\t\t\tvar item %s\n\t\t\tif err := rows.Scan(%s); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t\tout = append(out, item)\n\t\t}\n\t\treturn nil\n\t}, limit, offset); err != nil {\n\t\treturn nil, err\n\t}\n", typeName, scanArgs("item", table.Columns))
	fprintf(b, "\treturn out, nil\n}\n\n")
}

func writeLegacyCount(b *bytes.Buffer, table SQLTable, typeName, modelName string) {
	fprintf(b, "func (m *%s) Count(ctx context.Context) (int64, error) {\n", modelName)
	if hasSoftDelete(table) {
		fprintf(b, "\tquery := \"SELECT COUNT(*) FROM \" + %sTable + \" WHERE %s IS NULL\"\n", lowerCamel(typeName), table.SoftDeleteColumn)
	} else {
		fprintf(b, "\tquery, err := storage.CountAll(%sTable)\n", lowerCamel(typeName))
		fprintf(b, "\tif err != nil {\n\t\treturn 0, err\n\t}\n")
	}
	fprintf(b, "\tvar count int64\n")
	fprintf(b, "\tif err := m.store.QueryOne(ctx, query, func(row *sql.Row) error {\n\t\treturn row.Scan(&count)\n\t}); err != nil {\n\t\treturn 0, err\n\t}\n")
	fprintf(b, "\treturn count, nil\n}\n\n")
}

func writeFindOne(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	pk := primaryColumn(table)
	fprintf(b, "func (r *%s) FindOne(ctx context.Context, %s %s) (*entity.%s, error) {\n", receiverName, modelArgName(pk.Name), columnGoType(pk), typeName)
	if hasSoftDelete(table) {
		fprintf(b, "\tcolumns, err := storage.JoinIdentifiers(entity.%sColumns)\n", typeName)
		fprintf(b, "\tif err != nil {\n\t\treturn nil, err\n\t}\n")
		fprintf(b, "\tquery := \"SELECT \" + columns + \" FROM \" + entity.%sTable + \" WHERE %s = \" + storage.Placeholder(r.dialect, 1) + \" AND %s IS NULL LIMIT 1\"\n", typeName, pk.Name, table.SoftDeleteColumn)
	} else {
		fprintf(b, "\tquery, err := storage.SelectByID(entity.%sTable, entity.%sColumns, %q, r.dialect)\n", typeName, typeName, pk.Name)
		fprintf(b, "\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	}
	fprintf(b, "\tvar out entity.%s\n", typeName)
	fprintf(b, "\tif err := r.queryOne(ctx, query, func(row *sql.Row) error {\n\t\treturn row.Scan(%s)\n\t}, %s); err != nil {\n", scanArgs("out", table.Columns), modelArgName(pk.Name))
	fprintf(b, "\t\tif errors.Is(err, sql.ErrNoRows) {\n\t\t\treturn nil, storage.ErrNotFound\n\t\t}\n\t\treturn nil, err\n\t}\n")
	fprintf(b, "\treturn &out, nil\n}\n\n")
}

func writeInsert(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	fprintf(b, "func (r *%s) Insert(ctx context.Context, in *entity.%s) error {\n", receiverName, typeName)
	fprintf(b, "\tif in == nil {\n\t\treturn errors.New(\"%s is nil\")\n\t}\n", lowerCamel(typeName))
	fprintf(b, "\tquery, err := storage.Insert(entity.%sTable, entity.%sColumns, r.dialect)\n", typeName, typeName)
	fprintf(b, "\tif err != nil {\n\t\treturn err\n\t}\n")
	fprintf(b, "\tif _, err := r.exec(ctx, query, %s); err != nil {\n\t\treturn err\n\t}\n", valueArgs("in", table.Columns))
	fprintf(b, "\treturn nil\n}\n\n")
}

func writeUpdate(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	pk := primaryColumn(table)
	columns := updateColumns(table)
	fprintf(b, "func (r *%s) Update(ctx context.Context, in *entity.%s) error {\n", receiverName, typeName)
	fprintf(b, "\tif in == nil {\n\t\treturn errors.New(\"%s is nil\")\n\t}\n", lowerCamel(typeName))
	fprintf(b, "\tquery, err := storage.UpdateByID(entity.%sTable, []string{%s}, %q, r.dialect)\n", typeName, quotedColumnList(columns), pk.Name)
	fprintf(b, "\tif err != nil {\n\t\treturn err\n\t}\n")
	if hasSoftDelete(table) {
		fprintf(b, "\tquery += \" AND %s IS NULL\"\n", table.SoftDeleteColumn)
	}
	fprintf(b, "\tif _, err := r.exec(ctx, query, %s, in.%s); err != nil {\n\t\treturn err\n\t}\n", valueArgs("in", columns), modelFieldName(pk.Name))
	fprintf(b, "\treturn nil\n}\n\n")
}

func writeDelete(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	pk := primaryColumn(table)
	fprintf(b, "func (r *%s) Delete(ctx context.Context, %s %s) error {\n", receiverName, modelArgName(pk.Name), columnGoType(pk))
	if hasSoftDelete(table) {
		fprintf(b, "\tquery := \"UPDATE \" + entity.%sTable + \" SET %s = \" + storage.Placeholder(r.dialect, 1) + \" WHERE %s = \" + storage.Placeholder(r.dialect, 2) + \" AND %s IS NULL\"\n", typeName, table.SoftDeleteColumn, pk.Name, table.SoftDeleteColumn)
		fprintf(b, "\tif _, err := r.exec(ctx, query, %s, %s); err != nil {\n\t\treturn err\n\t}\n", softDeleteValueExpr(table), modelArgName(pk.Name))
	} else {
		fprintf(b, "\tquery, err := storage.DeleteByID(entity.%sTable, %q, r.dialect)\n", typeName, pk.Name)
		fprintf(b, "\tif err != nil {\n\t\treturn err\n\t}\n")
		fprintf(b, "\tif _, err := r.exec(ctx, query, %s); err != nil {\n\t\treturn err\n\t}\n", modelArgName(pk.Name))
	}
	fprintf(b, "\treturn nil\n}\n\n")
}

func writeList(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	pk := primaryColumn(table)
	fprintf(b, "func (r *%s) List(ctx context.Context, limit int, offset int) ([]entity.%s, error) {\n", receiverName, typeName)
	if hasSoftDelete(table) {
		fprintf(b, "\tcolumns, err := storage.JoinIdentifiers(entity.%sColumns)\n", typeName)
		fprintf(b, "\tif err != nil {\n\t\treturn nil, err\n\t}\n")
		fprintf(b, "\tquery := \"SELECT \" + columns + \" FROM \" + entity.%sTable + \" WHERE %s IS NULL ORDER BY %s LIMIT \" + storage.Placeholder(r.dialect, 1) + \" OFFSET \" + storage.Placeholder(r.dialect, 2)\n", typeName, table.SoftDeleteColumn, pk.Name)
	} else {
		fprintf(b, "\tquery, err := storage.SelectPage(entity.%sTable, entity.%sColumns, %q, r.dialect)\n", typeName, typeName, pk.Name)
		fprintf(b, "\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	}
	fprintf(b, "\tout := make([]entity.%s, 0)\n", typeName)
	fprintf(b, "\tif err := r.queryAll(ctx, query, func(rows *sql.Rows) error {\n")
	fprintf(b, "\t\tfor rows.Next() {\n\t\t\tvar item entity.%s\n\t\t\tif err := rows.Scan(%s); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t\tout = append(out, item)\n\t\t}\n\t\treturn nil\n\t}, limit, offset); err != nil {\n\t\treturn nil, err\n\t}\n", typeName, scanArgs("item", table.Columns))
	fprintf(b, "\treturn out, nil\n}\n\n")
}

func writeCount(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	fprintf(b, "func (r *%s) Count(ctx context.Context) (int64, error) {\n", receiverName)
	if hasSoftDelete(table) {
		fprintf(b, "\tquery := \"SELECT COUNT(*) FROM \" + entity.%sTable + \" WHERE %s IS NULL\"\n", typeName, table.SoftDeleteColumn)
	} else {
		fprintf(b, "\tquery, err := storage.CountAll(entity.%sTable)\n", typeName)
		fprintf(b, "\tif err != nil {\n\t\treturn 0, err\n\t}\n")
	}
	fprintf(b, "\tvar count int64\n")
	fprintf(b, "\tif err := r.queryOne(ctx, query, func(row *sql.Row) error {\n\t\treturn row.Scan(&count)\n\t}); err != nil {\n\t\treturn 0, err\n\t}\n")
	fprintf(b, "\treturn count, nil\n}\n\n")
}

func writeGORMFindOne(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	pk := primaryColumn(table)
	fprintf(b, "func (r *%s) FindOne(ctx context.Context, %s %s) (*entity.%s, error) {\n", receiverName, modelArgName(pk.Name), columnGoType(pk), typeName)
	fprintf(b, "\tdb, err := r.dbWithContext(ctx)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	fprintf(b, "\tvar out entity.%s\n", typeName)
	if hasSoftDelete(table) {
		fprintf(b, "\tdb = db.Where(%q)\n", table.SoftDeleteColumn+" IS NULL")
	}
	fprintf(b, "\tif err := db.Where(%q, %s).First(&out).Error; err != nil {\n\t\tif errors.Is(err, gorm.ErrRecordNotFound) {\n\t\t\treturn nil, storage.ErrNotFound\n\t\t}\n\t\treturn nil, err\n\t}\n", pk.Name+" = ?", modelArgName(pk.Name))
	fprintf(b, "\treturn &out, nil\n}\n\n")
}

func writeGORMInsert(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	fprintf(b, "func (r *%s) Insert(ctx context.Context, in *entity.%s) error {\n", receiverName, typeName)
	fprintf(b, "\tif in == nil {\n\t\treturn errors.New(\"%s is nil\")\n\t}\n", lowerCamel(typeName))
	fprintf(b, "\tdb, err := r.dbWithContext(ctx)\n\tif err != nil {\n\t\treturn err\n\t}\n")
	fprintf(b, "\treturn db.Create(in).Error\n}\n\n")
}

func writeGORMUpdate(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	pk := primaryColumn(table)
	columns := updateColumns(table)
	fprintf(b, "func (r *%s) Update(ctx context.Context, in *entity.%s) error {\n", receiverName, typeName)
	fprintf(b, "\tif in == nil {\n\t\treturn errors.New(\"%s is nil\")\n\t}\n", lowerCamel(typeName))
	if len(columns) == 0 {
		fprintf(b, "\treturn errors.New(\"update columns are required\")\n}\n\n")
		return
	}
	fprintf(b, "\tdb, err := r.dbWithContext(ctx)\n\tif err != nil {\n\t\treturn err\n\t}\n")
	if hasSoftDelete(table) {
		fprintf(b, "\tdb = db.Where(%q)\n", table.SoftDeleteColumn+" IS NULL")
	}
	fprintf(b, "\treturn db.Model(&entity.%s{}).Where(%q, in.%s).Updates(map[string]any{%s}).Error\n}\n\n", typeName, pk.Name+" = ?", modelFieldName(pk.Name), gormUpdateMap("in", columns))
}

func writeGORMDelete(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	pk := primaryColumn(table)
	fprintf(b, "func (r *%s) Delete(ctx context.Context, %s %s) error {\n", receiverName, modelArgName(pk.Name), columnGoType(pk))
	fprintf(b, "\tdb, err := r.dbWithContext(ctx)\n\tif err != nil {\n\t\treturn err\n\t}\n")
	if hasSoftDelete(table) {
		fprintf(b, "\treturn db.Model(&entity.%s{}).Where(%q, %s).Where(%q).Update(%q, %s).Error\n}\n\n", typeName, pk.Name+" = ?", modelArgName(pk.Name), table.SoftDeleteColumn+" IS NULL", table.SoftDeleteColumn, softDeleteValueExpr(table))
		return
	}
	fprintf(b, "\treturn db.Where(%q, %s).Delete(&entity.%s{}).Error\n}\n\n", pk.Name+" = ?", modelArgName(pk.Name), typeName)
}

func writeGORMList(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	pk := primaryColumn(table)
	fprintf(b, "func (r *%s) List(ctx context.Context, limit int, offset int) ([]entity.%s, error) {\n", receiverName, typeName)
	fprintf(b, "\tdb, err := r.dbWithContext(ctx)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	fprintf(b, "\tout := make([]entity.%s, 0)\n", typeName)
	if hasSoftDelete(table) {
		fprintf(b, "\tdb = db.Where(%q)\n", table.SoftDeleteColumn+" IS NULL")
	}
	fprintf(b, "\tif err := db.Order(%q).Limit(limit).Offset(offset).Find(&out).Error; err != nil {\n\t\treturn nil, err\n\t}\n", pk.Name+" ASC")
	fprintf(b, "\treturn out, nil\n}\n\n")
}

func writeGORMCount(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	fprintf(b, "func (r *%s) Count(ctx context.Context) (int64, error) {\n", receiverName)
	fprintf(b, "\tdb, err := r.dbWithContext(ctx)\n\tif err != nil {\n\t\treturn 0, err\n\t}\n")
	if hasSoftDelete(table) {
		fprintf(b, "\tdb = db.Where(%q)\n", table.SoftDeleteColumn+" IS NULL")
	}
	fprintf(b, "\tvar count int64\n")
	fprintf(b, "\tif err := db.Model(&entity.%s{}).Count(&count).Error; err != nil {\n\t\treturn 0, err\n\t}\n", typeName)
	fprintf(b, "\treturn count, nil\n}\n\n")
}

func writeAdvancedGORMRepoMethods(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	pk := primaryColumn(table)
	for _, column := range table.Columns {
		if !column.Unique || column.PrimaryKey {
			continue
		}
		fprintf(b, "func (r *%s) FindBy%s(ctx context.Context, %s %s) (*entity.%s, error) {\n", receiverName, modelFieldName(column.Name), modelArgName(column.Name), columnGoType(column), typeName)
		fprintf(b, "\tdb, err := r.dbWithContext(ctx)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n")
		fprintf(b, "\tvar out entity.%s\n", typeName)
		if hasSoftDelete(table) {
			fprintf(b, "\tdb = db.Where(%q)\n", table.SoftDeleteColumn+" IS NULL")
		}
		fprintf(b, "\tif err := db.Where(%q, %s).First(&out).Error; err != nil {\n\t\tif errors.Is(err, gorm.ErrRecordNotFound) {\n\t\t\treturn nil, storage.ErrNotFound\n\t\t}\n\t\treturn nil, err\n\t}\n\treturn &out, nil\n}\n\n", column.Name+" = ?", modelArgName(column.Name))
	}
	writeCompositeGORMUniqueFinders(b, table, typeName, receiverName)
	writeGORMIndexListFinders(b, table, typeName, receiverName)
	writeGORMFindByIDs(b, table, typeName, receiverName)
	fprintf(b, "func (r *%s) InsertMany(ctx context.Context, items []*entity.%s) error {\n", receiverName, typeName)
	fprintf(b, "\tdb, err := r.dbWithContext(ctx)\n\tif err != nil {\n\t\treturn err\n\t}\n\treturn db.Create(&items).Error\n}\n\n")
	fprintf(b, "func (r *%s) UpdateMany(ctx context.Context, items []*entity.%s) error {\n", receiverName, typeName)
	fprintf(b, "\tfor _, item := range items {\n\t\tif err := r.Update(ctx, item); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n\treturn nil\n}\n\n")
	fprintf(b, "func (r *%s) DeleteMany(ctx context.Context, ids ...%s) error {\n", receiverName, columnGoType(pk))
	fprintf(b, "\tfor _, id := range ids {\n\t\tif err := r.Delete(ctx, id); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n\treturn nil\n}\n\n")
	fprintf(b, "func (r *%s) UpdateFields(ctx context.Context, %s %s, fields map[string]any) error {\n", receiverName, modelArgName(pk.Name), columnGoType(pk))
	fprintf(b, "\tif len(fields) == 0 {\n\t\treturn nil\n\t}\n")
	fprintf(b, "\tdb, err := r.dbWithContext(ctx)\n\tif err != nil {\n\t\treturn err\n\t}\n")
	if hasSoftDelete(table) {
		fprintf(b, "\tdb = db.Where(%q)\n", table.SoftDeleteColumn+" IS NULL")
	}
	fprintf(b, "\tallowed := map[string]struct{}{%s}\n", columnSetLiteral(updateColumns(table)))
	fprintf(b, "\tfor column := range fields {\n\t\tif _, ok := allowed[column]; !ok {\n\t\t\treturn errors.New(\"field is not updatable: \" + column)\n\t\t}\n\t}\n")
	fprintf(b, "\treturn db.Model(&entity.%s{}).Where(%q, %s).Updates(fields).Error\n}\n\n", typeName, pk.Name+" = ?", modelArgName(pk.Name))
	if version, ok := versionColumn(table); ok {
		fprintf(b, "func (r *%s) UpdateWithVersion(ctx context.Context, in *entity.%s, expectedVersion %s) error {\n", receiverName, typeName, columnGoType(version))
		fprintf(b, "\tif in == nil {\n\t\treturn errors.New(\"%s is nil\")\n\t}\n", lowerCamel(typeName))
		fprintf(b, "\tdb, err := r.dbWithContext(ctx)\n\tif err != nil {\n\t\treturn err\n\t}\n")
		if hasSoftDelete(table) {
			fprintf(b, "\tdb = db.Where(%q)\n", table.SoftDeleteColumn+" IS NULL")
		}
		updates := gormUpdateMap("in", updateColumnsExcept(table, version.Name))
		if updates != "" {
			updates += ", "
		}
		fprintf(b, "\tupdates := map[string]any{%s%q: expectedVersion + 1}\n", updates, version.Name)
		fprintf(b, "\tresult := db.Model(&entity.%s{}).Where(%q, in.%s).Where(%q, expectedVersion).Updates(updates)\n", typeName, pk.Name+" = ?", modelFieldName(pk.Name), version.Name+" = ?")
		fprintf(b, "\tif result.Error != nil {\n\t\treturn result.Error\n\t}\n\tif result.RowsAffected == 0 {\n\t\treturn gorm.ErrRecordNotFound\n\t}\n\treturn nil\n}\n\n")
	}
	fprintf(b, "func (r *%s) ListAfter(ctx context.Context, after %s, limit int) ([]entity.%s, error) {\n", receiverName, columnGoType(pk), typeName)
	fprintf(b, "\tdb, err := r.dbWithContext(ctx)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	fprintf(b, "\tout := make([]entity.%s, 0)\n", typeName)
	if hasSoftDelete(table) {
		fprintf(b, "\tdb = db.Where(%q)\n", table.SoftDeleteColumn+" IS NULL")
	}
	fprintf(b, "\tif err := db.Where(%q, after).Order(%q).Limit(limit).Find(&out).Error; err != nil {\n\t\treturn nil, err\n\t}\n\treturn out, nil\n}\n\n", pk.Name+" > ?", pk.Name+" ASC")
}

func writeGORMFindByIDs(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	pk := primaryColumn(table)
	pkArg := "ids"
	pkField := modelFieldName(pk.Name)
	fprintf(b, "func (r *%s) FindByIDs(ctx context.Context, %s []%s) ([]entity.%s, error) {\n", receiverName, pkArg, columnGoType(pk), typeName)
	fprintf(b, "\tif len(%s) == 0 {\n\t\treturn []entity.%s{}, nil\n\t}\n", pkArg, typeName)
	fprintf(b, "\tdb, err := r.dbWithContext(ctx)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	if hasSoftDelete(table) {
		fprintf(b, "\tdb = db.Where(%q)\n", table.SoftDeleteColumn+" IS NULL")
	}
	fprintf(b, "\tout := make([]entity.%s, 0, len(%s))\n", typeName, pkArg)
	fprintf(b, "\tif err := db.Where(%q, %s).Order(%q).Find(&out).Error; err != nil {\n\t\treturn nil, err\n\t}\n", pk.Name+" IN ?", pkArg, pk.Name+" ASC")
	fprintf(b, "\tfound := make(map[%s]entity.%s, len(out))\n", columnGoType(pk), typeName)
	fprintf(b, "\tfor _, item := range out {\n\t\tfound[item.%s] = item\n\t}\n", pkField)
	fprintf(b, "\tordered := make([]entity.%s, 0, len(found))\n", typeName)
	fprintf(b, "\tfor _, id := range %s {\n\t\tif item, ok := found[id]; ok {\n\t\t\tordered = append(ordered, item)\n\t\t}\n\t}\n", pkArg)
	fprintf(b, "\treturn ordered, nil\n}\n\n")
}

func writeGORMIndexListFinders(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	for _, index := range modelIndexPrefixes(table) {
		name := uniqueFinderName(index.Columns)
		fprintf(b, "func (r *%s) FindBy%s(ctx context.Context, %s, limit int, offset int) ([]entity.%s, error) {\n", receiverName, name, uniqueFinderParams(index.Columns), typeName)
		fprintf(b, "\tdb, err := r.dbWithContext(ctx)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n")
		fprintf(b, "\tout := make([]entity.%s, 0)\n", typeName)
		if hasSoftDelete(table) {
			fprintf(b, "\tdb = db.Where(%q)\n", table.SoftDeleteColumn+" IS NULL")
		}
		for _, column := range index.Columns {
			fprintf(b, "\tdb = db.Where(%q, %s)\n", column.Name+" = ?", modelArgName(column.Name))
		}
		for _, column := range index.OrderColumns {
			fprintf(b, "\tdb = db.Order(%q)\n", column.Name+" ASC")
		}
		fprintf(b, "\tif err := db.Limit(limit).Offset(offset).Find(&out).Error; err != nil {\n\t\treturn nil, err\n\t}\n\treturn out, nil\n}\n\n")
		fprintf(b, "func (r *%s) CountBy%s(ctx context.Context, %s) (int64, error) {\n", receiverName, name, uniqueFinderParams(index.Columns))
		fprintf(b, "\tdb, err := r.dbWithContext(ctx)\n\tif err != nil {\n\t\treturn 0, err\n\t}\n")
		if hasSoftDelete(table) {
			fprintf(b, "\tdb = db.Where(%q)\n", table.SoftDeleteColumn+" IS NULL")
		}
		for _, column := range index.Columns {
			fprintf(b, "\tdb = db.Where(%q, %s)\n", column.Name+" = ?", modelArgName(column.Name))
		}
		fprintf(b, "\tvar count int64\n")
		fprintf(b, "\tif err := db.Model(&entity.%s{}).Count(&count).Error; err != nil {\n\t\treturn 0, err\n\t}\n", typeName)
		fprintf(b, "\treturn count, nil\n}\n\n")
	}
}

func writeCompositeGORMUniqueFinders(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	for _, index := range table.UniqueIndexes {
		columns, ok := uniqueIndexColumns(table, index)
		if !ok {
			continue
		}
		fprintf(b, "func (r *%s) FindBy%s(ctx context.Context, %s) (*entity.%s, error) {\n", receiverName, uniqueFinderName(columns), uniqueFinderParams(columns), typeName)
		fprintf(b, "\tdb, err := r.dbWithContext(ctx)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n")
		fprintf(b, "\tvar out entity.%s\n", typeName)
		if hasSoftDelete(table) {
			fprintf(b, "\tdb = db.Where(%q)\n", table.SoftDeleteColumn+" IS NULL")
		}
		fprintf(b, "\tif err := db.Where(%q, %s).First(&out).Error; err != nil {\n", gormUniqueWhere(columns), uniqueFinderArgs(columns))
		fprintf(b, "\t\tif errors.Is(err, gorm.ErrRecordNotFound) {\n\t\t\treturn nil, storage.ErrNotFound\n\t\t}\n\t\treturn nil, err\n\t}\n")
		fprintf(b, "\treturn &out, nil\n}\n\n")
	}
}

func primaryColumn(table SQLTable) SQLColumn {
	for _, column := range table.Columns {
		if column.Name == table.PrimaryKey {
			return column
		}
	}
	return table.Columns[0]
}

func hasSoftDelete(table SQLTable) bool {
	return strings.TrimSpace(table.SoftDeleteColumn) != ""
}

func tablesHaveSoftDelete(tables []SQLTable) bool {
	for _, table := range tables {
		if hasSoftDelete(table) {
			return true
		}
	}
	return false
}

func softDeleteColumn(table SQLTable) SQLColumn {
	for _, column := range table.Columns {
		if column.Name == table.SoftDeleteColumn {
			return column
		}
	}
	return SQLColumn{}
}

func softDeleteValueExpr(table SQLTable) string {
	column := softDeleteColumn(table)
	switch strings.TrimPrefix(columnGoType(column), "*") {
	case "int", "int64", "int32", "uint", "uint64", "uint32":
		return "time.Now().Unix()"
	default:
		return "time.Now().UTC()"
	}
}

func primaryKeyType(table SQLTable) string {
	return columnGoType(primaryColumn(table))
}

func quotedColumnList(columns []SQLColumn) string {
	items := make([]string, 0, len(columns))
	for _, column := range columns {
		items = append(items, fmt.Sprintf("%q", column.Name))
	}
	return strings.Join(items, ", ")
}

func nonPrimaryColumns(table SQLTable) []SQLColumn {
	columns := make([]SQLColumn, 0, len(table.Columns))
	for _, column := range table.Columns {
		if column.Name != table.PrimaryKey {
			columns = append(columns, column)
		}
	}
	return columns
}

func updateColumns(table SQLTable) []SQLColumn {
	columns := make([]SQLColumn, 0, len(table.Columns))
	for _, column := range table.Columns {
		if column.Name == table.PrimaryKey || column.Name == table.SoftDeleteColumn {
			continue
		}
		columns = append(columns, column)
	}
	return columns
}

func updateColumnsExcept(table SQLTable, excluded ...string) []SQLColumn {
	excludes := make(map[string]struct{}, len(excluded))
	for _, column := range excluded {
		excludes[column] = struct{}{}
	}
	columns := updateColumns(table)
	out := columns[:0]
	for _, column := range columns {
		if _, ok := excludes[column.Name]; ok {
			continue
		}
		out = append(out, column)
	}
	return out
}

func uniqueIndexColumns(table SQLTable, index SQLUniqueIndex) ([]SQLColumn, bool) {
	if len(index.Columns) < 2 {
		return nil, false
	}
	byName := make(map[string]SQLColumn, len(table.Columns))
	for _, column := range table.Columns {
		byName[column.Name] = column
	}
	columns := make([]SQLColumn, 0, len(index.Columns))
	for _, name := range index.Columns {
		column, ok := byName[name]
		if !ok {
			return nil, false
		}
		columns = append(columns, column)
	}
	return columns, true
}

func cacheableUniqueIndexes(table SQLTable) []modelUniqueIndex {
	indexes := make([]modelUniqueIndex, 0)
	for _, column := range table.Columns {
		if !column.Unique || column.PrimaryKey || !cacheableUniqueColumn(column) {
			continue
		}
		indexes = append(indexes, modelUniqueIndex{Columns: []SQLColumn{column}})
	}
	for _, index := range table.UniqueIndexes {
		columns, ok := uniqueIndexColumns(table, index)
		if !ok {
			continue
		}
		cacheable := true
		for _, column := range columns {
			if !cacheableUniqueColumn(column) {
				cacheable = false
				break
			}
		}
		if cacheable {
			indexes = append(indexes, modelUniqueIndex{Columns: columns})
		}
	}
	return indexes
}

func cacheableUniqueColumn(column SQLColumn) bool {
	return strings.TrimPrefix(columnGoType(column), "*") != "[]byte"
}

func modelUpsertIndexes(table SQLTable) []modelUniqueIndex {
	indexes := make([]modelUniqueIndex, 0)
	for _, column := range table.Columns {
		if column.Unique && !column.PrimaryKey {
			indexes = append(indexes, modelUniqueIndex{Columns: []SQLColumn{column}})
		}
	}
	for _, index := range table.UniqueIndexes {
		columns, ok := uniqueIndexColumns(table, index)
		if ok {
			indexes = append(indexes, modelUniqueIndex{Columns: columns})
		}
	}
	return indexes
}

func upsertUpdateColumns(table SQLTable, conflictColumns []SQLColumn) []SQLColumn {
	conflicts := make(map[string]struct{}, len(conflictColumns))
	for _, column := range conflictColumns {
		conflicts[column.Name] = struct{}{}
	}
	columns := updateColumns(table)
	out := columns[:0]
	for _, column := range columns {
		if _, ok := conflicts[column.Name]; ok {
			continue
		}
		out = append(out, column)
	}
	return out
}

func modelIndexPrefixes(table SQLTable) []modelIndexPrefix {
	if len(table.Indexes) == 0 {
		return nil
	}
	reserved := uniqueFinderNameSet(table)
	pk := primaryColumn(table)
	out := make([]modelIndexPrefix, 0, len(table.Indexes))
	seen := make(map[string]struct{}, len(table.Indexes))
	for _, index := range table.Indexes {
		columns, ok := indexColumns(table, index)
		if !ok {
			continue
		}
		filterCount := len(columns)
		if filterCount > 1 {
			filterCount--
		}
		filterColumns := append([]SQLColumn(nil), columns[:filterCount]...)
		if len(filterColumns) == 0 || filterColumns[0].Name == pk.Name || filterColumns[0].Name == table.SoftDeleteColumn {
			continue
		}
		name := uniqueFinderName(filterColumns)
		if _, ok := reserved[name]; ok {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		orderColumns := append([]SQLColumn(nil), columns[filterCount:]...)
		if !modelColumnsContain(orderColumns, pk.Name) {
			orderColumns = append(orderColumns, pk)
		}
		out = append(out, modelIndexPrefix{Columns: filterColumns, OrderColumns: orderColumns})
	}
	return out
}

func uniqueFinderNameSet(table SQLTable) map[string]struct{} {
	out := make(map[string]struct{})
	for _, column := range table.Columns {
		if column.Unique && !column.PrimaryKey {
			out[uniqueFinderName([]SQLColumn{column})] = struct{}{}
		}
	}
	for _, index := range table.UniqueIndexes {
		columns, ok := uniqueIndexColumns(table, index)
		if ok {
			out[uniqueFinderName(columns)] = struct{}{}
		}
	}
	return out
}

func indexColumns(table SQLTable, index SQLIndex) ([]SQLColumn, bool) {
	if len(index.Columns) == 0 {
		return nil, false
	}
	byName := make(map[string]SQLColumn, len(table.Columns))
	for _, column := range table.Columns {
		byName[column.Name] = column
	}
	columns := make([]SQLColumn, 0, len(index.Columns))
	for _, name := range index.Columns {
		column, ok := byName[name]
		if !ok {
			return nil, false
		}
		columns = append(columns, column)
	}
	return columns, true
}

func modelColumnsContain(columns []SQLColumn, name string) bool {
	for _, column := range columns {
		if column.Name == name {
			return true
		}
	}
	return false
}

func uniqueFinderName(columns []SQLColumn) string {
	parts := make([]string, 0, len(columns))
	for _, column := range columns {
		parts = append(parts, modelFieldName(column.Name))
	}
	return strings.Join(parts, "And")
}

func uniqueFinderParams(columns []SQLColumn) string {
	params := make([]string, 0, len(columns))
	for _, column := range columns {
		params = append(params, modelArgName(column.Name)+" "+columnGoType(column))
	}
	return strings.Join(params, ", ")
}

func uniqueFinderArgs(columns []SQLColumn) string {
	args := make([]string, 0, len(columns))
	for _, column := range columns {
		args = append(args, modelArgName(column.Name))
	}
	return strings.Join(args, ", ")
}

func uniqueFinderEntityArgs(columns []SQLColumn, receiver string) string {
	args := make([]string, 0, len(columns))
	for _, column := range columns {
		args = append(args, receiver+"."+modelFieldName(column.Name))
	}
	return strings.Join(args, ", ")
}

func uniqueCacheFieldName(columns []SQLColumn) string {
	return "cacheBy" + uniqueFinderName(columns)
}

func indexListCacheFieldName(columns []SQLColumn) string {
	return "listCacheBy" + uniqueFinderName(columns)
}

func indexCountCacheFieldName(columns []SQLColumn) string {
	return "countCacheBy" + uniqueFinderName(columns)
}

func indexListVersionFieldName(columns []SQLColumn) string {
	return "listVersionBy" + uniqueFinderName(columns)
}

func uniqueCachePrefix(columns []SQLColumn) string {
	names := make([]string, 0, len(columns))
	for _, column := range columns {
		names = append(names, column.Name)
	}
	return "by:" + strings.Join(names, ":")
}

func indexListCachePrefix(columns []SQLColumn) string {
	names := make([]string, 0, len(columns))
	for _, column := range columns {
		names = append(names, column.Name)
	}
	return "by:" + strings.Join(names, ":")
}

func uniqueCacheKeyCall(columns []SQLColumn, args string) string {
	return uniqueCacheKeyFuncName(columns) + "(" + args + ")"
}

func indexListCacheKeyCall(columns []SQLColumn, args string) string {
	return indexListCacheKeyFuncName(columns) + "(" + args + ")"
}

func indexCountCacheKeyCall(columns []SQLColumn, args string) string {
	return indexCountCacheKeyFuncName(columns) + "(" + args + ")"
}

func uniqueCacheKeyFromEntityCall(columns []SQLColumn, receiver string) string {
	args := make([]string, 0, len(columns))
	for _, column := range columns {
		args = append(args, receiver+"."+modelFieldName(column.Name))
	}
	return uniqueCacheKeyCall(columns, strings.Join(args, ", "))
}

func uniqueCacheKeyFuncName(columns []SQLColumn) string {
	return "uniqueKeyBy" + uniqueFinderName(columns)
}

func indexListCacheKeyFuncName(columns []SQLColumn) string {
	return "indexListKeyBy" + uniqueFinderName(columns)
}

func indexCountCacheKeyFuncName(columns []SQLColumn) string {
	return "indexCountKeyBy" + uniqueFinderName(columns)
}

func writeUniqueCacheKeyFuncs(b *bytes.Buffer, indexes []modelUniqueIndex) {
	for _, index := range indexes {
		columns := index.Columns
		fprintf(b, "func %s(%s) string {\n", uniqueCacheKeyFuncName(index.Columns), uniqueFinderParams(columns))
		parts := make([]string, 0, len(columns))
		for _, column := range columns {
			parts = append(parts, writeCacheKeyPart(b, column))
		}
		fprintf(b, "\treturn strings.Join([]string{%s}, \"|\")\n}\n\n", strings.Join(parts, ", "))
	}
}

func writeIndexListCacheKeyFuncs(b *bytes.Buffer, indexes []modelIndexPrefix, typeName string) {
	for _, index := range indexes {
		columns := index.Columns
		params := uniqueFinderParams(columns)
		listParams := params
		if listParams != "" {
			listParams += ", "
		}
		fprintf(b, "func %s(%slimit int, offset int) string {\n", indexListCacheKeyFuncName(columns), listParams)
		parts := make([]string, 0, len(columns)+2)
		for _, column := range columns {
			parts = append(parts, writeCacheKeyPart(b, column))
		}
		parts = append(parts, "\"limit=\"+strconv.Itoa(limit)", "\"offset=\"+strconv.Itoa(offset)")
		fprintf(b, "\treturn strings.Join([]string{%s}, \"|\")\n}\n\n", strings.Join(parts, ", "))
		fprintf(b, "func %s(%s) string {\n", indexCountCacheKeyFuncName(columns), params)
		countParts := make([]string, 0, len(columns))
		for _, column := range columns {
			countParts = append(countParts, writeCacheKeyPart(b, column))
		}
		fprintf(b, "\treturn strings.Join([]string{%s}, \"|\")\n}\n\n", strings.Join(countParts, ", "))
	}
	if len(indexes) > 0 {
		fprintf(b, "func %s() string {\n", redisIndexListVersionValueFuncName(typeName))
		fprintf(b, "\treturn strconv.FormatInt(time.Now().UnixNano(), 10)\n")
		fprintf(b, "}\n\n")
		fprintf(b, "func %s(version string, key string) string {\n", redisIndexListCacheKeyFuncName(typeName))
		fprintf(b, "\treturn version + \"|\" + key\n")
		fprintf(b, "}\n\n")
	}
}

func writeCacheKeyPart(b *bytes.Buffer, column SQLColumn) string {
	arg := modelArgName(column.Name)
	if !column.Nullable || column.PrimaryKey || !strings.HasPrefix(columnGoType(column), "*") {
		return "strconv.Quote(fmt.Sprint(" + arg + "))"
	}
	keyVar := arg + "CacheKey"
	fprintf(b, "\t%s := \"nil:%s\"\n", keyVar, column.Name)
	fprintf(b, "\tif %s != nil {\n", arg)
	fprintf(b, "\t\t%s = \"val:\" + strconv.Quote(fmt.Sprint(*%s))\n", keyVar, arg)
	fprintf(b, "\t}\n")
	return keyVar
}

func redisIndexListVersionValueFuncName(typeName string) string {
	return "redis" + exportName(typeName) + "IndexListVersionValue"
}

func redisIndexListCacheKeyFuncName(typeName string) string {
	return "redis" + exportName(typeName) + "IndexListCacheKey"
}

func gormUniqueWhere(columns []SQLColumn) string {
	parts := make([]string, 0, len(columns))
	for _, column := range columns {
		parts = append(parts, column.Name+" = ?")
	}
	return strings.Join(parts, " AND ")
}

func versionColumn(table SQLTable) (SQLColumn, bool) {
	for _, column := range table.Columns {
		if strings.EqualFold(column.Name, "version") {
			return column, true
		}
	}
	return SQLColumn{}, false
}

func columnSetLiteral(columns []SQLColumn) string {
	items := make([]string, 0, len(columns))
	for _, column := range columns {
		items = append(items, fmt.Sprintf("%q: {}", column.Name))
	}
	return strings.Join(items, ", ")
}

func scanArgs(receiver string, columns []SQLColumn) string {
	items := make([]string, 0, len(columns))
	for _, column := range columns {
		items = append(items, "&"+receiver+"."+modelFieldName(column.Name))
	}
	return strings.Join(items, ", ")
}

func valueArgs(receiver string, columns []SQLColumn) string {
	items := make([]string, 0, len(columns))
	for _, column := range columns {
		items = append(items, receiver+"."+modelFieldName(column.Name))
	}
	return strings.Join(items, ", ")
}

func gormUpdateMap(receiver string, columns []SQLColumn) string {
	items := make([]string, 0, len(columns))
	for _, column := range columns {
		items = append(items, fmt.Sprintf("%q: %s.%s", column.Name, receiver, modelFieldName(column.Name)))
	}
	return strings.Join(items, ", ")
}

func gormColumnTag(column SQLColumn) string {
	tag := "column:" + column.Name
	if column.PrimaryKey {
		tag += ";primaryKey"
	}
	return tag
}

func modelFieldName(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '_' || r == '-' || r == '.' || r == '/'
	})
	if len(parts) == 0 {
		return "X"
	}
	var b strings.Builder
	for _, part := range parts {
		if strings.EqualFold(part, "id") {
			b.WriteString("ID")
			continue
		}
		b.WriteString(exportName(part))
	}
	if b.Len() == 0 {
		return "X"
	}
	return b.String()
}

func modelArgName(name string) string {
	fieldName := modelFieldName(name)
	runes := []rune(fieldName)
	if len(runes) == 0 {
		return ""
	}
	if fieldName == "ID" {
		return "id"
	}
	runes[0] = unicode.ToLower(runes[0])
	arg := string(runes)
	if isGoKeyword(arg) {
		return arg + "Value"
	}
	return arg
}

func isGoKeyword(name string) bool {
	_, ok := goKeywords[name]
	return ok
}

var goKeywords = map[string]struct{}{
	"break":       {},
	"default":     {},
	"func":        {},
	"interface":   {},
	"select":      {},
	"case":        {},
	"defer":       {},
	"go":          {},
	"map":         {},
	"struct":      {},
	"chan":        {},
	"else":        {},
	"goto":        {},
	"package":     {},
	"switch":      {},
	"const":       {},
	"fallthrough": {},
	"if":          {},
	"range":       {},
	"type":        {},
	"continue":    {},
	"for":         {},
	"import":      {},
	"return":      {},
	"var":         {},
}

func modelsNeedTime(tables []SQLTable) bool {
	for _, table := range tables {
		for _, column := range table.Columns {
			if strings.TrimPrefix(columnGoType(column), "*") == "time.Time" {
				return true
			}
		}
	}
	return false
}

func columnGoType(column SQLColumn) string {
	typeName := strings.TrimSpace(column.GoType)
	if typeName == "" {
		typeName = sqlGoType(column.Type)
	}
	if column.Nullable && !column.PrimaryKey && typeName != "[]byte" {
		return "*" + typeName
	}
	return typeName
}

func applyModelTypesMap(tables []SQLTable, typesMap map[string]string) {
	if len(typesMap) == 0 {
		return
	}
	normalized := make(map[string]string, len(typesMap))
	for key, value := range typesMap {
		key = normalizeSQLTypeKey(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			normalized[key] = value
		}
	}
	if len(normalized) == 0 {
		return
	}
	for tableIndex := range tables {
		for columnIndex := range tables[tableIndex].Columns {
			key := normalizeSQLTypeKey(tables[tableIndex].Columns[columnIndex].Type)
			if goType := normalized[key]; goType != "" {
				tables[tableIndex].Columns[columnIndex].GoType = goType
			}
		}
	}
}

func normalizeSQLTypeKey(sqlType string) string {
	t := strings.ToLower(strings.TrimSpace(sqlType))
	t = strings.TrimSpace(strings.Split(t, "(")[0])
	return t
}

func sqlGoType(sqlType string) string {
	if goType, ok := sqlGoTypeKnown(sqlType); ok {
		return goType
	}
	return "string"
}

func sqlGoTypeKnown(sqlType string) (string, bool) {
	t := normalizeSQLTypeKey(sqlType)
	switch t {
	case "bigint", "int8", "serial8", "bigserial":
		return "int64", true
	case "int", "integer", "mediumint", "smallint", "tinyint", "serial", "int4", "int2":
		return "int", true
	case "bool", "boolean":
		return "bool", true
	case "float", "float4", "real":
		return "float32", true
	case "double", "float8", "decimal", "numeric":
		return "float64", true
	case "datetime", "timestamp", "timestamptz", "date", "time":
		return "time.Time", true
	case "blob", "binary", "varbinary", "bytea":
		return "[]byte", true
	case "char", "varchar", "text", "tinytext", "mediumtext", "longtext", "uuid", "json", "jsonb", "enum":
		return "string", true
	default:
		return "", false
	}
}

func singularize(name string) string {
	if strings.HasSuffix(name, "ies") && len(name) > 3 {
		return strings.TrimSuffix(name, "ies") + "y"
	}
	if strings.HasSuffix(name, "s") && len(name) > 1 {
		return strings.TrimSuffix(name, "s")
	}
	return name
}
