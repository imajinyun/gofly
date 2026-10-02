package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"

	"github.com/golang-migrate/migrate/v4/database"
)

var errHistory = errors.New("migration history verification failed")

const (
	migrationVersionTable  = "migrations"
	migrationChecksumTable = "checksums"
	legacyVersionTable     = "schema_migrations"
	legacyChecksumTable    = "gofly_migration_checksums"
)

type historyRow struct {
	Version        int
	Name, Up, Down string
}
type historyBackend interface {
	Ensure(context.Context) error
	List(context.Context) ([]historyRow, error)
	Record(context.Context, Entry) error
	Remove(context.Context, int) error
}

type sqlHistory struct {
	db     *sql.DB
	driver string
}

func (h sqlHistory) Ensure(ctx context.Context) error {
	_, err := h.db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS checksums (version BIGINT PRIMARY KEY, name VARCHAR(100) NOT NULL, up_sha256 VARCHAR(64) NOT NULL, down_sha256 VARCHAR(64) NOT NULL)")
	return err
}
func (h sqlHistory) List(ctx context.Context) ([]historyRow, error) {
	rows, err := h.db.QueryContext(ctx, "SELECT version,name,up_sha256,down_sha256 FROM checksums ORDER BY version")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []historyRow
	for rows.Next() {
		var row historyRow
		if err := rows.Scan(&row.Version, &row.Name, &row.Up, &row.Down); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
func (h sqlHistory) Record(ctx context.Context, e Entry) error {
	query := "INSERT INTO checksums(version,name,up_sha256,down_sha256) VALUES(?,?,?,?)"
	if h.driver == "postgres" {
		query = "INSERT INTO checksums(version,name,up_sha256,down_sha256) VALUES($1,$2,$3,$4)"
	}
	_, err := h.db.ExecContext(ctx, query, e.Version, e.Name, e.UpSHA256, e.DownSHA256)
	return err
}
func (h sqlHistory) Remove(ctx context.Context, version int) error {
	query := "DELETE FROM checksums WHERE version=?"
	if h.driver == "postgres" {
		query = "DELETE FROM checksums WHERE version=$1"
	}
	_, err := h.db.ExecContext(ctx, query, version)
	return err
}

func checkHistory(entries []Entry, rows []historyRow, current int) error {
	expected := map[int]Entry{}
	for _, entry := range entries {
		if entry.Version <= current {
			expected[entry.Version] = entry
		}
	}
	if current >= 0 {
		if _, ok := expected[current]; !ok {
			return fmt.Errorf("%w: current version %d is missing locally", errHistory, current)
		}
	}
	if len(expected) != len(rows) {
		return fmt.Errorf("%w: applied file set differs from recorded checksums; automatic adoption is disabled", errHistory)
	}
	for _, row := range rows {
		entry, ok := expected[row.Version]
		if !ok || row.Name != entry.Name || row.Up != entry.UpSHA256 || row.Down != entry.DownSHA256 {
			return fmt.Errorf("%w: checksum or name mismatch at version %d", errHistory, row.Version)
		}
	}
	return nil
}

// guardedDriver keeps golang-migrate's lock and dirty state authoritative.
// Checksums are written before the engine can clear dirty; a crash in that window
// leaves dirty set and requires operator inspection, never automatic repair.
type guardedDriver struct {
	database.Driver
	ctx                       context.Context
	history                   historyBackend
	entries                   []Entry
	action                    string
	steps                     int
	previous, target, changed int
}

func (d *guardedDriver) Lock() (err error) {
	if err = d.ctx.Err(); err != nil {
		return err
	}
	if err = d.Driver.Lock(); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, d.Unlock())
		}
	}()
	if err = d.history.Ensure(d.ctx); err != nil {
		return err
	}
	version, dirty, err := d.Version()
	if err != nil {
		return err
	}
	// Status must remain available to inspect dirty failures; the engine rejects writes.
	if dirty {
		return nil
	}
	rows, err := d.history.List(d.ctx)
	if err != nil {
		return err
	}
	if err = checkHistory(d.entries, rows, version); err != nil {
		return err
	}
	if d.action == "down" && d.steps > len(rows) {
		return fmt.Errorf("%w: requested rollback exceeds applied migrations", errHistory)
	}
	if d.action == "up" && d.steps > len(d.entries)-len(rows) {
		return fmt.Errorf("%w: requested steps exceed pending migrations", errHistory)
	}
	return nil
}

func (d *guardedDriver) SetVersion(version int, dirty bool) error {
	if err := d.ctx.Err(); err != nil {
		return err
	}
	if dirty {
		previous, _, err := d.Version()
		if err != nil {
			return err
		}
		d.previous = previous
		d.target = version
	}
	return d.Driver.SetVersion(version, dirty)
}

func (d *guardedDriver) Run(reader io.Reader) error {
	if err := d.ctx.Err(); err != nil {
		return err
	}
	if err := d.Driver.Run(reader); err != nil {
		return err
	}
	if d.action == "down" {
		if err := d.history.Remove(d.ctx, d.previous); err != nil {
			return err
		}
	} else {
		var found bool
		for _, entry := range d.entries {
			if entry.Version == d.target {
				found = true
				if err := d.history.Record(d.ctx, entry); err != nil {
					return err
				}
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: target version is missing", errHistory)
		}
	}
	d.changed++
	return nil
}
