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

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
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
}

type SQLColumn struct {
	Name       string
	Type       string
	PrimaryKey bool
	Nullable   bool
	Unique     bool
	GoType     string
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
			module = "github.com/gofly/gofly"
		}
	}
	style := normalizeModelStyle(opts.Style)
	if err := writeModelFiles(tables, opts.Dir, pkg, module, style, opts.Cache); err != nil {
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
			module = "github.com/gofly/gofly"
		}
	}
	style := normalizeModelStyle(opts.Style)
	if err := writeModelFiles(tables, opts.Dir, pkg, module, style, opts.Cache); err != nil {
		return err
	}
	return ensureModelGoModDependencies(opts.Dir, style)
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
	return ensureGoModRequire(path, module, version)
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
		prepared.SoftDeleteColumn = detectSoftDeleteColumn(prepared.Columns)
		out = append(out, prepared)
	}
	return out, nil
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
		fprintf(&b, "\n\t\"github.com/gofly/gofly/cache\"\n")
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
		fprintf(&b, "\t\"github.com/gofly/gofly/cache\"\n")
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

func writeModelFiles(tables []SQLTable, dir string, pkg string, module string, style string, cacheEnabled bool) error {
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
		if err := writeRepoFile(repoDir, table, pkg, module, style, cacheEnabled); err != nil {
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

func writeRepoFile(dir string, table SQLTable, pkg string, module string, style string, cacheEnabled bool) error {
	if style == modelStyleGORM {
		return writeGORMRepoFile(dir, table, module, cacheEnabled)
	}
	typeName := exportName(singularize(table.Name))
	repoName := typeName + "Repo"
	var b bytes.Buffer
	fprintf(&b, "package repo\n\n")
	fprintf(&b, "import (\n")
	fprintf(&b, "\t\"context\"\n")
	fprintf(&b, "\t\"database/sql\"\n")
	fprintf(&b, "\t\"errors\"\n")
	fprintf(&b, "\t\"sort\"\n")
	fprintf(&b, "\t\"strings\"\n")
	if hasSoftDelete(table) {
		fprintf(&b, "\t\"time\"\n")
	}
	fprintf(&b, "\n")
	if cacheEnabled {
		fprintf(&b, "\t\"github.com/gofly/gofly/cache\"\n")
		fprintf(&b, "\t\"github.com/gofly/gofly/core/kv/redis\"\n")
	}
	fprintf(&b, "\t\"github.com/gofly/gofly/core/storage\"\n")
	fprintf(&b, "\t%q\n", modelEntityImport(module))
	fprintf(&b, ")\n\n")
	fprintf(&b, "type %s struct {\n\tstore *storage.SQLStore\n\ttx *sql.Tx\n\tdialect storage.Dialect\n}\n\n", repoName)
	fprintf(&b, "func New%s(store *storage.SQLStore, dialect ...storage.Dialect) *%s {\n", repoName, repoName)
	fprintf(&b, "\td := storage.DialectQuestion\n\tif len(dialect) > 0 {\n\t\td = dialect[0]\n\t}\n\treturn &%s{store: store, dialect: d}\n}\n\n", repoName)
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
		writeConsistentCachedRepo(&b, table, typeName, repoName)
		writeRedisCachedRepo(&b, table, typeName, repoName)
	}
	formatted, err := format.Source(b.Bytes())
	if err != nil {
		return fmt.Errorf("format repo file: %w", err)
	}
	filename := lowerSnake(singularize(table.Name)) + ".go"
	return writeGeneratedFile(filepath.Join(dir, filename), formatted)
}

func writeGORMRepoFile(dir string, table SQLTable, module string, cacheEnabled bool) error {
	typeName := exportName(singularize(table.Name))
	repoName := typeName + "Repo"
	var b bytes.Buffer
	fprintf(&b, "package repo\n\n")
	fprintf(&b, "import (\n")
	fprintf(&b, "\t\"context\"\n")
	fprintf(&b, "\t\"errors\"\n")
	if hasSoftDelete(table) {
		fprintf(&b, "\t\"time\"\n")
	}
	fprintf(&b, "\n")
	if cacheEnabled {
		fprintf(&b, "\t\"github.com/gofly/gofly/cache\"\n")
		fprintf(&b, "\t\"github.com/gofly/gofly/core/kv/redis\"\n")
	}
	fprintf(&b, "\t\"github.com/gofly/gofly/core/storage\"\n")
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
		writeRedisCachedRepo(&b, table, typeName, repoName)
	}
	formatted, err := format.Source(b.Bytes())
	if err != nil {
		return fmt.Errorf("format gorm repo file: %w", err)
	}
	filename := lowerSnake(singularize(table.Name)) + ".go"
	return writeGeneratedFile(filepath.Join(dir, filename), formatted)
}

