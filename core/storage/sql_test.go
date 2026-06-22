package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	corebreaker "github.com/gofly/gofly/core/breaker"
)

const fakeDriverName = "gofly_storage_fake"

var registerFakeDriver sync.Once

func fakeDB(t *testing.T) *sql.DB {
	t.Helper()
	registerFakeDriver.Do(func() { sql.Register(fakeDriverName, fakeDriver{}) })
	db, err := sql.Open(fakeDriverName, "")
	if err != nil {
		t.Fatalf("open fake db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return fakeConn{}, nil }

type fakeConn struct{}

func (fakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported")
}
func (fakeConn) Close() error              { return nil }
func (fakeConn) Begin() (driver.Tx, error) { return fakeTx{}, nil }

func (fakeConn) Ping(ctx context.Context) error { return fakeWait(ctx, "ok") }

func (fakeConn) ExecContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	if err := fakeWait(ctx, query); err != nil {
		return nil, err
	}
	return driver.RowsAffected(1), nil
}

func (fakeConn) QueryContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if err := fakeWait(ctx, query); err != nil {
		return nil, err
	}
	return fakeRows{}, nil
}

func (fakeConn) BeginTx(ctx context.Context, _ driver.TxOptions) (driver.Tx, error) {
	if err := fakeWait(ctx, "ok"); err != nil {
		return nil, err
	}
	return fakeTx{}, nil
}

type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return nil }

type fakeRows struct{}

func (fakeRows) Columns() []string         { return []string{"id"} }
func (fakeRows) Close() error              { return nil }
func (fakeRows) Next([]driver.Value) error { return io.EOF }

func fakeWait(ctx context.Context, query string) error {
	switch query {
	case "block":
		<-ctx.Done()
		return ctx.Err()
	case "fail":
		return errors.New("driver failure")
	default:
		return nil
	}
}

func TestSQLBuilders(t *testing.T) {
	selectQuery, err := SelectByID("users", []string{"id", "name"}, "id", DialectPostgres)
	if err != nil {
		t.Fatalf("SelectByID returned error: %v", err)
	}
	if selectQuery != "SELECT id, name FROM users WHERE id = $1 LIMIT 1" {
		t.Fatalf("select query = %q", selectQuery)
	}

	insertQuery, err := Insert("users", []string{"id", "name"}, DialectQuestion)
	if err != nil {
		t.Fatalf("Insert returned error: %v", err)
	}
	if insertQuery != "INSERT INTO users (id, name) VALUES (?, ?)" {
		t.Fatalf("insert query = %q", insertQuery)
	}
}

func TestDialectNormalizationAndPlaceholderContracts(t *testing.T) {
	tests := []struct {
		name        string
		in          Dialect
		want        Dialect
		placeholder string
	}{
		{name: "empty defaults to question", in: "", want: DialectQuestion, placeholder: "?"},
		{name: "postgres alias", in: "postgresql", want: DialectPostgres, placeholder: "$2"},
		{name: "mysql alias", in: "mariadb", want: DialectMySQL, placeholder: "?"},
		{name: "sqlite alias", in: "sqlite3", want: DialectSQLite, placeholder: "?"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeDialect(tt.in); got != tt.want {
				t.Fatalf("NormalizeDialect(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if got := Placeholder(tt.in, 2); got != tt.placeholder {
				t.Fatalf("Placeholder(%q, 2) = %q, want %q", tt.in, got, tt.placeholder)
			}
		})
	}
}

func TestDialectQuoteIdentAndJoinQuotedIdentifiers(t *testing.T) {
	postgresQuoted, err := QuoteIdent(DialectPostgres, "public.users")
	if err != nil {
		t.Fatalf("QuoteIdent postgres returned error: %v", err)
	}
	if postgresQuoted != `"public"."users"` {
		t.Fatalf("postgres quoted = %q", postgresQuoted)
	}

	mysqlJoined, err := JoinQuotedIdentifiers(DialectMySQL, []string{"users", "created_at"})
	if err != nil {
		t.Fatalf("JoinQuotedIdentifiers mysql returned error: %v", err)
	}
	if mysqlJoined != "`users`, `created_at`" {
		t.Fatalf("mysql joined = %q", mysqlJoined)
	}

	if _, err := QuoteIdent(DialectSQLite, "users;drop"); !errors.Is(err, ErrInvalidIdentifier) {
		t.Fatalf("QuoteIdent unsafe error = %v, want ErrInvalidIdentifier", err)
	}
}

