package migration

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4/database"
)

func TestMigrationHistoryIntegrity(t *testing.T) {
	entries := []Entry{{Version: 1, Name: "init", UpSHA256: "up1", DownSHA256: "down1"}, {Version: 2, Name: "email", UpSHA256: "up2", DownSHA256: "down2"}}
	tests := []struct {
		name      string
		current   int
		rows      []historyRow
		wantError bool
	}{
		{"empty database", -1, nil, false},
		{"first applied", 1, []historyRow{{1, "init", "up1", "down1"}}, false},
		{"missing history", 1, nil, true},
		{"changed up", 1, []historyRow{{1, "init", "changed", "down1"}}, true},
		{"changed down", 1, []historyRow{{1, "init", "up1", "changed"}}, true},
		{"renamed", 1, []historyRow{{1, "renamed", "up1", "down1"}}, true},
		{"missing earlier version", 2, []historyRow{{2, "email", "up2", "down2"}}, true},
		{"unknown version", 3, []historyRow{{3, "removed", "up3", "down3"}}, true},
		{"history beyond version", -1, []historyRow{{1, "init", "up1", "down1"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkHistory(entries, tt.rows, tt.current)
			if (err != nil) != tt.wantError {
				t.Fatalf("current=%d rows=%v error=%v", tt.current, tt.rows, err)
			}
		})
	}
}

func TestMigrationMetadataFailureLeavesDirty(t *testing.T) {
	base := &migrationTestDriver{version: -1}
	store := &migrationTestHistory{recordErr: errors.New("storage unavailable")}
	d := &guardedDriver{Driver: base, ctx: context.Background(), history: store, action: "up", entries: []Entry{{Version: 1, Name: "init"}}}
	if err := d.SetVersion(1, true); err != nil {
		t.Fatal(err)
	}
	if err := d.Run(strings.NewReader("CREATE TABLE users(id INT)")); err == nil {
		t.Fatal("metadata failure was ignored")
	}
	if !base.dirty {
		t.Fatal("metadata failure cleared dirty state")
	}
	if !base.ran {
		t.Fatal("did not exercise SQL-success / metadata-failure boundary")
	}
}

func TestMigrationRunnerCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(ctx, "up", RunOptions{Dir: t.TempDir(), Driver: "mysql", DSN: "DO_NOT_LEAK"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled run: %v", err)
	}
}

func TestMigrationErrorRedaction(t *testing.T) {
	err := safeDatabaseError("execute", errors.New("mysql://user:DO_NOT_LEAK@host SELECT secret"))
	if strings.Contains(err.Error(), "DO_NOT_LEAK") || strings.Contains(err.Error(), "SELECT") {
		t.Fatalf("leaked error: %v", err)
	}
	if !errors.Is(safeDatabaseError("execute", context.Canceled), context.Canceled) {
		t.Fatal("lost cancellation classification")
	}
}

func TestMigrationSourceTransactionBoundaries(t *testing.T) {
	for _, body := range []string{
		"BEGIN; CREATE TABLE users(id INT);",
		"START TRANSACTION; SELECT 1;",
		"SET autocommit=0; SELECT 1;",
		"BEGIN; SELECT 1; COMMIT AND CHAIN;",
	} {
		t.Run(body, func(t *testing.T) {
			s := snapshotSource{entries: []Entry{{Version: 1, Name: "test", upSQL: []byte(body), downSQL: []byte("SELECT 1;")}}}
			reader, _, err := s.ReadUp(1)
			if reader != nil {
				reader.Close()
			}
			if err == nil {
				t.Fatal("accepted migration with an open or chained transaction")
			}
		})
	}
}

func TestMigrationSourceRejectsEmptyRollback(t *testing.T) {
	s := snapshotSource{entries: []Entry{{Version: 1, Name: "test", upSQL: []byte("CREATE TABLE users(id INT);"), downSQL: []byte("-- fill rollback before applying")}}}
	reader, _, err := s.ReadUp(1)
	if reader != nil {
		reader.Close()
	}
	if err == nil {
		t.Fatal("accepted an unfinished rollback that would be frozen by applied checksums")
	}
}

type migrationTestDriver struct {
	database.Driver
	version    int
	dirty, ran bool
}

func (d *migrationTestDriver) Version() (int, bool, error) { return d.version, d.dirty, nil }
func (d *migrationTestDriver) SetVersion(v int, dirty bool) error {
	d.version = v
	d.dirty = dirty
	return nil
}
func (d *migrationTestDriver) Run(io.Reader) error { d.ran = true; return nil }

type migrationTestHistory struct{ recordErr error }

func (h *migrationTestHistory) Ensure(context.Context) error               { return nil }
func (h *migrationTestHistory) List(context.Context) ([]historyRow, error) { return nil, nil }
func (h *migrationTestHistory) Record(context.Context, Entry) error        { return h.recordErr }
func (h *migrationTestHistory) Remove(context.Context, int) error          { return nil }
