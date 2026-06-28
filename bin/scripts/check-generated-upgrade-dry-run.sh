#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import subprocess
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "generated-upgrade-dry-run.json"
missing = []


def require(condition, message):
    if not condition:
        missing.append(message)


def read_text(path):
    if not path.is_file():
        missing.append(f"{path.relative_to(root)} is missing")
        return ""
    return path.read_text(encoding="utf-8")


def make_target_body(makefile, target):
    match = re.search(
        rf"^\.PHONY:\s*{re.escape(target)}\n"
        rf"{re.escape(target)}:.*?\n(?P<body>.*?)(?=^\.PHONY:|\Z)",
        makefile,
        re.S | re.M,
    )
    require(match is not None, f"Makefile target {target!r} is missing")
    return match.group("body") if match else ""


def make_target_deps(makefile, target):
    match = re.search(rf"^{re.escape(target)}:(?P<deps>[^#\n]*)", makefile, re.M)
    require(match is not None, f"Makefile target {target!r} is missing")
    return match.group("deps") if match else ""


if manifest_path.is_file():
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
else:
    manifest = {}
    missing.append("docs/reference/generated-upgrade-dry-run.json is missing")

require(
    manifest.get("schema") == "gofly.generated_upgrade_dry_run.v1",
    "generated upgrade dry-run schema must be gofly.generated_upgrade_dry_run.v1",
)

policy = manifest.get("artifactPolicy") or {}
require(
    policy.get("commitGeneratedRuntimeArtifacts") is False,
    "artifactPolicy.commitGeneratedRuntimeArtifacts must be false",
)
volatile_dirs = set(policy.get("volatileDirectories") or [])
for directory in (".tmp-test/generated-upgrade-dry-run", "$TMPDIR/gofly-generated-upgrade-*"):
    require(directory in volatile_dirs, f"artifactPolicy.volatileDirectories missing {directory!r}")

gitignore = read_text(root / ".gitignore")
require(".tmp-test/" in gitignore, ".gitignore must ignore .tmp-test/ generated dry-run artifacts")
tracked_tmp = subprocess.run(
    ["git", "ls-files", ".tmp-test"],
    cwd=root,
    check=False,
    text=True,
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
)
if tracked_tmp.returncode == 0:
    tracked_paths = [line for line in tracked_tmp.stdout.splitlines() if line.strip()]
    require(not tracked_paths, f"generated dry-run temp artifacts must not be tracked: {tracked_paths}")
else:
    missing.append(f"could not verify tracked .tmp-test artifacts: {tracked_tmp.stderr.strip()}")

contract = manifest.get("diffReportContract") or {}
categories = contract.get("categories") or []
category_names = {item.get("category") for item in categories if isinstance(item, dict)}
required_categories = {
    "deterministic-repeat-generation",
    "compatible-addition",
    "formatting-only",
    "breaking-candidate",
}
require(category_names == required_categories, f"diffReportContract categories mismatch: {sorted(category_names)!r}")
for item in categories:
    if not isinstance(item, dict):
        missing.append(f"diffReportContract category must be an object: {item!r}")
        continue
    category = item.get("category", "<missing>")
    for field in ("description", "requiresRollbackNote", "severity"):
        require(field in item and item[field] not in ("", None), f"category {category}: {field} is required")
    require(item.get("severity") in {"pass-or-fail", "review", "block"}, f"category {category}: unexpected severity")

required_fields = set(contract.get("requiredFields") or [])
for field in ("profile", "repeatGeneration", "categories", "summary", "rollbackNote"):
    require(field in required_fields, f"diffReportContract.requiredFields missing {field!r}")

goctl_policy = manifest.get("goctlGeneratorCompatibility") or {}
require(
    goctl_policy.get("source") == "docs/reference/goctl-generator-compatibility.json",
    "goctlGeneratorCompatibility.source must point to docs/reference/goctl-generator-compatibility.json",
)
require(
    goctl_policy.get("acceptanceGate") == "make goctl-generator-compat-check",
    "goctlGeneratorCompatibility.acceptanceGate must be make goctl-generator-compat-check",
)
require(
    len(str(goctl_policy.get("integrationPolicy") or "").split()) >= 10,
    "goctlGeneratorCompatibility.integrationPolicy must explain upgrade dry-run integration",
)
require(
    len(str(goctl_policy.get("rollbackOrEscalation") or "").split()) >= 10,
    "goctlGeneratorCompatibility.rollbackOrEscalation must be actionable",
)
goctl_path = root / "docs" / "reference" / "goctl-generator-compatibility.json"
if goctl_path.is_file():
    goctl_manifest = json.loads(goctl_path.read_text(encoding="utf-8"))
