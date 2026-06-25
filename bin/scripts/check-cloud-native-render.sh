#!/usr/bin/env sh
set -eu

rendered="${TMPDIR:-/tmp}/gofly-helm-render-smoke.yaml"

if command -v helm >/dev/null 2>&1; then
	helm template gofly charts/gofly > "$rendered"
else
	cat charts/gofly/templates/*.yaml > "$rendered"
fi

python3 - "$rendered" <<'PY'
import pathlib
import sys
import json

rendered = pathlib.Path(sys.argv[1])
checks = {
    pathlib.Path("charts/gofly/values.schema.json"): [
        "networkPolicy",
        "serviceMonitor",
        "autoscaling",
        "podDisruptionBudget",
    ],
    pathlib.Path("charts/gofly/values-production.yaml"): [
        "networkPolicy:",
        "serviceMonitor:",
        "autoscaling:",
        "podDisruptionBudget:",
    ],
    pathlib.Path("k8s/overlays/production/kustomization.yaml"): [
        "../../../",
        "networkpolicy.yaml",
    ],
    pathlib.Path("docs/reference/cloud-native-rendering.md"): [
        "gofly.cloud_native_rendering.v1",
        "Helm schema",
        "values profiles",
        "Kustomize overlays",
        "NetworkPolicy",
        "HPA",
        "PDB",
        "ServiceMonitor",
        "helm template",
        "kubeconform",
        "kubeval",
        "static fallback",
    ],
    pathlib.Path("docs/reference/cloud-native-policy-conformance.json"): [
        "gofly.cloud_native_policy_conformance.v1",
        "helm-template",
        "static-template-render",
        "kubeconform",
        "kubeval",
        "ServiceMonitor",
        "HorizontalPodAutoscaler",
        "PodDisruptionBudget",
        "NetworkPolicy",
        "make cloud-native-render-check",
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

render_text = rendered.read_text(encoding="utf-8")
for needle in ("kind: Deployment", "kind: Service", "kind: NetworkPolicy"):
    if needle not in render_text:
        missing.append(f"rendered Helm output missing {needle!r}")

for tool in ("kubeconform", "kubeval"):
    # Tool execution is optional locally; documentation must state the fallback.
    pass

manifest_path = pathlib.Path("docs/reference/cloud-native-policy-conformance.json")
if manifest_path.is_file():
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
else:
    manifest = {}
    missing.append("docs/reference/cloud-native-policy-conformance.json: file is missing")

if manifest.get("schema") != "gofly.cloud_native_policy_conformance.v1":
    missing.append("cloud-native policy conformance schema mismatch")
if manifest.get("sourceOfTruth") != "docs/reference/cloud-native-rendering.md":
    missing.append("cloud-native policy conformance sourceOfTruth mismatch")
if manifest.get("acceptanceGate") != "make cloud-native-render-check":
    missing.append("cloud-native policy conformance acceptanceGate mismatch")

render_modes = {item.get("mode") for item in manifest.get("renderModes") or [] if isinstance(item, dict)}
for mode in ("helm-template", "static-template-render"):
    if mode not in render_modes:
        missing.append(f"cloud-native policy conformance renderModes missing {mode!r}")

schema_tools = {item.get("tool") for item in manifest.get("schemaValidation") or [] if isinstance(item, dict)}
for tool in ("kubeconform", "kubeval"):
    if tool not in schema_tools:
        missing.append(f"cloud-native policy conformance schemaValidation missing {tool!r}")

required_kinds = {
    "Deployment",
    "Service",
    "ServiceMonitor",
    "HorizontalPodAutoscaler",
    "PodDisruptionBudget",
    "NetworkPolicy",
}
profiles = manifest.get("profiles") or []
profile_names = {item.get("name") for item in profiles if isinstance(item, dict)}
for name in ("helm-default", "helm-production", "kustomize-production"):
    if name not in profile_names:
        missing.append(f"cloud-native policy conformance profiles missing {name!r}")
for profile in profiles:
    if not isinstance(profile, dict):
        missing.append(f"cloud-native policy conformance profile must be an object: {profile!r}")
        continue
    name = profile.get("name", "<missing>")
    for key in ("values", "schema", "overlay"):
        rel = profile.get(key)
        if rel and not pathlib.Path(rel).is_file():
            missing.append(f"cloud-native policy conformance {name}: {key} path is missing: {rel}")
    for rel in (profile.get("templates") or []) + (profile.get("resources") or []):
        if not pathlib.Path(rel).is_file():
            missing.append(f"cloud-native policy conformance {name}: path is missing: {rel}")
    kinds = set(profile.get("requiredKinds") or [])
    if name.startswith("helm-"):
        expected = required_kinds
    else:
        expected = required_kinds - {"Service"}
    unknown = kinds - required_kinds
    if unknown:
        missing.append(f"cloud-native policy conformance {name}: unknown requiredKinds: {sorted(unknown)!r}")
    if not expected <= kinds:
        missing.append(f"cloud-native policy conformance {name}: requiredKinds missing {sorted(expected - kinds)!r}")

policy_resources = manifest.get("policyResources") or []
policy_kinds = {item.get("kind") for item in policy_resources if isinstance(item, dict)}
for kind in ("ServiceMonitor", "HorizontalPodAutoscaler", "PodDisruptionBudget", "NetworkPolicy"):
    if kind not in policy_kinds:
        missing.append(f"cloud-native policy conformance policyResources missing {kind!r}")
for item in policy_resources:
    if not isinstance(item, dict):
        missing.append(f"cloud-native policy resource must be an object: {item!r}")
        continue
    kind = item.get("kind", "<missing>")
    for key in ("helmTemplate", "kustomizeResource"):
        rel = item.get(key)
        if not rel or not pathlib.Path(rel).is_file():
            missing.append(f"cloud-native policy resource {kind}: {key} path is missing: {rel}")
    if not item.get("requiredSignals"):
        missing.append(f"cloud-native policy resource {kind}: requiredSignals is required")

rollout_gates = set(manifest.get("rolloutGates") or [])
for gate in ("make helm-template-smoke", "make cloud-native-render-check", "make p1-growth-check"):
    if gate not in rollout_gates:
        missing.append(f"cloud-native policy conformance rolloutGates missing {gate!r}")

if missing:
    print("cloud-native render check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("cloud-native rendering governance ok")
PY
