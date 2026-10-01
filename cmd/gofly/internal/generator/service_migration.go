package generator

import (
	"regexp"
	"strings"
	"time"

	"github.com/imajinyun/gofly/cmd/gofly/internal/migration"
)

type MigrationOptions struct {
	Name string
	Dir  string
	Time time.Time
}

func GenerateMigration(opts MigrationOptions) error {
	_, err := migration.Create(migration.CreateOptions{Name: opts.Name, Dir: opts.Dir, Time: opts.Time})
	return err
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
