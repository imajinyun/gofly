#!/bin/sh
set -eu

workdir="$(mktemp -d "${TMPDIR:-/tmp}/gosky-go-test.XXXXXX")"
cleanup() {
	rm -rf "$workdir"
}
trap cleanup EXIT HUP INT TERM

mkdir -p "$workdir/gocache" "$workdir/gotmp"

go_command="test"
if [ "${1:-}" = "--vet" ]; then
	go_command="vet"
	shift
fi

GOCACHE="$workdir/gocache" \
GOTMPDIR="$workdir/gotmp" \
GOFLAGS="${GOFLAGS:-} -count=1" \
GOPROXY="${GOPROXY:-direct}" \
"${GO:-go}" "$go_command" "$@"
