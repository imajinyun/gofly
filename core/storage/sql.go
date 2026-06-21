// Package storage provides SQL database connectivity with connection pooling,
// circuit breaking, and query observability for gofly services.
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	core "github.com/gofly/gofly/core"
	corebreaker "github.com/gofly/gofly/core/breaker"
)

var (
	// ErrNotFound is returned when a query returns no rows.
	ErrNotFound = errors.New("record not found")
	// ErrInvalidIdentifier is returned for invalid SQL identifiers.
	ErrInvalidIdentifier = errors.New("invalid sql identifier")
)

// PoolConfig configures the database connection pool.
type PoolConfig struct {
	MaxOpenConns    int           `json:"maxOpenConns,omitempty"`
	MaxIdleConns    int           `json:"maxIdleConns,omitempty"`
	ConnMaxLifetime time.Duration `json:"connMaxLifetime,omitempty"`
	ConnMaxIdleTime time.Duration `json:"connMaxIdleTime,omitempty"`
}

// Config configures a SQL database connection.
type Config struct {
	Driver        string        `json:"driver"`
	DSN           string        `json:"-"`
	Pool          PoolConfig    `json:"pool,omitempty"`
	Ping          time.Duration `json:"ping,omitempty"`
	QueryTimeout  time.Duration `json:"queryTimeout,omitempty"`
	SlowThreshold time.Duration `json:"slowThreshold,omitempty"`
}

// SQLStore is a database connection pool with observability.
type SQLStore struct {
	db            *sql.DB
	queryTimeout  time.Duration
	slowThreshold time.Duration
	breaker       StoreBreaker
	breakerSnap   func() any
	stats         *StoreStats
}

type Executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type Querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type Tx interface {
	Executor
	Querier
	Commit() error
	Rollback() error
}

type TxRunner interface {
	Transact(context.Context, *sql.TxOptions, TxFunc) error
}

type TxFunc func(ctx context.Context, tx *sql.Tx) error

type StoreOption func(*SQLStore)

// StoreBreaker is the small circuit-breaker contract SQLStore needs. Both
// core/breaker.Breaker and core/breaker.AdaptiveBreaker satisfy it.
type StoreBreaker interface {
	Do(context.Context, func() error) error
}

type OperationSnapshot struct {
	Requests      int64         `json:"requests"`
	Errors        int64         `json:"errors"`
	TotalDuration time.Duration `json:"totalDuration"`
	MaxDuration   time.Duration `json:"maxDuration"`
	AvgDuration   time.Duration `json:"avgDuration"`
}

type StoreSnapshot struct {
	Operations map[string]OperationSnapshot `json:"operations"`
	DB         sql.DBStats                  `json:"db"`
	Breaker    any                          `json:"breaker,omitempty"`
}

type StoreStats struct {
	mu        sync.RWMutex
	operation map[string]*operationStats
}

type operationStats struct {
	Requests      int64
	Errors        int64
	TotalDuration time.Duration
	MaxDuration   time.Duration
	AvgDuration   time.Duration
}

func Open(ctx context.Context, conf Config) (*SQLStore, error) {
	if conf.Driver == "" {
		return nil, errors.New("storage driver is required")
	}
	db, err := sql.Open(conf.Driver, conf.DSN)
	if err != nil {
		return nil, fmt.Errorf("open sql store: %w", err)
	}
	ApplyPoolConfig(db, conf.Pool)
	if conf.Ping > 0 {
		pingCtx, cancel := context.WithTimeout(ctx, conf.Ping)
		defer cancel()
		if err := db.PingContext(pingCtx); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("ping sql store: %w", err)
		}
	}
	return NewSQLStore(db, WithQueryTimeout(conf.QueryTimeout), WithSlowThreshold(conf.SlowThreshold)), nil
}

