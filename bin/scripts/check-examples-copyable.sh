#!/usr/bin/env sh
set -eu

GO_CMD="${GO:-go}"
root="$(pwd)"
workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT
mkdir -p "$workdir/gocache" "$workdir/gotmp"
export GOCACHE="$workdir/gocache"
export GOTMPDIR="$workdir/gotmp"

find examples -mindepth 2 -maxdepth 3 -name go.mod -print | sort | while IFS= read -r mod; do
	dir="$(dirname "$mod")"
	rel="${dir#examples/}"
	name="$(printf '%s' "$rel" | tr '/.' '--')"
	copy="$workdir/examples/$rel"
	mkdir -p "$(dirname "$copy")"
	cp -R "$dir" "$copy"
	(
		cd "$copy"
		"$GO_CMD" mod edit -replace "github.com/imajinyun/gofly=$root"
		"$GO_CMD" test -count=1 ./...
		"$GO_CMD" build -o "$workdir/$name.bin" ./...
	)
done

echo "examples copyable check passed"
