#!/usr/bin/env sh
# shellcheck disable=SC2086
set -eu

# gofmt dirs, Go package lists, test flags, and tool flags intentionally use
# whitespace-separated values so Makefile/CI callers can pass multiple entries.

root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
tmp="$(mktemp -d)"
trap 'chmod -R u+w "$tmp" 2>/dev/null || true; rm -rf "$tmp"' EXIT INT TERM

case " ${GOFLAGS:-} " in
*" -count=1 "*) ;;
*) export GOFLAGS="${GOFLAGS:+$GOFLAGS }-count=1" ;;
esac
export GOCACHE="${GOCACHE:-$tmp/gocache}"
export GOPROXY="${GOPROXY:-direct}"
export GOFLY_GOVERNANCE_CACHE_DISABLED="${GOFLY_GOVERNANCE_CACHE_DISABLED:-true}"
if [ -z "${GOTMPDIR:-}" ]; then
	export GOTMPDIR="$tmp/gotmp"
fi

mkdir -p "$GOCACHE" "$GOTMPDIR"

if [ "${GOVERNANCE_ISOLATE_GOMODCACHE:-false}" = "true" ] && [ -z "${GOMODCACHE:-}" ]; then
	export GOMODCACHE="$tmp/gomodcache"
	mkdir -p "$GOMODCACHE"
fi

go_cmd="${GO:-go}"
golangci_lint="${GOLANGCI_LINT:-golangci-lint}"
pkgs="${PKGS:-./...}"
gofmt_dirs="${GOFMT_DIRS:-app cache cmd core examples gateway ops rest rpc}"
testflags="${TESTFLAGS:--shuffle=on}"
govulncheck_scan="${GOVULNCHECK_SCAN:-package}"
gosec_flags="${GOSEC_FLAGS:--quiet -exclude-generated -exclude-dir=testdata -exclude-dir=vendor -exclude-dir=.tmp-test}"
coverage_threshold="${COVERAGE_THRESHOLD:-60}"
coverage_ratchet="${COVERAGE_RATCHET:-82}"

run_round() {
	round="$1"
	name="$2"
	shift 2
	printf '\n== Round %s: %s ==\n' "$round" "$name"
	"$@"
}

assert_go_tests_match() {
	pkg="$1"
	regex="$2"
	min_count="$3"
	matches="$($go_cmd test "$pkg" -list "$regex")"
	count="$(printf '%s\n' "$matches" | awk '/^Test/ { n++ } END { print n + 0 }')"
	if [ "$count" -lt "$min_count" ]; then
		printf 'expected at least %s tests matching %s in %s; got %s\n' "$min_count" "$regex" "$pkg" "$count"
		printf '%s\n' "$matches"
		exit 1
	fi
}

round_baseline() {
	"$go_cmd" version
	"$go_cmd" list ./... >/dev/null
}

round_format_check() {
	out="$(gofmt -s -l $gofmt_dirs)"
	if [ -n "$out" ]; then
		echo "gofmt needed for:"
		echo "$out"
		exit 1
	fi
}

round_tidy_check() {
	sh "$root/bin/scripts/check-mod-tidy.sh"
}

round_docs_check() {
	sh "$root/bin/scripts/check-doc-go-snippets.sh"
}

round_runtime_cache_bypass_tests() {
	GOFLY_CACHE_DISABLED="$GOFLY_GOVERNANCE_CACHE_DISABLED" "$go_cmd" test $testflags ./cache -run 'Test(CacheDisabledBy|TieredCacheDisabledBy)'
}

round_plugin_no_local_cache_tests() {
	"$go_cmd" test $testflags ./cmd/gofly/internal/generator -run 'TestPluginRunnerDownloadPlugin(DoesNotReuseLocalCache|IgnoresUserCache|UsesUniqueTempFile)'
}

round_ai_manifest_check() {
	"$go_cmd" run ./cmd/gofly ai manifest --json 2>/dev/null | python3 -c '
import json,sys
d=json.load(sys.stdin)
gp=d["data"]["llmGovernance"]["governancePipeline"]
assert len(gp)==9, f"expected 9 pipeline stages, got {len(gp)}"
stages=[s["stage"] for s in gp]
expected=["request-redaction","rate-limit","token-budget","response-cache","circuit-breaker","provider-call","usage-accounting","audit-log","telemetry-emit"]
assert stages==expected, f"pipeline mismatch:\n  expected={expected}\n  got     ={stages}"
print(f"AI governance pipeline: {len(gp)} stages OK")
'
}

round_generated_project_matrix_tests() {
	"$go_cmd" test $testflags ./cmd/gofly/internal/command -run 'TestAINewGeneratedProjectVerificationMatrix_BitsUT'
}

