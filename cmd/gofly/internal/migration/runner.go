package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	mysqlsql "github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	migratemysql "github.com/golang-migrate/migrate/v4/database/mysql"
	migratepg "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

// RunOptions contains the selected database and bounded execution settings.
// DSN is never included in a report or returned error message.
type RunOptions struct {
	Dir, Driver, DSN string
	Timeout          time.Duration
	Steps            int
}

// Run executes a migration operation explicitly; it is never called at service startup.
func Run(ctx context.Context, action string, opts RunOptions) (report Report, err error) {
	report = newReport(action)
	if err = ctx.Err(); err != nil {
		return report, err
	}
	if action != "up" && action != "down" && action != "status" {
		return report, errors.New("unsupported migration action")
	}
	if opts.Driver != "mysql" && opts.Driver != "postgres" {
		return report, errors.New("driver must be mysql or postgres")
	}
	if opts.DSN == "" {
		return report, errors.New("migration DSN is empty")
	}
	if opts.Steps < 0 || action == "down" && opts.Steps == 0 {
		return report, errors.New("rollback requires positive steps")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	validated, err := Validate(opts.Dir)
	if err != nil {
		return report, err
	}
	report.Migrations = validated.Migrations
	db, base, err := openDatabase(ctx, opts)
	if err != nil {
		return report, safeDatabaseError("connect", err)
	}
	defer func() { err = errors.Join(err, safeDatabaseError("close", db.Close())) }()
	driver := &guardedDriver{Driver: contextDriver{Driver: base, db: db, ctx: ctx}, ctx: ctx, history: sqlHistory{db: db, driver: opts.Driver}, entries: report.Migrations, action: action, steps: opts.Steps}
	defer func() { err = errors.Join(err, safeDatabaseError("close driver", base.Close())) }()
	if action == "status" {
		if err = driver.Lock(); err != nil {
			return report, safeDatabaseError("lock and validate", err)
		}
		defer func() { err = errors.Join(err, safeDatabaseError("unlock", driver.Unlock())) }()
		err = fillStatus(&report, driver)
		return report, safeDatabaseError("status", err)
	}
	engine, err := migrate.NewWithInstance("gofly-snapshot", snapshotSource{entries: report.Migrations}, opts.Driver, driver)
	if err != nil {
		return report, safeDatabaseError("initialize", err)
	}
	engine.LockTimeout = opts.Timeout
	stop := context.AfterFunc(ctx, func() {
		select {
		case engine.GracefulStop <- true:
		default:
		}
	})
	defer stop()
	if action == "down" {
		err = engine.Steps(-opts.Steps)
	} else if opts.Steps > 0 {
		err = engine.Steps(opts.Steps)
	} else {
		err = engine.Up()
	}
	if errors.Is(err, migrate.ErrNoChange) {
		err = nil
	}
	if err == nil {
		err = ctx.Err()
	}
	report.Changed = driver.changed
	if err != nil {
		return report, safeDatabaseError(action, err)
	}
	if err = fillStatus(&report, driver); err != nil {
		return report, safeDatabaseError("status", err)
	}
	return report, nil
}

// The engine drivers retain lock/version ownership. Their Run methods create
// background contexts, so use the same SQL batch semantics through ExecContext
// to propagate caller cancellation to the database driver as well.
type contextDriver struct {
	database.Driver
	db  *sql.DB
	ctx context.Context
}

func (d contextDriver) Run(reader io.Reader) (err error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxSQLBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxSQLBytes {
		return errors.New("migration SQL exceeds 8 MiB")
	}
	// Migration SQL is explicitly reviewed operator code from a validated local
	// snapshot; it is never constructed from request parameters or DSN values.
	conn, err := d.db.Conn(d.ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, conn.Close()) }()
	_, err = conn.ExecContext(d.ctx, string(data))
	if err != nil {
		return err
	}
	// PostgreSQL reports whether an explicit transaction is still open even
	// when Exec itself succeeded. Never let such a session reach checksum writes.
	err = conn.Raw(func(raw any) error {
		if pg, ok := raw.(*stdlib.Conn); ok && pg.Conn().PgConn().TxStatus() != 'I' {
			return errors.New("migration SQL left an uncommitted transaction")
		}
		return nil
	})
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, rollbackErr := conn.ExecContext(cleanup, "ROLLBACK")
		return errors.Join(err, rollbackErr)
	}
	return err
}

func fillStatus(report *Report, driver *guardedDriver) error {
	version, dirty, err := driver.Version()
	if err != nil {
		return err
	}
	report.CurrentVersion = version
	report.Dirty = dirty
	for i := range report.Migrations {
		entry := &report.Migrations[i]
		entry.Status = "pending"
		if entry.Version <= version {
			entry.Status = "applied"
		}
		if dirty && entry.Version == version {
			entry.Status = "dirty"
		}
	}
	return nil
}