else:
    goctl_manifest = {}
    missing.append("docs/reference/goctl-generator-compatibility.json is missing")
require(
    goctl_manifest.get("schema") == "gofly.goctl_generator_compatibility.v1",
    "goctl generator compatibility schema mismatch",
)
require(
    goctl_manifest.get("acceptanceGate") == "make goctl-generator-compat-check",
    "goctl generator compatibility acceptanceGate mismatch",
)
goctl_capabilities = {
    item.get("id"): item
    for item in goctl_manifest.get("capabilities") or []
    if isinstance(item, dict) and item.get("id")
}
required_goctl_capabilities = set(goctl_policy.get("requiredCapabilities") or [])
expected_goctl_capabilities = {
    "gozero-compatible-profile",
    "goctl-compatible-flags",
    "api-tooling-compatibility",
    "generated-version-fixtures",
    "upgrade-diff-contract",
    "route-layout-boundary",
}
require(
    required_goctl_capabilities == expected_goctl_capabilities,
    f"goctlGeneratorCompatibility.requiredCapabilities mismatch: {sorted(required_goctl_capabilities)!r}",
)
require(
    required_goctl_capabilities <= set(goctl_capabilities),
    f"goctl compatibility matrix missing required capabilities: {sorted(required_goctl_capabilities - set(goctl_capabilities))!r}",
)
for capability_id in required_goctl_capabilities:
    capability = goctl_capabilities.get(capability_id) or {}
    require(capability.get("status") == "implemented", f"goctl capability {capability_id} must be implemented")
    require(bool(capability.get("gate")), f"goctl capability {capability_id} must include a gate")
    require(bool(capability.get("rollbackNote")), f"goctl capability {capability_id} must include rollbackNote")
required_goctl_boundaries = set(goctl_policy.get("requiredBoundaries") or [])
goctl_boundaries = goctl_manifest.get("compatibilityBoundaries") or {}
expected_goctl_boundaries = {
    "noNewJSONEnvelopeFlags",
    "apiRouteAndDiffFormatValidationUnchanged",
    "positionalPluginAndMiddlewareNamesRemainNames",
    "generatedProjectDependenciesStayInGeneratedGoMod",
    "runtimeArtifactsRemainVolatile",
}
require(
    required_goctl_boundaries == expected_goctl_boundaries,
    f"goctlGeneratorCompatibility.requiredBoundaries mismatch: {sorted(required_goctl_boundaries)!r}",
)
for boundary in required_goctl_boundaries:
    require(goctl_boundaries.get(boundary) is True, f"goctl compatibility boundary {boundary} must be true")
require(
    "make generated-upgrade-dry-run-check" in set(goctl_manifest.get("releaseGates") or []),
    "goctl compatibility releaseGates must include make generated-upgrade-dry-run-check",
)

profiles = manifest.get("profiles") or []
profile_names = {item.get("profile") for item in profiles if isinstance(item, dict)}
require(profile_names == {"old", "current", "future"}, f"profiles mismatch: {sorted(profile_names)!r}")