func TestDialectLimitOffsetContracts(t *testing.T) {
	query, args, err := LimitOffset(DialectPostgres, 10, 20, 3)
	if err != nil {
		t.Fatalf("LimitOffset postgres returned error: %v", err)
	}
	if query != " LIMIT $3 OFFSET $4" {
		t.Fatalf("postgres clause = %q", query)
	}
	if len(args) != 2 || args[0] != 10 || args[1] != 20 {
		t.Fatalf("postgres args = %#v, want [10 20]", args)
	}

	query, args, err = LimitOffset(DialectSQLite, 5, 0, 1)
	if err != nil {
		t.Fatalf("LimitOffset sqlite returned error: %v", err)
	}
	if query != " LIMIT ?" {
		t.Fatalf("sqlite clause = %q", query)
	}
	if len(args) != 1 || args[0] != 5 {
		t.Fatalf("sqlite args = %#v, want [5]", args)
	}

	errorCases := []struct {
		name   string
		limit  int
		offset int
	}{
		{name: "negative limit", limit: -1},
		{name: "negative offset", limit: 1, offset: -1},
		{name: "offset without limit", limit: 0, offset: 1},
	}
	for _, tt := range errorCases {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := LimitOffset(DialectQuestion, tt.limit, tt.offset, 1); err == nil {
				t.Fatalf("LimitOffset(%d,%d) succeeded, want error", tt.limit, tt.offset)
			}
		})
	}
}

func TestDialectMethodsDelegateToHelpers_BitsUT(t *testing.T) {
	if got := DialectPostgres.Placeholder(3); got != "$3" {
		t.Fatalf("Dialect.Placeholder postgres = %q, want $3", got)
	}
	quoted, err := DialectMySQL.QuoteIdent("users.name")
	if err != nil {
		t.Fatalf("Dialect.QuoteIdent mysql: %v", err)
	}
	if quoted != "`users`.`name`" {
		t.Fatalf("Dialect.QuoteIdent mysql = %q, want backtick qualified identifier", quoted)
	}
	clause, args, err := DialectPostgres.LimitOffset(10, 20, 2)
	if err != nil {
		t.Fatalf("Dialect.LimitOffset postgres: %v", err)
	}
	if clause != " LIMIT $2 OFFSET $3" || len(args) != 2 || args[0] != 10 || args[1] != 20 {
		t.Fatalf("Dialect.LimitOffset = %q %#v, want postgres placeholders and args", clause, args)
	}
	if _, err := DialectQuestion.QuoteIdent("bad-name"); !errors.Is(err, ErrInvalidIdentifier) {
		t.Fatalf("Dialect.QuoteIdent invalid error = %v, want ErrInvalidIdentifier", err)
	}
	if _, _, err := DialectQuestion.LimitOffset(-1, 0, 1); err == nil || !strings.Contains(err.Error(), "limit must not be negative") {
		t.Fatalf("Dialect.LimitOffset negative error = %v, want negative limit error", err)
	}
}

func TestSQLMutationAndPageBuilders(t *testing.T) {
	updateQuery, err := UpdateByID("users", []string{"name", "age"}, "id", DialectPostgres)
	if err != nil {
		t.Fatalf("UpdateByID returned error: %v", err)
	}
	if updateQuery != "UPDATE users SET name = $1, age = $2 WHERE id = $3" {
		t.Fatalf("update query = %q", updateQuery)
	}

	deleteQuery, err := DeleteByID("users", "id", DialectQuestion)
	if err != nil {
		t.Fatalf("DeleteByID returned error: %v", err)
	}
	if deleteQuery != "DELETE FROM users WHERE id = ?" {
		t.Fatalf("delete query = %q", deleteQuery)
	}

	pageQuery, err := SelectPage("users", []string{"id", "name"}, "id", DialectQuestion)
	if err != nil {
		t.Fatalf("SelectPage returned error: %v", err)
	}
	if pageQuery != "SELECT id, name FROM users ORDER BY id LIMIT ? OFFSET ?" {
		t.Fatalf("page query = %q", pageQuery)
	}

	sqlitePageQuery, err := SelectPage("users", []string{"id", "name"}, "id", DialectSQLite)
	if err != nil {
		t.Fatalf("SelectPage sqlite returned error: %v", err)
	}
	if sqlitePageQuery != "SELECT id, name FROM users ORDER BY id LIMIT ? OFFSET ?" {
		t.Fatalf("sqlite page query = %q", sqlitePageQuery)
	}

	countQuery, err := CountAll("users")
	if err != nil {
		t.Fatalf("CountAll returned error: %v", err)
	}
	if countQuery != "SELECT COUNT(*) FROM users" {
		t.Fatalf("count query = %q", countQuery)
	}
}

