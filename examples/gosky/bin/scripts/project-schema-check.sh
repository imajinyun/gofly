#!/bin/sh
set -eu

schema_dir="internal/migrations"
schema="$schema_dir/20261001042731_project_authorization.up.sql"
rollback="$schema_dir/20261001042731_project_authorization.down.sql"
policy_schema="$schema_dir/20261002133038_casbin_policy_store.up.sql"
policy_rollback="$schema_dir/20261002133038_casbin_policy_store.down.sql"
relation_schema="$schema_dir/20261004064637_project_member_relation_roles.up.sql"
relation_rollback="$schema_dir/20261004064637_project_member_relation_roles.down.sql"
identity_schema="$schema_dir/20261005065439_identity_session.up.sql"
identity_rollback="$schema_dir/20261005065439_identity_session.down.sql"
repo_root="$(CDPATH='' cd -- "$(dirname -- "$0")/../../../.." && pwd)"
validation_tmp="$(mktemp -d "${TMPDIR:-/tmp}/gosky-migration-validate.XXXXXX")"

cleanup() {
	rm -rf "$validation_tmp"
}
trap cleanup EXIT HUP INT TERM

mkdir -p "$validation_tmp/gocache" "$validation_tmp/gotmp"

if [ ! -f "$schema" ] || [ ! -f "$rollback" ]; then
	echo "missing Project authorization migration pair: $schema / $rollback" >&2
	exit 1
fi

if [ ! -f "$policy_schema" ] || [ ! -f "$policy_rollback" ]; then
	echo "missing Casbin policy migration pair: $policy_schema / $policy_rollback" >&2
	exit 1
fi

if [ ! -f "$relation_schema" ] || [ ! -f "$relation_rollback" ]; then
	echo "missing Project member-relation migration pair: $relation_schema / $relation_rollback" >&2
	exit 1
fi

if [ ! -f "$identity_schema" ] || [ ! -f "$identity_rollback" ]; then
	echo "missing gosky identity/session migration pair: $identity_schema / $identity_rollback" >&2
	exit 1
fi

for table in projects project_members opslogs; do
	if ! grep -Eq "^CREATE TABLE $table" "$schema"; then
		echo "Project authorization migration is missing table: $table" >&2
		exit 1
	fi
done

for table in casbin_rules casbin_policy_events; do
	if ! grep -Eq "^CREATE TABLE $table" "$policy_schema"; then
		echo "Casbin policy migration is missing table: $table" >&2
		exit 1
	fi
done

for table in tenants users tenant_users auth_sessions auth_login_logs; do
	if ! grep -Eq "^CREATE TABLE $table" "$identity_schema"; then
		echo "gosky identity/session migration is missing table: $table" >&2
		exit 1
	fi
done

for contract in \
	'UNIQUE KEY uk_users_subject' \
	'UNIQUE KEY uk_users_login_name' \
	'UNIQUE KEY uk_tenant_users_tenant_user' \
	'UNIQUE KEY uk_auth_sessions_refresh_token_hash' \
	'KEY idx_auth_login_logs_tenant_created'; do
	if ! grep -Eq "$contract" "$identity_schema"; then
		echo "gosky identity/session migration is missing schema contract: $contract" >&2
		exit 1
	fi
done

for contract in \
	'UNIQUE KEY uk_casbin_rules_key' \
	'CHECK \(ptype IN' \
	'UNIQUE KEY uk_casbin_policy_events_change' \
	'KEY idx_casbin_policy_events_domain_id'; do
	if ! grep -Eq "$contract" "$policy_schema"; then
		echo "Casbin policy migration is missing schema contract: $contract" >&2
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

for table in casbin_policy_events casbin_rules; do
	if ! grep -Eq "^DROP TABLE $table;" "$policy_rollback"; then
		echo "Casbin policy rollback is missing table: $table" >&2
		exit 1
	fi
done

for table in auth_login_logs auth_sessions tenant_users users tenants; do
	if ! grep -Eq "^DROP TABLE $table;" "$identity_rollback"; then
		echo "gosky identity/session rollback is missing table: $table" >&2
		exit 1
	fi
done

for contract in "ENUM('viewer', 'editor')" "WHEN 'member' THEN 'viewer'" "WHEN 'collaborator' THEN 'editor'"; do
	if ! grep -Fq "$contract" "$relation_schema"; then
		echo "Project member-relation migration is missing schema contract: $contract" >&2
		exit 1
	fi
done

for contract in "ENUM('member', 'collaborator')" "WHEN 'viewer' THEN 'member'" "WHEN 'editor' THEN 'collaborator'"; do
	if ! grep -Fq "$contract" "$relation_rollback"; then
		echo "Project member-relation rollback is missing schema contract: $contract" >&2
		exit 1
	fi
done

(
	cd "$repo_root"
	GOCACHE="$validation_tmp/gocache" \
	GOTMPDIR="$validation_tmp/gotmp" \
	GOFLAGS="${GOFLAGS:-} -count=1" \
	"${GO:-go}" run ./cmd/gofly --output json migrate validate --dir "examples/gosky/$schema_dir"
)

sh ./bin/scripts/go-test.sh -shuffle=on ./internal/routes -run '^TestRegisterRoutesProjectReadAuthorizationContract$'