func NewSQLStore(db *sql.DB, opts ...StoreOption) *SQLStore {
	s := &SQLStore{db: db, stats: NewStoreStats()}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func WithSlowThreshold(threshold time.Duration) StoreOption {
	return func(s *SQLStore) {
		s.slowThreshold = threshold
	}
}

func WithQueryTimeout(timeout time.Duration) StoreOption {
	return func(s *SQLStore) {
		if timeout > 0 {
			s.queryTimeout = timeout
		}
	}
}

func WithBreaker(br *corebreaker.Breaker) StoreOption {
	return func(s *SQLStore) {
		if br != nil {
			s.breaker = br
			s.breakerSnap = func() any { return br.Snapshot() }
		}
	}
}

func WithAdaptiveBreaker(br *corebreaker.AdaptiveBreaker) StoreOption {
	return func(s *SQLStore) {
		if br != nil {
			s.breaker = br
			s.breakerSnap = func() any { return br.Snapshot() }
		}
	}
}

func WithStoreBreaker(br StoreBreaker) StoreOption {
	return func(s *SQLStore) {
		s.breaker = br
		s.breakerSnap = nil
	}
}

func (s *SQLStore) DB() *sql.DB {
	if s == nil {
		return nil
	}
	return s.db
}

func (s *SQLStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *SQLStore) Ping(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("sql store is nil")
	}
	if err := s.run(ctx, "ping", func(callCtx context.Context) error {
		return s.db.PingContext(callCtx)
	}); err != nil {
		return fmt.Errorf("ping sql store: %w", err)
	}
	return nil
}

func (s *SQLStore) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("sql store is nil")
	}
	var result sql.Result
	if err := s.run(ctx, "exec", func(callCtx context.Context) error {
		var err error
		result, err = s.db.ExecContext(callCtx, query, args...)
		return err
	}); err != nil {
		return nil, fmt.Errorf("exec sql: %w", err)
	}
	return result, nil
}

func (s *SQLStore) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	if s == nil || s.db == nil {
		return nil
	}
	ctx = core.Context(ctx)
	return s.db.QueryRowContext(ctx, query, args...)
}

func (s *SQLStore) QueryOne(ctx context.Context, query string, scan func(*sql.Row) error, args ...any) error {
	if s == nil || s.db == nil {
		return errors.New("sql store is nil")
	}
	if scan == nil {
		return errors.New("scan function is required")
	}
	if err := s.run(ctx, "query_one", func(callCtx context.Context) error {
		return scan(s.db.QueryRowContext(callCtx, query, args...))
	}); err != nil {
		return fmt.Errorf("query one sql: %w", err)
	}
	return nil
}

func (s *SQLStore) QueryRows(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("sql store is nil")
	}
	var rows *sql.Rows
	if err := s.run(ctx, "query_rows", func(callCtx context.Context) error {
		var err error
		rows, err = s.db.QueryContext(callCtx, query, args...)
		return err
	}); err != nil {
		return nil, fmt.Errorf("query sql: %w", err)
	}
	return rows, nil
}

