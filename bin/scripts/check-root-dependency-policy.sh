#!/usr/bin/env sh
set -eu

go_cmd="${GO:-go}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM
script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"

policy_file="${ROOT_DEPENDENCY_POLICY_FILE:-$script_dir/root-dependency-policy.tsv}"
active_policy_file="$tmp/root-dependency-policy.active.tsv"
direct_modules_file="$tmp/direct-modules.txt"

if [ ! -f "$policy_file" ]; then
	printf '%s\n' "root dependency policy file not found: $policy_file"
	exit 1
fi

awk '
/^[[:space:]]*($|#)/ { next }
{ print }
' "$policy_file" >"$active_policy_file"

main_module="$("$go_cmd" list -m -f '{{.Path}}')"
"$go_cmd" list -m -f '{{if not .Indirect}}{{.Path}}{{end}}' all |
	awk 'NF > 0' >"$direct_modules_file"

policy_error=0

awk -F'|' '
NF != 4 {
	printf "root dependency policy record must have 4 fields: %s\n", $0
	err = 1
	next
}
$1 == "" || $2 == "" || $3 == "" || $4 == "" {
	printf "root dependency policy record has an empty field: %s\n", $0
	err = 1
	next
}
$2 !~ /^(runtime|rpc|observability|benchmark|integration-test|unit-test|generator-datasource)$/ {
	printf "root dependency policy tier is not recognized for %s: %s\n", $1, $2
	err = 1
}
seen[$1]++ {
	printf "duplicate root dependency policy record: %s\n", $1
	err = 1
}
END { exit err ? 1 : 0 }
' "$active_policy_file" || policy_error=1

while IFS= read -r module; do
	if [ "$module" = "$main_module" ]; then
		continue
	fi

	if ! awk -F'|' -v module="$module" '$1 == module { found = 1 } END { exit found ? 0 : 1 }' "$active_policy_file"; then
		printf '%s\n' "root direct dependency $module is missing from the root dependency policy."
		printf '%s\n' "Classify it in $policy_file with tier, owner path, and reason before committing go.mod drift."
		policy_error=1
		continue
	fi

	why="$("$go_cmd" mod why -m "$module" 2>/dev/null || true)"
	case "$why" in
	*"main module does not need module $module"*)
		printf '%s\n' "root direct dependency $module is classified but not needed by the root module."
		printf '%s\n' "Run 'go mod tidy' or move generated-project-only/test-fixture dependencies to their owning module."
		policy_error=1
		;;
	esac
done <"$direct_modules_file"

for module in ${GENERATED_ONLY_ROOT_MODULES:-gorm.io/gorm go.mongodb.org/mongo-driver}; do
	if awk -F'|' -v module="$module" '$1 == module { found = 1 } END { exit found ? 0 : 1 }' "$active_policy_file"; then
		printf '%s\n' "generated-project-only module $module must not be classified as an allowed root dependency."
		policy_error=1
	fi
done

if [ "$policy_error" -ne 0 ]; then
	exit 1
fi

printf '%s\n' "root dependency policy ok"
