#!/usr/bin/env sh
# shellcheck disable=SC2016,SC2086
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
gosec_inventory_baseline="${GOSEC_INVENTORY_BASELINE:-$root/bin/scripts/gosec-exception-baseline.json}"
coverage_threshold="${COVERAGE_THRESHOLD:-60}"
coverage_ratchet_default="90"
coverage_ratchet="${COVERAGE_RATCHET:-$coverage_ratchet_default}"
skip_report="${GOVERNANCE_SKIP_REPORT:-$tmp/governance-skip-report.json}"

write_skip_report() {
	python3 - "$skip_report" <<'PY'
import json, pathlib, sys
path = pathlib.Path(sys.argv[1])
path.write_text(json.dumps({"schema":"gofly.governance_skip_report.v1","skips":[]}, indent=2) + "\n", encoding="utf-8")
PY
}

record_skip() {
	round="$1"
	name="$2"
	env_name="$3"
	reason="$4"
	risk="$5"
	compensating_gate="$6"
	required_for_release="$7"
	python3 - "$skip_report" "$round" "$name" "$env_name" "$reason" "$risk" "$compensating_gate" "$required_for_release" <<'PY'
import json, pathlib, sys
path = pathlib.Path(sys.argv[1])
if path.exists():
    data = json.loads(path.read_text(encoding="utf-8"))
else:
    data = {"schema":"gofly.governance_skip_report.v1","skips":[]}
required = sys.argv[8].lower() == "true"
data.setdefault("skips", []).append({
    "round": sys.argv[2],
    "name": sys.argv[3],
    "env": sys.argv[4],
    "reason": sys.argv[5],
    "risk": sys.argv[6],
    "compensating_gate": sys.argv[7],
    "required_for_release": required,
})
path.write_text(json.dumps(data, indent=2, sort_keys=True) + "\n", encoding="utf-8")
PY
}

assert_not_release_skip() {
	env_name="$1"
	if [ "${GOVERNANCE_RELEASE:-false}" = "true" ]; then
		printf '%s must not be true during release governance\n' "$env_name"
		exit 1
	fi
	case "${GITHUB_REF:-}" in
	refs/tags/v*)
		printf '%s must not be true during release tag governance\n' "$env_name"
		exit 1
		;;
	esac
}

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
	assert_coverage_ratchet_alignment
	"$go_cmd" version
	"$go_cmd" list ./... >/dev/null
}