round_coverage_check() {
	COVERAGE_THRESHOLD="$coverage_threshold" COVERAGE_RATCHET="$coverage_ratchet" COVERAGE_PROFILE="$tmp/coverage.out" COVERAGE_TMPDIR="$tmp/coverage-tmp" PKGS="$pkgs" TESTFLAGS="$testflags" sh "$root/bin/scripts/coverage-check.sh"
}

run_govulncheck() {
	if [ -n "${GOVULNCHECK_TOOL:-}" ]; then
		"$GOVULNCHECK_TOOL" "$@"
		return
	fi
	"$go_cmd" tool govulncheck "$@"
}

run_gosec() {
	if [ -n "${GOSEC_TOOL:-}" ]; then
		"$GOSEC_TOOL" "$@"
		return
	fi
	"$go_cmd" tool gosec "$@"
}

round_security_audit() {
	run_govulncheck -scan="$govulncheck_scan" -show=traces $pkgs
	run_gosec $gosec_flags $pkgs
}

round_final_package_listing() {
	"$go_cmd" list -deps ./... >/dev/null
}

round_final_convergence() {
	round_docs_check
	round_coverage_check
	if [ "${GOVERNANCE_SKIP_SECURITY:-false}" = "true" ]; then
		printf 'security audit skipped because GOVERNANCE_SKIP_SECURITY=true\n'
	else
		round_security_audit
	fi
	round_final_package_listing
}

cd "$root"

printf 'gofly governance workflow\n'
printf 'root: %s\n' "$root"
printf 'GOFLAGS=%s\n' "$GOFLAGS"
printf 'GOFLY_GOVERNANCE_CACHE_DISABLED=%s\n' "$GOFLY_GOVERNANCE_CACHE_DISABLED"
printf 'GOCACHE=%s\n' "$GOCACHE"
printf 'GOTMPDIR=%s\n' "$GOTMPDIR"
printf 'GOMODCACHE=%s\n' "${GOMODCACHE:-<go default>}"
printf 'GOVERNANCE_ISOLATE_GOMODCACHE=%s\n' "${GOVERNANCE_ISOLATE_GOMODCACHE:-false}"
printf 'GOPROXY=%s\n' "$GOPROXY"
printf 'GOVULNCHECK_SCAN=%s\n' "$govulncheck_scan"
printf 'GOSEC_FLAGS=%s\n' "$gosec_flags"
printf 'COVERAGE_THRESHOLD=%s\n' "$coverage_threshold"
printf 'COVERAGE_RATCHET=%s\n' "$coverage_ratchet"
printf 'GOVERNANCE_SKIP_GENERATED_MATRIX=%s\n' "${GOVERNANCE_SKIP_GENERATED_MATRIX:-false}"

run_round 1 "baseline and module graph" round_baseline
run_round 2 "format check" round_format_check
run_round 3 "unit tests without test cache" "$go_cmd" test $testflags $pkgs
run_round 4 "vet static analysis" "$go_cmd" vet $pkgs
run_round 5 "golangci-lint" "$golangci_lint" run $pkgs
if [ "${GOVERNANCE_SKIP_RACE:-false}" = "true" ]; then
	printf '\n== Round 6: race detector ==\n'
	printf 'skipped because GOVERNANCE_SKIP_RACE=true\n'
else
	run_round 6 "race detector" "$go_cmd" test $testflags -race $pkgs
fi
run_round 7 "module tidy verification" round_tidy_check
assert_go_tests_match ./cache 'Test(CacheDisabledBy|TieredCacheDisabledBy)' 4
run_round 8 "runtime cache bypass tests" round_runtime_cache_bypass_tests
assert_go_tests_match ./cmd/gofly/internal/generator 'TestPluginRunnerDownloadPlugin(DoesNotReuseLocalCache|IgnoresUserCache|UsesUniqueTempFile)' 3
run_round 9 "plugin no-local-cache tests" round_plugin_no_local_cache_tests
run_round 10 "AI governance pipeline manifest check" round_ai_manifest_check
if [ "${GOVERNANCE_SKIP_GENERATED_MATRIX:-false}" = "true" ]; then
	printf '\n== Round 11: generated project verification matrix ==\n'
	printf 'skipped because GOVERNANCE_SKIP_GENERATED_MATRIX=true; CI must run make test-generated-matrix separately\n'
else
	assert_go_tests_match ./cmd/gofly/internal/command 'TestAINewGeneratedProjectVerificationMatrix_BitsUT' 1
	run_round 11 "generated project verification matrix" round_generated_project_matrix_tests
fi
run_round 12 "docs, coverage, security, and final package listing" round_final_convergence

printf '\nGovernance workflow completed successfully.\n'