for profile in profiles:
    if not isinstance(profile, dict):
        missing.append(f"profile entry must be an object: {profile!r}")
        continue
    name = profile.get("profile", "<missing>")
    for field in ("api", "proto", "serviceConfig"):
        rel = profile.get(field)
        require(bool(rel), f"profile {name}: {field} is required")
        if rel:
            require((root / rel).is_file(), f"profile {name}: {field} path is missing: {rel}")
    plugin = profile.get("pluginProfile") or {}
    registry = plugin.get("registry")
    require(plugin.get("protocol") == "1", f"profile {name}: plugin protocol must be 1")
    require(bool(plugin.get("compatibility")), f"profile {name}: plugin compatibility is required")
    require(bool(registry), f"profile {name}: plugin registry is required")
    if registry:
        require((root / registry).is_file(), f"profile {name}: plugin registry path is missing: {registry}")

    snapshot = profile.get("generatedSnapshot") or {}
    require(bool(snapshot.get("expectedDiff")), f"profile {name}: generatedSnapshot.expectedDiff is required")
    metadata = snapshot.get("metadata") or {}
    for key in ("generated.project.contract", "generated.project.runtime"):
        require(bool(metadata.get(key)), f"profile {name}: generatedSnapshot.metadata.{key} is required")

    dependency_policy = profile.get("dependencyPolicy") or {}
    require(
        dependency_policy.get("owner") == "generated-project-dependencies",
        f"profile {name}: dependencyPolicy.owner must be generated-project-dependencies",
    )
    require(
        dependency_policy.get("allowedLocation") == "generated project go.mod or isolated temporary test module",
        f"profile {name}: dependencyPolicy.allowedLocation must keep dependencies out of the root module",
    )
    require(
        dependency_policy.get("rootModulePolicy") == "must-not-add-generated-only-dependencies",
        f"profile {name}: dependencyPolicy.rootModulePolicy must reject generated-only root dependencies",
    )
    verification_gates = set(dependency_policy.get("verificationGates") or [])
    require(verification_gates, f"profile {name}: dependencyPolicy.verificationGates is required")
    require(
        any(gate in verification_gates for gate in ("make generated-version-compat-check", "make generated-upgrade-dry-run-check")),
        f"profile {name}: dependencyPolicy.verificationGates must include a generated compatibility gate",
    )
    require(
        any(gate in verification_gates for gate in ("make root-dependency-policy-check", "make dependency-upgrade-evidence-check")),
        f"profile {name}: dependencyPolicy.verificationGates must include a dependency boundary gate",
    )
    for gate in verification_gates:
        require(gate.startswith("make "), f"profile {name}: dependencyPolicy gate must be a make target: {gate!r}")
    require(
        len(str(dependency_policy.get("rollbackOrEscalation") or "").split()) >= 10,
        f"profile {name}: dependencyPolicy.rollbackOrEscalation must be actionable",
    )

    diff_report = profile.get("diffReport") or {}
    require(diff_report.get("repeatGeneration") == "must-pass", f"profile {name}: diffReport.repeatGeneration must be must-pass")
    require(bool(diff_report.get("summary")), f"profile {name}: diffReport.summary is required")
    require(bool(diff_report.get("rollbackNote")), f"profile {name}: diffReport.rollbackNote is required")
    diff_categories = set(diff_report.get("categories") or [])
    require(
        "deterministic-repeat-generation" in diff_categories,
        f"profile {name}: diffReport.categories must include deterministic-repeat-generation",
    )
    unknown = diff_categories - required_categories
    require(not unknown, f"profile {name}: unknown diff categories: {sorted(unknown)!r}")

matrix_rel = manifest.get("migrationFidelityMatrix")
require(matrix_rel == "docs/reference/migration-fidelity-matrix.json", "migrationFidelityMatrix must point to docs/reference/migration-fidelity-matrix.json")
matrix_path = root / matrix_rel if matrix_rel else root / "docs" / "reference" / "migration-fidelity-matrix.json"
if matrix_path.is_file():
    matrix = json.loads(matrix_path.read_text(encoding="utf-8"))
else:
    matrix = {}
    missing.append("docs/reference/migration-fidelity-matrix.json is missing")

require(
    matrix.get("schema") == "gofly.generated_migration_fidelity.v1",
    "migration fidelity matrix schema must be gofly.generated_migration_fidelity.v1",
)
require(
    matrix.get("sourceOfTruth") == "docs/reference/generated-upgrade-dry-run.json",
    "migration fidelity matrix sourceOfTruth must be docs/reference/generated-upgrade-dry-run.json",
)
require(
    matrix.get("acceptanceGate") == "make generated-upgrade-dry-run-check",
    "migration fidelity matrix acceptanceGate must be make generated-upgrade-dry-run-check",
)
required_path_fields = set(matrix.get("requiredPathFields") or [])
for field in (
    "framework",
    "example",
    "docs",
    "dryRunProfile",
    "deterministicRegeneration",
    "diffCategories",
    "smokeGates",
    "rollbackNote",
    "compatibilityCaveat",
):
    require(field in required_path_fields, f"migration fidelity requiredPathFields missing {field!r}")

