#!/usr/bin/env sh
set -eu

script_dir="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
root="$(CDPATH='' cd -- "$script_dir/../.." && pwd)"
# shellcheck source=bin/scripts/examples-lib.sh
. "$script_dir/examples-lib.sh"

cd "$root"
sh "$script_dir/check-examples-layout.sh"

GO_CMD="${GO:-go}"
workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT HUP INT TERM
examples_prepare_go_env "$workdir"

examples_catalog_paths "$root" | while IFS= read -r dir; do
	rel="${dir#examples/}"
	name="$(printf '%s' "$rel" | tr '/.' '--')"
	copy="$workdir/examples/$rel"
	output="$workdir/bin/$name"
	mkdir -p "$(dirname "$copy")"
	cp -R "$dir" "$copy"
	(
		cd "$copy"
		"$GO_CMD" mod edit -replace "github.com/imajinyun/gofly=$root"
		"$GO_CMD" test -shuffle=on ./...
		examples_build_module "$GO_CMD" "$output"
	)
done

echo "examples copyable check passed"
