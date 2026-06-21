#!/usr/bin/env sh
set -eu

GO_CMD="${GO:-go}"
root="$(pwd)"
workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

for mod in examples/*/go.mod; do
	dir="$(dirname "$mod")"
	name="$(basename "$dir")"
	copy="$workdir/$name"
	cp -R "$dir" "$copy"
	(
		cd "$copy"
		"$GO_CMD" mod edit -replace "github.com/gofly/gofly=$root"
		"$GO_CMD" test -count=1 ./...
		"$GO_CMD" build -o "$workdir/$name.bin" ./...
	)
done

echo "examples copyable check passed"
