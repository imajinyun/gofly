#!/usr/bin/env sh
set -eu

script_dir="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
root="$(CDPATH='' cd -- "$script_dir/../.." && pwd)"
. "$script_dir/examples-lib.sh"

cd "$root"
sh "$script_dir/check-examples-layout.sh"

GO_CMD="${GO:-go}"
workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT HUP INT TERM
examples_prepare_go_env "$workdir"

examples_catalog_paths "$root" | while IFS= read -r example; do
	printf 'checking %s\n' "$example"
	name="$(printf '%s' "${example#examples/}" | tr '/.' '--')"
	output="$workdir/bin/$name"
	(
		cd "$root/$example"
		examples_build_module "$GO_CMD" "$output"
		"$GO_CMD" vet ./...
	)
done

echo "examples build and vet passed"
