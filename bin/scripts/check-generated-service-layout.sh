#!/usr/bin/env sh
set -eu

root="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
go_cmd="${GO:-go}"
testflags="${TESTFLAGS:--count=1 -shuffle=on}"
tmp_root="$(mktemp -d "${TMPDIR:-/tmp}/gofly-generated-service-layout-XXXXXX")"
trap 'rm -rf "$tmp_root"' EXIT
mkdir -p "$tmp_root/gocache" "$tmp_root/gotmp"

run_go_test() {
	pkg="$1"
	pattern="$2"
	printf 'generated-service-layout: %s %s\n' "$pkg" "$pattern"
	(
		cd "$root"
		GOCACHE="${GOCACHE:-$tmp_root/gocache}" GOTMPDIR="${GOTMPDIR:-$tmp_root/gotmp}" "$go_cmd" test $testflags "$pkg" -run "$pattern"
	)
}

require_doc_marker() {
	marker="$1"
	if ! grep -Fq "$marker" "$root/docs/reference/generated-service-layout.md"; then
		printf 'generated-service-layout failed: docs/reference/generated-service-layout.md missing marker: %s\n' "$marker" >&2
		exit 1
	fi
}

run_go_test ./cmd/gofly/internal/generator 'TestGoldenPathProductionServiceLayoutContract'
run_go_test ./cmd/gofly/internal/command 'TestNewServiceGeneratedProjectSmokeMatrix'

require_doc_marker 'schema: gofly.generated_service_layout.v1'
require_doc_marker 'Tier 0 Golden Path'
require_doc_marker 'gofly new service <name> --style production'
require_doc_marker 'internal/smoke/service_smoke_test.go'
require_doc_marker '/admin/control-plane'
require_doc_marker 'make generated-service-layout-check'

printf 'generated-service-layout ok\n'