func (s *SQLStore) QueryAll(ctx context.Context, query string, scan func(*sql.Rows) error, args ...any) error {
	if s == nil || s.db == nil {
		return errors.New("sql store is nil")
	}
	if scan == nil {
		return errors.New("scan function is required")
	}
	if err := s.run(ctx, "query_all", func(callCtx context.Context) error {
		rows, err := s.db.QueryContext(callCtx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		if err := scan(rows); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate sql rows: %w", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("query sql: %w", err)
	}
	return nil
}

func (s *SQLStore) Transact(ctx context.Context, opts *sql.TxOptions, fn TxFunc) error {
	if s == nil || s.db == nil {
		return errors.New("sql store is nil")
	}
	if fn == nil {
		return errors.New("transaction function is required")
	}
	if err := s.run(ctx, "transaction", func(callCtx context.Context) error {
		return WithTx(callCtx, s.db, opts, fn)
	}); err != nil {
		return fmt.Errorf("transaction sql: %w", err)
	}
	return nil
}

func WithTx(ctx context.Context, db *sql.DB, opts *sql.TxOptions, fn TxFunc) error {
	if db == nil {
		return errors.New("sql db is nil")
	}
	if fn == nil {
		return errors.New("transaction function is required")
	}
	ctx = core.Context(ctx)
	tx, err := db.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(ctx, tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return errors.Join(err, fmt.Errorf("rollback transaction: %w", rbErr))
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func (s *SQLStore) Snapshot() StoreSnapshot {
	if s == nil {
		return StoreSnapshot{Operations: map[string]OperationSnapshot{}}
	}
	snapshot := StoreSnapshot{Operations: s.stats.Snapshot()}
	if s.db != nil {
		snapshot.DB = s.db.Stats()
	}
	if s.breakerSnap != nil {
		snapshot.Breaker = s.breakerSnap()
	}
	return snapshot
}

func (s *SQLStore) run(ctx context.Context, operation string, fn func(context.Context) error) error {
	ctx = core.Context(ctx)
	start := time.Now()
	callCtx, cancel := s.operationContext(ctx)
	defer cancel()

	var err error
	if ctxErr := callCtx.Err(); ctxErr != nil {
		err = ctxErr
	} else if s.breaker != nil {
		err = s.breaker.Do(callCtx, func() error { return fn(callCtx) })
	} else {
		err = fn(callCtx)
	}
	s.observe(callCtx, operation, start, err)
	return err
}

func (s *SQLStore) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if s == nil || s.queryTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, s.queryTimeout)
}

func (s *SQLStore) observe(ctx context.Context, operation string, start time.Time, err error) {
	if s == nil || s.stats == nil {
		return
	}
	duration := time.Since(start)
	s.stats.Observe(operation, duration, err != nil)
	if s.slowThreshold > 0 && duration >= s.slowThreshold {
		slog.WarnContext(ctx, "slow sql operation", "operation", operation, "duration", duration, "error", err)
	}
}

func ApplyPoolConfig(db *sql.DB, conf PoolConfig) {
	if db == nil {
		return
	}
	if conf.MaxOpenConns > 0 {
		db.SetMaxOpenConns(conf.MaxOpenConns)
	}
	if conf.MaxIdleConns > 0 {
		db.SetMaxIdleConns(conf.MaxIdleConns)
	}
	if conf.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(conf.ConnMaxLifetime)
	}
	if conf.ConnMaxIdleTime > 0 {
		db.SetConnMaxIdleTime(conf.ConnMaxIdleTime)
	}
}

type Dialect string

const (
	DialectQuestion Dialect = "question"
	DialectPostgres Dialect = "postgres"
	DialectMySQL    Dialect = "mysql"
	DialectSQLite   Dialect = "sqlite"
)

// SQLDialect is the portable SQL rendering contract used by storage builders
// and generated repositories. Dialect itself implements this interface; the
// interface is provided for callers that want to accept custom dialect renderers
// without depending on concrete string constants.
type SQLDialect interface {
	Placeholder(n int) string
	QuoteIdent(name string) (string, error)
	LimitOffset(limit int, offset int, start int) (string, []any, error)
}

// NormalizeDialect maps common driver aliases to gofly's stable dialect names.
// Unknown values are kept unchanged so older callers that relied on question
// placeholders continue to work through Placeholder.
func NormalizeDialect(dialect Dialect) Dialect {
	switch Dialect(strings.ToLower(strings.TrimSpace(string(dialect)))) {
	case "", DialectQuestion:
		return DialectQuestion
	case "pg", "pgsql", "postgresql", DialectPostgres:
		return DialectPostgres
	case "mariadb", DialectMySQL:
		return DialectMySQL
	case "sqlite3", DialectSQLite:
		return DialectSQLite
	default:
		return dialect
	}
}

func (d Dialect) Placeholder(n int) string { return Placeholder(d, n) }

func (d Dialect) QuoteIdent(name string) (string, error) { return QuoteIdent(d, name) }

func (d Dialect) LimitOffset(limit int, offset int, start int) (string, []any, error) {
	return LimitOffset(d, limit, offset, start)
}

func Placeholder(dialect Dialect, n int) string {
	if n <= 0 {
		n = 1
	}
	if NormalizeDialect(dialect) == DialectPostgres {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
}

func Placeholders(dialect Dialect, n int) string {
	parts := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		parts = append(parts, Placeholder(dialect, i))
	}
	return strings.Join(parts, ", ")
}

func QuoteIdent(dialect Dialect, name string) (string, error) {
	segments := strings.Split(name, ".")
	quoted := make([]string, 0, len(segments))
	for _, segment := range segments {
		if err := ValidateIdentifier(segment); err != nil {
			return "", err
		}
		quote := `"`
		if NormalizeDialect(dialect) == DialectMySQL {
			quote = "`"
		}
		quoted = append(quoted, quote+segment+quote)
	}
	return strings.Join(quoted, "."), nil
}

func JoinQuotedIdentifiers(dialect Dialect, names []string) (string, error) {
	parts := make([]string, 0, len(names))
	for _, name := range names {
		quoted, err := QuoteIdent(dialect, name)
		if err != nil {
			return "", err
		}
		parts = append(parts, quoted)
	}
	return strings.Join(parts, ", "), nil
}

func LimitOffset(dialect Dialect, limit int, offset int, start int) (string, []any, error) {
	if limit < 0 {
		return "", nil, errors.New("limit must not be negative")
	}
	if offset < 0 {
		return "", nil, errors.New("offset must not be negative")
	}
	if start <= 0 {
		start = 1
	}
	if limit == 0 {
		if offset > 0 {
			return "", nil, errors.New("limit is required when offset is set")
		}
		return "", nil, nil
	}
	clause := " LIMIT " + Placeholder(dialect, start)
	args := []any{limit}
	if offset > 0 {
		clause += " OFFSET " + Placeholder(dialect, start+1)
		args = append(args, offset)
	}
	return clause, args, nil
}

var identifierRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func ValidateIdentifier(name string) error {
	if !identifierRE.MatchString(name) {
		return fmt.Errorf("%w: %s", ErrInvalidIdentifier, name)
	}
	return nil
}

func JoinIdentifiers(names []string) (string, error) {
	parts := make([]string, 0, len(names))
	for _, name := range names {
		if err := ValidateIdentifier(name); err != nil {
			return "", err
		}
		parts = append(parts, name)
	}
	return strings.Join(parts, ", "), nil
}

func SelectByID(table string, columns []string, idColumn string, dialect Dialect) (string, error) {
	if err := ValidateIdentifier(table); err != nil {
		return "", err
	}
	if len(columns) == 0 {
		return "", errors.New("select columns are required")
	}
	if err := ValidateIdentifier(idColumn); err != nil {
		return "", err
	}
	joined, err := JoinIdentifiers(columns)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("SELECT %s FROM %s WHERE %s = %s LIMIT 1", joined, table, idColumn, Placeholder(dialect, 1)), nil
}

func Insert(table string, columns []string, dialect Dialect) (string, error) {
	if err := ValidateIdentifier(table); err != nil {
		return "", err
	}
	if len(columns) == 0 {
		return "", errors.New("insert columns are required")
	}
	joined, err := JoinIdentifiers(columns)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, joined, Placeholders(dialect, len(columns))), nil
}

