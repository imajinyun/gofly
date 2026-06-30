#!/usr/bin/env sh
set -eu

rendered="${TMPDIR:-/tmp}/gofly-helm-render-smoke.yaml"
rendered_production="${TMPDIR:-/tmp}/gofly-helm-render-production-smoke.yaml"
kustomize_rendered="${TMPDIR:-/tmp}/gofly-kustomize-production-render-smoke.yaml"
report_path="${CLOUD_NATIVE_RENDER_REPORT:-.tmp-test/cloud-native-render/render-report.json}"
mkdir -p "$(dirname -- "$report_path")"

if command -v helm >/dev/null 2>&1; then
	helm template gofly charts/gofly > "$rendered"
	helm template gofly charts/gofly -f charts/gofly/values-production.yaml > "$rendered_production"
	render_mode="helm-template"
	helm_available="true"
else
	cat charts/gofly/templates/*.yaml > "$rendered"
	cat charts/gofly/templates/*.yaml > "$rendered_production"
	render_mode="static-template-render"
	helm_available="false"
fi

if command -v kustomize >/dev/null 2>&1; then
	kustomize build k8s/overlays/production > "$kustomize_rendered"
	kustomize_available="true"
else
	cat k8s/deployment.yaml k8s/servicemonitor.yaml k8s/hpa.yaml k8s/pdb.yaml k8s/networkpolicy.yaml > "$kustomize_rendered"
	kustomize_available="false"
fi
if command -v kubeconform >/dev/null 2>&1; then
	kubeconform_available="true"
else
	kubeconform_available="false"
fi
if command -v kubeval >/dev/null 2>&1; then
	kubeval_available="true"
else
	kubeval_available="false"
fi

kubeconform_status="tool-unavailable"
kubeconform_output=""
if [ "$kubeconform_available" = "true" ]; then
	kubeconform_output="${TMPDIR:-/tmp}/gofly-kubeconform-output.txt"
	if kubeconform -ignore-missing-schemas -summary "$rendered_production" "$kustomize_rendered" >"$kubeconform_output" 2>&1; then
		kubeconform_status="passed"
	else
		cat "$kubeconform_output" >&2
		kubeconform_status="failed"
	fi
fi

kubeval_status="tool-unavailable"
kubeval_output=""
if [ "$kubeval_available" = "true" ]; then
	kubeval_output="${TMPDIR:-/tmp}/gofly-kubeval-output.txt"
	if kubeval --ignore-missing-schemas "$rendered_production" "$kustomize_rendered" >"$kubeval_output" 2>&1; then
		kubeval_status="passed"
	else
		cat "$kubeval_output" >&2
		kubeval_status="failed"
	fi
fi

python3 - "$rendered" "$rendered_production" "$kustomize_rendered" "$report_path" "$render_mode" "$helm_available" "$kustomize_available" "$kubeconform_available" "$kubeval_available" "$kubeconform_status" "$kubeval_status" "$kubeconform_output" "$kubeval_output" <<'PY'
import pathlib
import sys
import json

rendered = pathlib.Path(sys.argv[1])
rendered_production = pathlib.Path(sys.argv[2])
kustomize_rendered = pathlib.Path(sys.argv[3])
report_path = pathlib.Path(sys.argv[4])
render_mode = sys.argv[5]
helm_available = sys.argv[6] == "true"
kustomize_available = sys.argv[7] == "true"
kubeconform_available = sys.argv[8] == "true"
kubeval_available = sys.argv[9] == "true"
kubeconform_status = sys.argv[10]
kubeval_status = sys.argv[11]
kubeconform_output = sys.argv[12]
kubeval_output = sys.argv[13]
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
        "gofly.cloud_native_render_report.v1",
        "helm-template",
        "static-template-render",
        "toolAvailabilityPolicy",
        "kubeconform",
        "kubeval",
        "renderedGoldens",
        "renderReport",
        "fallbackStatus",
        "fallbackReasons",
        "ServiceMonitor",
        "HorizontalPodAutoscaler",
        "PodDisruptionBudget",
        "NetworkPolicy",
        "make cloud-native-render-check",
    ],
    pathlib.Path("docs/reference/cloud-native-rendered-production.golden.yaml"): [
        "kind: Deployment",
        "kind: Service",
        "kind: ServiceMonitor",
        "kind: HorizontalPodAutoscaler",
        "kind: PodDisruptionBudget",
        "kind: NetworkPolicy",
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
production_render_text = rendered_production.read_text(encoding="utf-8")
kustomize_render_text = kustomize_rendered.read_text(encoding="utf-8")
for needle in ("kind: Deployment", "kind: Service", "kind: NetworkPolicy"):
    if needle not in render_text:
        missing.append(f"rendered Helm output missing {needle!r}")
for needle in (
    "kind: ServiceMonitor",
    "kind: HorizontalPodAutoscaler",
    "kind: PodDisruptionBudget",
    "kind: NetworkPolicy",
):
    if needle not in production_render_text:
        missing.append(f"rendered production Helm output missing {needle!r}")
for needle in (
    "kind: Deployment",
    "kind: ServiceMonitor",
    "kind: HorizontalPodAutoscaler",
    "kind: PodDisruptionBudget",
    "kind: NetworkPolicy",
):
    if needle not in kustomize_render_text:
        missing.append(f"rendered Kustomize output missing {needle!r}")

if kubeconform_status == "failed":
    missing.append("kubeconform schema validation failed")
if kubeval_status == "failed":
    missing.append("kubeval schema validation failed")

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

render_report = manifest.get("renderReport") or {}
if render_report.get("schema") != "gofly.cloud_native_render_report.v1":
    missing.append("cloud-native policy conformance renderReport schema mismatch")
if render_report.get("path") != ".tmp-test/cloud-native-render/render-report.json":
    missing.append("cloud-native policy conformance renderReport path mismatch")
required_report_fields = {
    "schema",
    "renderMode",
    "helm.available",
    "helm.requiredWhenAvailable",
    "helm.fallbackStatus",
    "kustomize.available",
    "kustomize.fallbackStatus",
    "kubeconform.schemaValidationStatus",
    "kubeval.schemaValidationStatus",
    "fallbackReasons",
    "requiredKinds",
}
if set(render_report.get("requiredFields") or []) != required_report_fields:
    missing.append("cloud-native policy conformance renderReport requiredFields mismatch")

render_modes = {item.get("mode") for item in manifest.get("renderModes") or [] if isinstance(item, dict)}
for mode in ("helm-template", "static-template-render", "kustomize-build"):
    if mode not in render_modes:
        missing.append(f"cloud-native policy conformance renderModes missing {mode!r}")

schema_tools = {item.get("tool") for item in manifest.get("schemaValidation") or [] if isinstance(item, dict)}
for tool in ("kubeconform", "kubeval"):
    if tool not in schema_tools:
        missing.append(f"cloud-native policy conformance schemaValidation missing {tool!r}")

adopter_proof = manifest.get("adopterRolloutProof") or {}
if adopter_proof.get("schema") != "gofly.cloud_native_adopter_rollout_proof.v1":
    missing.append("cloud-native policy conformance adopterRolloutProof schema mismatch")
if adopter_proof.get("source") != "docs/reference/cloud-native-policy-conformance.json":
    missing.append("cloud-native policy conformance adopterRolloutProof source mismatch")
if adopter_proof.get("dashboardReportField") != "cloudNativeAdoption.rolloutProof":
    missing.append("cloud-native policy conformance adopterRolloutProof dashboardReportField mismatch")
if set(adopter_proof.get("acceptanceGates") or []) != {
    "make helm-template-smoke",
    "make cloud-native-render-check",
    "make p1-growth-check",
}:
    missing.append("cloud-native policy conformance adopterRolloutProof acceptanceGates mismatch")
if len(str(adopter_proof.get("policy") or "").split()) < 20:
    missing.append("cloud-native policy conformance adopterRolloutProof policy must be actionable")

expected_rollout_profiles = {
    "helm-default": ("local-smoke", "make helm-template-smoke"),
    "helm-production": ("production-candidate", "make cloud-native-render-check"),
    "kustomize-production": ("static-fallback-evidence", "make cloud-native-render-check"),
}
rollout_profiles = {
    item.get("profile"): item
    for item in adopter_proof.get("rolloutProfiles") or []
    if isinstance(item, dict) and item.get("profile")
}
if set(rollout_profiles) != set(expected_rollout_profiles):
    missing.append(
        "cloud-native policy conformance adopterRolloutProof rolloutProfiles drifted "
        f"missing={sorted(set(expected_rollout_profiles) - set(rollout_profiles))} "
        f"extra={sorted(set(rollout_profiles) - set(expected_rollout_profiles))}"
    )
for profile, (classification, gate) in expected_rollout_profiles.items():
    item = rollout_profiles.get(profile) or {}
    if item.get("classification") != classification:
        missing.append(f"cloud-native adopterRolloutProof {profile}: classification must be {classification}")
    if item.get("requiredGate") != gate:
        missing.append(f"cloud-native adopterRolloutProof {profile}: requiredGate must be {gate}")
    for field in ("renderMode", "adopterAction", "rollbackAction"):
        if len(str(item.get(field) or "").split()) < 8:
            missing.append(f"cloud-native adopterRolloutProof {profile}: {field} must be actionable")

resource_requirements = {
    item.get("kind"): item
    for item in adopter_proof.get("policyResourceRequirements") or []
    if isinstance(item, dict) and item.get("kind")
}
for kind in ("ServiceMonitor", "HorizontalPodAutoscaler", "PodDisruptionBudget", "NetworkPolicy"):
    item = resource_requirements.get(kind)
    if not item:
        missing.append(f"cloud-native adopterRolloutProof policyResourceRequirements missing {kind!r}")
        continue
    for field in ("adopterAction", "rollbackAction"):
        if len(str(item.get(field) or "").split()) < 8:
            missing.append(f"cloud-native adopterRolloutProof {kind}: {field} must be actionable")
if len(str(adopter_proof.get("toolFallbackPolicy") or "").split()) < 15:
    missing.append("cloud-native adopterRolloutProof toolFallbackPolicy must be actionable")

tool_policy = manifest.get("toolAvailabilityPolicy") or {}
for tool, field in (
    ("helm", "renderMode"),
    ("kustomize", "renderMode"),
    ("kubeconform", "schemaValidationStatus"),
    ("kubeval", "schemaValidationStatus"),
):
    policy = tool_policy.get(tool)
    if not isinstance(policy, dict):
        missing.append(f"cloud-native policy conformance toolAvailabilityPolicy missing {tool!r}")
        continue
    if policy.get("reportField") != field:
        missing.append(f"cloud-native policy conformance {tool}: reportField must be {field!r}")
    if not policy.get("missingStatus"):
        missing.append(f"cloud-native policy conformance {tool}: missingStatus is required")
    if tool in ("helm", "kustomize") and policy.get("requiredWhenAvailable") is not True:
        missing.append(f"cloud-native policy conformance {tool}: requiredWhenAvailable must be true")
    if tool in ("kubeconform", "kubeval") and policy.get("requiredWhenAvailable") is not False:
        missing.append(f"cloud-native policy conformance {tool}: requiredWhenAvailable must be false")

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

golden_profiles = {item.get("profile") for item in manifest.get("renderedGoldens") or [] if isinstance(item, dict)}
if "kustomize-production" not in golden_profiles:
    missing.append("cloud-native policy conformance renderedGoldens missing kustomize-production")
for item in manifest.get("renderedGoldens") or []:
    if not isinstance(item, dict):
        missing.append(f"cloud-native rendered golden must be an object: {item!r}")
        continue
    name = item.get("name", "<missing>")
    path = item.get("path")
    if not path or not pathlib.Path(path).is_file():
        missing.append(f"cloud-native rendered golden {name}: path is missing: {path}")
        continue
    if item.get("fallbackStatus") not in {"tool-unavailable-explicit", "not-fallback"}:
        missing.append(f"cloud-native rendered golden {name}: fallbackStatus must be explicit")
    if not item.get("producedBy"):
        missing.append(f"cloud-native rendered golden {name}: producedBy is required")
    text = pathlib.Path(path).read_text(encoding="utf-8")
    kinds = set(item.get("requiredKinds") or [])
    if not required_kinds <= kinds:
        missing.append(f"cloud-native rendered golden {name}: requiredKinds missing {sorted(required_kinds - kinds)!r}")
    for kind in kinds:
        if f"kind: {kind}" not in text:
            missing.append(f"cloud-native rendered golden {name}: file missing kind {kind!r}")

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

fallback_status = "not-fallback" if helm_available else "static-fallback"
kustomize_fallback_status = "not-fallback" if kustomize_available else "static-fallback"
fallback_reasons = []
if not helm_available:
    fallback_reasons.append({
        "tool": "helm",
        "status": "static-fallback",
        "reason": "helm binary is unavailable; static chart template concatenation was used",
    })
if not kustomize_available:
    fallback_reasons.append({
        "tool": "kustomize",
        "status": "static-fallback",
        "reason": "kustomize binary is unavailable; static production resource concatenation was used",
    })
if not kubeconform_available:
    fallback_reasons.append({
        "tool": "kubeconform",
        "status": "tool-unavailable",
        "reason": "kubeconform binary is unavailable; schema validation was not run",
    })
if not kubeval_available:
    fallback_reasons.append({
        "tool": "kubeval",
        "status": "tool-unavailable",
        "reason": "kubeval binary is unavailable; schema validation was not run",
    })
fallback_tools = {item["tool"] for item in fallback_reasons}
for tool, available in (
    ("helm", helm_available),
    ("kustomize", kustomize_available),
    ("kubeconform", kubeconform_available),
    ("kubeval", kubeval_available),
):
    if not available and tool not in fallback_tools:
        missing.append(f"{tool} is unavailable but fallbackReasons has no entry")
report = {
    "schema": "gofly.cloud_native_render_report.v1",
    "renderMode": render_mode,
    "fallbackReasons": fallback_reasons,
    "helm": {
        "available": helm_available,
        "requiredWhenAvailable": True,
        "fallbackStatus": fallback_status,
        "defaultRender": str(rendered),
        "productionRender": str(rendered_production),
    },
    "kustomize": {
        "available": kustomize_available,
        "requiredWhenAvailable": True,
        "fallbackStatus": kustomize_fallback_status,
        "productionRender": str(kustomize_rendered),
    },
    "kubeconform": {
        "available": kubeconform_available,
        "schemaValidationStatus": kubeconform_status,
        "output": kubeconform_output,
    },
    "kubeval": {
        "available": kubeval_available,
        "schemaValidationStatus": kubeval_status,
        "output": kubeval_output,
    },
    "requiredKinds": sorted(required_kinds),
    "golden": "docs/reference/cloud-native-rendered-production.golden.yaml",
}
report_path.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")

if helm_available and render_mode != "helm-template":
    missing.append("helm is available, so renderMode must be helm-template")
if not helm_available and fallback_status != "static-fallback":
    missing.append("helm is unavailable, so fallbackStatus must be static-fallback")
if kustomize_available and kustomize_fallback_status != "not-fallback":
    missing.append("kustomize is available, so fallbackStatus must be not-fallback")
if not kustomize_available and kustomize_fallback_status != "static-fallback":
    missing.append("kustomize is unavailable, so fallbackStatus must be static-fallback")

if missing:
    print("cloud-native render check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("cloud-native rendering governance ok")
PY
