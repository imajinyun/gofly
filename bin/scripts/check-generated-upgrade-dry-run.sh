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
scaffold_compat_path = root / "docs" / "reference" / "generated-scaffold-long-term-compatibility.json"
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
if scaffold_compat_path.is_file():
    scaffold_compat = json.loads(scaffold_compat_path.read_text(encoding="utf-8"))
else:
    scaffold_compat = {}
    missing.append("docs/reference/generated-scaffold-long-term-compatibility.json is missing")

require(
    manifest.get("schema") == "gofly.generated_upgrade_dry_run.v1",
    "generated upgrade dry-run schema must be gofly.generated_upgrade_dry_run.v1",
)
require(
    scaffold_compat.get("schema") == "gofly.generated_scaffold_long_term_compatibility.v1",
    "generated scaffold long-term compatibility schema mismatch",
)
require(scaffold_compat.get("status") == "blocking", "generated scaffold long-term compatibility status must be blocking")
require(
    scaffold_compat.get("blockingGate") == "make generated-upgrade-dry-run-check",
    "generated scaffold long-term compatibility blockingGate mismatch",
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

source_of_truth = set(scaffold_compat.get("sourceOfTruth") or [])
for source in (
    "docs/reference/generated-upgrade-dry-run.json",
    "docs/reference/generated-version-compat.md",
    "testdata/generated-compat/matrix.json",
    "docs/reference/goctl-generator-compatibility.json",
    "docs/reference/migration-fidelity-matrix.json",
):
    require(source in source_of_truth, f"generated scaffold compatibility sourceOfTruth missing {source!r}")
    require((root / source).exists(), f"generated scaffold compatibility sourceOfTruth path missing: {source}")
require(
    set(scaffold_compat.get("referenceFrameworks") or []) == {"go-zero", "Kratos"},
    "generated scaffold compatibility referenceFrameworks mismatch",
)
require(
    set(scaffold_compat.get("acceptanceGates") or []) == {
        "make generated-upgrade-dry-run-check",
        "make generated-version-compat-check",
        "make goctl-generator-compat-check",
        "make test-generated-matrix",
    },
    "generated scaffold compatibility acceptanceGates mismatch",
)
compat_policy = scaffold_compat.get("compatibilityPolicy") or {}
require(set(compat_policy.get("versionProfiles") or []) == profile_names, "generated scaffold compatibility versionProfiles mismatch")
require(compat_policy.get("repeatGeneration") == "must-pass", "generated scaffold compatibility repeatGeneration mismatch")
require(compat_policy.get("generatedProjectCompileSmoke") == "required", "generated scaffold compatibility compile smoke must be required")
require(compat_policy.get("diffClassification") == "required", "generated scaffold compatibility diffClassification must be required")
require(
    compat_policy.get("dependencyBoundary") == "generated project go.mod or isolated temporary test module",
    "generated scaffold compatibility dependencyBoundary mismatch",
)
require(
    compat_policy.get("rootModulePolicy") == "must-not-add-generated-only-dependencies",
    "generated scaffold compatibility rootModulePolicy mismatch",
)
require(
    len(str(compat_policy.get("rollbackPolicy") or "").split()) >= 16,
    "generated scaffold compatibility rollbackPolicy must be actionable",
)

r8_profile_matrix = scaffold_compat.get("r8ProfileMatrix") or {}
require(r8_profile_matrix.get("aiflowTask") == "GOFLY-GOV-10R8-03", "generated scaffold R8 profile matrix aiflowTask mismatch")
require(
    r8_profile_matrix.get("acceptanceGate") == "make generated-upgrade-dry-run-check",
    "generated scaffold R8 profile matrix acceptanceGate mismatch",
)
r8_profiles = r8_profile_matrix.get("profiles") or []
required_r8_profile_ids = {"rest-service", "rpc-service", "model-service", "production-service"}
actual_r8_profile_ids = {item.get("id") for item in r8_profiles if isinstance(item, dict)}
require(actual_r8_profile_ids == required_r8_profile_ids, f"generated scaffold R8 profile ids mismatch: {sorted(actual_r8_profile_ids)!r}")
for item in r8_profiles:
    if not isinstance(item, dict):
        missing.append(f"generated scaffold R8 profile entry must be an object: {item!r}")
        continue
    profile_id = item.get("id", "<missing>")
    for field in (
        "id",
        "generatorSurface",
        "referenceFrameworks",
        "fixtures",
        "generatedOnlyDependencyPolicy",
        "repeatDiffPolicy",
        "compileSmoke",
        "rollbackOrEscalation",
    ):
        require(item.get(field) not in ("", None, []), f"generated scaffold R8 profile {profile_id}: {field} is required")
    frameworks = set(item.get("referenceFrameworks") or [])
    require(frameworks <= {"go-zero", "Kratos"} and frameworks, f"generated scaffold R8 profile {profile_id}: referenceFrameworks mismatch")
    for fixture in item.get("fixtures") or []:
        require((root / fixture).exists(), f"generated scaffold R8 profile {profile_id}: fixture path missing: {fixture}")
    gate = item.get("compileSmoke", "")
    require(gate.startswith("make "), f"generated scaffold R8 profile {profile_id}: compileSmoke must be a make target")
    target = gate.removeprefix("make ").split()[0]
    makefile = read_text(root / "Makefile")
    require(re.search(rf"^{re.escape(target)}:", makefile, re.M), f"generated scaffold R8 profile {profile_id}: compileSmoke target {target!r} missing")
    for field in ("generatedOnlyDependencyPolicy", "repeatDiffPolicy", "rollbackOrEscalation"):
        require(len(str(item.get(field) or "").split()) >= 10, f"generated scaffold R8 profile {profile_id}: {field} must be actionable")

r8_cross_checks = r8_profile_matrix.get("crossCuttingChecks") or []
required_r8_cross_check_ids = {"import-aliases", "generated-only-dependencies", "repeat-generation-diff"}
actual_r8_cross_check_ids = {item.get("id") for item in r8_cross_checks if isinstance(item, dict)}
require(actual_r8_cross_check_ids == required_r8_cross_check_ids, f"generated scaffold R8 cross checks mismatch: {sorted(actual_r8_cross_check_ids)!r}")
for item in r8_cross_checks:
    if not isinstance(item, dict):
        missing.append(f"generated scaffold R8 cross check entry must be an object: {item!r}")
        continue
    check_id = item.get("id", "<missing>")
    for field in ("id", "evidence", "gate", "rollbackOrEscalation"):
        require(item.get(field) not in ("", None, []), f"generated scaffold R8 cross check {check_id}: {field} is required")
    gate = item.get("gate", "")
    require(gate.startswith("make "), f"generated scaffold R8 cross check {check_id}: gate must be a make target")
    target = gate.removeprefix("make ").split()[0]
    makefile = read_text(root / "Makefile")
    require(re.search(rf"^{re.escape(target)}:", makefile, re.M), f"generated scaffold R8 cross check {check_id}: gate target {target!r} missing")
    for evidence in item.get("evidence") or []:
        require((root / evidence).exists(), f"generated scaffold R8 cross check {check_id}: evidence path missing: {evidence}")
    require(len(str(item.get("rollbackOrEscalation") or "").split()) >= 10, f"generated scaffold R8 cross check {check_id}: rollbackOrEscalation must be actionable")

matrix_profiles_raw = json.loads((root / "testdata/generated-compat/matrix.json").read_text(encoding="utf-8")).get("profiles") or []
matrix_by_profile = {
    item.get("profile"): item
    for item in matrix_profiles_raw
    if isinstance(item, dict) and item.get("profile")
}
scaffold_profiles = scaffold_compat.get("profileMatrix") or []
scaffold_by_profile = {
    item.get("profile"): item
    for item in scaffold_profiles
    if isinstance(item, dict) and item.get("profile")
}
require(set(scaffold_by_profile) == profile_names, f"generated scaffold compatibility profileMatrix mismatch: {sorted(scaffold_by_profile)!r}")
for profile_name, item in sorted(scaffold_by_profile.items()):
    matrix_profile = matrix_by_profile.get(profile_name) or {}
    manifest_profile = next((profile for profile in profiles if profile.get("profile") == profile_name), {})
    require(item.get("expectedDiff") == matrix_profile.get("expectedDiff"), f"generated scaffold compatibility {profile_name}: expectedDiff must match version matrix")
    fixtures = item.get("fixtures") or {}
    for field in ("api", "proto", "serviceConfig"):
        require(fixtures.get(field) == manifest_profile.get(field), f"generated scaffold compatibility {profile_name}: fixture {field} must match generated upgrade profile")
        require((root / str(fixtures.get(field) or "")).is_file(), f"generated scaffold compatibility {profile_name}: fixture path missing for {field}")
    categories = set(item.get("requiredDiffCategories") or [])
    manifest_categories = set((manifest_profile.get("diffReport") or {}).get("categories") or [])
    require("deterministic-repeat-generation" in categories, f"generated scaffold compatibility {profile_name}: deterministic diff category is required")
    require(categories <= required_categories, f"generated scaffold compatibility {profile_name}: unknown diff categories {sorted(categories - required_categories)!r}")
    require(categories <= manifest_categories or profile_name == "future", f"generated scaffold compatibility {profile_name}: categories must be covered by generated upgrade profile")
    require(item.get("smokeGate", "").startswith("make "), f"generated scaffold compatibility {profile_name}: smokeGate must be a make target")
    require(bool(item.get("frameworkAlignment")), f"generated scaffold compatibility {profile_name}: frameworkAlignment is required")
    require(len(str(item.get("rollbackOrEscalation") or "").split()) >= 10, f"generated scaffold compatibility {profile_name}: rollbackOrEscalation must be actionable")

edge_cases = scaffold_compat.get("edgeCases") or []
required_edge_cases = {
    "goctl-compatible-profile",
    "route-layout-boundary",
    "api-import-and-diff-format",
    "api-import-compatibility",
    "proto-import-compatibility",
    "alias-collision-boundary",
    "generated-dependency-boundary",
    "repeat-generation-diff-boundary",
}
actual_edge_cases = {item.get("id") for item in edge_cases if isinstance(item, dict)}
require(actual_edge_cases == required_edge_cases, f"generated scaffold compatibility edgeCases mismatch: {sorted(actual_edge_cases)!r}")
for item in edge_cases:
    if not isinstance(item, dict):
        missing.append(f"generated scaffold compatibility edge case must be an object: {item!r}")
        continue
    edge_id = item.get("id", "<missing>")
    for field in ("id", "surface", "evidence", "gate", "rollbackOrEscalation"):
        require(item.get(field) not in ("", None, []), f"generated scaffold compatibility edge {edge_id}: {field} is required")
    gate = item.get("gate", "")
    require(gate.startswith("make "), f"generated scaffold compatibility edge {edge_id}: gate must be a make target")
    for evidence in item.get("evidence") or []:
        require((root / evidence).exists(), f"generated scaffold compatibility edge {edge_id}: evidence path missing: {evidence}")
    require(len(str(item.get("rollbackOrEscalation") or "").split()) >= 10, f"generated scaffold compatibility edge {edge_id}: rollbackOrEscalation must be actionable")

edge_surface_expectations = {
    "api-import-compatibility": "api-imports",
    "proto-import-compatibility": "proto-imports",
    "alias-collision-boundary": "import-aliases",
    "generated-dependency-boundary": "dependencies",
    "repeat-generation-diff-boundary": "repeat-diff",
}
for edge_id, surface in edge_surface_expectations.items():
    edge = next((item for item in edge_cases if isinstance(item, dict) and item.get("id") == edge_id), {})
    require(edge.get("surface") == surface, f"generated scaffold compatibility edge {edge_id}: surface must be {surface!r}")
    rollback_text = str(edge.get("rollbackOrEscalation") or "").lower()
    require("rollback" in rollback_text or "pin" in rollback_text or "discard" in rollback_text, f"generated scaffold compatibility edge {edge_id}: rollbackOrEscalation must name rollback, pin, or discard")

adopter_actions = scaffold_compat.get("adopterActions") or []
required_actions = {
    "temporary-project-generation",
    "upgrade-diff-review",
    "goctl-compatibility-review",
    "importer-compatibility-review",
}
actual_actions = {item.get("id") for item in adopter_actions if isinstance(item, dict)}
require(actual_actions == required_actions, f"generated scaffold compatibility adopterActions mismatch: {sorted(actual_actions)!r}")
for item in adopter_actions:
    if not isinstance(item, dict):
        missing.append(f"generated scaffold compatibility adopter action must be an object: {item!r}")
        continue
    action_id = item.get("id", "<missing>")
    for field in ("id", "command", "expectedEvidence", "rollbackOrEscalation"):
        require(item.get(field) not in ("", None, []), f"generated scaffold compatibility action {action_id}: {field} is required")
    command = item.get("command", "")
    require(command.startswith("make "), f"generated scaffold compatibility action {action_id}: command must be a make target")
    target = command.removeprefix("make ").split()[0]
    makefile = read_text(root / "Makefile")
    require(re.search(rf"^{re.escape(target)}:", makefile, re.M), f"generated scaffold compatibility action {action_id}: command target {target!r} missing")
    for field in ("expectedEvidence", "rollbackOrEscalation"):
        require(len(str(item.get(field) or "").split()) >= 10, f"generated scaffold compatibility action {action_id}: {field} must be actionable")

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

p9_matrix = manifest.get("p9HistoricalFixtureMatrix") or {}
require(
    p9_matrix.get("schema") == "gofly.generated_historical_fixture_matrix.v1",
    "p9HistoricalFixtureMatrix schema mismatch",
)
require(
    p9_matrix.get("aiflowTask") == "GOFLY-GOV-10P9-02",
    "p9HistoricalFixtureMatrix aiflowTask mismatch",
)
require(p9_matrix.get("status") == "blocking", "p9HistoricalFixtureMatrix status must be blocking")
require(
    set(p9_matrix.get("sourceOfTruth") or []) == {
        "testdata/generated-compat/matrix.json",
        "docs/reference/generated-version-compat.md",
        "docs/reference/generated-upgrade-dry-run.json",
    },
    "p9HistoricalFixtureMatrix sourceOfTruth mismatch",
)
for source in p9_matrix.get("sourceOfTruth") or []:
    require((root / source).exists(), f"p9HistoricalFixtureMatrix source path missing: {source}")
require(
    set(p9_matrix.get("acceptanceGates") or []) == {
        "make generated-version-compat-check",
        "make generated-upgrade-dry-run-check",
    },
    "p9HistoricalFixtureMatrix acceptanceGates mismatch",
)
execution_contract = p9_matrix.get("executionContract") or {}
for field in (
    "temporaryProjectRoot",
    "generatorCommand",
    "moduleReplacement",
    "compileSmoke",
    "repeatGeneration",
    "reportSchema",
    "artifactPolicy",
):
    require(
        len(str(execution_contract.get(field) or "").split()) >= 3,
        f"p9HistoricalFixtureMatrix executionContract.{field} must be actionable",
    )
require("gofly.generated_version_compat_report.v1" in str(execution_contract.get("reportSchema") or ""), "p9HistoricalFixtureMatrix reportSchema mismatch")
require("diff -ru" in str(execution_contract.get("repeatGeneration") or ""), "p9HistoricalFixtureMatrix repeatGeneration must require diff -ru")
require("go test ./..." in str(execution_contract.get("compileSmoke") or ""), "p9HistoricalFixtureMatrix compileSmoke must run go test ./...")

p9_profiles = {
    item.get("profile"): item
    for item in p9_matrix.get("profiles") or []
    if isinstance(item, dict) and item.get("profile")
}
require(set(p9_profiles) == profile_names, f"p9HistoricalFixtureMatrix profiles mismatch: {sorted(p9_profiles)!r}")
for profile_name, item in sorted(p9_profiles.items()):
    matrix_profile = matrix_profiles.get(profile_name) or {}
    manifest_profile = next((profile for profile in profiles if profile.get("profile") == profile_name), {})
    fixture_set = item.get("fixtureSet") or {}
    for field in ("api", "proto", "serviceConfig"):
        require(fixture_set.get(field) == manifest_profile.get(field), f"p9HistoricalFixtureMatrix {profile_name}: fixture {field} must match generated upgrade profile")
        require((root / str(fixture_set.get(field) or "")).is_file(), f"p9HistoricalFixtureMatrix {profile_name}: fixture path missing for {field}")
    require(
        fixture_set.get("snapshot") == "testdata/generated-compat/matrix.json",
        f"p9HistoricalFixtureMatrix {profile_name}: snapshot must be the generated compatibility matrix",
    )
    require(
        item.get("expectedDiff") == matrix_profile.get("expectedDiff"),
        f"p9HistoricalFixtureMatrix {profile_name}: expectedDiff must match version matrix",
    )
    required_report_fields = set(item.get("requiredReportFields") or [])
    for field in ("profile", "generatedFiles", "goTest", "repeatGenerationDiff", "expectedDiff", "verification"):
        require(field in required_report_fields, f"p9HistoricalFixtureMatrix {profile_name}: requiredReportFields missing {field!r}")
    blocking_checks = set(item.get("blockingChecks") or [])
    for check in ("generatedFiles > 0", "goTest == passed", "repeatGenerationDiff == clean"):
        require(check in blocking_checks, f"p9HistoricalFixtureMatrix {profile_name}: blockingChecks missing {check!r}")
    require(
        len(str(item.get("rollbackOrEscalation") or "").split()) >= 12,
        f"p9HistoricalFixtureMatrix {profile_name}: rollbackOrEscalation must be actionable",
    )

p9_diff_policy = p9_matrix.get("diffExplanationPolicy") or {}
require(
    set(p9_diff_policy.get("requiredCategories") or []) == required_categories,
    "p9HistoricalFixtureMatrix diffExplanationPolicy.requiredCategories mismatch",
)
for field in ("blockingRule", "reportUse"):
    require(
        len(str(p9_diff_policy.get(field) or "").split()) >= 12,
        f"p9HistoricalFixtureMatrix diffExplanationPolicy.{field} must be actionable",
    )
require(
    "gofly.generated_version_compat_report.v1" in str(p9_diff_policy.get("reportUse") or ""),
    "p9HistoricalFixtureMatrix diffExplanationPolicy.reportUse must reference the generated version report schema",
)

p10_fidelity = manifest.get("p10GoctlGeneratorFidelity") or {}
require(
    p10_fidelity.get("schema") == "gofly.goctl_generator_fidelity_closeout.v1",
    "p10GoctlGeneratorFidelity schema mismatch",
)
require(
    p10_fidelity.get("aiflowTask") == "GOFLY-P10-2-GOCTL-GENERATOR-FIDELITY",
    "p10GoctlGeneratorFidelity aiflowTask mismatch",
)
require(p10_fidelity.get("status") == "blocking-contract", "p10GoctlGeneratorFidelity status must be blocking-contract")
require(
    p10_fidelity.get("acceptanceGate") == "make generated-upgrade-dry-run-check",
    "p10GoctlGeneratorFidelity acceptanceGate mismatch",
)
for source in (
    "docs/reference/goctl-generator-compatibility.json",
    "docs/reference/generated-scaffold-long-term-compatibility.json",
    "testdata/generated-compat/matrix.json",
):
    require(source in set(p10_fidelity.get("sourceOfTruth") or []), f"p10GoctlGeneratorFidelity sourceOfTruth missing {source!r}")
    require((root / source).exists(), f"p10GoctlGeneratorFidelity source path missing: {source}")
p10_rows = {
    item.get("id"): item
    for item in p10_fidelity.get("fidelityRows") or []
    if isinstance(item, dict) and item.get("id")
}
expected_p10_rows = {
    "gozero-compatible-profile": {"ProfileGoZeroCompatible", "goctl-layout", "route-layout-boundary"},
    "goctl-compatible-flags": {"name-from-filename", "go_opt", "go-grpc_opt", "go_grpc_opt"},
    "api-import-diff-route": {"api import", "api route", "api diff", "api-import-compatibility"},
    "proto-import-compatibility": {"proto-import-compatibility", "protoc plugin", "generated service descriptors"},
    "alias-collision-boundary": {"alias-collision-boundary", "Go package aliases", "generated symbol names"},
    "repeat-diff-rollback": {"deterministic-repeat-generation", "diffReportContract", "rollbackNote"},
}
require(set(p10_rows) == set(expected_p10_rows), f"p10GoctlGeneratorFidelity rows mismatch: {sorted(p10_rows)!r}")
for row_id, expected_evidence in expected_p10_rows.items():
    row = p10_rows.get(row_id) or {}
    for field in ("id", "surface", "evidence", "gate", "rollbackOrEscalation"):
        require(row.get(field), f"p10GoctlGeneratorFidelity {row_id}: {field} is required")
    evidence = set(row.get("evidence") or [])
    require(expected_evidence <= evidence, f"p10GoctlGeneratorFidelity {row_id}: evidence missing {sorted(expected_evidence - evidence)!r}")
    gate = str(row.get("gate") or "")
    require(gate.startswith("make "), f"p10GoctlGeneratorFidelity {row_id}: gate must be a make target")
    if gate.startswith("make "):
        target = gate.removeprefix("make ").split()[0]
        require(re.search(rf"^{re.escape(target)}:", makefile, re.M), f"p10GoctlGeneratorFidelity {row_id}: gate target {target!r} missing")
    require(len(str(row.get("rollbackOrEscalation") or "").split()) >= 10, f"p10GoctlGeneratorFidelity {row_id}: rollbackOrEscalation must be actionable")
promotion_policy = str(p10_fidelity.get("promotionPolicy") or "")
for needle in ("goctl-compatible profile", "goctl-style flags", "API import", "proto import", "alias-collision", "rollback notes"):
    require(needle in promotion_policy, f"p10GoctlGeneratorFidelity promotionPolicy missing {needle!r}")
runtime_policy = str(p10_fidelity.get("runtimeArtifactPolicy") or "")
for needle in (".tmp-test", "temporary directories", "must not be committed"):
    require(needle in runtime_policy, f"p10GoctlGeneratorFidelity runtimeArtifactPolicy missing {needle!r}")

p11_proof = manifest.get("p11LiveUpgradeProof") or {}
require(
    p11_proof.get("schema") == "gofly.generated_live_upgrade_proof.v1",
    "p11LiveUpgradeProof schema mismatch",
)
require(
    p11_proof.get("aiflowTask") == "GOFLY-P11-2-GENERATED-PROJECT-LIVE-UPGRADE",
    "p11LiveUpgradeProof aiflowTask mismatch",
)
require(p11_proof.get("status") == "blocking-contract", "p11LiveUpgradeProof status must be blocking-contract")
require(
    set(p11_proof.get("sourceOfTruth") or []) == {
        "testdata/generated-compat/matrix.json",
        "docs/reference/generated-upgrade-dry-run.json",
        "docs/reference/generated-version-compat.md",
        "docs/reference/generated-scaffold-long-term-compatibility.json",
    },
    "p11LiveUpgradeProof sourceOfTruth mismatch",
)
for source in p11_proof.get("sourceOfTruth") or []:
    require((root / source).exists(), f"p11LiveUpgradeProof source path missing: {source}")
require(
    set(p11_proof.get("acceptanceGates") or []) == {
        "make generated-upgrade-dry-run-check",
        "make generated-version-compat-check",
    },
    "p11LiveUpgradeProof acceptanceGates mismatch",
)
p11_execution = p11_proof.get("executionContract") or {}
for field in (
    "temporaryProjectRoot",
    "generatorCommand",
    "moduleReplacement",
    "compileSmoke",
    "repeatGeneration",
    "reportSchema",
    "artifactPolicy",
):
    require(
        len(str(p11_execution.get(field) or "").split()) >= 5,
        f"p11LiveUpgradeProof executionContract.{field} must be actionable",
    )
require("go run ./cmd/gofly new service" in str(p11_execution.get("generatorCommand") or ""), "p11LiveUpgradeProof generatorCommand must run gofly new service")
require("go mod edit -replace" in str(p11_execution.get("moduleReplacement") or ""), "p11LiveUpgradeProof moduleReplacement must use a generated-project replace")
require("go test ./..." in str(p11_execution.get("compileSmoke") or ""), "p11LiveUpgradeProof compileSmoke must run go test ./...")
require("gofly.generated_version_compat_report.v1" in str(p11_execution.get("reportSchema") or ""), "p11LiveUpgradeProof reportSchema mismatch")
for forbidden in ("must not be committed", "runtime evidence"):
    require(forbidden in str(p11_execution.get("artifactPolicy") or ""), f"p11LiveUpgradeProof artifactPolicy missing {forbidden!r}")

p11_profiles = {
    item.get("profile"): item
    for item in p11_proof.get("profiles") or []
    if isinstance(item, dict) and item.get("profile")
}
require(set(p11_profiles) == profile_names, f"p11LiveUpgradeProof profiles mismatch: {sorted(p11_profiles)!r}")
for profile_name, item in sorted(p11_profiles.items()):
    matrix_profile = matrix_profiles.get(profile_name) or {}
    manifest_profile = next((profile for profile in profiles if profile.get("profile") == profile_name), {})
    fixture_set = item.get("fixtureSet") or {}
    for field in ("api", "proto", "serviceConfig"):
        require(fixture_set.get(field) == manifest_profile.get(field), f"p11LiveUpgradeProof {profile_name}: fixture {field} must match generated upgrade profile")
        require((root / str(fixture_set.get(field) or "")).is_file(), f"p11LiveUpgradeProof {profile_name}: fixture path missing for {field}")
    require(
        fixture_set.get("snapshot") == "testdata/generated-compat/matrix.json",
        f"p11LiveUpgradeProof {profile_name}: snapshot must be the generated compatibility matrix",
    )
    require(item.get("expectedDiff") == matrix_profile.get("expectedDiff"), f"p11LiveUpgradeProof {profile_name}: expectedDiff must match version matrix")
    require(item.get("generatedProjectSmoke") == "go test ./...", f"p11LiveUpgradeProof {profile_name}: generatedProjectSmoke mismatch")
    require(item.get("repeatGeneration") == "clean", f"p11LiveUpgradeProof {profile_name}: repeatGeneration must be clean")
    dependency_policy = manifest_profile.get("dependencyPolicy") or {}
    require(
        item.get("dependencyBoundary") == dependency_policy.get("allowedLocation"),
        f"p11LiveUpgradeProof {profile_name}: dependencyBoundary must match profile dependency policy",
    )
    require(
        item.get("rootModulePolicy") == "must-not-add-generated-only-dependencies",
        f"p11LiveUpgradeProof {profile_name}: rootModulePolicy mismatch",
    )
    require(
        set(item.get("diffCategories") or []) <= required_categories,
        f"p11LiveUpgradeProof {profile_name}: diffCategories contain unsupported values",
    )
    require(
        "deterministic-repeat-generation" in set(item.get("diffCategories") or []),
        f"p11LiveUpgradeProof {profile_name}: deterministic-repeat-generation category is required",
    )
    for gate in ("make generated-version-compat-check", "make generated-upgrade-dry-run-check"):
        require(gate in set(item.get("verificationGates") or []), f"p11LiveUpgradeProof {profile_name}: verificationGates missing {gate!r}")
    for field in ("upgradePath", "rollbackAction"):
        require(len(str(item.get(field) or "").split()) >= 12, f"p11LiveUpgradeProof {profile_name}: {field} must be actionable")

p11_promotion_policy = str(p11_proof.get("promotionPolicy") or "")
for needle in ("old", "current", "future", "temporary projects", "go test ./...", "generated-only dependencies", "rollback actions"):
    require(needle in p11_promotion_policy, f"p11LiveUpgradeProof promotionPolicy missing {needle!r}")
p11_runtime_policy = str(p11_proof.get("runtimeArtifactPolicy") or "")
for needle in ("runtime evidence", "ignored temporary paths", "must never be committed"):
    require(needle in p11_runtime_policy, f"p11LiveUpgradeProof runtimeArtifactPolicy missing {needle!r}")

p12_replay = manifest.get("p12RealBranchReplay") or {}
require(
    p12_replay.get("schema") == "gofly.generated_real_branch_replay.v1",
    "p12RealBranchReplay schema mismatch",
)
require(
    p12_replay.get("aiflowTask") == "GOFLY-P12-2-GENERATED-UPGRADE-REAL-BRANCH",
    "p12RealBranchReplay aiflowTask mismatch",
)
require(p12_replay.get("status") == "blocking-contract", "p12RealBranchReplay status must be blocking-contract")
require(
    set(p12_replay.get("sourceOfTruth") or []) == {
        "docs/reference/generated-upgrade-dry-run.json",
        "docs/reference/generated-version-compat.md",
        "docs/reference/goctl-generator-compatibility.json",
        "testdata/generated-compat/matrix.json",
    },
    "p12RealBranchReplay sourceOfTruth mismatch",
)
for source in p12_replay.get("sourceOfTruth") or []:
    require((root / source).exists(), f"p12RealBranchReplay source path missing: {source}")
require(
    set(p12_replay.get("acceptanceGates") or []) == {
        "make generated-upgrade-dry-run-check",
        "make generated-version-compat-check",
        "make root-dependency-policy-check",
    },
    "p12RealBranchReplay acceptanceGates mismatch",
)
p12_branch = p12_replay.get("branchContract") or {}
for field in ("source", "worktreePolicy", "cleanupPolicy", "auditPolicy"):
    require(len(str(p12_branch.get(field) or "").split()) >= 10, f"p12RealBranchReplay branchContract.{field} must be actionable")
for needle in ("temporary worktree", "must not write", "gofly repository"):
    require(needle in str(p12_branch.get("worktreePolicy") or ""), f"p12RealBranchReplay worktreePolicy missing {needle!r}")
for needle in ("runtime evidence", "ignored"):
    require(needle in str(p12_branch.get("cleanupPolicy") or ""), f"p12RealBranchReplay cleanupPolicy missing {needle!r}")
minimum_fields = set(p12_branch.get("minimumFields") or [])
for field in (
    "repository",
    "branch",
    "baseCommit",
    "profile",
    "generatorVersion",
    "previousGeneratedSnapshot",
    "replayWorktree",
    "diffReport",
    "rollbackAction",
):
    require(field in minimum_fields, f"p12RealBranchReplay branchContract.minimumFields missing {field!r}")

p12_steps = {
    item.get("id"): item
    for item in p12_replay.get("replaySteps") or []
    if isinstance(item, dict) and item.get("id")
}
expected_p12_steps = {
    "capture-branch-baseline": {
        "phase": "baseline",
        "gate": "make generated-version-compat-check",
        "evidence": {"repository", "branch", "baseCommit", "previousGeneratedSnapshot"},
    },
    "replay-in-temp-worktree": {
        "phase": "generation",
        "gate": "make generated-upgrade-dry-run-check",
        "evidence": {
            "temporary worktree",
            "go run ./cmd/gofly new service",
            "go mod edit -replace github.com/imajinyun/gofly=<repo-root>",
        },
    },
    "classify-repeat-diff": {
        "phase": "diff",
        "gate": "make generated-upgrade-dry-run-check",
        "evidence": required_categories,
    },
    "run-branch-smoke": {
        "phase": "verification",
        "gate": "go test ./...",
        "evidence": {"go test ./...", "generated project go.mod", "root module unchanged"},
    },
}
require(set(p12_steps) == set(expected_p12_steps), f"p12RealBranchReplay replaySteps mismatch: {sorted(p12_steps)!r}")
for step_id, expected in expected_p12_steps.items():
    step = p12_steps.get(step_id) or {}
    require(step.get("phase") == expected["phase"], f"p12RealBranchReplay {step_id}: phase mismatch")
    require(step.get("gate") == expected["gate"], f"p12RealBranchReplay {step_id}: gate mismatch")
    require(expected["evidence"] <= set(step.get("requiredEvidence") or []), f"p12RealBranchReplay {step_id}: requiredEvidence missing {sorted(expected['evidence'] - set(step.get('requiredEvidence') or []))!r}")
    require(
        len(str(step.get("rollbackOrEscalation") or "").split()) >= 12,
        f"p12RealBranchReplay {step_id}: rollbackOrEscalation must be actionable",
    )

p12_profiles = {
    item.get("profile"): item
    for item in p12_replay.get("profileMapping") or []
    if isinstance(item, dict) and item.get("profile")
}
require(set(p12_profiles) == profile_names, f"p12RealBranchReplay profileMapping mismatch: {sorted(p12_profiles)!r}")
for profile_name, item in sorted(p12_profiles.items()):
    p11_profile = p11_profiles.get(profile_name) or {}
    require(
        set(item.get("acceptedDiffCategories") or []) == set(p11_profile.get("diffCategories") or []),
        f"p12RealBranchReplay {profile_name}: acceptedDiffCategories must match p11LiveUpgradeProof diffCategories",
    )
    for field in ("branchUseCase", "rollbackAction"):
        require(len(str(item.get(field) or "").split()) >= 10, f"p12RealBranchReplay {profile_name}: {field} must be actionable")

p12_promotion_policy = str(p12_replay.get("promotionPolicy") or "")
for needle in ("real branch replay", "temporary worktree", "classified repeat diff", "root dependency boundary", "rollback action"):
    require(needle in p12_promotion_policy, f"p12RealBranchReplay promotionPolicy missing {needle!r}")
p12_runtime_policy = str(p12_replay.get("runtimeArtifactPolicy") or "")
for needle in ("runtime evidence", "ignored temporary paths", "must never be committed"):
    require(needle in p12_runtime_policy, f"p12RealBranchReplay runtimeArtifactPolicy missing {needle!r}")

p13_maturity = manifest.get("p13GoctlGeneratorMaturity") or {}
require(
    p13_maturity.get("schema") == "gofly.goctl_generator_maturity_closeout.v1",
    "p13GoctlGeneratorMaturity schema mismatch",
)
require(
    p13_maturity.get("aiflowTask") == "GOFLY-P13-02-GOCTL-GENERATOR-MATURITY",
    "p13GoctlGeneratorMaturity aiflowTask mismatch",
)
require(p13_maturity.get("status") == "blocking-contract", "p13GoctlGeneratorMaturity status must be blocking-contract")
expected_p13_sources = {
    "docs/reference/generated-upgrade-dry-run.json",
    "docs/reference/generated-version-compat.md",
    "docs/reference/goctl-generator-compatibility.json",
    "docs/reference/generated-scaffold-long-term-compatibility.json",
    "testdata/generated-compat/matrix.json",
}
require(
    set(p13_maturity.get("sourceOfTruth") or []) == expected_p13_sources,
    "p13GoctlGeneratorMaturity sourceOfTruth mismatch",
)
for source in p13_maturity.get("sourceOfTruth") or []:
    require((root / source).exists(), f"p13GoctlGeneratorMaturity source path missing: {source}")
require(
    set(p13_maturity.get("acceptanceGates") or []) == {
        "make generated-version-compat-check",
        "make generated-upgrade-dry-run-check",
        "make goctl-generator-compat-check",
    },
    "p13GoctlGeneratorMaturity acceptanceGates mismatch",
)
p13_execution = p13_maturity.get("executionPolicy") or {}
for field in (
    "temporaryProjectSmoke",
    "repeatGeneration",
    "dependencyBoundary",
    "artifactPolicy",
):
    require(
        len(str(p13_execution.get(field) or "").split()) >= 4,
        f"p13GoctlGeneratorMaturity executionPolicy.{field} must be actionable",
    )
require("go test ./..." in str(p13_execution.get("temporaryProjectSmoke") or ""), "p13GoctlGeneratorMaturity temporaryProjectSmoke must run go test ./...")
require("diff -ru" in str(p13_execution.get("repeatGeneration") or ""), "p13GoctlGeneratorMaturity repeatGeneration must require diff -ru")
require(
    p13_execution.get("diffReportSchema") == "gofly.generated_version_compat_report.v1",
    "p13GoctlGeneratorMaturity diffReportSchema mismatch",
)
require(
    p13_execution.get("dependencyBoundary") == "generated project go.mod or isolated temporary test module",
    "p13GoctlGeneratorMaturity dependencyBoundary mismatch",
)
require(
    p13_execution.get("rootModulePolicy") == "must-not-add-generated-only-dependencies",
    "p13GoctlGeneratorMaturity rootModulePolicy mismatch",
)
for needle in ("runtime evidence", "must not be committed"):
    require(needle in str(p13_execution.get("artifactPolicy") or ""), f"p13GoctlGeneratorMaturity artifactPolicy missing {needle!r}")

p13_surfaces = {
    item.get("id"): item
    for item in p13_maturity.get("maturitySurfaces") or []
    if isinstance(item, dict) and item.get("id")
}
expected_p13_surfaces = {
    "api-import-format-validation",
    "historical-fixture-matrix",
    "real-adopter-branch-replay",
    "multi-language-client-contract",
    "generated-diff-classification",
    "generated-dependency-boundary",
}
require(set(p13_surfaces) == expected_p13_surfaces, f"p13GoctlGeneratorMaturity surfaces mismatch: {sorted(p13_surfaces)!r}")
for surface_id, item in sorted(p13_surfaces.items()):
    for field in ("id", "capability", "surface", "gate", "rollbackOrEscalation"):
        require(item.get(field), f"p13GoctlGeneratorMaturity {surface_id}: {field} is required")
    gate = str(item.get("gate") or "")
    require(gate.startswith("make "), f"p13GoctlGeneratorMaturity {surface_id}: gate must be a make target")
    if gate.startswith("make "):
        target = gate.removeprefix("make ").split()[0]
        require(re.search(rf"^{re.escape(target)}:", makefile, re.M), f"p13GoctlGeneratorMaturity {surface_id}: gate target {target!r} missing")
    require(
        len(str(item.get("rollbackOrEscalation") or "").split()) >= 12,
        f"p13GoctlGeneratorMaturity {surface_id}: rollbackOrEscalation must be actionable",
    )

api_surface = p13_surfaces.get("api-import-format-validation") or {}
require(api_surface.get("capability") == "api-tooling-compatibility", "p13 API surface capability mismatch")
api_capability = goctl_capabilities.get("api-tooling-compatibility") or {}
api_evidence = set(api_capability.get("evidence") or [])
required_api_evidence = set(api_surface.get("requiredEvidence") or [])
require(
    required_api_evidence <= api_evidence,
    f"p13 API surface evidence missing from goctl capability: {sorted(required_api_evidence - api_evidence)!r}",
)

fixture_surface = p13_surfaces.get("historical-fixture-matrix") or {}
require(fixture_surface.get("capability") == "generated-version-fixtures", "p13 historical fixture capability mismatch")
require(set(fixture_surface.get("profiles") or []) == profile_names, "p13 historical fixture profiles mismatch")
required_report_fields = set(fixture_surface.get("requiredReportFields") or [])
for field in ("profile", "generatedFiles", "goTest", "repeatGenerationDiff", "expectedDiff", "verification"):
    require(field in required_report_fields, f"p13 historical fixture requiredReportFields missing {field!r}")
fixture_capability = goctl_capabilities.get("generated-version-fixtures") or {}
require(fixture_capability.get("status") == "implemented", "p13 historical fixture capability must be implemented")
require(fixture_surface.get("gate") == "make generated-version-compat-check", "p13 historical fixture gate mismatch")

branch_surface = p13_surfaces.get("real-adopter-branch-replay") or {}
require(branch_surface.get("capability") == "p12RealBranchReplay", "p13 branch replay capability mismatch")
branch_evidence = set(branch_surface.get("requiredEvidence") or [])
require(
    branch_evidence == minimum_fields,
    f"p13 branch replay requiredEvidence must match p12 minimumFields: {sorted(branch_evidence ^ minimum_fields)!r}",
)
require(branch_surface.get("gate") == "make generated-upgrade-dry-run-check", "p13 branch replay gate mismatch")

client_surface = p13_surfaces.get("multi-language-client-contract") or {}
require(client_surface.get("capability") == "multi-language-client-generation", "p13 client surface capability mismatch")
client_capability = goctl_capabilities.get("multi-language-client-generation") or {}
require(client_capability.get("status") == "implemented", "p13 client capability must be implemented")
client_languages = set(client_surface.get("languages") or [])
for language in ("typescript", "javascript", "dart", "java", "kotlin"):
    require(language in client_languages, f"p13 client surface languages missing {language!r}")
    require(language in set(client_capability.get("evidence") or []), f"p13 client capability evidence missing language {language!r}")
client_evidence = set(client_capability.get("evidence") or [])
required_client_evidence = set(client_surface.get("requiredEvidence") or [])
require(
    required_client_evidence <= client_evidence,
    f"p13 client surface evidence missing from goctl capability: {sorted(required_client_evidence - client_evidence)!r}",
)

diff_surface = p13_surfaces.get("generated-diff-classification") or {}
require(diff_surface.get("capability") == "upgrade-diff-contract", "p13 diff surface capability mismatch")
require(
    set(diff_surface.get("requiredCategories") or []) == required_categories,
    "p13 diff surface requiredCategories mismatch",
)
diff_capability = goctl_capabilities.get("upgrade-diff-contract") or {}
require(diff_capability.get("status") == "implemented", "p13 diff contract capability must be implemented")

dependency_surface = p13_surfaces.get("generated-dependency-boundary") or {}
require(dependency_surface.get("capability") == "generated-project-dependencies", "p13 dependency surface capability mismatch")
dependency_evidence = set(dependency_surface.get("requiredEvidence") or [])
for needle in ("generated project go.mod", "isolated temporary test module", "root module unchanged", "must-not-add-generated-only-dependencies"):
    require(needle in dependency_evidence, f"p13 dependency surface requiredEvidence missing {needle!r}")
for profile_name, profile in sorted(proof_by_profile.items()):
    require(
        profile.get("dependencyBoundary") == p13_execution.get("dependencyBoundary"),
        f"p13 dependency boundary must match adopter proof for {profile_name}",
    )

p13_promotion_policy = str(p13_maturity.get("promotionPolicy") or "")
for needle in (
    ".api import",
    ".api format",
    ".api validation",
    "old/current/future fixtures",
    "real adopter branch replay",
    "multi-language client evidence",
    "go test ./...",
    "clean repeat-generation diff classification",
    "root module dependency hygiene",
):
    require(needle in p13_promotion_policy, f"p13GoctlGeneratorMaturity promotionPolicy missing {needle!r}")
p13_runtime_policy = str(p13_maturity.get("runtimeArtifactPolicy") or "")
for needle in (".tmp-test", "GENERATED_VERSION_COMPAT_TMPDIR", "local temp directory", "must never be committed"):
    require(needle in p13_runtime_policy, f"p13GoctlGeneratorMaturity runtimeArtifactPolicy missing {needle!r}")

p14_replay = manifest.get("p14GeneratorAdopterReplayEvidence") or {}
require(
    p14_replay.get("schema") == "gofly.generator_adopter_replay_evidence.v1",
    "p14GeneratorAdopterReplayEvidence schema mismatch",
)
require(
    p14_replay.get("aiflowTask") == "GOFLY-P14-03-GENERATOR-ADOPTER-REPLAY-EVIDENCE",
    "p14GeneratorAdopterReplayEvidence aiflowTask mismatch",
)
require(
    p14_replay.get("status") == "blocking-contract",
    "p14GeneratorAdopterReplayEvidence status must be blocking-contract",
)
require(
    set(p14_replay.get("sourceOfTruth") or []) == expected_p13_sources,
    "p14GeneratorAdopterReplayEvidence sourceOfTruth mismatch",
)
for source in p14_replay.get("sourceOfTruth") or []:
    require((root / source).exists(), f"p14GeneratorAdopterReplayEvidence source path missing: {source}")
require(
    set(p14_replay.get("acceptanceGates") or []) == {
        "make generated-version-compat-check",
        "make generated-upgrade-dry-run-check",
        "make root-dependency-policy-check",
    },
    "p14GeneratorAdopterReplayEvidence acceptanceGates mismatch",
)
p14_previous_refs = {
    item.get("field"): item
    for item in p14_replay.get("previousContractRefs") or []
    if isinstance(item, dict) and item.get("field")
}
require(
    set(p14_previous_refs) == {"p12RealBranchReplay", "p13GoctlGeneratorMaturity"},
    f"p14GeneratorAdopterReplayEvidence previousContractRefs mismatch: {sorted(p14_previous_refs)!r}",
)
for ref_name, ref in sorted(p14_previous_refs.items()):
    require(ref.get("status") == "blocking-contract", f"p14GeneratorAdopterReplayEvidence {ref_name}: status mismatch")
    carry_forward = set(ref.get("requiredCarryForward") or [])
    require(len(carry_forward) >= 4, f"p14GeneratorAdopterReplayEvidence {ref_name}: requiredCarryForward must include at least four rows")

p14_execution = p14_replay.get("executionPolicy") or {}
for field in (
    "temporaryReplayRoot",
    "fixtureReplay",
    "adopterReplayRows",
    "repeatGeneration",
    "diffClassification",
    "temporaryProjectSmoke",
    "dependencyBoundary",
    "artifactPolicy",
):
    require(
        len(str(p14_execution.get(field) or "").split()) >= 8,
        f"p14GeneratorAdopterReplayEvidence executionPolicy.{field} must be actionable",
    )
require(
    "gofly repository" in str(p14_execution.get("temporaryReplayRoot") or ""),
    "p14GeneratorAdopterReplayEvidence temporaryReplayRoot must keep replay outside the gofly repository",
)
require("diff -ru" in str(p14_execution.get("repeatGeneration") or ""), "p14GeneratorAdopterReplayEvidence repeatGeneration must require diff -ru")
require("go test ./..." in str(p14_execution.get("temporaryProjectSmoke") or ""), "p14GeneratorAdopterReplayEvidence temporaryProjectSmoke must run go test ./...")
require(
    p14_execution.get("diffReportSchema") == "gofly.generated_version_compat_report.v1",
    "p14GeneratorAdopterReplayEvidence diffReportSchema mismatch",
)
require(
    p14_execution.get("dependencyBoundary") == p13_execution.get("dependencyBoundary"),
    "p14GeneratorAdopterReplayEvidence dependencyBoundary must match P13 execution policy",
)
require(
    p14_execution.get("rootModulePolicy") == "must-not-add-generated-only-dependencies",
    "p14GeneratorAdopterReplayEvidence rootModulePolicy mismatch",
)
for needle in ("runtime evidence", "must not be committed"):
    require(needle in str(p14_execution.get("artifactPolicy") or ""), f"p14GeneratorAdopterReplayEvidence artifactPolicy missing {needle!r}")

p14_rows = {
    item.get("profile"): item
    for item in p14_replay.get("replayRows") or []
    if isinstance(item, dict) and item.get("profile")
}
require(set(p14_rows) == profile_names, f"p14GeneratorAdopterReplayEvidence replayRows profiles mismatch: {sorted(p14_rows)!r}")
expected_branch_evidence = minimum_fields | {"smokeResult", "dependencyBoundary"}
for profile_name, row in sorted(p14_rows.items()):
    matrix_profile = matrix_profiles.get(profile_name) or {}
    manifest_profile = next((profile for profile in profiles if profile.get("profile") == profile_name), {})
    p12_profile = p12_profiles.get(profile_name) or {}
    for field in (
        "id",
        "profile",
        "adopterBranchClass",
        "fixtureSet",
        "expectedDiff",
        "acceptedDiffCategories",
        "requiredBranchEvidence",
        "generatedProjectSmoke",
        "repeatGeneration",
        "dependencyBoundary",
        "rootModulePolicy",
        "blockingChecks",
        "rollbackAction",
    ):
        require(row.get(field), f"p14GeneratorAdopterReplayEvidence {profile_name}: {field} is required")
    fixture_set = row.get("fixtureSet") or {}
    for field in ("api", "proto", "serviceConfig"):
        require(
            fixture_set.get(field) == manifest_profile.get(field),
            f"p14GeneratorAdopterReplayEvidence {profile_name}: fixture {field} must match generated upgrade profile",
        )
        require(
            (root / str(fixture_set.get(field) or "")).is_file(),
            f"p14GeneratorAdopterReplayEvidence {profile_name}: fixture path missing for {field}",
        )
    require(
        fixture_set.get("snapshot") == "testdata/generated-compat/matrix.json",
        f"p14GeneratorAdopterReplayEvidence {profile_name}: snapshot must be the generated compatibility matrix",
    )
    require(
        row.get("expectedDiff") == matrix_profile.get("expectedDiff"),
        f"p14GeneratorAdopterReplayEvidence {profile_name}: expectedDiff must match version matrix",
    )
    require(
        set(row.get("acceptedDiffCategories") or []) == set(p12_profile.get("acceptedDiffCategories") or []),
        f"p14GeneratorAdopterReplayEvidence {profile_name}: acceptedDiffCategories must match P12 replay mapping",
    )
    require(
        expected_branch_evidence == set(row.get("requiredBranchEvidence") or []),
        f"p14GeneratorAdopterReplayEvidence {profile_name}: requiredBranchEvidence mismatch",
    )
    require(row.get("generatedProjectSmoke") == "go test ./...", f"p14GeneratorAdopterReplayEvidence {profile_name}: generatedProjectSmoke mismatch")
    require(row.get("repeatGeneration") == "clean", f"p14GeneratorAdopterReplayEvidence {profile_name}: repeatGeneration must be clean")
    require(
        row.get("diffReportSchema") == "gofly.generated_version_compat_report.v1",
        f"p14GeneratorAdopterReplayEvidence {profile_name}: diffReportSchema mismatch",
    )
    require(
        row.get("dependencyBoundary") == p14_execution.get("dependencyBoundary"),
        f"p14GeneratorAdopterReplayEvidence {profile_name}: dependencyBoundary mismatch",
    )
    require(
        row.get("rootModulePolicy") == "must-not-add-generated-only-dependencies",
        f"p14GeneratorAdopterReplayEvidence {profile_name}: rootModulePolicy mismatch",
    )
    blocking_checks = set(row.get("blockingChecks") or [])
    for check in (
        "fixture replay goTest == passed",
        "repeatGenerationDiff == clean",
        "smokeResult == passed",
        "root module unchanged",
    ):
        require(check in blocking_checks, f"p14GeneratorAdopterReplayEvidence {profile_name}: blockingChecks missing {check!r}")
    require(
        len(str(row.get("rollbackAction") or "").split()) >= 14,
        f"p14GeneratorAdopterReplayEvidence {profile_name}: rollbackAction must be actionable",
    )

p14_decision = p14_replay.get("promotionDecision") or {}
require(
    p14_decision.get("result") == "hold-until-replay-evidence-attached",
    "p14GeneratorAdopterReplayEvidence promotionDecision.result mismatch",
)
require(p14_decision.get("requiredCompletedReplayRows") == 3, "p14GeneratorAdopterReplayEvidence requiredCompletedReplayRows mismatch")
require(p14_decision.get("completedReplayRows") == 0, "p14GeneratorAdopterReplayEvidence completedReplayRows must remain 0 until real branch evidence is attached")
require(
    p14_decision.get("nextReviewGate") == "make generated-version-compat-check && make generated-upgrade-dry-run-check",
    "p14GeneratorAdopterReplayEvidence nextReviewGate mismatch",
)
for field in ("releaseNotePolicy",):
    require(
        len(str(p14_decision.get(field) or "").split()) >= 18,
        f"p14GeneratorAdopterReplayEvidence promotionDecision.{field} must be actionable",
    )

p14_promotion_policy = str(p14_replay.get("promotionPolicy") or "")
for needle in (
    "old, current, and future fixture replay",
    "real adopter branch metadata",
    "clean repeat-generation diff classification",
    "go test ./...",
    "root module dependency hygiene",
    "rollback action",
):
    require(needle in p14_promotion_policy, f"p14GeneratorAdopterReplayEvidence promotionPolicy missing {needle!r}")
p14_runtime_policy = str(p14_replay.get("runtimeArtifactPolicy") or "")
for needle in (".tmp-test", "GENERATED_VERSION_COMPAT_TMPDIR", "local temp directory", "must never be committed"):
    require(needle in p14_runtime_policy, f"p14GeneratorAdopterReplayEvidence runtimeArtifactPolicy missing {needle!r}")

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
    "p9HistoricalFixtureMatrix",
    "p10GoctlGeneratorFidelity",
    "p11LiveUpgradeProof",
    "p12RealBranchReplay",
    "p13GoctlGeneratorMaturity",
    "p14GeneratorAdopterReplayEvidence",
    "gofly.generated_version_compat_report.v1",
):
    require(needle in doc, f"docs/reference/generated-upgrade-dry-run.md missing {needle!r}")

if missing:
    print("generated upgrade dry-run check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("generated upgrade dry-run governance ok")
PY