paths = matrix.get("paths") or []
frameworks = {item.get("framework") for item in paths if isinstance(item, dict)}
require(frameworks == {"gin", "go-zero", "kratos", "kitex"}, f"migration fidelity frameworks mismatch: {sorted(frameworks)!r}")
for item in paths:
    if not isinstance(item, dict):
        missing.append(f"migration fidelity path must be an object: {item!r}")
        continue
    framework = item.get("framework", "<missing>")
    for field in required_path_fields:
        require(field in item and item[field] not in ("", None, [], {}), f"migration fidelity {framework}: {field} is required")
    example = item.get("example")
    if example:
        require((root / example).is_dir(), f"migration fidelity {framework}: example path is missing: {example}")
    for doc_path in item.get("docs") or []:
        require((root / doc_path).is_file(), f"migration fidelity {framework}: doc path is missing: {doc_path}")
    require(item.get("dryRunProfile") in profile_names, f"migration fidelity {framework}: dryRunProfile must reference generated upgrade profile")
    require(item.get("deterministicRegeneration") == "required", f"migration fidelity {framework}: deterministicRegeneration must be required")
    diff_categories = set(item.get("diffCategories") or [])
    require("deterministic-repeat-generation" in diff_categories, f"migration fidelity {framework}: deterministic diff category is required")
    unknown = diff_categories - required_categories
    require(not unknown, f"migration fidelity {framework}: unknown diff categories: {sorted(unknown)!r}")
    require(any("make " in gate or gate.startswith("go test ") or gate.startswith("go run ") for gate in item.get("smokeGates") or []), f"migration fidelity {framework}: smokeGates must include runnable commands")
    require(len(item.get("rollbackNote", "")) >= 24, f"migration fidelity {framework}: rollbackNote is too short")
    require(len(item.get("compatibilityCaveat", "")) >= 24, f"migration fidelity {framework}: compatibilityCaveat is too short")

makefile = read_text(root / "Makefile")
rehearsal = manifest.get("upgradeRehearsal") or {}
require(rehearsal.get("schema") == "gofly.upgrade_rehearsal.v1", "upgrade rehearsal schema must be gofly.upgrade_rehearsal.v1")
require(rehearsal.get("source") == "docs/reference/generated-upgrade-dry-run.json", "upgrade rehearsal source mismatch")
require(rehearsal.get("acceptanceGate") == "make generated-upgrade-dry-run-check", "upgrade rehearsal acceptanceGate mismatch")
steps = rehearsal.get("steps") or []
required_step_ids = {
    "inventory-current-project",
    "regenerate-dry-run",
    "goctl-compatibility-review",
    "dependency-boundary-review",
    "release-evidence-review",
    "adopter-smoke-and-rollback",
}
actual_step_ids = {item.get("id") for item in steps if isinstance(item, dict)}
require(actual_step_ids == required_step_ids, f"upgrade rehearsal steps mismatch: {sorted(actual_step_ids)!r}")
seen_phases = set()
for item in steps:
    if not isinstance(item, dict):
        missing.append(f"upgrade rehearsal step must be an object: {item!r}")
        continue
    step = item.get("id", "<missing>")
    for field in ("id", "phase", "evidence", "gate", "expectedOutput", "failureClass", "rollbackOrEscalation"):
        require(bool(item.get(field)), f"upgrade rehearsal {step}: {field} is required")
    seen_phases.add(item.get("phase", ""))
    gate = item.get("gate", "")
    require(gate.startswith("make "), f"upgrade rehearsal {step}: gate must be a make target")
    if gate.startswith("make "):
        target = gate.removeprefix("make ").split()[0]
        require(re.search(rf"^{re.escape(target)}:", makefile, re.M), f"upgrade rehearsal {step}: gate target {target!r} missing")
    for evidence in item.get("evidence") or []:
        require((root / evidence).exists(), f"upgrade rehearsal {step}: evidence path is missing: {evidence}")
    require(item.get("failureClass") in {"candidate", "rollback-required"}, f"upgrade rehearsal {step}: failureClass must be candidate or rollback-required")
    for field in ("expectedOutput", "rollbackOrEscalation"):
        require(len(str(item.get(field) or "").split()) >= 8, f"upgrade rehearsal {step}: {field} must be actionable")
require({"baseline", "generation", "dependency", "release", "verification"} <= seen_phases, "upgrade rehearsal must cover baseline, generation, dependency, release, and verification phases")