func BatchInsert(table string, columns []string, rows int, dialect Dialect) (string, error) {
	if rows <= 0 {
		return "", errors.New("batch insert rows must be positive")
	}
	if err := ValidateIdentifier(table); err != nil {
		return "", err
	}
	if len(columns) == 0 {
		return "", errors.New("insert columns are required")
	}
	joined, err := JoinIdentifiers(columns)
	if err != nil {
		return "", err
	}
	values := make([]string, 0, rows)
	placeholder := 1
	for range rows {
		parts := make([]string, 0, len(columns))
		for range columns {
			parts = append(parts, Placeholder(dialect, placeholder))
			placeholder++
		}
		values = append(values, "("+strings.Join(parts, ", ")+")")
	}
	return fmt.Sprintf("INSERT INTO %s (%s) VALUES %s", table, joined, strings.Join(values, ", ")), nil
}

func Upsert(table string, columns []string, conflictColumns []string, updateColumns []string, dialect Dialect) (string, error) {
	insertQuery, err := Insert(table, columns, dialect)
	if err != nil {
		return "", err
	}
	if len(conflictColumns) == 0 {
		return "", errors.New("conflict columns are required")
	}
	if len(updateColumns) == 0 {
		return "", errors.New("update columns are required")
	}
	conflict, err := JoinIdentifiers(conflictColumns)
	if err != nil {
		return "", err
	}
	updates := make([]string, 0, len(updateColumns))
	for _, column := range updateColumns {
		if err := ValidateIdentifier(column); err != nil {
			return "", err
		}
		switch NormalizeDialect(dialect) {
		case DialectPostgres, DialectSQLite:
			updates = append(updates, fmt.Sprintf("%s = EXCLUDED.%s", column, column))
		default:
			updates = append(updates, fmt.Sprintf("%s = VALUES(%s)", column, column))
		}
	}
	if normalized := NormalizeDialect(dialect); normalized == DialectPostgres || normalized == DialectSQLite {
		return fmt.Sprintf("%s ON CONFLICT (%s) DO UPDATE SET %s", insertQuery, conflict, strings.Join(updates, ", ")), nil
	}
	return fmt.Sprintf("%s ON DUPLICATE KEY UPDATE %s", insertQuery, strings.Join(updates, ", ")), nil
}

