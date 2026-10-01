#!/usr/bin/env sh
set -eu

root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
cd "$root"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
export GOCACHE="${GOCACHE:-$tmp/gocache}"
export GOTMPDIR="${GOTMPDIR:-$tmp/gotmp}"
export GOPROXY=direct
export GOFLAGS="${GOFLAGS:--count=1}"
mkdir -p "$GOCACHE" "$GOTMPDIR"
go_cmd="${GO:-go}"
python3 -B -m unittest discover -s bin/scripts/tests -p test_merge_migration_coverage.py

if [ "${MIGRATION_INTEGRATION:-false}" = true ]; then
    : "${GOFLY_MIGRATION_TEST_DRIVER:?set mysql or postgres for an empty disposable database}"
    : "${GOFLY_MIGRATION_TEST_DSN:?supply an empty disposable database DSN}"
    export GOFLY_MIGRATION_TEST_REPORT="${GOFLY_MIGRATION_TEST_REPORT:-$root/.tmp-test/migration-integration.json}"
    mkdir -p "$(dirname -- "$GOFLY_MIGRATION_TEST_REPORT")"
    "$go_cmd" test -tags=integration -count=1 -shuffle=on ./cmd/gofly/internal/command -run '^TestMigrationDatabaseLifecycle$' -v
    printf 'migration integration report: %s\n' "$GOFLY_MIGRATION_TEST_REPORT"
    exit 0
fi

"$go_cmd" test -count=1 -shuffle=on ./cmd/gofly/internal/migration ./cmd/gofly/internal/command ./cmd/gofly/internal/generator -run 'TestMigration|TestExecuteMigrate|TestGenerateMigration'
"$go_cmd" build -o "$tmp/gofly" ./cmd/gofly
printf 'CREATE TABLE users (id BIGINT PRIMARY KEY, name VARCHAR(80) NOT NULL);\n' > "$tmp/schema.sql"
"$tmp/gofly" --output json migrate gen init --from-ddl "$tmp/schema.sql" --dialect mysql --dir "$tmp/migrations" --version 1 > "$tmp/generated.json"
"$tmp/gofly" --output json migrate validate --dir "$tmp/migrations" > "$tmp/validated.json"
if "$tmp/gofly" --output json migrate gen init --from-ddl "$tmp/schema.sql" --dialect mysql --dir "$tmp/migrations" --version 1 > "$tmp/collision.json"; then
    echo 'migration collision unexpectedly succeeded' >&2
    exit 1
fi
report="${MIGRATION_REPORT:-$root/.tmp-test/migration-report.json}"
python3 - "$tmp" "$report" <<'PY'
import hashlib
import json
import pathlib
import sys

tmp = pathlib.Path(sys.argv[1])
generated = json.loads((tmp / "generated.json").read_text())
validated = json.loads((tmp / "validated.json").read_text())
collision = json.loads((tmp / "collision.json").read_text())
assert generated["ok"] and len(generated["data"]["files"]) == 2
assert validated["ok"] and len(validated["data"]["migrations"]) == 1
assert collision["ok"] is False
assert (tmp / "schema.sql").read_bytes() == (tmp / "migrations/1_init.up.sql").read_bytes()
artifacts = []
for path in sorted((tmp / "migrations").glob("*.sql")):
    artifacts.append({"name": path.name, "sha256": hashlib.sha256(path.read_bytes()).hexdigest(), "sql": path.read_text()})
report = pathlib.Path(sys.argv[2])
report.parent.mkdir(parents=True, exist_ok=True)
report.write_text(json.dumps({"schema": "gofly.migration_smoke.v1", "status": "passed", "collisionRejected": True, "artifacts": artifacts}, indent=2) + "\n")
print(f"migration smoke report: {report}")
PY
