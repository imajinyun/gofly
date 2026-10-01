#!/bin/sh
set -eu

schema_dir="internal/migrations"
schema="$schema_dir/20261001042731_project_authorization.up.sql"
rollback="$schema_dir/20261001042731_project_authorization.down.sql"
repo_root="$(CDPATH='' cd -- "$(dirname -- "$0")/../../.." && pwd)"
validation_tmp="$(mktemp -d "${TMPDIR:-/private/tmp}/gosky-migration-validate.XXXXXX")"

cleanup() {
	rm -rf "$validation_tmp"
}
trap cleanup EXIT HUP INT TERM

mkdir -p "$validation_tmp/gocache" "$validation_tmp/gotmp"

if [ ! -f "$schema" ] || [ ! -f "$rollback" ]; then
	echo "missing Project authorization migration pair: $schema / $rollback" >&2
	exit 1
fi

for table in projects project_members opslogs; do
	if ! grep -Eq "^CREATE TABLE $table" "$schema"; then
		echo "Project authorization migration is missing table: $table" >&2
		exit 1
	fi
done

for column in \
	'project_id BIGINT UNSIGNED' \
	'tenant_id BIGINT UNSIGNED' \
	'project_tenant_id BIGINT UNSIGNED' \
	'project_state ENUM' \
	'member_subject VARCHAR' \
	'auth_basis VARCHAR'; do
	if ! grep -Eq "^[[:space:]]*$column" "$schema"; then
		echo "Project authorization migration is missing schema contract: $column" >&2
		exit 1
	fi
done

if ! grep -Eq 'FOREIGN KEY \([[:space:]]*$' "$schema" || ! grep -Eq 'project_id,[[:space:]]*$' "$schema" || ! grep -Eq 'project_tenant_id' "$schema"; then
	echo "Project member tenant consistency foreign key is missing" >&2
	exit 1
fi

for table in opslogs project_members projects; do
	if ! grep -Eq "^DROP TABLE $table;" "$rollback"; then
		echo "Project authorization rollback is missing table: $table" >&2
		exit 1
	fi
done

(
	cd "$repo_root"
	GOCACHE="$validation_tmp/gocache" \
	GOTMPDIR="$validation_tmp/gotmp" \
	GOFLAGS="${GOFLAGS:-} -count=1" \
	go run ./cmd/gofly --output json migrate validate --dir "examples/gosky/$schema_dir"
)

sh ./bin/go-test.sh -shuffle=on ./internal/routes -run '^TestRegisterRoutesProjectReadAuthorizationContract$'
