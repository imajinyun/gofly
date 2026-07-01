#!/usr/bin/env sh
set -eu

GO_CMD="${GO:-go}"
workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT INT TERM

(cd examples/resilience && "$GO_CMD" run . --json) >"$workdir/resilience-drill.json"

python3 - "$workdir/resilience-drill.json" <<'PY'
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
report = json.loads(path.read_text(encoding="utf-8"))
missing = []

if report.get("schema") != "gofly.resilience_drill.v1":
    missing.append("schema must be gofly.resilience_drill.v1")
if report.get("scenario") != "limiter-retry-breaker-recovery":
    missing.append("scenario must be limiter-retry-breaker-recovery")

layers = set(report.get("layers") or [])
for layer in ("rate-limit", "retry", "circuit-breaker", "downstream"):
    if layer not in layers:
        missing.append(f"layers missing {layer!r}")

results = report.get("results") or {}
if results.get("downstreamCalls", 0) < 5:
    missing.append("downstreamCalls must prove retry attempts occurred")
if results.get("breakerOpen", 0) <= 0:
    missing.append("breakerOpen must be greater than zero")
if results.get("rejected", 0) <= 0:
    missing.append("rejected must be greater than zero")
if results.get("ok", 0) <= 0:
    missing.append("ok must be greater than zero")
if results.get("finalBreaker") != "closed":
    missing.append("finalBreaker must be closed")
if results.get("recovered") is not True:
    missing.append("recovered must be true")

gates = set(report.get("gates") or [])
for gate in ("make resilience-drill-check", "make examples-smoke"):
    if gate not in gates:
        missing.append(f"gates missing {gate!r}")

manifest_path = pathlib.Path("docs/reference/resilience-drill.json")
if not manifest_path.is_file():
    missing.append("docs/reference/resilience-drill.json is missing")
    manifest = {}
else:
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))

if manifest.get("schema") != "gofly.resilience_drill_evidence.v1":
    missing.append("manifest schema must be gofly.resilience_drill_evidence.v1")
if manifest.get("example") != "examples/resilience":
    missing.append("manifest example must be examples/resilience")
if manifest.get("reportSchema") != "gofly.resilience_drill.v1":
    missing.append("manifest reportSchema must be gofly.resilience_drill.v1")

commands = manifest.get("commands") or {}
if commands.get("drill") != "go run -C examples/resilience . --json":
    missing.append("manifest commands.drill must run the JSON drill")
if commands.get("gate") != "make resilience-drill-check":
    missing.append("manifest commands.gate must be make resilience-drill-check")

required = set(manifest.get("requiredEvidence") or [])
for item in (
    "rate-limit rejection",
    "retry attempts",
    "breaker open",
    "recovery to closed",
    "surface matrix",
    "enable disable paths",
    "invalid configuration",
    "downgrade fallback path",
):
    if item not in required:
        missing.append(f"manifest requiredEvidence missing {item!r}")

p13 = manifest.get("p13GoZeroResilienceDefaults") or {}
if p13.get("status") != "blocking":
    missing.append("p13GoZeroResilienceDefaults.status must be blocking")
if p13.get("task") != "GOFLY-P13-04-GOZERO-RESILIENCE-DEFAULTS":
    missing.append("p13GoZeroResilienceDefaults.task must identify P13-04")
if "go-zero" not in p13.get("goal", ""):
    missing.append("p13GoZeroResilienceDefaults.goal must describe go-zero alignment")

required_capabilities = [
    "timeout",
    "concurrency-limit",
    "rate-limit",
    "breaker",
    "adaptive-shedding",
    "retry",
    "enable-disable",
    "invalid-config",
    "downgrade-fallback",
]
if p13.get("requiredCapabilities") != required_capabilities:
    missing.append("p13GoZeroResilienceDefaults.requiredCapabilities must match the P13 matrix contract")

gate_policy = p13.get("gatePolicy") or {}
allowed_modes = set(gate_policy.get("allowedModes") or [])
disallowed_modes = set(gate_policy.get("disallowedModes") or [])
for mode in ("runtime-tested", "client-runtime-tested", "mixed-runtime-tested", "candidate-via-governance"):
    if mode not in allowed_modes:
        missing.append(f"p13 gatePolicy.allowedModes missing {mode!r}")
for mode in ("unsupported-report-only", "documentation-only"):
    if mode not in disallowed_modes:
        missing.append(f"p13 gatePolicy.disallowedModes missing {mode!r}")
if gate_policy.get("requiredSurfaceCount") != 3:
    missing.append("p13 gatePolicy.requiredSurfaceCount must be 3")
if gate_policy.get("requiredCapabilityCountPerSurface") != len(required_capabilities):
    missing.append("p13 gatePolicy.requiredCapabilityCountPerSurface must match required capabilities")
if gate_policy.get("gatewayAdaptiveSheddingBoundary") != "candidate-via-governance":
    missing.append("p13 gatewayAdaptiveSheddingBoundary must record the current gateway boundary")
if gate_policy.get("latencyBudget") != "report-only":
    missing.append("p13 latencyBudget must remain report-only")
if "blocking" not in gate_policy.get("allocationBudget", ""):
    missing.append("p13 allocationBudget must describe blocking allocation evidence")

surfaces = p13.get("surfaces") or []
if len(surfaces) != gate_policy.get("requiredSurfaceCount", 0):
    missing.append("p13 surfaces must contain exactly REST, RPC, and Gateway")

