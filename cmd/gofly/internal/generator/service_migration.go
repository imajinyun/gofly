package generator

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type MigrationOptions struct {
	Name string
	Dir  string
	Time time.Time
}

func GenerateMigration(opts MigrationOptions) error {
	if strings.TrimSpace(opts.Name) == "" {
		return errors.New("migration name is required")
	}
	if opts.Dir == "" {
		opts.Dir = filepath.Join(".", "migrations")
	}
	now := opts.Time
	if now.IsZero() {
		now = time.Now()
	}
	name := migrationName(opts.Name)
	stamp := now.Format("20060102150405")
	files := map[string]string{
		filepath.Join(opts.Dir, stamp+"_"+name+".up.sql"):   "-- write forward migration SQL here\n",
		filepath.Join(opts.Dir, stamp+"_"+name+".down.sql"): "-- write rollback migration SQL here\n",
	}
	for path, content := range files {
		if err := writeGeneratedFile(path, []byte(content)); err != nil {
			return fmt.Errorf("write migration file: %w", err)
		}
	}
	return nil
}

var migrationNameRE = regexp.MustCompile(`[^a-z0-9_]+`)

func migrationName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, " ", "_")
	name = migrationNameRE.ReplaceAllString(name, "_")
	name = strings.Trim(name, "_")
	if name == "" {
		return "migration"
	}
	return name
}
