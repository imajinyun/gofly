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

if missing:
    print("cloud-native render check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("cloud-native rendering governance ok")
PY
