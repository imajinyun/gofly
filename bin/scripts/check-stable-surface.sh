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

run_check "CLI JSON golden contracts" "$go_cmd" test $testflags ./cmd/gofly/internal/command -run 'Test(NewCommandsEmitJSONEnvelope|IDLGenerateCommandsEmitJSONEnvelope|VersionCommandJSONEnvelope|ExecuteAIManifestJSONEnvelope|DoctorCommandJSON|ReleaseCheckCommandJSONAndChangelogBlocker|ReleaseCheckGlobalJSONDoesNotDuplicateError|RPCDescriptorCommandJSONCompatible)$'

run_check "control-plane golden contracts" "$go_cmd" test $testflags ./core/controlplane -run 'TestControlPlane(PureOrderingAndClassification|ProviderSourceAndWatchBoundaries|ProviderLoadBoundaries)'

run_check "REST OpenAPI and control-plane golden contracts" "$go_cmd" test $testflags ./rest -run 'Test(ServerOpenAPIExportsRegisteredRoutes|ServerRouteOptionAndOpenAPIBoundaries|OpenAPIExportsDefaultErrorResponses|ControlPlaneRuntimeSnapshotGoldenContractAndSemanticDiff)$'

run_check "generated production service compile smoke" "$go_cmd" test $testflags ./cmd/gofly/internal/command -run 'TestNewServiceGeneratedProjectSmokeMatrix'

run_check "generated production service OpenAPI envelope fixture" "$go_cmd" test $testflags ./cmd/gofly/internal/generator -run 'TestGeneratedServiceOpenAPIValidationEnvelopeContract'

printf '\nstable surface release-blocking contracts ok\n'