matrix_profiles = {
    item.get("profile"): item
    for item in (json.loads((root / "testdata/generated-compat/matrix.json").read_text(encoding="utf-8")).get("profiles") or [])
    if isinstance(item, dict) and item.get("profile")
}
adopter_proof = manifest.get("adopterUpgradeProof") or {}
require(
    adopter_proof.get("schema") == "gofly.generated_adopter_upgrade_proof.v1",
    "adopterUpgradeProof schema must be gofly.generated_adopter_upgrade_proof.v1",
)
require(
    adopter_proof.get("source") == "docs/reference/generated-upgrade-dry-run.json",
    "adopterUpgradeProof source mismatch",
)
require(
    adopter_proof.get("compatibilityMatrix") == "testdata/generated-compat/matrix.json",
    "adopterUpgradeProof compatibilityMatrix mismatch",
)
require(
    set(adopter_proof.get("acceptanceGates") or []) == {
        "make generated-upgrade-dry-run-check",
        "make generated-version-compat-check",
    },
    "adopterUpgradeProof acceptanceGates mismatch",
)
require(
    adopter_proof.get("dashboardReportField") == "generatedUpgradeDryRun.adopterUpgradeProof",
    "adopterUpgradeProof dashboardReportField mismatch",
)
require(
    len(str(adopter_proof.get("policy") or "").split()) >= 20,
    "adopterUpgradeProof policy must be actionable",
)
proof_paths = adopter_proof.get("paths") or []
proof_by_profile = {
    item.get("profile"): item
    for item in proof_paths
    if isinstance(item, dict) and item.get("profile")
}
require(set(proof_by_profile) == profile_names, f"adopterUpgradeProof profiles mismatch: {sorted(proof_by_profile)!r}")
for profile_name, proof in sorted(proof_by_profile.items()):
    matrix_profile = matrix_profiles.get(profile_name) or {}
    manifest_profile = next((item for item in profiles if item.get("profile") == profile_name), {})
    for field in (
        "profile",
        "adopterDecision",
        "compatibilityGate",
        "dryRunGate",
        "expectedDiff",
        "dependencyBoundary",
        "rollbackAction",
    ):
        require(proof.get(field), f"adopterUpgradeProof {profile_name}: {field} is required")
    require(
        proof.get("compatibilityGate") == "make generated-version-compat-check",
        f"adopterUpgradeProof {profile_name}: compatibilityGate mismatch",
    )
    require(
        proof.get("dryRunGate") == "make generated-upgrade-dry-run-check",
        f"adopterUpgradeProof {profile_name}: dryRunGate mismatch",
    )
    require(
        proof.get("expectedDiff") == matrix_profile.get("expectedDiff"),
        f"adopterUpgradeProof {profile_name}: expectedDiff must match generated compatibility matrix",
    )
    require(
        proof.get("dependencyBoundary") == (manifest_profile.get("dependencyPolicy") or {}).get("allowedLocation"),
        f"adopterUpgradeProof {profile_name}: dependencyBoundary must match profile dependency policy",
    )
    for field in ("adopterDecision", "rollbackAction"):
        require(len(str(proof.get(field) or "").split()) >= 10, f"adopterUpgradeProof {profile_name}: {field} must be actionable")

target_body = make_target_body(makefile, "generated-upgrade-dry-run-check")
contract_deps = make_target_deps(makefile, "contract-docs-check")
require(
    "check-generated-upgrade-dry-run.sh" in target_body,
    "generated-upgrade-dry-run-check must call check-generated-upgrade-dry-run.sh",
)
require(
    "generated-upgrade-dry-run-check" in contract_deps,
    "contract-docs-check must depend on generated-upgrade-dry-run-check",
)

doc = read_text(root / "docs" / "reference" / "generated-upgrade-dry-run.md")
for needle in (
    "gofly.generated_upgrade_dry_run.v1",
    "make generated-upgrade-dry-run-check",
    "deterministic-repeat-generation",
    "compatible-addition",
    "formatting-only",
    "breaking-candidate",
    "rollbackNote",
):
    require(needle in doc, f"docs/reference/generated-upgrade-dry-run.md missing {needle!r}")

if missing:
    print("generated upgrade dry-run check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("generated upgrade dry-run governance ok")
PY
