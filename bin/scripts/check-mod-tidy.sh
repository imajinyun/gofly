#!/usr/bin/env sh
set -eu

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM
go_cmd="${GO:-go}"
generated_only_root_modules="${GENERATED_ONLY_ROOT_MODULES:-gorm.io/gorm go.mongodb.org/mongo-driver}"

module_declared_in_go_mod() {
	module="$1"
	awk -v module="$module" '($1 == module || ($1 == "require" && $2 == module)) { found = 1 } END { exit found ? 0 : 1 }' go.mod
}

reject_unneeded_generated_only_modules() {
	for module in $generated_only_root_modules; do
		if ! module_declared_in_go_mod "$module"; then
			continue
		fi
		why="$($go_cmd mod why -m "$module" 2>/dev/null || true)"
		case "$why" in
		*"main module does not need module $module"*)
			printf '%s\n' "go.mod declares generated-only module $module, but the root module does not import it."
			printf '%s\n' "Run 'go mod tidy' to remove it; generated projects should add it to their own go.mod only."
			exit 1
			;;
		esac
	done
}

reject_unneeded_generated_only_modules

cp go.mod "$tmp/go.mod.before"
if [ -f go.sum ]; then
	cp go.sum "$tmp/go.sum.before"
else
	: >"$tmp/go.sum.before"
fi

restore_mod_files() {
	cp "$tmp/go.mod.before" go.mod
	if [ -s "$tmp/go.sum.before" ]; then
		cp "$tmp/go.sum.before" go.sum
	else
		rm -f go.sum
	fi
}

if ! "$go_cmd" mod tidy; then
	restore_mod_files
	echo "go mod tidy failed; restored go.mod/go.sum."
	exit 1
fi

tidy_ok=1
if ! cmp -s go.mod "$tmp/go.mod.before"; then
	tidy_ok=0
fi
if [ -f go.sum ]; then
	if ! cmp -s go.sum "$tmp/go.sum.before"; then
		tidy_ok=0
	fi
elif [ -s "$tmp/go.sum.before" ]; then
	tidy_ok=0
fi

if [ "$tidy_ok" -ne 1 ]; then
	echo "go.mod/go.sum are not tidy. Run 'go mod tidy' and commit the changes."
	if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
		git diff -- go.mod go.sum || true
	else
		diff -u "$tmp/go.mod.before" go.mod || true
		if [ -f go.sum ]; then
			diff -u "$tmp/go.sum.before" go.sum || true
		else
			diff -u "$tmp/go.sum.before" /dev/null || true
		fi
	fi
	restore_mod_files
	echo "restored go.mod/go.sum after tidy check failure."
	exit 1
fi

"$go_cmd" mod verify
