#!/usr/bin/env sh
set -eu

workdir="$(mktemp -d "${TMPDIR:-/tmp}/gofly-cloud-native-render-XXXXXX")"
trap 'rm -rf "$workdir"' EXIT INT TERM

helm_default="$workdir/helm-default.yaml"
helm_production="$workdir/helm-production.yaml"
kustomize_production="$workdir/kustomize-production.yaml"
report_path="${CLOUD_NATIVE_RENDER_REPORT:-.tmp-test/cloud-native-render/render-report.json}"
mkdir -p "$(dirname -- "$report_path")"

if command -v helm >/dev/null 2>&1; then
	helm template gofly deploy/helm/gofly >"$helm_default"
	helm template gofly deploy/helm/gofly -f deploy/helm/gofly/values-production.yaml >"$helm_production"
	helm_available=true
	helm_mode=helm-template
else
	cat deploy/k8s/deployment.yaml deploy/k8s/servicemonitor.yaml deploy/k8s/hpa.yaml deploy/k8s/pdb.yaml deploy/k8s/networkpolicy.yaml >"$helm_default"
	cp "$helm_default" "$helm_production"
	helm_available=false
	helm_mode=static-template-render
fi

if command -v kustomize >/dev/null 2>&1; then
	kustomize build deploy/k8s/overlays/production >"$kustomize_production"
	kustomize_available=true
	kustomize_mode=kustomize-build
else
	cat \
		deploy/k8s/deployment.yaml \
		deploy/k8s/servicemonitor.yaml \
		deploy/k8s/hpa.yaml \
		deploy/k8s/pdb.yaml \
		deploy/k8s/networkpolicy.yaml >"$kustomize_production"
	kustomize_available=false
	kustomize_mode=static-resource-render
fi

kubeconform_available=false
kubeconform_status=tool-unavailable
if command -v kubeconform >/dev/null 2>&1; then
	kubeconform_available=true
	if kubeconform -ignore-missing-schemas -summary "$helm_production" "$kustomize_production" >"$workdir/kubeconform.txt" 2>&1; then
		kubeconform_status=passed
	else
		kubeconform_status=failed
		cat "$workdir/kubeconform.txt" >&2
	fi
fi

python3 - \
	"$helm_default" \
	"$helm_production" \
	"$kustomize_production" \
	"$report_path" \
	"$helm_available" \
	"$helm_mode" \
	"$kustomize_available" \
	"$kustomize_mode" \
	"$kubeconform_available" \
	"$kubeconform_status" <<'PY'
import json
import pathlib
import sys

helm_default = pathlib.Path(sys.argv[1])
helm_production = pathlib.Path(sys.argv[2])
kustomize_production = pathlib.Path(sys.argv[3])
report_path = pathlib.Path(sys.argv[4])
helm_available = sys.argv[5] == "true"
helm_mode = sys.argv[6]
kustomize_available = sys.argv[7] == "true"
kustomize_mode = sys.argv[8]
kubeconform_available = sys.argv[9] == "true"
kubeconform_status = sys.argv[10]

required_sources = [
    pathlib.Path("deploy/helm/gofly/values.schema.json"),
    pathlib.Path("deploy/helm/gofly/values-production.yaml"),
    pathlib.Path("deploy/k8s/overlays/production/kustomization.yaml"),
]
missing = [f"required source is missing: {path}" for path in required_sources if not path.is_file()]

required_kinds = {
    "helm-default": {"Deployment", "Service", "ServiceMonitor", "HorizontalPodAutoscaler", "PodDisruptionBudget", "NetworkPolicy"},
    "helm-production": {"Deployment", "Service", "ServiceMonitor", "HorizontalPodAutoscaler", "PodDisruptionBudget", "NetworkPolicy"},
    "kustomize-production": {"Deployment", "ServiceMonitor", "HorizontalPodAutoscaler", "PodDisruptionBudget", "NetworkPolicy"},
}
renders = {
    "helm-default": helm_default,
    "helm-production": helm_production,
    "kustomize-production": kustomize_production,
}
for profile, path in renders.items():
    text = path.read_text(encoding="utf-8")
    for kind in sorted(required_kinds[profile]):
        if f"kind: {kind}" not in text:
            missing.append(f"{profile} render is missing kind {kind}")

if kubeconform_status == "failed":
    missing.append("kubeconform schema validation failed")

fallback_reasons = []
if not helm_available:
    fallback_reasons.append({
        "tool": "helm",
        "status": "tool-unavailable",
        "reason": "helm is unavailable; equivalent static Kubernetes resources were inspected without chart value expansion",
    })
if not kustomize_available:
    fallback_reasons.append({
        "tool": "kustomize",
        "status": "tool-unavailable",
        "reason": "kustomize is unavailable; production resources were concatenated for structural checks",
    })
if not kubeconform_available:
    fallback_reasons.append({
        "tool": "kubeconform",
        "status": "tool-unavailable",
        "reason": "kubeconform is unavailable; schema validation was not run",
    })

report = {
    "schema": "gofly.cloud_native_render_report.v1",
    "status": "failed" if missing else "passed",
    "renderMode": helm_mode,
    "helm": {
        "available": helm_available,
        "mode": helm_mode,
        "requiredKinds": sorted(required_kinds["helm-production"]),
    },
    "kustomize": {
        "available": kustomize_available,
        "mode": kustomize_mode,
        "requiredKinds": sorted(required_kinds["kustomize-production"]),
    },
    "kubeconform": {
        "available": kubeconform_available,
        "schemaValidationStatus": kubeconform_status,
    },
    "fallbackReasons": fallback_reasons,
    "errors": missing,
    "sourceOfTruth": [str(path) for path in required_sources],
}
report_path.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")

if missing:
    print("cloud-native render check failed:", file=sys.stderr)
    for item in missing:
        print(f"  {item}", file=sys.stderr)
    raise SystemExit(1)

print(f"cloud-native rendering governance ok: {report_path}")
PY