func UpdateByID(table string, columns []string, idColumn string, dialect Dialect) (string, error) {
	if err := ValidateIdentifier(table); err != nil {
		return "", err
	}
	if len(columns) == 0 {
		return "", errors.New("update columns are required")
	}
	if err := ValidateIdentifier(idColumn); err != nil {
		return "", err
	}
	sets := make([]string, 0, len(columns))
	for i, column := range columns {
		if err := ValidateIdentifier(column); err != nil {
			return "", err
		}
		sets = append(sets, fmt.Sprintf("%s = %s", column, Placeholder(dialect, i+1)))
	}
	return fmt.Sprintf("UPDATE %s SET %s WHERE %s = %s", table, strings.Join(sets, ", "), idColumn, Placeholder(dialect, len(columns)+1)), nil
}

func DeleteByID(table string, idColumn string, dialect Dialect) (string, error) {
	if err := ValidateIdentifier(table); err != nil {
		return "", err
	}
	if err := ValidateIdentifier(idColumn); err != nil {
		return "", err
	}
	return fmt.Sprintf("DELETE FROM %s WHERE %s = %s", table, idColumn, Placeholder(dialect, 1)), nil
}

func SelectPage(table string, columns []string, orderBy string, dialect Dialect) (string, error) {
	if err := ValidateIdentifier(table); err != nil {
		return "", err
	}
	if len(columns) == 0 {
		return "", errors.New("select columns are required")
	}
	if err := ValidateIdentifier(orderBy); err != nil {
		return "", err
	}
	joined, err := JoinIdentifiers(columns)
	if err != nil {
		return "", err
	}
	pagination, _, err := LimitOffset(dialect, 1, 1, 1)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("SELECT %s FROM %s ORDER BY %s%s", joined, table, orderBy, pagination), nil
}

func SelectForUpdate(table string, columns []string, whereColumn string, dialect Dialect, skipLocked bool) (string, error) {
	if err := ValidateIdentifier(table); err != nil {
		return "", err
	}
	if len(columns) == 0 {
		return "", errors.New("select columns are required")
	}
	if err := ValidateIdentifier(whereColumn); err != nil {
		return "", err
	}
	joined, err := JoinIdentifiers(columns)
	if err != nil {
		return "", err
	}
	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s = %s FOR UPDATE", joined, table, whereColumn, Placeholder(dialect, 1))
	if skipLocked {
		query += " SKIP LOCKED"
	}
	return query, nil
}

