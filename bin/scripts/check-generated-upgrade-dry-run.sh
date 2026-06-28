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