func TestSQLAdvancedBuilders(t *testing.T) {
	batch, err := BatchInsert("users", []string{"id", "name"}, 2, DialectPostgres)
	if err != nil {
		t.Fatalf("BatchInsert returned error: %v", err)
	}
	if batch != "INSERT INTO users (id, name) VALUES ($1, $2), ($3, $4)" {
		t.Fatalf("batch insert = %q", batch)
	}
	upsert, err := Upsert("users", []string{"id", "name"}, []string{"id"}, []string{"name"}, DialectPostgres)
	if err != nil {
		t.Fatalf("Upsert returned error: %v", err)
	}
	if upsert != "INSERT INTO users (id, name) VALUES ($1, $2) ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name" {
		t.Fatalf("postgres upsert = %q", upsert)
	}
	mysqlUpsert, err := Upsert("users", []string{"id", "name"}, []string{"id"}, []string{"name"}, DialectMySQL)
	if err != nil {
		t.Fatalf("mysql Upsert returned error: %v", err)
	}
	if mysqlUpsert != "INSERT INTO users (id, name) VALUES (?, ?) ON DUPLICATE KEY UPDATE name = VALUES(name)" {
		t.Fatalf("mysql upsert = %q", mysqlUpsert)
	}
	locked, err := SelectForUpdate("jobs", []string{"id", "payload"}, "state", DialectQuestion, true)
	if err != nil {
		t.Fatalf("SelectForUpdate returned error: %v", err)
	}
	if locked != "SELECT id, payload FROM jobs WHERE state = ? FOR UPDATE SKIP LOCKED" {
		t.Fatalf("select for update = %q", locked)
	}
}

func TestSQLAdvancedBuildersRejectUnsafeInput(t *testing.T) {
	if _, err := BatchInsert("users", []string{"id"}, 0, DialectQuestion); err == nil {
		t.Fatal("BatchInsert rows=0 succeeded, want error")
	}
	if _, err := Upsert("users", []string{"id"}, []string{"id;drop"}, []string{"name"}, DialectQuestion); !errors.Is(err, ErrInvalidIdentifier) {
		t.Fatalf("Upsert unsafe conflict error = %v, want ErrInvalidIdentifier", err)
	}
	if _, err := SelectForUpdate("jobs", []string{"id"}, "state;drop", DialectQuestion, false); !errors.Is(err, ErrInvalidIdentifier) {
		t.Fatalf("SelectForUpdate unsafe error = %v, want ErrInvalidIdentifier", err)
	}
}

func TestSelectBuildersRejectEmptyColumns(t *testing.T) {
	if _, err := SelectByID("users", nil, "id", DialectQuestion); err == nil {
		t.Fatal("SelectByID with empty columns succeeded, want error")
	}
	if _, err := SelectPage("users", []string{}, "id", DialectQuestion); err == nil {
		t.Fatal("SelectPage with empty columns succeeded, want error")
	}
	if _, err := SelectForUpdate("jobs", nil, "state", DialectQuestion, false); err == nil {
		t.Fatal("SelectForUpdate with empty columns succeeded, want error")
	}
	if _, _, err := SelectWhere("orders", nil, NewWhere().Eq("id", 1), DialectQuestion); err == nil {
		t.Fatal("SelectWhere with empty columns succeeded, want error")
	}
}

func TestWhereBuilderBuildsParameterizedQuery(t *testing.T) {
	where := NewWhere().
		Eq("tenant_id", 42).
		In("state", "created", "paid").
		Between("created_at", 10, 20).
		OrderBy("created_at", true).
		Limit(10).
		Offset(20)
	query, args, err := SelectWhere("orders", []string{"id", "state"}, where, DialectPostgres)
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT id, state FROM orders WHERE tenant_id = $1 AND state IN ($2, $3) AND created_at BETWEEN $4 AND $5 ORDER BY created_at DESC LIMIT $6 OFFSET $7"
	if query != want {
		t.Fatalf("query = %q, want %q", query, want)
	}
	if got := len(args); got != 7 || args[0] != 42 || args[6] != 20 {
		t.Fatalf("args = %#v, want parameterized args", args)
	}
}

