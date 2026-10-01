# Versioned SQL migrations

Use `gofly migrate` to generate SQL files and explicitly apply them to MySQL or
PostgreSQL. `migration` aliases `migrate`; `new` aliases `create`. The runner uses
the pinned golang-migrate engine, database locks, dirty-state detection and an
additional checksum ledger. Applications never run these commands at startup.

## Create or generate a migration

```sh
# Empty, editable up/down templates.
gofly migrate create add_user_email --dir migrations

# Initial schema: preserve reviewed CREATE TABLE definitions verbatim.
gofly migrate gen init_users --from-ddl schema/users.sql --dialect mysql --dir migrations

# Explicit versions make generated filenames reproducible.
gofly migrate gen init_users --from-ddl schema/users.sql --dialect postgres --dir migrations --version 1 --json
```

The output is `<version>_<name>.up.sql` and `<version>_<name>.down.sql`. The default
version is a UTC timestamp with second precision. A version collision fails;
use another explicit positive version or retry in a later second. Existing
files are never overwritten. Pair creation uses an exclusive directory lock
and removes files created by a failed write. After a process crash, inspect any
partial pair before removing `.gofly-migration.lock` and retrying.

Older gofly versions used local timestamps. When extending such a directory,
use `--version` to preserve ordering after its highest existing version.

Initial generation accepts unconditional `CREATE TABLE name (...)` statements
with MySQL backtick or PostgreSQL double-quoted identifiers, including qualified
names. It preserves the original SQL, column definitions, composite keys and
constraints. Down SQL drops tables in reverse input order, without CASCADE.
Order input tables so referenced tables are created first. DROP TABLE destroys
table data; a down migration is not a backup or a data recovery mechanism.

Non-CREATE statements, executable comments, duplicate tables, `IF NOT EXISTS`,
and `CREATE TABLE AS SELECT` are rejected. This is an initial-DDL generator,
not a complete SQL syntax validator or an incremental schema diff engine.
The target database validates SQL during execution. Editing an initial schema
and regenerating it does not produce ALTER statements. Write a new migration
for subsequent changes; Atlas integration for automated diff is future work.

Migration files must be regular files, not symlinks, within the selected
directory. Each file is limited to 8 MiB and each snapshot to 64 MiB. Every SQL
file must have a valid paired filename; unrelated documentation is allowed.
Empty templates can be validated structurally but cannot be executed until
they contain SQL. No ORM or Go-model schema inference is performed.

Explicit transaction blocks must end with COMMIT or ROLLBACK in the same file.
Chained/prepared transactions, session autocommit changes and indirect SQL
execution through CALL/PREPARE/EXECUTE are unsupported. Quoted SQL must use
doubled quotes rather than ambiguous backslash escapes. PostgreSQL execution
also checks the actual session transaction state before recording success.

## Validate, apply and inspect

```sh
gofly migrate validate --dir migrations --json

# Set this secret through your deployment environment or secret manager.
# MySQL uses a go-sql-driver/mysql DSN; PostgreSQL accepts a pgx DSN or URL.
gofly migrate status --driver mysql --dir migrations --dsn-env DATABASE_DSN --json
gofly migrate up --driver mysql --dir migrations --dsn-env DATABASE_DSN
gofly migrate up --driver postgres --steps 1 --dir migrations
gofly migrate down --driver postgres --steps 1 --dir migrations
```

The default secret variable is `GOFLY_MIGRATE_DSN`. DSNs are never included in
reports or command arguments. `--timeout` defaults to 30 seconds and must be
positive and at most one hour. Connections and SQL execution are bounded;
cancellation prevents further migrations. Prefer deployment jobs with an
account authorized for DDL. Use TLS in the supplied driver configuration when
connecting across a network.

`up` applies all pending versions unless `--steps` is supplied. Repeating it
with no pending versions succeeds without running SQL again. `down` always
requires a positive `--steps`; there is no implicit "roll back everything".
`status` reports current version (`-1` for none), dirty state and local entries.
On a dirty database, pending/applied labels are provisional; the version and
dirty flag describe the interrupted transition, not proof that SQL completed.

`validate` is offline: it checks file names, pairs, versions and filesystem
boundaries. Snapshot readers and pair writers acquire the same exclusive
`.gofly-migration.lock`; the selected directory must be writable even for
validation/status. A busy reader or writer fails immediately and can be retried. Database-connected commands initialize their metadata tables and
verify checksums under the same database lock used for execution. Status may
therefore initialize metadata on a fresh database. The engine's
`schema_migrations` table and `gofly_migration_checksums` must remain in the same
database/schema and be managed by the same deployment workflow.

Checksums cover both directions of every applied migration. Editing, renaming,
removing applied files or inserting an older version fails closed. Migration
SQL is read into a bounded snapshot before execution, so subsequent file edits
cannot change the SQL associated with the stored checksum. Checksums verify
migration history; they do not detect manual changes to the live schema.

## Failure and recovery

The engine marks a transition dirty before executing SQL. The checksum update
must succeed before it clears dirty. SQL errors, cancellation, process failure
or checksum persistence failures can leave dirty state. Subsequent writes are
blocked; `status` remains available. Database errors expose safe driver codes
where available, never raw SQL, SQL literals or driver connection messages.

Do not blindly clear dirty state. Inspect the database, the reviewed migration
and the checksum ledger, restore or complete the intended change, then repair
metadata through an operator-reviewed recovery procedure. MySQL DDL may commit
partially even when a later statement fails. PostgreSQL transaction behavior
depends on the SQL batch and any explicit transaction statements. There is no
automatic rollback guarantee across databases.

There is intentionally no `force`, automatic baseline adoption or automatic
schema repair. An existing database migrated solely with another runner lacks
gofly checksum history and is rejected. Do not alternate external runners and
gofly against the same metadata without an explicit future adoption workflow.
The current capability is a CLI runner; generated applications do not receive
an embedded public migration API or a startup hook.

## JSON and verification

Use global `--output json` for a consistent success/error envelope and exit
codes: 0 success, 1 operational failure, 2 invalid usage. `--json` is also
available on each migration command. Result schema `gofly.migration.v1` exposes
`action`, actual `files`, `migrations`, `currentVersion`, `dirty`, and `changed`.

```sh
make migration-check

# Requires a new, empty disposable database; never supply a production DSN.
GOFLY_MIGRATION_TEST_DRIVER=mysql \
GOFLY_MIGRATION_TEST_DSN="$DISPOSABLE_DATABASE_DSN" \
make migration-integration-check
```

The offline gate builds and invokes the real CLI, checks preserved DDL and
collision handling, and retains `.tmp-test/migration-report.json` with SQL and
checksums. The integration gate checks upgrades, idempotency, rollback,
retained rows, tampering and dirty-state failures and retains
`.tmp-test/migration-integration.json`. Repeat it separately with `postgres`
and a new disposable database. It refuses to run without explicit test
connection settings.

Full-repository `make cover-check` also starts disposable MySQL and PostgreSQL
containers pinned by digest and runs these E2Es with coverage enabled. It merges
fresh counters into the ordinary test profile while rejecting unknown source
blocks or changed statement counts; the denominator and 90.5% ratchet remain
unchanged. This full coverage gate, and therefore `make governance-10-rounds`,
requires a reachable Docker daemon. Profiles and database reports are retained
under `.tmp-test/migration-coverage/`. Targeted package coverage does not start
database containers.
The requested `COVERAGE_PROFILE` contains the merged result; the separate
baseline and combined profiles are retained with the database evidence.