func CountAll(table string) (string, error) {
	if err := ValidateIdentifier(table); err != nil {
		return "", err
	}
	return fmt.Sprintf("SELECT COUNT(*) FROM %s", table), nil
}

type Where struct {
	clauses  []whereClause
	orders   []orderClause
	limit    int
	offset   int
	hasLimit bool
}

type whereClause struct {
	column string
	op     string
	args   []any
}

type orderClause struct {
	column string
	desc   bool
}

func NewWhere() *Where { return &Where{} }

func (w *Where) Eq(column string, value any) *Where { return w.add(column, "=", value) }

func (w *Where) Ne(column string, value any) *Where { return w.add(column, "!=", value) }

func (w *Where) Gt(column string, value any) *Where { return w.add(column, ">", value) }

func (w *Where) Gte(column string, value any) *Where { return w.add(column, ">=", value) }

func (w *Where) Lt(column string, value any) *Where { return w.add(column, "<", value) }

func (w *Where) Lte(column string, value any) *Where { return w.add(column, "<=", value) }

func (w *Where) Like(column string, value any) *Where { return w.add(column, "LIKE", value) }

func (w *Where) IsNull(column string) *Where {
	if w == nil {
		w = NewWhere()
	}
	w.clauses = append(w.clauses, whereClause{column: column, op: "IS NULL"})
	return w
}

func (w *Where) IsNotNull(column string) *Where {
	if w == nil {
		w = NewWhere()
	}
	w.clauses = append(w.clauses, whereClause{column: column, op: "IS NOT NULL"})
	return w
}

func (w *Where) Between(column string, start any, end any) *Where {
	if w == nil {
		w = NewWhere()
	}
	w.clauses = append(w.clauses, whereClause{column: column, op: "BETWEEN", args: []any{start, end}})
	return w
}

func (w *Where) In(column string, values ...any) *Where {
	if w == nil {
		w = NewWhere()
	}
	w.clauses = append(w.clauses, whereClause{column: column, op: "IN", args: append([]any(nil), values...)})
	return w
}

func (w *Where) OrderBy(column string, desc ...bool) *Where {
	if w == nil {
		w = NewWhere()
	}
	w.orders = append(w.orders, orderClause{column: column, desc: len(desc) > 0 && desc[0]})
	return w
}

func (w *Where) Limit(limit int) *Where {
	if w == nil {
		w = NewWhere()
	}
	if limit > 0 {
		w.limit = limit
		w.hasLimit = true
	}
	return w
}

func (w *Where) Offset(offset int) *Where {
	if w == nil {
		w = NewWhere()
	}
	if offset > 0 {
		w.offset = offset
	}
	return w
}

func (w *Where) add(column string, op string, value any) *Where {
	if w == nil {
		w = NewWhere()
	}
	w.clauses = append(w.clauses, whereClause{column: column, op: op, args: []any{value}})
	return w
}

