#!/usr/bin/env sh
set -eu

go_cmd="${GO:-go}"
testflags="${TESTFLAGS:--count=1 -shuffle=on}"
scripts_dir="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"

run_check() {
	name="$1"
	shift
	printf '\n== stable surface: %s ==\n' "$name"
	"$@"
}

run_go_test() {
	name="$1"
	pkg="$2"
	pattern="$3"
	# TESTFLAGS is an operator-controlled argument list and must split into arguments.
	# shellcheck disable=SC2086
	run_check "$name" "$go_cmd" test $testflags "$pkg" -run "$pattern"
}

python3 - <<'PY'
import pathlib
import sys

checks = {
    pathlib.Path("docs/reference/stable-surface.md"): [
        "gofly.stable_surface.v1",
        "v1 candidate",
        "rest",
        "core/governance",
        "core/controlplane",
        "CLI JSON",
        "generated production service",
        "Tier 2 to Tier 1",
        "rpc",
        "gateway",
        "app",
        "make stable-surface-check",
        "deprecation",
        "release note",
    ],
    pathlib.Path("docs/reference/api-surface.md"): [
        "v1 candidate",
        "stable-surface.md",
        "Tier 0",
        "Tier 1",
        "Tier 2",
    ],
    pathlib.Path("docs/reference/compatibility.md"): [
        "v1 candidate",
        "Tier 2 to Tier 1",
        "compatibility tests",
    ],
    pathlib.Path("docs/releases/stable.md"): [
        "v1 candidate",
        "stable-surface.md",
        "deprecation",
        "coexistence window",
    ],
    pathlib.Path("Makefile"): [
        "stable-surface-check",
        "check-stable-surface.sh",
    ],
}

missing = []
for path, needles in checks.items():
    if not path.is_file():
        missing.append(f"{path}: file is missing")
        continue
    text = path.read_text(encoding="utf-8")
    for needle in needles:
        if needle not in text:
            missing.append(f"{path}: missing {needle!r}")

if missing:
    print("stable surface check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("stable surface governance ok")
PY

run_check "public Go API compatibility" sh "$scripts_dir/check-public-api.sh"

run_go_test "CLI JSON golden contracts" ./cmd/gofly/internal/command 'Test(NewCommandsEmitJSONEnvelope|IDLGenerateCommandsEmitJSONEnvelope|VersionCommandJSONEnvelope|ExecuteAIManifestJSONEnvelope|DoctorCommandJSON|ReleaseCheckCommandJSONAndChangelogBlocker|ReleaseCheckGlobalJSONDoesNotDuplicateError|RPCDescriptorCommandJSONCompatible)$'

run_go_test "control-plane golden contracts" ./core/controlplane 'TestControlPlane(PureOrderingAndClassification|ProviderSourceAndWatchBoundaries|ProviderLoadBoundaries)'

run_go_test "REST OpenAPI and control-plane golden contracts" ./rest 'Test(ServerOpenAPIExportsRegisteredRoutes|ServerRouteOptionAndOpenAPIBoundaries|OpenAPIExportsDefaultErrorResponses|ControlPlaneRuntimeSnapshotGoldenContractAndSemanticDiff)$'

run_go_test "generated production service compile smoke" ./cmd/gofly/internal/command 'TestNewServiceGeneratedProjectSmokeMatrix'

run_go_test "generated production service OpenAPI envelope fixture" ./cmd/gofly/internal/generator 'TestGeneratedServiceOpenAPIValidationEnvelopeContract'

printf '\nstable surface release-blocking contracts ok\n'