func TestWhereBuilderComparisonPredicates(t *testing.T) {
	where := NewWhere().
		Ne("state", "deleted").
		Gt("score", 10).
		Gte("created_at", 20).
		Lt("retry_count", 5).
		Lte("updated_at", 30).
		Like("email", "%@example.com")

	query, args, err := SelectWhere("users", []string{"id"}, where, DialectPostgres)
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT id FROM users WHERE state != $1 AND score > $2 AND created_at >= $3 AND retry_count < $4 AND updated_at <= $5 AND email LIKE $6"
	if query != want {
		t.Fatalf("query = %q, want %q", query, want)
	}
	wantArgs := []any{"deleted", 10, 20, 5, 30, "%@example.com"}
	if len(args) != len(wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
	for i, want := range wantArgs {
		if args[i] != want {
			t.Fatalf("args[%d] = %#v, want %#v; args=%#v", i, args[i], want, args)
		}
	}
}

func TestWhereBuilderNullPredicates(t *testing.T) {
	query, args, err := SelectWhere(
		"users",
		[]string{"id", "name"},
		NewWhere().IsNull("deleted_at").IsNotNull("created_at"),
		DialectQuestion,
	)
	if err != nil {
		t.Fatal(err)
	}
	if query != "SELECT id, name FROM users WHERE deleted_at IS NULL AND created_at IS NOT NULL" {
		t.Fatalf("query = %q", query)
	}
	if len(args) != 0 {
		t.Fatalf("args = %#v, want empty", args)
	}
}

func TestWhereBuilderRejectsUnsafeOrderBy(t *testing.T) {
	tests := []struct {
		name  string
		order string
	}{
		{name: "sql injection suffix", order: "created_at DESC; DROP TABLE orders"},
		{name: "caller supplied direction", order: "created_at DESC"},
		{name: "comma separated columns", order: "created_at, id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := SelectWhere("orders", []string{"id"}, NewWhere().OrderBy(tt.order), DialectQuestion)
			if !errors.Is(err, ErrInvalidIdentifier) {
				t.Fatalf("SelectWhere unsafe order error = %v, want ErrInvalidIdentifier", err)
			}
		})
	}
}

func TestWhereBuilderOrderByDirectionIsStructured(t *testing.T) {
	query, args, err := SelectWhere(
		"orders",
		[]string{"id"},
		NewWhere().OrderBy(" created_at ").OrderBy("id", true),
		DialectQuestion,
	)
	if err != nil {
		t.Fatal(err)
	}
	if query != "SELECT id FROM orders ORDER BY created_at ASC, id DESC" {
		t.Fatalf("query = %q, want structured order directions", query)
	}
	if len(args) != 0 {
		t.Fatalf("args = %#v, want no order args", args)
	}
}

func TestWhereBuilderEmptyInMatchesNothing(t *testing.T) {
	query, args, err := CountWhere("orders", NewWhere().In("state"), DialectQuestion)
	if err != nil {
		t.Fatal(err)
	}
	if query != "SELECT COUNT(*) FROM orders WHERE 1 = 0" || len(args) != 0 {
		t.Fatalf("query=%q args=%#v, want empty IN false predicate", query, args)
	}
}

func TestWhereBuilderRejectsOffsetWithoutLimit(t *testing.T) {
	_, _, err := SelectWhere("orders", []string{"id"}, NewWhere().Offset(10), DialectSQLite)
	if err == nil || !strings.Contains(err.Error(), "limit is required") {
		t.Fatalf("SelectWhere offset without limit error = %v, want limit required", err)
	}
}

func TestValidateIdentifierRejectsUnsafeInput(t *testing.T) {
	_, err := SelectByID("users;drop", []string{"id"}, "id", DialectQuestion)
	if !errors.Is(err, ErrInvalidIdentifier) {
		t.Fatalf("SelectByID error = %v, want ErrInvalidIdentifier", err)
	}
	_, err = Insert("users", []string{"id", "name from users"}, DialectQuestion)
	if !errors.Is(err, ErrInvalidIdentifier) {
		t.Fatalf("Insert error = %v, want ErrInvalidIdentifier", err)
	}
}