func (w *Where) Build(dialect Dialect, start int) (string, []any, error) {
	if w == nil {
		return "", nil, nil
	}
	if start <= 0 {
		start = 1
	}
	parts := make([]string, 0, len(w.clauses)+2)
	args := make([]any, 0)
	placeholder := start
	for _, clause := range w.clauses {
		if err := ValidateIdentifier(clause.column); err != nil {
			return "", nil, err
		}
		switch clause.op {
		case "IN":
			if len(clause.args) == 0 {
				parts = append(parts, "1 = 0")
				continue
			}
			placeholders := make([]string, 0, len(clause.args))
			for range clause.args {
				placeholders = append(placeholders, Placeholder(dialect, placeholder))
				placeholder++
			}
			parts = append(parts, fmt.Sprintf("%s IN (%s)", clause.column, strings.Join(placeholders, ", ")))
			args = append(args, clause.args...)
		case "BETWEEN":
			if len(clause.args) != 2 {
				return "", nil, errors.New("between requires two values")
			}
			parts = append(parts, fmt.Sprintf("%s BETWEEN %s AND %s", clause.column, Placeholder(dialect, placeholder), Placeholder(dialect, placeholder+1)))
			placeholder += 2
			args = append(args, clause.args...)
		case "IS NULL", "IS NOT NULL":
			if len(clause.args) != 0 {
				return "", nil, errors.New("null where clause does not accept values")
			}
			parts = append(parts, fmt.Sprintf("%s %s", clause.column, clause.op))
		default:
			if len(clause.args) != 1 {
				return "", nil, errors.New("where clause requires one value")
			}
			parts = append(parts, fmt.Sprintf("%s %s %s", clause.column, clause.op, Placeholder(dialect, placeholder)))
			placeholder++
			args = append(args, clause.args[0])
		}
	}
	var b strings.Builder
	if len(parts) > 0 {
		b.WriteString(" WHERE ")
		b.WriteString(strings.Join(parts, " AND "))
	}
	if len(w.orders) > 0 {
		orders := make([]string, 0, len(w.orders))
		for _, order := range w.orders {
			column := strings.TrimSpace(order.column)
			if column == "" {
				continue
			}
			if err := ValidateIdentifier(column); err != nil {
				return "", nil, err
			}
			direction := "ASC"
			if order.desc {
				direction = "DESC"
			}
			orders = append(orders, column+" "+direction)
		}
		if len(orders) > 0 {
			b.WriteString(" ORDER BY ")
			b.WriteString(strings.Join(orders, ", "))
		}
	}
	pagination, paginationArgs, err := LimitOffset(dialect, 0, 0, placeholder)
	if err != nil {
		return "", nil, err
	}
	if w.hasLimit {
		pagination, paginationArgs, err = LimitOffset(dialect, w.limit, w.offset, placeholder)
		if err != nil {
			return "", nil, err
		}
	} else if w.offset > 0 {
		return "", nil, errors.New("limit is required when offset is set")
	}
	b.WriteString(pagination)
	args = append(args, paginationArgs...)
	return b.String(), args, nil
}

func SelectWhere(table string, columns []string, where *Where, dialect Dialect) (string, []any, error) {
	if err := ValidateIdentifier(table); err != nil {
		return "", nil, err
	}
	if len(columns) == 0 {
		return "", nil, errors.New("select columns are required")
	}
	joined, err := JoinIdentifiers(columns)
	if err != nil {
		return "", nil, err
	}
	clause, args, err := where.Build(dialect, 1)
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("SELECT %s FROM %s%s", joined, table, clause), args, nil
}

func CountWhere(table string, where *Where, dialect Dialect) (string, []any, error) {
	if err := ValidateIdentifier(table); err != nil {
		return "", nil, err
	}
	clause, args, err := where.Build(dialect, 1)
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("SELECT COUNT(*) FROM %s%s", table, clause), args, nil
}

func NewStoreStats() *StoreStats {
	return &StoreStats{operation: make(map[string]*operationStats)}
}

func (s *StoreStats) Observe(operation string, duration time.Duration, failed bool) {
	if s == nil {
		return
	}
	if operation == "" {
		operation = "unknown"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := s.operation[operation]
	if stats == nil {
		stats = &operationStats{}
		s.operation[operation] = stats
	}
	stats.Requests++
	if failed {
		stats.Errors++
	}
	stats.TotalDuration += duration
	if duration > stats.MaxDuration {
		stats.MaxDuration = duration
	}
	stats.AvgDuration = stats.TotalDuration / time.Duration(stats.Requests)
}

func (s *StoreStats) Snapshot() map[string]OperationSnapshot {
	if s == nil {
		return map[string]OperationSnapshot{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.operation))
	for key := range s.operation {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make(map[string]OperationSnapshot, len(keys))
	for _, key := range keys {
		stats := s.operation[key]
		out[key] = OperationSnapshot{
			Requests:      stats.Requests,
			Errors:        stats.Errors,
			TotalDuration: stats.TotalDuration,
			MaxDuration:   stats.MaxDuration,
			AvgDuration:   stats.AvgDuration,
		}
	}
	return out
}
