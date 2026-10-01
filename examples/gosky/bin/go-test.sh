#!/bin/sh
set -eu

workdir="$(mktemp -d "${TMPDIR:-/private/tmp}/gosky-go-test.XXXXXX")"
cleanup() {
	rm -rf "$workdir"
}
trap cleanup EXIT HUP INT TERM

mkdir -p "$workdir/gocache" "$workdir/gotmp"

GOCACHE="$workdir/gocache" \
GOTMPDIR="$workdir/gotmp" \
GOFLAGS="${GOFLAGS:-} -count=1" \
go test "$@"
