package command

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/imajinyun/gofly/cmd/gofly/internal/migration"
	"github.com/imajinyun/gofly/core/proc"
)

func migrateCommand(args []string) error {
	if printCommandHelp("migrate", args) {
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("%w: expected gofly migrate create|gen|validate|up|down|status", errUsage)
	}
	action := args[0]
	if action == "new" {
		action = "create"
	}
	switch action {
	case "create", "gen":
		return migrateCreateCommand(action, args[1:])
	case "validate", "up", "down", "status":
		return migrateRunCommand(action, args[1:])
	default:
		return fmt.Errorf("%w: unsupported migration command %q", errUsage, action)
	}
}

func migrateCreateCommand(action string, args []string) error {
	fs := flag.NewFlagSet("migrate "+action, flag.ContinueOnError)
	name := fs.String("name", "", "migration name")
	dir := fs.String("dir", "migrations", "migration directory")
	version := fs.String("version", "", "explicit positive version (default UTC timestamp)")
	asJSON := fs.Bool("json", false, "output JSON")
	var ddl, dialect string
	if action == "gen" {
		fs.StringVar(&ddl, "from-ddl", "", "initial CREATE TABLE SQL file")
		fs.StringVar(&dialect, "dialect", "", "mysql or postgres")
	}
	leading, args := splitLeadingName(args)
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *name == "" {
		*name = leading
	}
	if *name == "" && len(remaining) > 0 {
		*name = remaining[0]
		remaining = remaining[1:]
	}
	if strings.TrimSpace(*name) == "" {
		return fmt.Errorf("%w: migration name is required", errUsage)
	}
	if *version != "" {
		if _, err := migration.ParseVersion(*version); err != nil {
			return fmt.Errorf("%w: %s", errUsage, err)
		}
	}
	if action == "gen" && (ddl == "" || (dialect != "mysql" && dialect != "postgres") || len(remaining) > 0) {
		return fmt.Errorf("%w: gen requires a name, --from-ddl and --dialect mysql|postgres", errUsage)
	}
	report, err := migration.Create(migration.CreateOptions{Name: *name, Dir: *dir, Version: *version, DDLFile: ddl, Dialect: dialect})
	return printMigrationResult(action, report, err, *asJSON)
}

func migrateRunCommand(action string, args []string) error {
	fs := flag.NewFlagSet("migrate "+action, flag.ContinueOnError)
	dir := fs.String("dir", "migrations", "migration directory")
	asJSON := fs.Bool("json", false, "output JSON")
	var driver, dsnEnv string
	var timeout time.Duration
	var steps int
	if action != "validate" {
		fs.StringVar(&driver, "driver", "", "mysql or postgres")
		fs.StringVar(&dsnEnv, "dsn-env", "GOFLY_MIGRATE_DSN", "environment variable containing DSN")
		fs.DurationVar(&timeout, "timeout", 30*time.Second, "operation timeout")
	}
	if action == "up" || action == "down" {
		fs.IntVar(&steps, "steps", 0, "number of migrations (required for down)")
	}
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if len(remaining) > 0 {
		return fmt.Errorf("%w: unexpected migration arguments", errUsage)
	}
	if action == "validate" {
		report, err := migration.Validate(*dir)
		return printMigrationResult(action, report, err, *asJSON)
	}
	if (driver != "mysql" && driver != "postgres") || timeout <= 0 || timeout > time.Hour || steps < 0 || action == "down" && steps == 0 {
		return fmt.Errorf("%w: use --driver mysql|postgres, --timeout between 0 and 1h, and positive --steps for down", errUsage)
	}
	dsn := os.Getenv(dsnEnv)
	if dsn == "" {
		return fmt.Errorf("%w: DSN environment variable is empty", errUsage)
	}
	ctx, cancel := proc.SignalContext(context.Background())
	defer cancel()
	report, err := migration.Run(ctx, action, migration.RunOptions{Dir: *dir, Driver: driver, DSN: dsn, Timeout: timeout, Steps: steps})
	return printMigrationResult(action, report, err, *asJSON)
}

func printMigrationResult(action string, report migration.Report, err error, asJSON bool) error {
	if err != nil {
		if asJSON && outputMode() != outputJSON {
			if printErr := printJSONError("migrate."+action, err); printErr != nil {
				return errors.Join(err, printErr)
			}
			return errors.Join(errJSONAlreadyReported, err)
		}
		return err
	}
	if asJSON || outputMode() == outputJSON {
		return printJSONEnvelope("migrate."+action, report)
	}
	for _, file := range report.Files {
		cliOutputfIf("created %s\n", file)
	}
	if action == "create" || action == "gen" {
		return nil
	}
	if action != "validate" {
		cliOutputfIf("version=%d dirty=%t changed=%d\n", report.CurrentVersion, report.Dirty, report.Changed)
	}
	for _, entry := range report.Migrations {
		cliOutputfIf("%d %s %s\n", entry.Version, entry.Name, entry.Status)
	}
	cliOutputfIf("%s: %d migration pairs\n", action, len(report.Migrations))
	return nil
}