func TestSQLBuilderErrorBoundaries_BitsUT(t *testing.T) {
	errorCases := []struct {
		name string
		fn   func() error
	}{
		{name: "join invalid", fn: func() error { _, err := JoinIdentifiers([]string{"id", "bad-name"}); return err }},
		{name: "select bad id", fn: func() error { _, err := SelectByID("users", []string{"id"}, "bad-name", DialectQuestion); return err }},
		{name: "select bad column", fn: func() error { _, err := SelectByID("users", []string{"bad-name"}, "id", DialectQuestion); return err }},
		{name: "insert bad table", fn: func() error { _, err := Insert("bad-table", []string{"id"}, DialectQuestion); return err }},
		{name: "insert empty columns", fn: func() error { _, err := Insert("users", nil, DialectQuestion); return err }},
		{name: "batch bad table", fn: func() error { _, err := BatchInsert("bad-table", []string{"id"}, 1, DialectQuestion); return err }},
		{name: "batch empty columns", fn: func() error { _, err := BatchInsert("users", nil, 1, DialectQuestion); return err }},
		{name: "upsert insert error", fn: func() error {
			_, err := Upsert("bad-table", []string{"id"}, []string{"id"}, []string{"name"}, DialectQuestion)
			return err
		}},
		{name: "upsert no conflict", fn: func() error {
			_, err := Upsert("users", []string{"id"}, nil, []string{"name"}, DialectQuestion)
			return err
		}},
		{name: "upsert no updates", fn: func() error {
			_, err := Upsert("users", []string{"id"}, []string{"id"}, nil, DialectQuestion)
			return err
		}},
		{name: "upsert bad update", fn: func() error {
			_, err := Upsert("users", []string{"id"}, []string{"id"}, []string{"bad-name"}, DialectQuestion)
			return err
		}},
		{name: "update bad table", fn: func() error { _, err := UpdateByID("bad-table", []string{"name"}, "id", DialectQuestion); return err }},
		{name: "update empty columns", fn: func() error { _, err := UpdateByID("users", nil, "id", DialectQuestion); return err }},
		{name: "update bad id", fn: func() error { _, err := UpdateByID("users", []string{"name"}, "bad-name", DialectQuestion); return err }},
		{name: "update bad column", fn: func() error { _, err := UpdateByID("users", []string{"bad-name"}, "id", DialectQuestion); return err }},
		{name: "delete bad table", fn: func() error { _, err := DeleteByID("bad-table", "id", DialectQuestion); return err }},
		{name: "delete bad id", fn: func() error { _, err := DeleteByID("users", "bad-name", DialectQuestion); return err }},
		{name: "page bad table", fn: func() error { _, err := SelectPage("bad-table", []string{"id"}, "id", DialectQuestion); return err }},
		{name: "page bad order", fn: func() error { _, err := SelectPage("users", []string{"id"}, "bad-name", DialectQuestion); return err }},
		{name: "page bad column", fn: func() error { _, err := SelectPage("users", []string{"bad-name"}, "id", DialectQuestion); return err }},
		{name: "count bad table", fn: func() error { _, _, err := CountWhere("bad-table", nil, DialectQuestion); return err }},
		{name: "select for update bad table", fn: func() error {
			_, err := SelectForUpdate("bad-table", []string{"id"}, "id", DialectQuestion, false)
			return err
		}},
		{name: "select for update empty columns", fn: func() error { _, err := SelectForUpdate("users", nil, "id", DialectQuestion, false); return err }},
		{name: "select for update bad where", fn: func() error {
			_, err := SelectForUpdate("users", []string{"id"}, "bad-name", DialectQuestion, false)
			return err
		}},
		{name: "select for update bad column", fn: func() error {
			_, err := SelectForUpdate("users", []string{"bad-name"}, "id", DialectQuestion, false)
			return err
		}},
		{name: "count all bad table", fn: func() error { _, err := CountAll("bad-table"); return err }},
	}
	for _, tt := range errorCases {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.fn(); err == nil {
				t.Fatal("builder succeeded, want error")
			}
		})
	}
}