func openDatabase(ctx context.Context, opts RunOptions) (db *sql.DB, driver database.Driver, err error) {
	defer func() {
		if err != nil && db != nil {
			err = errors.Join(err, db.Close())
		}
	}()
	if opts.Driver == "mysql" {
		cfg, e := mysqlsql.ParseDSN(opts.DSN)
		if e != nil {
			return nil, nil, e
		}
		if cfg.DBName == "" {
			return nil, nil, errors.New("database name is required")
		}
		cfg.MultiStatements = true
		cfg.Timeout = opts.Timeout
		cfg.ReadTimeout = opts.Timeout
		cfg.WriteTimeout = opts.Timeout
		connector, e := mysqlsql.NewConnector(cfg)
		if e != nil {
			return nil, nil, e
		}
		db = sql.OpenDB(connector)
	} else {
		cfg, e := pgx.ParseConfig(opts.DSN)
		if e != nil {
			return nil, nil, e
		}
		cfg.ConnectTimeout = opts.Timeout
		cfg.RuntimeParams["statement_timeout"] = strconv.FormatInt(max(1, opts.Timeout.Milliseconds()), 10)
		cfg.RuntimeParams["lock_timeout"] = cfg.RuntimeParams["statement_timeout"]
		db = stdlib.OpenDB(*cfg)
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err = db.PingContext(ctx); err != nil {
		return db, nil, err
	}
	if err = migrateLegacyMetadataTables(ctx, db, opts.Driver); err != nil {
		return db, nil, err
	}
	if opts.Driver == "mysql" {
		driver, err = migratemysql.WithInstance(db, &migratemysql.Config{MigrationsTable: migrationVersionTable, StatementTimeout: opts.Timeout})
	} else {
		driver, err = migratepg.WithInstance(db, &migratepg.Config{MigrationsTable: migrationVersionTable, StatementTimeout: opts.Timeout})
	}
	return db, driver, err
}

// migrateLegacyMetadataTables renames only the complete metadata pair created
// by pre-rename gofly versions. Partial or mixed state fails closed because it
// can belong to an external runner or an interrupted manual intervention.
func migrateLegacyMetadataTables(ctx context.Context, db *sql.DB, driver string) error {
	migrationsExists, err := migrationMetadataTableExists(ctx, db, driver, migrationVersionTable)
	if err != nil {
		return err
	}
	checksumsExists, err := migrationMetadataTableExists(ctx, db, driver, migrationChecksumTable)
	if err != nil {
		return err
	}
	legacyMigrationsExists, err := migrationMetadataTableExists(ctx, db, driver, legacyVersionTable)
	if err != nil {
		return err
	}
	legacyChecksumsExists, err := migrationMetadataTableExists(ctx, db, driver, legacyChecksumTable)
	if err != nil {
		return err
	}

	if !legacyMigrationsExists && !legacyChecksumsExists {
		if migrationsExists == checksumsExists {
			return nil
		}
		return errors.New("migration metadata is incomplete; migrations and checksums must both exist")
	}
	if migrationsExists || checksumsExists || !legacyMigrationsExists || !legacyChecksumsExists {
		return errors.New("legacy migration metadata conflicts with migrations/checksums; resolve the table state manually")
	}
	if driver == "mysql" {
		_, err = db.ExecContext(ctx, "RENAME TABLE schema_migrations TO migrations, gofly_migration_checksums TO checksums")
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, "ALTER TABLE schema_migrations RENAME TO migrations"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "ALTER TABLE gofly_migration_checksums RENAME TO checksums"); err != nil {
		return err
	}
	return tx.Commit()
}

func migrationMetadataTableExists(ctx context.Context, db *sql.DB, driver, table string) (bool, error) {
	if driver == "postgres" {
		var exists bool
		err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = $1)", table).Scan(&exists)
		return exists, err
	}
	var count int
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?", table).Scan(&count)
	return count > 0, err
}

// Database errors may contain passwords, raw migration SQL and user data.
// Keep only stable driver codes and safe local integrity errors in CLI output.
func safeDatabaseError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("migration %s: %w", operation, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("migration %s: %w", operation, context.DeadlineExceeded)
	}
	if errors.Is(err, errHistory) {
		return err
	}
	var dirty migrate.ErrDirty
	if errors.As(err, &dirty) {
		return fmt.Errorf("migration database is dirty at version %d; inspect and repair before retrying", dirty.Version)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return fmt.Errorf("migration %s failed (SQLSTATE %s); inspect the database and migration files", operation, pgErr.Code)
	}
	var mysqlErr *mysqlsql.MySQLError
	if errors.As(err, &mysqlErr) {
		return fmt.Errorf("migration %s failed (MySQL error %d); inspect the database and migration files", operation, mysqlErr.Number)
	}
	return fmt.Errorf("migration %s failed; inspect connectivity, migration files and database state", operation)
}