func writeSQLRepoRuntimeHelpers(b *bytes.Buffer, repoName string) {
	fprintf(b, "func (r *%s) WithTx(tx *sql.Tx) *%s {\n", repoName, repoName)
	fprintf(b, "\tif r == nil {\n\t\treturn nil\n\t}\n")
	fprintf(b, "\tclone := *r\n\tclone.tx = tx\n\treturn &clone\n}\n\n")
	fprintf(b, "func (r *%s) Transact(ctx context.Context, opts *sql.TxOptions, fn func(context.Context, *%s) error) error {\n", repoName, repoName)
	fprintf(b, "\tif r == nil || r.store == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(strings.TrimSuffix(repoName, "Repo"))+" repo store is nil")
	fprintf(b, "\tif fn == nil {\n\t\treturn errors.New(\"transaction function is required\")\n\t}\n")
	fprintf(b, "\treturn r.store.Transact(ctx, opts, func(ctx context.Context, tx *sql.Tx) error {\n\t\treturn fn(ctx, r.WithTx(tx))\n\t})\n}\n\n")
	fprintf(b, "func (r *%s) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {\n", repoName)
	fprintf(b, "\tif r == nil {\n\t\treturn nil, errors.New(%q)\n\t}\n", lowerCamel(strings.TrimSuffix(repoName, "Repo"))+" repo is nil")
	fprintf(b, "\tif r.tx != nil {\n\t\treturn r.tx.ExecContext(ctx, query, args...)\n\t}\n")
	fprintf(b, "\tif r.store == nil {\n\t\treturn nil, errors.New(%q)\n\t}\n", lowerCamel(strings.TrimSuffix(repoName, "Repo"))+" repo store is nil")
	fprintf(b, "\treturn r.store.Exec(ctx, query, args...)\n}\n\n")
	fprintf(b, "func (r *%s) queryOne(ctx context.Context, query string, scan func(*sql.Row) error, args ...any) error {\n", repoName)
	fprintf(b, "\tif r == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(strings.TrimSuffix(repoName, "Repo"))+" repo is nil")
	fprintf(b, "\tif scan == nil {\n\t\treturn errors.New(\"scan function is required\")\n\t}\n")
	fprintf(b, "\tif r.tx != nil {\n\t\treturn scan(r.tx.QueryRowContext(ctx, query, args...))\n\t}\n")
	fprintf(b, "\tif r.store == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(strings.TrimSuffix(repoName, "Repo"))+" repo store is nil")
	fprintf(b, "\treturn r.store.QueryOne(ctx, query, scan, args...)\n}\n\n")
	fprintf(b, "func (r *%s) queryAll(ctx context.Context, query string, scan func(*sql.Rows) error, args ...any) error {\n", repoName)
	fprintf(b, "\tif r == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(strings.TrimSuffix(repoName, "Repo"))+" repo is nil")
	fprintf(b, "\tif scan == nil {\n\t\treturn errors.New(\"scan function is required\")\n\t}\n")
	fprintf(b, "\tif r.tx != nil {\n")
	fprintf(b, "\t\trows, err := r.tx.QueryContext(ctx, query, args...)\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n\t\tdefer rows.Close()\n")
	fprintf(b, "\t\tif err := scan(rows); err != nil {\n\t\t\treturn err\n\t\t}\n\t\treturn rows.Err()\n\t}\n")
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
	fprintf(b, "func (r *%s) InsertMany(ctx context.Context, items []*entity.%s) error {\n", receiverName, typeName)
	fprintf(b, "\tfor _, item := range items {\n\t\tif err := r.Insert(ctx, item); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n\treturn nil\n}\n\n")
	fprintf(b, "func (r *%s) UpdateMany(ctx context.Context, items []*entity.%s) error {\n", receiverName, typeName)
	fprintf(b, "\tfor _, item := range items {\n\t\tif err := r.Update(ctx, item); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n\treturn nil\n}\n\n")
	fprintf(b, "func (r *%s) DeleteMany(ctx context.Context, ids ...%s) error {\n", receiverName, columnGoType(pk))
	fprintf(b, "\tfor _, id := range ids {\n\t\tif err := r.Delete(ctx, id); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n\treturn nil\n}\n\n")
	writeSQLUpdateFields(b, table, typeName, receiverName)
	writeSQLOptimisticLock(b, table, typeName, receiverName)
	writeSQLCursorPage(b, table, typeName, receiverName)
}

func writeUniqueFinders(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	for _, column := range table.Columns {
		if !column.Unique || column.PrimaryKey {
			continue
		}
		fprintf(b, "func (r *%s) FindBy%s(ctx context.Context, %s %s) (*entity.%s, error) {\n", receiverName, modelFieldName(column.Name), lowerCamel(column.Name), columnGoType(column), typeName)
		fprintf(b, "\tcolumns, err := storage.JoinIdentifiers(entity.%sColumns)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n", typeName)
		fprintf(b, "\tquery := \"SELECT \" + columns + \" FROM \" + entity.%sTable + \" WHERE %s = \" + storage.Placeholder(r.dialect, 1)", typeName, column.Name)
		if hasSoftDelete(table) {
			fprintf(b, " + \" AND %s IS NULL\"", table.SoftDeleteColumn)
		}
		fprintf(b, " + \" LIMIT 1\"\n")
		fprintf(b, "\tvar out entity.%s\n", typeName)
		fprintf(b, "\tif err := r.queryOne(ctx, query, func(row *sql.Row) error {\n\t\treturn row.Scan(%s)\n\t}, %s); err != nil {\n", scanArgs("out", table.Columns), lowerCamel(column.Name))
		fprintf(b, "\t\tif errors.Is(err, sql.ErrNoRows) {\n\t\t\treturn nil, storage.ErrNotFound\n\t\t}\n\t\treturn nil, err\n\t}\n")
		fprintf(b, "\treturn &out, nil\n}\n\n")
	}
}

func writeSQLUpdateFields(b *bytes.Buffer, table SQLTable, typeName, receiverName string) {
	pk := primaryColumn(table)
	columns := updateColumns(table)
	fprintf(b, "func (r *%s) UpdateFields(ctx context.Context, %s %s, fields map[string]any) error {\n", receiverName, lowerCamel(pk.Name), columnGoType(pk))
	fprintf(b, "\tif len(fields) == 0 {\n\t\treturn nil\n\t}\n")
	fprintf(b, "\tallowed := map[string]struct{}{%s}\n", columnSetLiteral(columns))
	fprintf(b, "\tfieldNames := make([]string, 0, len(fields))\n")
	fprintf(b, "\tfor column := range fields {\n\t\tif _, ok := allowed[column]; !ok {\n\t\t\treturn errors.New(\"field is not updatable: \" + column)\n\t\t}\n\t\tfieldNames = append(fieldNames, column)\n\t}\n")
	fprintf(b, "\tsort.Strings(fieldNames)\n")
	fprintf(b, "\tsetParts := make([]string, 0, len(fieldNames))\n\targs := make([]any, 0, len(fieldNames)+1)\n\tidx := 1\n")
	fprintf(b, "\tfor _, column := range fieldNames {\n\t\tsetParts = append(setParts, column+\" = \"+storage.Placeholder(r.dialect, idx))\n\t\targs = append(args, fields[column])\n\t\tidx++\n\t}\n")
	fprintf(b, "\targs = append(args, %s)\n", lowerCamel(pk.Name))
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

func writeConsistentCachedRepo(b *bytes.Buffer, table SQLTable, typeName, repoName string) {
	pk := primaryColumn(table)
	cachedName := "Cached" + repoName
	pkArg := lowerCamel(pk.Name)
	pkField := modelFieldName(pk.Name)
	fprintf(b, "type %s struct {\n\trepo *%s\n\tcache *cache.ModelCache[*entity.%s, %s]\n}\n\n", cachedName, repoName, typeName, columnGoType(pk))
	fprintf(b, "func NewConsistentCached%s(repo *%s, opts ...cache.ModelOption[*entity.%s, %s]) *%s {\n", repoName, repoName, typeName, columnGoType(pk), cachedName)
	fprintf(b, "\tloader := func(ctx context.Context, id %s) (*entity.%s, error) {\n\t\tif repo == nil {\n\t\t\treturn nil, errors.New(%q)\n\t\t}\n\t\treturn repo.FindOne(ctx, id)\n\t}\n", columnGoType(pk), typeName, lowerCamel(typeName)+" repo is nil")
	fprintf(b, "\treturn &%s{repo: repo, cache: cache.NewModel(loader, opts...)}\n}\n\n", cachedName)
	fprintf(b, "func (c *%s) FindOne(ctx context.Context, %s %s) (*entity.%s, error) {\n", cachedName, lowerCamel(pk.Name), columnGoType(pk), typeName)
	fprintf(b, "\treturn c.FindByIDCached(ctx, %s)\n}\n\n", pkArg)
	fprintf(b, "func (c *%s) FindByIDCached(ctx context.Context, %s %s) (*entity.%s, error) {\n", cachedName, pkArg, columnGoType(pk), typeName)
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn nil, errors.New(%q)\n\t}\n", lowerCamel(typeName)+" cached repo is nil")
	fprintf(b, "\tif c.cache == nil {\n\t\treturn c.repo.FindOne(ctx, %s)\n\t}\n\treturn c.cache.Get(ctx, %s)\n}\n\n", pkArg, pkArg)
	fprintf(b, "func (c *%s) Insert(ctx context.Context, in *entity.%s) error {\n", cachedName, typeName)
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" cached repo is nil")
	fprintf(b, "\tif err := c.repo.Insert(ctx, in); err != nil {\n\t\treturn err\n\t}\n\tif c.cache != nil && in != nil {\n\t\tc.cache.Set(in.%s, in)\n\t}\n\treturn nil\n}\n\n", pkField)
	fprintf(b, "func (c *%s) Update(ctx context.Context, in *entity.%s) error {\n", cachedName, typeName)
	fprintf(b, "\treturn c.UpdateWithInvalidate(ctx, in)\n}\n\n")
	fprintf(b, "func (c *%s) UpdateWithInvalidate(ctx context.Context, in *entity.%s) error {\n", cachedName, typeName)
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" cached repo is nil")
	fprintf(b, "\tif err := c.repo.Update(ctx, in); err != nil {\n\t\treturn err\n\t}\n\tif c.cache != nil && in != nil {\n\t\tc.cache.Invalidate(in.%s)\n\t}\n\treturn nil\n}\n\n", pkField)
	fprintf(b, "func (c *%s) Delete(ctx context.Context, %s %s) error {\n", cachedName, lowerCamel(pk.Name), columnGoType(pk))
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" cached repo is nil")
	fprintf(b, "\tif err := c.repo.Delete(ctx, %s); err != nil {\n\t\treturn err\n\t}\n\tif c.cache != nil {\n\t\tc.cache.Invalidate(%s)\n\t}\n\treturn nil\n}\n\n", pkArg, pkArg)
}

func writeRedisCachedRepo(b *bytes.Buffer, table SQLTable, typeName, repoName string) {
	pk := primaryColumn(table)
	cachedName := "RedisCached" + repoName
	pkType := columnGoType(pk)
	pkArg := lowerCamel(pk.Name)
	pkField := modelFieldName(pk.Name)
	fprintf(b, "type %s struct {\n\trepo *%s\n\tcache *cache.RedisModelCache[*entity.%s, %s]\n}\n\n", cachedName, repoName, typeName, pkType)
	fprintf(b, "func NewRedisCached%s(repo *%s, client *redis.Client, opts ...cache.RedisModelOption[*entity.%s, %s]) *%s {\n", repoName, repoName, typeName, pkType, cachedName)
	fprintf(b, "\tloader := func(ctx context.Context, id %s) (*entity.%s, error) {\n\t\tif repo == nil {\n\t\t\treturn nil, errors.New(%q)\n\t\t}\n\t\treturn repo.FindOne(ctx, id)\n\t}\n", pkType, typeName, lowerCamel(typeName)+" repo is nil")
	fprintf(b, "\toptions := append([]cache.RedisModelOption[*entity.%s, %s]{cache.WithRedisModelNotFound[*entity.%s, %s](redis.ErrNil), cache.WithRedisModelKeyPrefix[*entity.%s, %s](entity.%sTable)}, opts...)\n", typeName, pkType, typeName, pkType, typeName, pkType, typeName)
	fprintf(b, "\treturn &%s{repo: repo, cache: cache.NewRedisModel(loader, client, options...)}\n}\n\n", cachedName)
	fprintf(b, "func (c *%s) FindOne(ctx context.Context, %s %s) (*entity.%s, error) {\n", cachedName, pkArg, pkType, typeName)
	fprintf(b, "\treturn c.FindByIDCached(ctx, %s)\n}\n\n", pkArg)
	fprintf(b, "func (c *%s) FindByIDCached(ctx context.Context, %s %s) (*entity.%s, error) {\n", cachedName, pkArg, pkType, typeName)
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn nil, errors.New(%q)\n\t}\n", lowerCamel(typeName)+" redis cached repo is nil")
	fprintf(b, "\tif c.cache == nil {\n\t\treturn c.repo.FindOne(ctx, %s)\n\t}\n\treturn c.cache.Get(ctx, %s)\n}\n\n", pkArg, pkArg)
	fprintf(b, "func (c *%s) Insert(ctx context.Context, in *entity.%s) error {\n", cachedName, typeName)
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" redis cached repo is nil")
	fprintf(b, "\tif err := c.repo.Insert(ctx, in); err != nil {\n\t\treturn err\n\t}\n\tif c.cache != nil && in != nil {\n\t\treturn c.cache.Set(ctx, in.%s, in)\n\t}\n\treturn nil\n}\n\n", pkField)
	fprintf(b, "func (c *%s) Update(ctx context.Context, in *entity.%s) error {\n", cachedName, typeName)
	fprintf(b, "\treturn c.UpdateWithInvalidate(ctx, in)\n}\n\n")
	fprintf(b, "func (c *%s) UpdateWithInvalidate(ctx context.Context, in *entity.%s) error {\n", cachedName, typeName)
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" redis cached repo is nil")
	fprintf(b, "\tif err := c.repo.Update(ctx, in); err != nil {\n\t\treturn err\n\t}\n\tif c.cache != nil && in != nil {\n\t\treturn c.cache.Invalidate(ctx, in.%s)\n\t}\n\treturn nil\n}\n\n", pkField)
	fprintf(b, "func (c *%s) Delete(ctx context.Context, %s %s) error {\n", cachedName, pkArg, pkType)
	fprintf(b, "\tif c == nil || c.repo == nil {\n\t\treturn errors.New(%q)\n\t}\n", lowerCamel(typeName)+" redis cached repo is nil")
	fprintf(b, "\tif err := c.repo.Delete(ctx, %s); err != nil {\n\t\treturn err\n\t}\n\tif c.cache != nil {\n\t\treturn c.cache.Invalidate(ctx, %s)\n\t}\n\treturn nil\n}\n\n", pkArg, pkArg)
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
		module = "github.com/gofly/gofly"
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
	fprintf(&b, "\n\t\"github.com/gofly/gofly/cache\"\n")
	fprintf(&b, "\t\"github.com/gofly/gofly/core/storage\"\n")
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
			}
			continue
		}
		if strings.HasPrefix(lower, "key ") || strings.HasPrefix(lower, "index ") || strings.HasPrefix(lower, "constraint ") {
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
		column := cleanSQLIdent(fields[0])
		if column != "" {
			columns = append(columns, column)
		}
	}
	return columns
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
	fprintf(b, "func (m *%s) FindOne(ctx context.Context, %s %s) (*%s, error) {\n", modelName, lowerCamel(pk.Name), columnGoType(pk), typeName)
	if hasSoftDelete(table) {
		fprintf(b, "\tcolumns, err := storage.JoinIdentifiers(%sColumns)\n", lowerCamel(typeName))
		fprintf(b, "\tif err != nil {\n\t\treturn nil, err\n\t}\n")
		fprintf(b, "\tquery := \"SELECT \" + columns + \" FROM \" + %sTable + \" WHERE %s = \" + storage.Placeholder(m.dialect, 1) + \" AND %s IS NULL LIMIT 1\"\n", lowerCamel(typeName), pk.Name, table.SoftDeleteColumn)
	} else {
		fprintf(b, "\tquery, err := storage.SelectByID(%sTable, %sColumns, %q, m.dialect)\n", lowerCamel(typeName), lowerCamel(typeName), pk.Name)
		fprintf(b, "\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	}
	fprintf(b, "\tvar out %s\n", typeName)
	fprintf(b, "\tif err := m.store.QueryOne(ctx, query, func(row *sql.Row) error {\n\t\treturn row.Scan(%s)\n\t}, %s); err != nil {\n", scanArgs("out", table.Columns), lowerCamel(pk.Name))
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
	fprintf(b, "func (m *%s) Delete(ctx context.Context, %s %s) error {\n", modelName, lowerCamel(pk.Name), columnGoType(pk))
	if hasSoftDelete(table) {
		fprintf(b, "\tquery := \"UPDATE \" + %sTable + \" SET %s = \" + storage.Placeholder(m.dialect, 1) + \" WHERE %s = \" + storage.Placeholder(m.dialect, 2) + \" AND %s IS NULL\"\n", lowerCamel(typeName), table.SoftDeleteColumn, pk.Name, table.SoftDeleteColumn)
		fprintf(b, "\tif _, err := m.store.Exec(ctx, query, %s, %s); err != nil {\n\t\treturn err\n\t}\n", softDeleteValueExpr(table), lowerCamel(pk.Name))
	} else {
		fprintf(b, "\tquery, err := storage.DeleteByID(%sTable, %q, m.dialect)\n", lowerCamel(typeName), pk.Name)
		fprintf(b, "\tif err != nil {\n\t\treturn err\n\t}\n")
		fprintf(b, "\tif _, err := m.store.Exec(ctx, query, %s); err != nil {\n\t\treturn err\n\t}\n", lowerCamel(pk.Name))
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
	fprintf(b, "func (r *%s) FindOne(ctx context.Context, %s %s) (*entity.%s, error) {\n", receiverName, lowerCamel(pk.Name), columnGoType(pk), typeName)
	if hasSoftDelete(table) {
		fprintf(b, "\tcolumns, err := storage.JoinIdentifiers(entity.%sColumns)\n", typeName)
		fprintf(b, "\tif err != nil {\n\t\treturn nil, err\n\t}\n")
		fprintf(b, "\tquery := \"SELECT \" + columns + \" FROM \" + entity.%sTable + \" WHERE %s = \" + storage.Placeholder(r.dialect, 1) + \" AND %s IS NULL LIMIT 1\"\n", typeName, pk.Name, table.SoftDeleteColumn)
	} else {
		fprintf(b, "\tquery, err := storage.SelectByID(entity.%sTable, entity.%sColumns, %q, r.dialect)\n", typeName, typeName, pk.Name)
		fprintf(b, "\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	}
	fprintf(b, "\tvar out entity.%s\n", typeName)
	fprintf(b, "\tif err := r.queryOne(ctx, query, func(row *sql.Row) error {\n\t\treturn row.Scan(%s)\n\t}, %s); err != nil {\n", scanArgs("out", table.Columns), lowerCamel(pk.Name))
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
	fprintf(b, "func (r *%s) Delete(ctx context.Context, %s %s) error {\n", receiverName, lowerCamel(pk.Name), columnGoType(pk))
	if hasSoftDelete(table) {
		fprintf(b, "\tquery := \"UPDATE \" + entity.%sTable + \" SET %s = \" + storage.Placeholder(r.dialect, 1) + \" WHERE %s = \" + storage.Placeholder(r.dialect, 2) + \" AND %s IS NULL\"\n", typeName, table.SoftDeleteColumn, pk.Name, table.SoftDeleteColumn)
		fprintf(b, "\tif _, err := r.exec(ctx, query, %s, %s); err != nil {\n\t\treturn err\n\t}\n", softDeleteValueExpr(table), lowerCamel(pk.Name))
	} else {
		fprintf(b, "\tquery, err := storage.DeleteByID(entity.%sTable, %q, r.dialect)\n", typeName, pk.Name)
		fprintf(b, "\tif err != nil {\n\t\treturn err\n\t}\n")
		fprintf(b, "\tif _, err := r.exec(ctx, query, %s); err != nil {\n\t\treturn err\n\t}\n", lowerCamel(pk.Name))
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
	fprintf(b, "func (r *%s) FindOne(ctx context.Context, %s %s) (*entity.%s, error) {\n", receiverName, lowerCamel(pk.Name), columnGoType(pk), typeName)
	fprintf(b, "\tdb, err := r.dbWithContext(ctx)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	fprintf(b, "\tvar out entity.%s\n", typeName)
	if hasSoftDelete(table) {
		fprintf(b, "\tdb = db.Where(%q)\n", table.SoftDeleteColumn+" IS NULL")
	}
	fprintf(b, "\tif err := db.Where(%q, %s).First(&out).Error; err != nil {\n\t\tif errors.Is(err, gorm.ErrRecordNotFound) {\n\t\t\treturn nil, storage.ErrNotFound\n\t\t}\n\t\treturn nil, err\n\t}\n", pk.Name+" = ?", lowerCamel(pk.Name))
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
	fprintf(b, "func (r *%s) Delete(ctx context.Context, %s %s) error {\n", receiverName, lowerCamel(pk.Name), columnGoType(pk))
	fprintf(b, "\tdb, err := r.dbWithContext(ctx)\n\tif err != nil {\n\t\treturn err\n\t}\n")
	if hasSoftDelete(table) {
		fprintf(b, "\treturn db.Model(&entity.%s{}).Where(%q, %s).Where(%q).Update(%q, %s).Error\n}\n\n", typeName, pk.Name+" = ?", lowerCamel(pk.Name), table.SoftDeleteColumn+" IS NULL", table.SoftDeleteColumn, softDeleteValueExpr(table))
		return
	}
	fprintf(b, "\treturn db.Where(%q, %s).Delete(&entity.%s{}).Error\n}\n\n", pk.Name+" = ?", lowerCamel(pk.Name), typeName)
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
		fprintf(b, "func (r *%s) FindBy%s(ctx context.Context, %s %s) (*entity.%s, error) {\n", receiverName, modelFieldName(column.Name), lowerCamel(column.Name), columnGoType(column), typeName)
		fprintf(b, "\tdb, err := r.dbWithContext(ctx)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n")
		fprintf(b, "\tvar out entity.%s\n", typeName)
		if hasSoftDelete(table) {
			fprintf(b, "\tdb = db.Where(%q)\n", table.SoftDeleteColumn+" IS NULL")
		}
		fprintf(b, "\tif err := db.Where(%q, %s).First(&out).Error; err != nil {\n\t\tif errors.Is(err, gorm.ErrRecordNotFound) {\n\t\t\treturn nil, storage.ErrNotFound\n\t\t}\n\t\treturn nil, err\n\t}\n\treturn &out, nil\n}\n\n", column.Name+" = ?", lowerCamel(column.Name))
	}
	fprintf(b, "func (r *%s) InsertMany(ctx context.Context, items []*entity.%s) error {\n", receiverName, typeName)
	fprintf(b, "\tdb, err := r.dbWithContext(ctx)\n\tif err != nil {\n\t\treturn err\n\t}\n\treturn db.Create(&items).Error\n}\n\n")
	fprintf(b, "func (r *%s) UpdateMany(ctx context.Context, items []*entity.%s) error {\n", receiverName, typeName)
	fprintf(b, "\tfor _, item := range items {\n\t\tif err := r.Update(ctx, item); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n\treturn nil\n}\n\n")
	fprintf(b, "func (r *%s) DeleteMany(ctx context.Context, ids ...%s) error {\n", receiverName, columnGoType(pk))
	fprintf(b, "\tfor _, id := range ids {\n\t\tif err := r.Delete(ctx, id); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n\treturn nil\n}\n\n")
	fprintf(b, "func (r *%s) UpdateFields(ctx context.Context, %s %s, fields map[string]any) error {\n", receiverName, lowerCamel(pk.Name), columnGoType(pk))
	fprintf(b, "\tif len(fields) == 0 {\n\t\treturn nil\n\t}\n")
	fprintf(b, "\tdb, err := r.dbWithContext(ctx)\n\tif err != nil {\n\t\treturn err\n\t}\n")
	if hasSoftDelete(table) {
		fprintf(b, "\tdb = db.Where(%q)\n", table.SoftDeleteColumn+" IS NULL")
	}
	fprintf(b, "\tallowed := map[string]struct{}{%s}\n", columnSetLiteral(updateColumns(table)))
	fprintf(b, "\tfor column := range fields {\n\t\tif _, ok := allowed[column]; !ok {\n\t\t\treturn errors.New(\"field is not updatable: \" + column)\n\t\t}\n\t}\n")
	fprintf(b, "\treturn db.Model(&entity.%s{}).Where(%q, %s).Updates(fields).Error\n}\n\n", typeName, pk.Name+" = ?", lowerCamel(pk.Name))
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
	if strings.EqualFold(name, "id") {
		return "ID"
	}
	return exportName(name)
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