assert_coverage_ratchet_alignment() {
	makefile_ratchet="$(awk '/^COVERAGE_RATCHET[[:space:]]*\?=/ { print $3; exit }' "$root/Makefile")"
	if [ "$makefile_ratchet" != "$coverage_ratchet_default" ]; then
		printf 'coverage ratchet drift: Makefile default is %s, governance script default is %s\n' "$makefile_ratchet" "$coverage_ratchet_default"
		exit 1
	fi
	agents_ratchet="$(awk '/Makefile.*COVERAGE_RATCHET/ { for (i = 1; i <= NF; i++) if ($i ~ /^[`"]?[0-9]+%[`".,]?$/) { gsub(/[^0-9.]/, "", $i); print $i; exit } }' "$root/AGENTS.md")"
	if [ "$agents_ratchet" != "$coverage_ratchet_default" ]; then
		printf 'coverage ratchet drift: AGENTS.md documents %s%%, governance script default is %s%%\n' "${agents_ratchet:-<missing>}" "$coverage_ratchet_default"
		exit 1
	fi
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
	"$go_cmd" run ./cmd/gofly ai manifest --schema jsonschema 2>/dev/null | python3 -c '
import json,sys
d=json.load(sys.stdin)
assert d["ok"] is True, "schema envelope should be ok"
assert d["command"]=="ai.manifest.schema", "unexpected command %r" % (d["command"],)
schema=d["data"]
assert schema["$schema"]=="https://json-schema.org/draft/2020-12/schema", "unexpected JSON Schema dialect"
props=schema["properties"]
for name in ["schemaVersion","commands","controlPlane","llmGovernance","featureLibrary"]:
    assert name in props, f"schema missing property {name}"
command_props=props["commands"]["items"]["properties"]
for name in ["inputSchema","outputContract","sideEffects","riskLevel","supportsDryRun","mutatesFilesystem"]:
    assert name in command_props, f"command schema missing property {name}"
print("AI manifest JSON Schema OK")
'
	"$go_cmd" run ./cmd/gofly ai control-plane --schema jsonschema 2>/dev/null | python3 -c '
import json,sys
d=json.load(sys.stdin)
assert d["ok"] is True, "control-plane schema envelope should be ok"
assert d["command"]=="ai.control_plane.schema", "unexpected command %r" % (d["command"],)
schema=d["data"]
assert schema["$schema"]=="https://json-schema.org/draft/2020-12/schema", "unexpected control-plane JSON Schema dialect"
assert schema["$id"]=="https://gofly.dev/schemas/ai-control-plane.schema.json", "unexpected control-plane schema id"
assert schema["xSchemaChecksum"], "control-plane schema checksum should be non-empty"
props=schema["properties"]
for name in ["snapshot","diff","consumerAction","snapshotResult","watchEvent"]:
    assert name in props, f"control-plane schema missing property {name}"
action_props=props["consumerAction"]["properties"]
for name in ["changeType","action","reason","requiresFullReconcile","nextActions"]:
    assert name in action_props, f"consumer action schema missing property {name}"
print("AI control-plane JSON Schema OK")
'
	control_plane_schema_checksum="$($go_cmd run ./cmd/gofly ai control-plane --schema jsonschema 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["xSchemaChecksum"])')"
	"$go_cmd" run ./cmd/gofly ai manifest --json 2>/dev/null | python3 -c '
import json,sys
d=json.load(sys.stdin)
cp=d["data"]["controlPlane"]
assert cp["schemaId"]=="https://gofly.dev/schemas/ai-control-plane.schema.json", "unexpected control-plane manifest schema id %r" % (cp,)
assert cp["schemaCommand"]=="gofly ai control-plane --schema jsonschema", "unexpected control-plane schema command %r" % (cp,)
assert cp["schemaChecksum"]==sys.argv[1], "manifest schema checksum should match schema output"
assert "generated project control-plane contributors for scaffold contract, sanitized runtime config and governance policy snapshots" in cp["capabilities"], "manifest missing generated project control-plane capability"
assert "native REST admin control-plane endpoint with pluggable runtime contributors and sanitized REST runtime snapshots" in cp["capabilities"], "manifest missing native REST control-plane capability"
assert "control-plane contributor for REST governance runtime cache counts across rate limiters, concurrency limiters and breakers" in cp["capabilities"], "manifest missing REST governance runtime cache capability"
assert "ai new --apply --verify runs generated project control-plane snapshot assertions when the scaffold exposes a snapshot contract test" in cp["capabilities"], "manifest missing generated project control-plane verify capability"
assert cp["defaultMetadata"]["generated.project.contract"]=="available", "manifest missing generated project default metadata"
assert cp["defaultMetadata"]["generated.project.verify.controlplane"]=="available", "manifest missing generated project verify metadata"
assert cp["defaultMetadata"]["rest.runtime"]=="available", "manifest missing REST runtime metadata"
assert cp["defaultMetadata"]["rest.governance.runtime"]=="available", "manifest missing REST governance runtime metadata"
print("AI control-plane schema checksum OK")
' "$control_plane_schema_checksum"
	"$go_cmd" run ./cmd/gofly ai control-plane --json 2>/dev/null | python3 -c '
import json,sys
d=json.load(sys.stdin)
assert d["ok"] is True, "control-plane envelope should be ok"
assert d["command"]=="ai.control_plane", "unexpected command %r" % (d["command"],)
data=d["data"]
snapshot=data["snapshot"]
assert data["source"]=="ai-manifest", "unexpected control-plane source %r" % (data["source"],)
assert snapshot["version"]=="gofly-control-plane.v1", "unexpected snapshot version %r" % (snapshot["version"],)
assert snapshot["checksum"], "snapshot checksum should be non-empty"
assert snapshot["metadata"]["generated.project.contract"]=="available", "snapshot missing generated project contract metadata"
assert snapshot["metadata"]["generated.project.verify.controlplane"]=="available", "snapshot missing generated project verify metadata"
assert snapshot["metadata"]["rest.runtime"]=="available", "snapshot missing REST runtime metadata"
assert snapshot["metadata"]["rest.governance.runtime"]=="available", "snapshot missing REST governance runtime metadata"
assert data["diff"]["changeType"]=="initial-snapshot", "unexpected snapshot diff %r" % (data["diff"],)
action=data["consumerAction"]
assert action["changeType"]=="initial-snapshot", "unexpected snapshot consumer change type %r" % (action,)
assert action["action"]=="load-baseline", "unexpected snapshot consumer action %r" % (action,)
assert action["requiresFullReconcile"] is True, "initial snapshot should require full reconciliation"
assert "secret values" in data["secretBoundary"], "secret boundary should forbid secret values"
print("AI control-plane snapshot OK")
'
	checksum="$($go_cmd run ./cmd/gofly ai control-plane --json 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["snapshot"]["checksum"])')"
	"$go_cmd" run ./cmd/gofly ai control-plane --from-checksum "$checksum" --json 2>/dev/null | python3 -c '
import json,sys
d=json.load(sys.stdin)
diff=d["data"]["diff"]
assert diff["changed"] is False, "matching checksum should be unchanged"
assert diff["changeType"]=="none", "unexpected checksum diff %r" % (diff,)
action=d["data"]["consumerAction"]
assert action["action"]=="skip", "unchanged checksum should map to skip action: %r" % (action,)
assert action["requiresFullReconcile"] is False, "unchanged checksum should not require reconciliation"
print("AI control-plane checksum diff OK")
'
	previous_snapshot="$tmp/previous-control-plane.json"
	"$go_cmd" run ./cmd/gofly ai control-plane --json 2>/dev/null | python3 -c '
import json,sys
d=json.load(sys.stdin)
snapshot=d["data"]["snapshot"]
snapshot.setdefault("metadata", {})["llm"]="planned"
with open(sys.argv[1], "w", encoding="utf-8") as f:
    json.dump(snapshot, f)
' "$previous_snapshot"
	"$go_cmd" run ./cmd/gofly ai control-plane --from-snapshot "$previous_snapshot" --json 2>/dev/null | python3 -c '
import json,sys
d=json.load(sys.stdin)
diff=d["data"]["diff"]
assert diff["changed"] is True, "previous snapshot should produce a semantic diff"
assert diff["changeType"]=="metadata-change", "unexpected semantic snapshot diff %r" % (diff,)
assert "metadata" in diff["changedFields"], "semantic diff should include metadata: %r" % (diff,)
action=d["data"]["consumerAction"]
assert action["action"]=="refresh-capability-cache", "metadata change should refresh capability cache: %r" % (action,)
assert action["requiresFullReconcile"] is False, "metadata-only change should not require full reconcile"
print("AI control-plane semantic snapshot diff OK")
'
	"$go_cmd" run ./cmd/gofly ai control-plane --watch --max-events 1 --timeout 2s --json 2>/dev/null | python3 -c '
import json,sys
lines=[line for line in sys.stdin.read().splitlines() if line.strip()]
assert len(lines)==1, "expected 1 control-plane event, got %d" % (len(lines),)
d=json.loads(lines[0])
assert d["ok"] is True, "control-plane event envelope should be ok"
assert d["command"]=="ai.control_plane.event", "unexpected command %r" % (d["command"],)
data=d["data"]
assert data["index"]==0, "unexpected event index %r" % (data["index"],)
assert data["source"]=="ai-manifest", "unexpected control-plane event source %r" % (data["source"],)
assert data["snapshot"]["checksum"], "event snapshot checksum should be non-empty"
assert data["diff"]["changed"] is True, "initial watch event should be changed"
assert data["diff"]["changedFields"], "initial watch event should include changed fields"
action=data["consumerAction"]
assert action["action"]=="full-reconcile", "watch mixed change should map to full reconcile: %r" % (action,)
assert action["requiresFullReconcile"] is True, "watch mixed change should require full reconcile"
print("AI control-plane watch event OK")
'
	runtime_snapshot="$tmp/runtime-control-plane.json"
	python3 - "$runtime_snapshot" <<'PY'
import json,sys
snapshot={
    "version":"gofly-control-plane.v1",
    "metadata":{"rest.runtime":"available","rest.governance.runtime":"available"},
    "configs":{"rest.runtime":{"service":"runtime","address":"127.0.0.1:8080"}}
}
with open(sys.argv[1], "w", encoding="utf-8") as f:
    json.dump(snapshot, f)
PY
	python3 - "$tmp" "$tmp/control-plane-http-port" >"$tmp/control-plane-http.log" 2>&1 <<'PY' &
import functools
import http.server
import pathlib
import sys

directory = sys.argv[1]
port_file = pathlib.Path(sys.argv[2])
handler = functools.partial(http.server.SimpleHTTPRequestHandler, directory=directory)
server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
port_file.write_text(str(server.server_address[1]), encoding="utf-8")
server.serve_forever()
PY
	cp_pid="$!"
	python3 - "$tmp/control-plane-http-port" <<'PY'
import pathlib,sys,time
port_file=pathlib.Path(sys.argv[1])
for _ in range(50):
    if port_file.exists() and port_file.read_text(encoding="utf-8").strip():
        raise SystemExit(0)
    time.sleep(0.1)
raise SystemExit("http server did not write a port")
PY
	cp_port="$(cat "$tmp/control-plane-http-port")"
	"$go_cmd" run ./cmd/gofly ai control-plane --source "http://127.0.0.1:$cp_port/runtime-control-plane.json" --watch --max-events 1 --timeout 2s --json 2>/dev/null | python3 -c '
import json,sys
lines=[line for line in sys.stdin.read().splitlines() if line.strip()]
assert len(lines)==1, "expected 1 runtime control-plane event, got %d" % (len(lines),)
d=json.loads(lines[0])
data=d["data"]
assert data["source"].startswith("http://127.0.0.1:"), "unexpected runtime source %r" % (data["source"],)
assert data["snapshot"]["metadata"]["rest.runtime"]=="available", "runtime source missing REST metadata"
assert data["snapshot"]["checksum"], "runtime source checksum should be non-empty"
print("AI runtime control-plane source watch OK")
'
	kill "$cp_pid" 2>/dev/null || true
	wait "$cp_pid" 2>/dev/null || true
}

round_generated_project_matrix_tests() {
	"$go_cmd" test $testflags ./cmd/gofly/internal/command -run 'TestAINewGeneratedProjectVerificationMatrix_BitsUT'
}

round_generated_project_control_plane_smoke() {
	project="$tmp/control-plane-smoke"
	"$go_cmd" run ./cmd/gofly ai new --template go-rest-minimal --name smoke --module example.com/smoke --dir "$project" --apply --json >/dev/null
	port="$(python3 -c 'import socket
s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')"
	python3 - "$project/etc/smoke.json" "$port" <<'PY'
import json,sys
path,port=sys.argv[1],int(sys.argv[2])
with open(path, encoding="utf-8") as f:
    data=json.load(f)
data.setdefault("rest", {})["host"]="127.0.0.1"
data["rest"]["port"]=port
data["rest"]["admin"]={"enabled": True, "pathPrefix": "/admin", "token": "smoke-token"}
data.setdefault("service", {}).setdefault("trace", {}).pop("sampler", None)
with open(path, "w", encoding="utf-8") as f:
    json.dump(data, f)
PY
	(
		cd "$project"
		"$go_cmd" mod edit -replace github.com/gofly/gofly="$root"
		"$go_cmd" mod tidy
	)
	(
		cd "$project"
		"$go_cmd" run ./cmd/smoke
	) >"$tmp/control-plane-smoke.log" 2>&1 &
	pid="$!"
	cleanup_smoke() {
		kill "$pid" 2>/dev/null || true
		wait "$pid" 2>/dev/null || true
	}
	trap 'cleanup_smoke; chmod -R u+w "$tmp" 2>/dev/null || true; rm -rf "$tmp"' EXIT INT TERM
	python3 - "$port" <<'PY'
import json,sys,time,urllib.request
url=f"http://127.0.0.1:{sys.argv[1]}/admin/control-plane"
last=None
for _ in range(60):
    try:
        req=urllib.request.Request(url, headers={"Authorization":"Bearer smoke-token"})
        with urllib.request.urlopen(req, timeout=1) as resp:
            data=json.load(resp)
        assert data["version"]=="gofly-control-plane.v1", data
        assert data["checksum"], data
        assert data["metadata"]["rest.runtime"]=="available", data
        assert data["metadata"]["rest.governance.runtime"]=="available", data
        assert "rest.runtime" in data["configs"], data
        print("Generated project REST admin control-plane smoke OK")
        raise SystemExit(0)
    except Exception as exc:
        last=exc
        time.sleep(1)
raise SystemExit(f"control-plane smoke failed: {last}")
PY
	cleanup_smoke
	trap 'chmod -R u+w "$tmp" 2>/dev/null || true; rm -rf "$tmp"' EXIT INT TERM
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
	GOSEC_INVENTORY_BASELINE="$gosec_inventory_baseline" sh "$root/bin/scripts/gosec-exception-inventory.sh" >/dev/null
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
		assert_not_release_skip GOVERNANCE_SKIP_SECURITY
		printf 'security audit skipped because GOVERNANCE_SKIP_SECURITY=true\n'
		record_skip 13 "security audit" GOVERNANCE_SKIP_SECURITY "GOVERNANCE_SKIP_SECURITY=true" "govulncheck/gosec findings may be missed" "run make security before merge/release" true
	else
		round_security_audit
	fi
	round_final_package_listing
}

cd "$root"
write_skip_report

if [ "${GOVERNANCE_ONLY_GENERATED_CONTROL_PLANE_SMOKE:-false}" = "true" ]; then
	printf 'gofly generated project runtime control-plane smoke\n'
	round_generated_project_control_plane_smoke
	printf '\nGenerated project runtime control-plane smoke completed successfully.\n'
	exit 0
fi

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
printf 'GOSEC_INVENTORY_BASELINE=%s\n' "$gosec_inventory_baseline"
printf 'COVERAGE_THRESHOLD=%s\n' "$coverage_threshold"
printf 'COVERAGE_RATCHET=%s\n' "$coverage_ratchet"
printf 'GOVERNANCE_SKIP_GENERATED_MATRIX=%s\n' "${GOVERNANCE_SKIP_GENERATED_MATRIX:-false}"
printf 'GOVERNANCE_SKIP_GENERATED_CONTROL_PLANE_SMOKE=%s\n' "${GOVERNANCE_SKIP_GENERATED_CONTROL_PLANE_SMOKE:-false}"
printf 'GOVERNANCE_SKIP_REPORT=%s\n' "$skip_report"

run_round 1 "baseline and module graph" round_baseline
run_round 2 "format check" round_format_check
run_round 3 "unit tests without test cache" "$go_cmd" test $testflags $pkgs
run_round 4 "vet static analysis" "$go_cmd" vet $pkgs
run_round 5 "golangci-lint" "$golangci_lint" run $pkgs
if [ "${GOVERNANCE_SKIP_RACE:-false}" = "true" ]; then
	assert_not_release_skip GOVERNANCE_SKIP_RACE
	printf '\n== Round 6: race detector ==\n'
	printf 'skipped because GOVERNANCE_SKIP_RACE=true\n'
	record_skip 6 "race detector" GOVERNANCE_SKIP_RACE "GOVERNANCE_SKIP_RACE=true" "race conditions may be missed" "run go test -race ./... before merge/release" true
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
	record_skip 11 "generated project verification matrix" GOVERNANCE_SKIP_GENERATED_MATRIX "GOVERNANCE_SKIP_GENERATED_MATRIX=true" "generated project regressions may be missed in this job" "make test-generated-matrix in build-test job" false
else
	assert_go_tests_match ./cmd/gofly/internal/command 'TestAINewGeneratedProjectVerificationMatrix_BitsUT' 1
	run_round 11 "generated project verification matrix" round_generated_project_matrix_tests
fi
if [ "${GOVERNANCE_SKIP_GENERATED_CONTROL_PLANE_SMOKE:-false}" = "true" ]; then
	assert_not_release_skip GOVERNANCE_SKIP_GENERATED_CONTROL_PLANE_SMOKE
	printf '\n== Round 12: generated project runtime control-plane smoke ==\n'
	printf 'skipped because GOVERNANCE_SKIP_GENERATED_CONTROL_PLANE_SMOKE=true\n'
	record_skip 12 "generated project runtime control-plane smoke" GOVERNANCE_SKIP_GENERATED_CONTROL_PLANE_SMOKE "GOVERNANCE_SKIP_GENERATED_CONTROL_PLANE_SMOKE=true" "generated runtime control-plane regressions may be missed" "make generated-control-plane-smoke in build-test job" true
else
	run_round 12 "generated project runtime control-plane smoke" round_generated_project_control_plane_smoke
fi
run_round 13 "docs, coverage, security, and final package listing" round_final_convergence

printf '\nGovernance skip report: %s\n' "$skip_report"
printf '\nGovernance workflow completed successfully.\n'