func TestSelectForUpdateAndCountAllBoundaries_BitsUT(t *testing.T) {
	query, err := SelectForUpdate("jobs", []string{"id", "state"}, "id", DialectPostgres, true)
	if err != nil {
		t.Fatalf("SelectForUpdate postgres skip locked error = %v", err)
	}
	want := "SELECT id, state FROM jobs WHERE id = $1 FOR UPDATE SKIP LOCKED"
	if query != want {
		t.Fatalf("SelectForUpdate postgres = %q, want %q", query, want)
	}

	query, err = SelectForUpdate("jobs", []string{"id"}, "id", DialectMySQL, false)
	if err != nil {
		t.Fatalf("SelectForUpdate mysql error = %v", err)
	}
	want = "SELECT id FROM jobs WHERE id = ? FOR UPDATE"
	if query != want {
		t.Fatalf("SelectForUpdate mysql = %q, want %q", query, want)
	}

	query, err = CountAll("jobs")
	if err != nil {
		t.Fatalf("CountAll error = %v", err)
	}
	if query != "SELECT COUNT(*) FROM jobs" {
		t.Fatalf("CountAll = %q", query)
	}
}

func TestWhereBuilderInternalBoundaries_BitsUT(t *testing.T) {
	if clause, args, err := ((*Where)(nil)).Build(DialectPostgres, 0); err != nil || clause != "" || args != nil {
		t.Fatalf("nil Where Build = %q %#v %v, want zero", clause, args, err)
	}
	query, args, err := SelectWhere("users", []string{"id"}, ((*Where)(nil)).IsNull("deleted_at").OrderBy(" ").Limit(0).Offset(0), DialectPostgres)
	if err != nil {
		t.Fatalf("nil receiver where helpers: %v", err)
	}
	if query != "SELECT id FROM users WHERE deleted_at IS NULL" || len(args) != 0 {
		t.Fatalf("query=%q args=%#v, want null predicate without blank order/limit/offset", query, args)
	}

	errorCases := []struct {
		name  string
		where *Where
	}{
		{name: "invalid clause column", where: &Where{clauses: []whereClause{{column: "bad-name", op: "=", args: []any{1}}}}},
		{name: "between wrong args", where: &Where{clauses: []whereClause{{column: "age", op: "BETWEEN", args: []any{1}}}}},
		{name: "null with args", where: &Where{clauses: []whereClause{{column: "deleted_at", op: "IS NULL", args: []any{1}}}}},
		{name: "default missing arg", where: &Where{clauses: []whereClause{{column: "id", op: "="}}}},
		{name: "invalid order column", where: &Where{orders: []orderClause{{column: "bad-name"}}}},
	}
	for _, tt := range errorCases {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := tt.where.Build(DialectQuestion, 0); err == nil {
				t.Fatal("Where.Build succeeded, want error")
			}
		})
	}
}

func TestNilSQLStore(t *testing.T) {
	var store *SQLStore
	if err := store.Close(); err != nil {
		t.Fatalf("nil Close error = %v", err)
	}
	if row := store.QueryRow(t.Context(), "SELECT 1"); row != nil {
		t.Fatalf("nil QueryRow = %#v, want nil row", row)
	}
	if err := store.Ping(t.Context()); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("nil Ping error = %v, want nil store error", err)
	}
}

func TestSQLStoreQueryRowAcceptsNilContext(t *testing.T) {
	store := NewSQLStore(fakeDB(t))
	row := store.QueryRow(context.TODO(), "SELECT 1")
	if row == nil {
		t.Fatal("QueryRow returned nil row for initialized store")
	}
}

func TestStoreStatsSnapshot(t *testing.T) {
	stats := NewStoreStats()
	stats.Observe("exec", 10*time.Millisecond, false)
	stats.Observe("exec", 30*time.Millisecond, true)
	snapshot := stats.Snapshot()
	got := snapshot["exec"]
	if got.Requests != 2 || got.Errors != 1 || got.MaxDuration != 30*time.Millisecond || got.AvgDuration != 20*time.Millisecond {
		t.Fatalf("stats = %#v, want aggregated exec stats", got)
	}

	store := NewSQLStore(nil)
	if store.Snapshot().Operations == nil {
		t.Fatal("store snapshot operations should be non-nil")
	}
}

func TestSQLStoreExecUsesConfiguredQueryTimeout(t *testing.T) {
	store := NewSQLStore(fakeDB(t), WithQueryTimeout(time.Millisecond))
	_, err := store.Exec(context.Background(), "block")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Exec timeout error = %v, want DeadlineExceeded", err)
	}
	snapshot := store.Snapshot().Operations["exec"]
	if snapshot.Requests != 1 || snapshot.Errors != 1 {
		t.Fatalf("exec stats = %#v, want one failed request", snapshot)
	}
}