expected_surface_modes = {
    "rest": "runtime-tested",
    "rpc": "runtime-tested",
    "gateway": "mixed-runtime-tested",
}
seen_surfaces = set()
for surface in surfaces:
    if not isinstance(surface, dict):
        missing.append(f"p13 surface entry must be an object: {surface!r}")
        continue
    surface_name = surface.get("name")
    seen_surfaces.add(surface_name)
    if surface_name not in expected_surface_modes:
        missing.append(f"p13 surface has unknown name: {surface_name!r}")
        continue
    if surface.get("mode") != expected_surface_modes[surface_name]:
        missing.append(f"p13 surface {surface_name}: mode must be {expected_surface_modes[surface_name]!r}")
    if len(surface.get("defaultBehavior", "")) < 40:
        missing.append(f"p13 surface {surface_name}: defaultBehavior must be descriptive")

    capabilities = surface.get("capabilities") or []
    if len(capabilities) != gate_policy.get("requiredCapabilityCountPerSurface", 0):
        missing.append(f"p13 surface {surface_name}: capability count must be {len(required_capabilities)}")
    capability_map = {item.get("name"): item for item in capabilities if isinstance(item, dict)}
    for capability in required_capabilities:
        evidence = capability_map.get(capability)
        if not evidence:
            missing.append(f"p13 surface {surface_name}: missing capability {capability!r}")
            continue
        mode = evidence.get("mode")
        if mode in disallowed_modes:
            missing.append(f"p13 surface {surface_name} capability {capability}: mode {mode!r} is disallowed")
        if mode not in allowed_modes:
            missing.append(f"p13 surface {surface_name} capability {capability}: unsupported mode {mode!r}")
        tests = evidence.get("tests") or []
        if not tests:
            missing.append(f"p13 surface {surface_name} capability {capability}: tests are required")
        for test_ref in tests:
            if not isinstance(test_ref, str) or ":Test" not in test_ref:
                missing.append(f"p13 surface {surface_name} capability {capability}: invalid test reference {test_ref!r}")
                continue
            file_name, test_name = test_ref.split(":", 1)
            test_path = pathlib.Path(file_name)
            if not test_path.is_file():
                missing.append(f"p13 surface {surface_name} capability {capability}: test file missing {file_name}")
                continue
            test_text = test_path.read_text(encoding="utf-8")
            if f"func {test_name}(" not in test_text:
                missing.append(f"{file_name}: missing test function {test_name}")

    gateway_adaptive = capability_map.get("adaptive-shedding")
    if surface_name == "gateway":
        if not gateway_adaptive or gateway_adaptive.get("mode") != "candidate-via-governance":
            missing.append("p13 gateway adaptive-shedding must remain candidate-via-governance until dedicated limiter exists")
        if not gateway_adaptive or "dedicated Gateway adaptive limiter" not in gateway_adaptive.get("followUp", ""):
            missing.append("p13 gateway adaptive-shedding must record the dedicated limiter follow-up")
    elif capability_map.get("adaptive-shedding", {}).get("mode") != "runtime-tested":
        missing.append(f"p13 surface {surface_name}: adaptive-shedding must be runtime-tested")

for surface_name in expected_surface_modes:
    if surface_name not in seen_surfaces:
        missing.append(f"p13 surfaces missing {surface_name!r}")

reference_evidence = manifest.get("referenceAppEvidence") or []
expected_components = {"saga compensation", "outbox retry", "topology fallback", "rollback note"}
components = {item.get("component") for item in reference_evidence if isinstance(item, dict)}
for component in expected_components:
    if component not in components:
        missing.append(f"manifest referenceAppEvidence missing {component!r}")
for item in reference_evidence:
    if not isinstance(item, dict):
        missing.append(f"referenceAppEvidence entry must be an object: {item!r}")
        continue
    path = pathlib.Path(item.get("path", ""))
    signal = item.get("signal", "")
    if not path.is_file():
        missing.append(f"reference evidence path is missing: {path}")
        continue
    if not signal:
        missing.append(f"reference evidence signal is required for {path}")
        continue
    if signal not in path.read_text(encoding="utf-8"):
        missing.append(f"{path}: missing reference evidence signal {signal!r}")

docs = {
    pathlib.Path("docs/reference/resilience-drill.md"): [
        "gofly.resilience_drill_evidence.v1",
        "gofly.resilience_drill.v1",
        "make resilience-drill-check",
        "go run -C examples/resilience . --json",
        "production-orders",
        "GOFLY-P13-04-GOZERO-RESILIENCE-DEFAULTS",
        "adaptive-shedding",
        "candidate-via-governance",
    ],
    pathlib.Path("docs/concepts/governance.md"): [
        "make resilience-drill-check",
        "gofly.resilience_drill.v1",
    ],
    pathlib.Path("examples/README.md"): [
        "go run -C examples/resilience . --json",
        "gofly.resilience_drill.v1",
    ],
    pathlib.Path("Makefile"): [
        "resilience-drill-check",
        "check-resilience-drill.sh",
    ],
}
for doc, needles in docs.items():
    if not doc.is_file():
        missing.append(f"{doc}: file is missing")
        continue
    text = doc.read_text(encoding="utf-8")
    for needle in needles:
        if needle not in text:
            missing.append(f"{doc}: missing {needle!r}")

if missing:
    print("resilience drill check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    raise SystemExit(1)

print("resilience drill governance ok")
PY