func TestSQLStoreOptionalBreakerRejectsOpenCircuit(t *testing.T) {
	br := corebreaker.New(corebreaker.WithFailureThreshold(1), corebreaker.WithOpenTimeout(time.Hour))
	store := NewSQLStore(fakeDB(t), WithBreaker(br))
	if _, err := store.Exec(context.Background(), "fail"); err == nil {
		t.Fatal("first Exec succeeded, want driver failure")
	}
	_, err := store.Exec(context.Background(), "ok")
	if !errors.Is(err, corebreaker.ErrOpen) {
		t.Fatalf("second Exec error = %v, want breaker open", err)
	}
	if snapshot := store.Snapshot(); snapshot.Breaker == nil || snapshot.Operations["exec"].Errors != 2 {
		t.Fatalf("snapshot = %#v, want breaker snapshot and two exec errors", snapshot)
	}
}

func TestSQLStoreConfigAppliesGovernanceOptions(t *testing.T) {
	store := NewSQLStore(fakeDB(t), WithQueryTimeout(50*time.Millisecond), WithSlowThreshold(time.Nanosecond))
	if store.queryTimeout != 50*time.Millisecond || store.slowThreshold != time.Nanosecond {
		t.Fatalf("store config timeout=%s slow=%s, want applied options", store.queryTimeout, store.slowThreshold)
	}
}

func TestSQLStoreTransactHonorsCanceledContextAndRecordsStats(t *testing.T) {
	store := NewSQLStore(fakeDB(t))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := store.Transact(ctx, nil, func(context.Context, *sql.Tx) error {
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Transact canceled error = %v, want Canceled", err)
	}
	snapshot := store.Snapshot().Operations["transaction"]
	if snapshot.Requests != 1 || snapshot.Errors != 1 {
		t.Fatalf("transaction stats = %#v, want one failed request", snapshot)
	}
}

func TestSQLStoreTransactRollsBackOnCallbackError_BitsUT(t *testing.T) {
	store := NewSQLStore(fakeDB(t))
	boom := errors.New("boom")
	err := store.Transact(context.Background(), nil, func(context.Context, *sql.Tx) error {
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Transact callback error = %v, want boom", err)
	}
	snapshot := store.Snapshot().Operations["transaction"]
	if snapshot.Requests != 1 || snapshot.Errors != 1 {
		t.Fatalf("transaction stats = %#v, want one failed transaction", snapshot)
	}
}

func TestWithTxContracts(t *testing.T) {
	db := fakeDB(t)
	called := false
	if err := WithTx(context.Background(), db, nil, func(ctx context.Context, tx *sql.Tx) error {
		called = true
		if ctx == nil || tx == nil {
			t.Fatalf("WithTx callback ctx/tx = %#v/%#v, want non-nil", ctx, tx)
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTx success: %v", err)
	}
	if !called {
		t.Fatal("WithTx did not call callback")
	}

	boom := errors.New("boom")
	if err := WithTx(context.Background(), db, nil, func(context.Context, *sql.Tx) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("WithTx callback error = %v, want boom", err)
	}
	if err := WithTx(context.Background(), nil, nil, func(context.Context, *sql.Tx) error { return nil }); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("WithTx nil db err = %v, want nil db error", err)
	}
	if err := WithTx(context.Background(), db, nil, nil); err == nil || !strings.Contains(err.Error(), "transaction function") {
		t.Fatalf("WithTx nil callback err = %v, want callback error", err)
	}
}

func TestWithTxRollsBackAndRepanics(t *testing.T) {
	db := fakeDB(t)
	defer func() {
		if got := recover(); got != "panic in tx" {
			t.Fatalf("recover = %#v, want panic in tx", got)
		}
	}()
	_ = WithTx(context.Background(), db, nil, func(context.Context, *sql.Tx) error {
		panic("panic in tx")
	})
}

func TestSQLStoreQueryOneAndQueryAllAndQueryRows(t *testing.T) {
	store := NewSQLStore(fakeDB(t))
	// fake driver returns no rows, so Scan returns sql.ErrNoRows — that is a valid QueryOne outcome.
	if err := store.QueryOne(context.Background(), "SELECT 1", func(r *sql.Row) error { return r.Scan(new(int)) }); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("QueryOne: %v", err)
	}
	if err := store.QueryAll(context.Background(), "SELECT 1", func(r *sql.Rows) error { return nil }); err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	rows, err := store.QueryRows(context.Background(), "SELECT 1")
	if err != nil {
		t.Fatalf("QueryRows: %v", err)
	}
	_ = rows.Close()
}

func TestSQLStoreNilGuards(t *testing.T) {
	var nilStore *SQLStore
	if _, err := nilStore.Exec(context.Background(), "SELECT 1"); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("nil Exec = %v, want nil error", err)
	}
	if err := nilStore.QueryOne(context.Background(), "SELECT 1", nil); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("nil QueryOne = %v, want nil error", err)
	}
	if _, err := nilStore.QueryRows(context.Background(), "SELECT 1"); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("nil QueryRows = %v, want nil error", err)
	}
	if err := nilStore.QueryAll(context.Background(), "SELECT 1", nil); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("nil QueryAll = %v, want nil error", err)
	}
	if err := nilStore.Transact(context.Background(), nil, nil); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("nil Transact = %v, want nil error", err)
	}
	if err := nilStore.Ping(context.Background()); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("nil Ping = %v, want nil error", err)
	}
	if db := nilStore.DB(); db != nil {
		t.Fatal("nil DB should return nil")
	}

	// scan nil guard
	store := NewSQLStore(fakeDB(t))
	if err := store.QueryOne(context.Background(), "SELECT 1", nil); err == nil || !strings.Contains(err.Error(), "scan") {
		t.Fatalf("QueryOne nil scan = %v, want scan required error", err)
	}
	if err := store.QueryAll(context.Background(), "SELECT 1", nil); err == nil || !strings.Contains(err.Error(), "scan") {
		t.Fatalf("QueryAll nil scan = %v, want scan required error", err)
	}
}

func TestOpenValidationAndPoolConfig(t *testing.T) {
	if _, err := Open(context.Background(), Config{}); err == nil || !strings.Contains(err.Error(), "driver") {
		t.Fatalf("Open empty driver = %v, want driver error", err)
	}
	ApplyPoolConfig(nil, PoolConfig{MaxOpenConns: 10})
	_ = fakeDB(t)
	store, err := Open(context.Background(), Config{
		Driver:        fakeDriverName,
		DSN:           "",
		Ping:          time.Second,
		QueryTimeout:  50 * time.Millisecond,
		SlowThreshold: time.Nanosecond,
		Pool: PoolConfig{
			MaxOpenConns:    7,
			MaxIdleConns:    3,
			ConnMaxLifetime: time.Minute,
			ConnMaxIdleTime: time.Second,
		},
	})
	if err != nil {
		t.Fatalf("Open fake driver error = %v", err)
	}
	defer store.Close()
	if store.DB() == nil || store.queryTimeout != 50*time.Millisecond || store.slowThreshold != time.Nanosecond {
		t.Fatalf("opened store = %#v, want configured DB store", store)
	}
	if got := store.DB().Stats().MaxOpenConnections; got != 7 {
		t.Fatalf("MaxOpenConnections = %d, want 7", got)
	}
}

func TestWithAdaptiveAndStoreBreaker(t *testing.T) {
	abr := corebreaker.NewAdaptive()
	store := NewSQLStore(fakeDB(t), WithAdaptiveBreaker(abr))
	if store.breaker == nil {
		t.Fatal("WithAdaptiveBreaker should set breaker")
	}

	store2 := NewSQLStore(fakeDB(t), WithStoreBreaker(abr))
	if store2.breaker == nil || store2.breakerSnap != nil {
		t.Fatal("WithStoreBreaker should set breaker and nil snap")
	}
}

func TestStoreStatsNilGuard(t *testing.T) {
	var nilStats *StoreStats
	nilStats.Observe("op", time.Millisecond, false)
	if snap := nilStats.Snapshot(); len(snap) != 0 {
		t.Fatalf("nil Snapshot = %v, want empty", snap)
	}
}

func TestSQLStoreObserveNilStats(t *testing.T) {
	store := NewSQLStore(fakeDB(t))
	store.stats = nil
	// Should not panic
	_, _ = store.Exec(context.Background(), "SELECT 1")
}
