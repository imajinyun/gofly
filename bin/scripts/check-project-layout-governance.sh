#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import subprocess
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "project-layout-governance.json"
missing = []


def require(condition, message):
    if not condition:
        missing.append(message)


def read_text(path):
    if not path.is_file():
        missing.append(f"{path.relative_to(root)} is missing")
        return ""
    return path.read_text(encoding="utf-8")


def make_target_names(makefile):
    return set(re.findall(r"^([A-Za-z0-9_-]+):", makefile, re.M))


def gate_is_known(gate, targets):
    if gate.startswith("make "):
        target = gate.removeprefix("make ").split()[0]
        return target in targets
    return gate.startswith("go ")


if manifest_path.is_file():
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
else:
    manifest = {}
    missing.append("docs/reference/project-layout-governance.json is missing")

makefile = read_text(root / "Makefile")
gitignore = read_text(root / ".gitignore")
targets = make_target_names(makefile)

require(manifest.get("schema") == "gofly.project_layout_governance.v1", "project layout governance schema mismatch")
require(manifest.get("status") == "blocking-contract", "project layout governance status must be blocking-contract")
require(manifest.get("acceptanceGate") == "make project-layout-governance-check", "project layout governance acceptanceGate mismatch")
require(manifest.get("noBigBangMove") is True, "project layout governance must forbid big-bang moves")
require(len(str(manifest.get("policy") or "").split()) >= 20, "project layout governance policy must be actionable")
require("project-layout-governance-check" in targets, "Makefile must expose project-layout-governance-check")
docs_check = next((line for line in makefile.splitlines() if line.startswith("docs-check:")), "")
require("project-layout-governance-check" in docs_check, "docs-check must depend on project-layout-governance-check")
require("command-family-dependency-map-check" in targets, "Makefile must expose command-family-dependency-map-check")
require("command-family-dependency-map-check" in docs_check, "docs-check must depend on command-family-dependency-map-check")
require("command-split-readiness-check" in targets, "Makefile must expose command-split-readiness-check")
require("command-split-readiness-check" in docs_check, "docs-check must depend on command-split-readiness-check")
require("command-help-split-dry-run-check" in targets, "Makefile must expose command-help-split-dry-run-check")
require("command-help-split-dry-run-check" in docs_check, "docs-check must depend on command-help-split-dry-run-check")
require("command-doctor-split-dry-run-check" in targets, "Makefile must expose command-doctor-split-dry-run-check")
require("command-doctor-split-dry-run-check" in docs_check, "docs-check must depend on command-doctor-split-dry-run-check")
require("command-shared-reduction-plan-check" in targets, "Makefile must expose command-shared-reduction-plan-check")
require("command-shared-reduction-plan-check" in docs_check, "docs-check must depend on command-shared-reduction-plan-check")
require("command-output-json-adapter-dry-run-check" in targets, "Makefile must expose command-output-json-adapter-dry-run-check")
require("command-output-json-adapter-dry-run-check" in docs_check, "docs-check must depend on command-output-json-adapter-dry-run-check")
require("command-help-doctor-split-preflight-check" in targets, "Makefile must expose command-help-doctor-split-preflight-check")
require("command-help-doctor-split-preflight-check" in docs_check, "docs-check must depend on command-help-doctor-split-preflight-check")
require("command-next-family-candidate-refresh-check" in targets, "Makefile must expose command-next-family-candidate-refresh-check")
require("command-next-family-candidate-refresh-check" in docs_check, "docs-check must depend on command-next-family-candidate-refresh-check")
require("check-project-layout-governance.sh" in makefile, "Makefile must call check-project-layout-governance.sh")

top_level_boundary = manifest.get("topLevelDirectoryBoundaries") or {}
require(
    top_level_boundary.get("status") == "blocking-contract",
    "topLevelDirectoryBoundaries.status must be blocking-contract",
)
require(
    top_level_boundary.get("forbidUnknownTrackedDirectories") is True,
    "topLevelDirectoryBoundaries must forbid unknown tracked directories",
)
require(
    "tracked top-level" in str(top_level_boundary.get("policy") or ""),
    "topLevelDirectoryBoundaries policy must describe tracked top-level directory handling",
)
tracked_top_level_dirs = sorted(
    {
        rel.split("/", 1)[0]
        for rel in subprocess.run(
            ["git", "ls-files"],
            cwd=root,
            check=True,
            text=True,
            stdout=subprocess.PIPE,
        ).stdout.splitlines()
        if "/" in rel
    }
)
directory_entries = top_level_boundary.get("directories") or []
declared_top_level_dirs = []
allowed_categories = {
    "adoption-proof",
    "automation",
    "cli",
    "deployment",
    "documentation",
    "extension-api",
    "fixtures",
    "framework-api",
    "framework-runtime",
    "operations",
    "performance-evidence",
    "tooling",
}
for entry in directory_entries:
    if not isinstance(entry, dict):
        missing.append(f"topLevelDirectoryBoundaries entry must be object: {entry!r}")
        continue
    item_id = entry.get("id", "")
    declared_top_level_dirs.append(item_id)
    require(item_id, "topLevelDirectoryBoundaries entry id is required")
    require("/" not in item_id, f"top-level directory id must not contain slash: {item_id!r}")
    require((root / item_id).is_dir(), f"top-level directory does not exist: {item_id}")
    require(entry.get("category") in allowed_categories, f"top-level directory {item_id}: unknown category {entry.get('category')!r}")
    require(len(str(entry.get("purpose") or "").split()) >= 6, f"top-level directory {item_id}: purpose must be descriptive")
    require(gate_is_known(str(entry.get("gate") or ""), targets), f"top-level directory {item_id}: gate is not known")
    require(
        any(rel == item_id or rel.startswith(item_id + "/") for rel in subprocess.run(
            ["git", "ls-files", item_id],
            cwd=root,
            check=True,
            text=True,
            stdout=subprocess.PIPE,
        ).stdout.splitlines()),
        f"top-level directory {item_id}: no tracked files found",
    )
require(
    sorted(declared_top_level_dirs) == tracked_top_level_dirs,
    "topLevelDirectoryBoundaries must account for every tracked top-level directory exactly once: "
    f"declared={sorted(declared_top_level_dirs)!r}, actual={tracked_top_level_dirs!r}",
)
require(
    len(declared_top_level_dirs) == len(set(declared_top_level_dirs)),
    "topLevelDirectoryBoundaries must not contain duplicate directory ids",
)

root_file_boundary = manifest.get("rootFileBoundaries") or {}
require(root_file_boundary.get("status") == "blocking-contract", "rootFileBoundaries.status must be blocking-contract")
require(
    root_file_boundary.get("forbidUnknownRootFiles") is True,
    "rootFileBoundaries must forbid unknown root files",
)
require(
    "Every tracked root-level file" in str(root_file_boundary.get("policy") or ""),
    "rootFileBoundaries policy must describe tracked root-level file handling",
)
tracked_root_files = sorted(
    rel
    for rel in subprocess.run(
        ["git", "ls-files"],
        cwd=root,
        check=True,
        text=True,
        stdout=subprocess.PIPE,
    ).stdout.splitlines()
    if "/" not in rel
)
root_file_families = root_file_boundary.get("families") or []
declared_root_files = []
allowed_root_file_categories = {
    "container",
    "documentation",
    "governance",
    "module",
    "release",
}
for family in root_file_families:
    if not isinstance(family, dict):
        missing.append(f"rootFileBoundaries family must be object: {family!r}")
        continue
    family_id = family.get("id", "")
    files = family.get("files") or []
    require(family_id, "root file family id is required")
    require(family.get("category") in allowed_root_file_categories, f"root file family {family_id}: unknown category {family.get('category')!r}")
    require(len(str(family.get("purpose") or "").split()) >= 8, f"root file family {family_id}: purpose must be descriptive")
    require(gate_is_known(str(family.get("gate") or ""), targets), f"root file family {family_id}: gate is not known")
    require(files, f"root file family {family_id}: files are required")
    for filename in files:
        declared_root_files.append(filename)
        require("/" not in filename, f"root file family {family_id}: file must be a root-level path: {filename!r}")
        require((root / filename).is_file(), f"root file family {family_id}: missing {filename}")
require(
    sorted(declared_root_files) == tracked_root_files,
    "rootFileBoundaries must account for every tracked root-level file exactly once: "
    f"declared={sorted(declared_root_files)!r}, actual={tracked_root_files!r}",
)
require(
    len(declared_root_files) == len(set(declared_root_files)),
    "rootFileBoundaries must not contain duplicate root files",
)

script_boundary = manifest.get("scriptFamilyBoundaries") or {}
require(script_boundary.get("status") == "blocking-contract", "scriptFamilyBoundaries.status must be blocking-contract")
require(script_boundary.get("root") == "bin/scripts", "scriptFamilyBoundaries.root must be bin/scripts")
require(
    script_boundary.get("forbidUnknownScripts") is True,
    "scriptFamilyBoundaries must forbid unknown scripts",
)
require(
    "one-family-at-a-time" in str(script_boundary.get("policy") or ""),
    "scriptFamilyBoundaries policy must preserve one-family-at-a-time migration",
)
tracked_script_files = sorted(
    rel.removeprefix("bin/scripts/")
    for rel in subprocess.run(
        ["git", "ls-files", "bin/scripts"],
        cwd=root,
        check=True,
        text=True,
        stdout=subprocess.PIPE,
    ).stdout.splitlines()
    if rel.startswith("bin/scripts/")
)
script_families = script_boundary.get("families") or []
declared_script_files = []
allowed_script_categories = {
    "adoption-proof",
    "code-generation",
    "dependency-governance",
    "documentation",
    "extension-api",
    "framework-api",
    "performance-evidence",
    "release-governance",
    "tooling",
}
for family in script_families:
    if not isinstance(family, dict):
        missing.append(f"scriptFamilyBoundaries family must be object: {family!r}")
        continue
    family_id = family.get("id", "")
    files = family.get("files") or []
    require(family_id, "script family id is required")
    require(family.get("category") in allowed_script_categories, f"script family {family_id}: unknown category {family.get('category')!r}")
    require(len(str(family.get("purpose") or "").split()) >= 8, f"script family {family_id}: purpose must be descriptive")
    require(gate_is_known(str(family.get("gate") or ""), targets), f"script family {family_id}: gate is not known")
    require(files, f"script family {family_id}: files are required")
    for filename in files:
        declared_script_files.append(filename)
        require("/" not in filename, f"script family {family_id}: file must be relative to bin/scripts root: {filename!r}")
        require((root / "bin" / "scripts" / filename).is_file(), f"script family {family_id}: missing bin/scripts/{filename}")
require(
    sorted(declared_script_files) == tracked_script_files,
    "scriptFamilyBoundaries must account for every tracked bin/scripts file exactly once: "
    f"declared={sorted(declared_script_files)!r}, actual={tracked_script_files!r}",
)
require(
    len(declared_script_files) == len(set(declared_script_files)),
    "scriptFamilyBoundaries must not contain duplicate script files",
)

deploy_boundary = manifest.get("deployAssetBoundary") or {}
require(deploy_boundary.get("status") == "blocking-contract", "deployAssetBoundary.status must be blocking-contract")
require("deploy/" in str(deploy_boundary.get("policy") or ""), "deployAssetBoundary policy must require deploy/")
require(gate_is_known(str(deploy_boundary.get("gate") or ""), targets), "deployAssetBoundary gate is not known")
for rel in deploy_boundary.get("requiredPaths") or []:
    require((root / rel).is_dir(), f"deployAssetBoundary missing required path: {rel}")
for rel in deploy_boundary.get("forbiddenTopLevelPaths") or []:
    require(not (root / rel).exists(), f"retired top-level deploy asset path must not exist: {rel}")
require(
    deploy_boundary.get("requiredPaths") == ["deploy/k8s", "deploy/helm/gofly"],
    "deployAssetBoundary requiredPaths must pin deploy/k8s and deploy/helm/gofly",
)
require(
    deploy_boundary.get("forbiddenTopLevelPaths") == ["k8s", "charts"],
    "deployAssetBoundary forbiddenTopLevelPaths must retire k8s and charts",
)
require(
    deploy_boundary.get("forbidUnknownDeployFiles") is True,
    "deployAssetBoundary must forbid unknown deploy files",
)
tracked_deploy_files = sorted(
    subprocess.run(
        ["git", "ls-files", "deploy"],
        cwd=root,
        check=True,
        text=True,
        stdout=subprocess.PIPE,
    ).stdout.splitlines()
)
deploy_families = deploy_boundary.get("families") or []
declared_deploy_files = []
allowed_deploy_categories = {"helm", "kustomize"}
for family in deploy_families:
    if not isinstance(family, dict):
        missing.append(f"deployAssetBoundary family must be object: {family!r}")
        continue
    family_id = family.get("id", "")
    files = family.get("files") or []
    require(family_id, "deploy asset family id is required")
    require(family.get("category") in allowed_deploy_categories, f"deploy asset family {family_id}: unknown category {family.get('category')!r}")
    require(len(str(family.get("purpose") or "").split()) >= 8, f"deploy asset family {family_id}: purpose must be descriptive")
    require(gate_is_known(str(family.get("gate") or ""), targets), f"deploy asset family {family_id}: gate is not known")
    require(files, f"deploy asset family {family_id}: files are required")
    for rel in files:
        declared_deploy_files.append(rel)
        require(str(rel).startswith("deploy/"), f"deploy asset family {family_id}: file must stay under deploy/: {rel!r}")
        require((root / rel).is_file(), f"deploy asset family {family_id}: missing {rel}")
require(
    sorted(declared_deploy_files) == tracked_deploy_files,
    "deployAssetBoundary must account for every tracked deploy file exactly once: "
    f"declared={sorted(declared_deploy_files)!r}, actual={tracked_deploy_files!r}",
)
require(
    len(declared_deploy_files) == len(set(declared_deploy_files)),
    "deployAssetBoundary must not contain duplicate deploy files",
)

ignored = set(manifest.get("runtimeIgnoredPaths") or [])
expected_ignored = {".aiflow/", ".harness/", ".tmp-test/", ".trae/", "coverage.out", "docs/superpowers/"}
require(ignored == expected_ignored, f"runtimeIgnoredPaths mismatch: {sorted(ignored)!r}")
for path in expected_ignored:
    if path == "coverage.out":
        require("*.out" in gitignore or "coverage.*" in gitignore or "coverage.out" in gitignore, ".gitignore must cover coverage.out")
    else:
        require(path in gitignore, f".gitignore must cover {path}")

ignored_boundary = manifest.get("ignoredArtifactBoundary") or {}
require(
    ignored_boundary.get("status") == "blocking-contract",
    "ignoredArtifactBoundary.status must be blocking-contract",
)
require(
    ignored_boundary.get("forbidTrackedArtifacts") is True,
    "ignoredArtifactBoundary must forbid tracked artifacts",
)
require(
    ".aiflow/" in str(ignored_boundary.get("policy") or "") and "Root aiflow.yaml" in str(ignored_boundary.get("policy") or ""),
    "ignoredArtifactBoundary policy must preserve root aiflow.yaml and local .aiflow/ split",
)
allowed_ignored_categories = {
    "benchmark-transient",
    "binary-artifact",
    "coverage-artifact",
    "generated-docs",
    "runtime-state",
}
expected_ignored_artifacts = {
    ".aiflow/",
    ".harness/",
    ".tmp-test/",
    ".trae/",
    "bench/current.txt",
    "bench/regression-report.json",
    "bench/summary.md",
    "bin/gofly",
    "coverage.out",
    "docs/*",
    "docs/superpowers/",
}
ignored_artifacts = ignored_boundary.get("artifacts") or []
declared_ignored_artifacts = []
for artifact in ignored_artifacts:
    if not isinstance(artifact, dict):
        missing.append(f"ignoredArtifactBoundary artifact must be object: {artifact!r}")
        continue
    artifact_path = artifact.get("path", "")
    declared_ignored_artifacts.append(artifact_path)
    require(artifact_path, "ignored artifact path is required")
    require(
        artifact.get("category") in allowed_ignored_categories,
        f"ignored artifact {artifact_path}: unknown category {artifact.get('category')!r}",
    )
    require(len(str(artifact.get("reason") or "").split()) >= 8, f"ignored artifact {artifact_path}: reason must be descriptive")
    ignore_pattern = str(artifact.get("ignorePattern") or "")
    require(ignore_pattern, f"ignored artifact {artifact_path}: ignorePattern is required")
    require(ignore_pattern in gitignore, f"ignored artifact {artifact_path}: .gitignore must contain {ignore_pattern!r}")
    if artifact_path == "docs/*":
        allowed_exceptions = set(artifact.get("allowedTrackedExceptions") or [])
        require(allowed_exceptions == {"docs/index.md"}, "ignored artifact docs/* must only allow tracked docs/index.md")
        tracked_top_docs = [
            rel
            for rel in subprocess.run(
                ["git", "ls-files", "docs"],
                cwd=root,
                check=True,
                text=True,
                stdout=subprocess.PIPE,
            ).stdout.splitlines()
            if rel.startswith("docs/") and "/" not in rel.removeprefix("docs/")
        ]
        require(
            set(tracked_top_docs) <= allowed_exceptions,
            f"ignored artifact docs/*: tracked top-level docs are forbidden except docs/index.md: {tracked_top_docs!r}",
        )
        sample_ignored_paths = artifact.get("sampleIgnoredPaths") or []
        require(sample_ignored_paths, "ignored artifact docs/* must provide sampleIgnoredPaths")
        for sample in sample_ignored_paths:
            require(str(sample).startswith("docs/"), f"ignored artifact docs/* sample must stay under docs/: {sample!r}")
            check_ignore = subprocess.run(
                ["git", "check-ignore", str(sample)],
                cwd=root,
                check=False,
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
            )
            require(check_ignore.returncode == 0, f"ignored artifact docs/* sample {sample}: git check-ignore must match")
    elif any(ch in artifact_path for ch in "*?[]"):
        tracked_matches = subprocess.run(
            ["git", "ls-files", artifact_path],
            cwd=root,
            check=True,
            text=True,
            stdout=subprocess.PIPE,
        ).stdout.splitlines()
        require(not tracked_matches, f"ignored artifact pattern {artifact_path}: tracked files are forbidden: {tracked_matches!r}")
    else:
        tracked_matches = subprocess.run(
            ["git", "ls-files", artifact_path],
            cwd=root,
            check=True,
            text=True,
            stdout=subprocess.PIPE,
        ).stdout.splitlines()
        require(not tracked_matches, f"ignored artifact {artifact_path}: must not be tracked")
        check_ignore = subprocess.run(
            ["git", "check-ignore", artifact_path.rstrip("/")],
            cwd=root,
            check=False,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        require(check_ignore.returncode == 0, f"ignored artifact {artifact_path}: git check-ignore must match")
require(
    set(declared_ignored_artifacts) == expected_ignored_artifacts,
    "ignoredArtifactBoundary must pin runtime, docs, benchmark, binary, and coverage artifacts: "
    f"declared={sorted(declared_ignored_artifacts)!r}",
)
require(
    len(declared_ignored_artifacts) == len(set(declared_ignored_artifacts)),
    "ignoredArtifactBoundary must not contain duplicate artifact paths",
)

docs_taxonomy = manifest.get("docsTaxonomyBoundaries") or {}
require(docs_taxonomy.get("status") == "blocking-contract", "docsTaxonomyBoundaries.status must be blocking-contract")
require(docs_taxonomy.get("root") == "docs", "docsTaxonomyBoundaries.root must be docs")
require(
    docs_taxonomy.get("forbidUnknownTrackedDocDirectories") is True,
    "docsTaxonomyBoundaries must forbid unknown tracked doc directories",
)
require(
    "docs/superpowers" in str(docs_taxonomy.get("policy") or ""),
    "docsTaxonomyBoundaries policy must keep docs/superpowers local-only",
)
tracked_doc_dirs = sorted(
    {
        rel.split("/", 2)[1]
        for rel in subprocess.run(
            ["git", "ls-files", "docs"],
            cwd=root,
            check=True,
            text=True,
            stdout=subprocess.PIPE,
        ).stdout.splitlines()
        if rel.startswith("docs/") and "/" in rel.removeprefix("docs/")
    }
)
doc_families = docs_taxonomy.get("families") or []
declared_doc_dirs = []
allowed_doc_categories = {
    "case-study",
    "explanation",
    "how-to",
    "learning-path",
    "reference",
}
for family in doc_families:
    if not isinstance(family, dict):
        missing.append(f"docsTaxonomyBoundaries family must be object: {family!r}")
        continue
    family_id = family.get("id", "")
    dirs = family.get("directories") or []
    require(family_id, "docs taxonomy family id is required")
    require(family.get("category") in allowed_doc_categories, f"docs taxonomy family {family_id}: unknown category {family.get('category')!r}")
    require(len(str(family.get("purpose") or "").split()) >= 8, f"docs taxonomy family {family_id}: purpose must be descriptive")
    require(gate_is_known(str(family.get("gate") or ""), targets), f"docs taxonomy family {family_id}: gate is not known")
    require(dirs, f"docs taxonomy family {family_id}: directories are required")
    for dirname in dirs:
        declared_doc_dirs.append(dirname)
        require("/" not in dirname, f"docs taxonomy family {family_id}: directory must be relative to docs/: {dirname!r}")
        require(dirname != "superpowers", "docs/superpowers must not be declared as a tracked docs taxonomy directory")
        require((root / "docs" / dirname).is_dir(), f"docs taxonomy family {family_id}: missing docs/{dirname}")
require(
    sorted(declared_doc_dirs) == tracked_doc_dirs,
    "docsTaxonomyBoundaries must account for every tracked docs subdirectory exactly once: "
    f"declared={sorted(declared_doc_dirs)!r}, actual={tracked_doc_dirs!r}",
)
require(
    len(declared_doc_dirs) == len(set(declared_doc_dirs)),
    "docsTaxonomyBoundaries must not contain duplicate docs directories",
)

examples_plan = manifest.get("examplesGroupingPlan") or {}
require(examples_plan.get("status") == "planned-only", "examplesGroupingPlan.status must be planned-only")
require(examples_plan.get("admissionGate") == "make project-layout-governance-check", "examplesGroupingPlan admissionGate mismatch")
require("one family at a time" in str(examples_plan.get("migrationPolicy") or ""), "examplesGroupingPlan migrationPolicy must require one-family migration")
groups = examples_plan.get("groups") or []
grouped_examples = []
for group in groups:
    if not isinstance(group, dict):
        missing.append(f"examples group must be object: {group!r}")
        continue
    group_id = group.get("id", "<missing>")
    for field in ("id", "futurePath", "currentExamples", "gate"):
        require(group.get(field), f"examples group {group_id}: {field} is required")
    require(str(group.get("futurePath", "")).startswith("examples/"), f"examples group {group_id}: futurePath must stay under examples/")
    require(gate_is_known(str(group.get("gate") or ""), targets), f"examples group {group_id}: gate is not known")
    for example in group.get("currentExamples") or []:
        grouped_examples.append(example)
        require((root / "examples" / example).is_dir(), f"examples group {group_id}: missing examples/{example}")
actual_examples = sorted(path.name for path in (root / "examples").iterdir() if path.is_dir())
require(sorted(grouped_examples) == actual_examples, "examplesGroupingPlan must account for every examples/* directory exactly once")
require(len(grouped_examples) == len(set(grouped_examples)), "examplesGroupingPlan must not contain duplicate example directories")

physical_admission = examples_plan.get("physicalMigrationAdmission") or {}
require(
    physical_admission.get("status") == "blocked-until-ready",
    "examples physicalMigrationAdmission.status must be blocked-until-ready",
)
require(
    "single dedicated migration commit" in str(physical_admission.get("policy") or ""),
    "examples physicalMigrationAdmission policy must require a dedicated migration commit",
)
require(
    physical_admission.get("selectedGroup") in ("", None),
    "examples physicalMigrationAdmission.selectedGroup must stay empty until a family is selected",
)
required_physical_signals = physical_admission.get("requiredSignals") or []
require(
    len(required_physical_signals) >= 8,
    "examples physicalMigrationAdmission must require at least 8 readiness signals",
)
for phrase in ("one examples family", "pre-move gates", "post-move gates", "examples/README.md", "go.mod", "rollback note"):
    require(
        any(phrase in str(signal) for signal in required_physical_signals),
        f"examples physicalMigrationAdmission missing required signal phrase {phrase!r}",
    )
expected_physical_gates = {
    "make project-layout-governance-check",
    "make examples-smoke",
    "make examples-copyable-check",
    "make docs-check",
    "git diff --check",
}
require(
    set(physical_admission.get("requiredGates") or []) == expected_physical_gates,
    "examples physicalMigrationAdmission.requiredGates mismatch",
)
for gate in physical_admission.get("requiredGates") or []:
    require(gate == "git diff --check" or gate_is_known(str(gate), targets), f"examples physicalMigrationAdmission gate is not known: {gate}")
assessments = physical_admission.get("familyAssessments") or []
group_by_id = {group.get("id"): group for group in groups if isinstance(group, dict)}
assessment_ids = []
allowed_required_path_updates = {
    "examples/README.md",
    "examples/*/go.mod",
    "bin/scripts/",
    "Makefile",
    "docs/",
    "docs/reference/project-layout-governance.json",
}
for assessment in assessments:
    if not isinstance(assessment, dict):
        missing.append(f"examples physical migration assessment must be object: {assessment!r}")
        continue
    assessment_id = assessment.get("id", "<missing>")
    assessment_ids.append(assessment_id)
    group = group_by_id.get(assessment_id)
    require(group is not None, f"examples physical migration assessment {assessment_id}: group does not exist")
    require(assessment.get("status") == "blocked", f"examples physical migration assessment {assessment_id}: status must be blocked")
    require(assessment.get("candidate") is False, f"examples physical migration assessment {assessment_id}: candidate must be false")
    if group:
        require(
            assessment.get("futurePath") == group.get("futurePath"),
            f"examples physical migration assessment {assessment_id}: futurePath mismatch",
        )
        require(
            sorted(assessment.get("currentExamples") or []) == sorted(group.get("currentExamples") or []),
            f"examples physical migration assessment {assessment_id}: currentExamples mismatch",
        )
    blockers = assessment.get("blockers") or []
    require(len(blockers) >= 2, f"examples physical migration assessment {assessment_id}: at least 2 blockers required")
    require(
        all(len(str(blocker).split()) >= 6 for blocker in blockers),
        f"examples physical migration assessment {assessment_id}: blockers must be descriptive",
    )
    require(
        set(assessment.get("requiredPathUpdates") or []) == allowed_required_path_updates,
        f"examples physical migration assessment {assessment_id}: requiredPathUpdates mismatch",
    )
    for gate in assessment.get("preMoveGates") or []:
        require(gate_is_known(str(gate), targets), f"examples physical migration assessment {assessment_id}: unknown preMoveGate {gate!r}")
    post_move_gates = assessment.get("postMoveGates") or []
    require(
        expected_physical_gates <= set(post_move_gates),
        f"examples physical migration assessment {assessment_id}: postMoveGates must include common physical migration gates",
    )
    for gate in post_move_gates:
        require(gate == "git diff --check" or gate_is_known(str(gate), targets), f"examples physical migration assessment {assessment_id}: unknown postMoveGate {gate!r}")
    require(
        "Restore the previous" in str(assessment.get("rollbackRequirement") or ""),
        f"examples physical migration assessment {assessment_id}: rollbackRequirement must describe restore path",
    )
require(
    sorted(assessment_ids) == sorted(group_by_id),
    "examples physicalMigrationAdmission.familyAssessments must account for every planned examples group exactly once",
)
require(
    len(assessment_ids) == len(set(assessment_ids)),
    "examples physicalMigrationAdmission.familyAssessments must not contain duplicate ids",
)

readiness = examples_plan.get("readinessContract") or {}
require(
    readiness.get("status") == "blocking-contract",
    "examplesGroupingPlan.readinessContract.status must be blocking-contract",
)
require(
    "Every current example" in str(readiness.get("policy") or ""),
    "examples readiness policy must describe every current example",
)
required_example_fields = [
    "id",
    "kind",
    "command",
    "verify",
    "ports",
    "dependencyMode",
    "copyable",
    "smokeGate",
    "readmeSource",
    "rollbackNote",
]
require(
    readiness.get("requiredFields") == required_example_fields,
    "examples readiness requiredFields must pin the complete admission contract",
)
allowed_example_kinds = {"command", "deployment-checklist", "library", "matrix", "server"}
allowed_dependency_modes = {"local-only", "optional-docker", "simulated-external"}
require(
    set(readiness.get("allowedKinds") or []) == allowed_example_kinds,
    "examples readiness allowedKinds mismatch",
)
require(
    set(readiness.get("allowedDependencyModes") or []) == allowed_dependency_modes,
    "examples readiness allowedDependencyModes mismatch",
)
readiness_entries = readiness.get("entries") or []
readiness_ids = []
for entry in readiness_entries:
    if not isinstance(entry, dict):
        missing.append(f"examples readiness entry must be object: {entry!r}")
        continue
    example_id = entry.get("id", "<missing>")
    readiness_ids.append(example_id)
    for field in required_example_fields:
        if field == "ports":
            require(field in entry and isinstance(entry.get(field), list), f"examples readiness {example_id}: ports list is required")
        elif field == "copyable":
            require(entry.get(field) is True, f"examples readiness {example_id}: copyable must be true")
        else:
            require(entry.get(field), f"examples readiness {example_id}: {field} is required")
    require(example_id in actual_examples, f"examples readiness {example_id}: example directory does not exist")
    require(entry.get("kind") in allowed_example_kinds, f"examples readiness {example_id}: unknown kind {entry.get('kind')!r}")
    require(
        entry.get("dependencyMode") in allowed_dependency_modes,
        f"examples readiness {example_id}: unknown dependencyMode {entry.get('dependencyMode')!r}",
    )
    require(gate_is_known(str(entry.get("smokeGate") or ""), targets), f"examples readiness {example_id}: smokeGate is not known")
    require(str(entry.get("command") or "").startswith(("go ", "make ")), f"examples readiness {example_id}: command must be a Go or make command")
    require(str(entry.get("verify") or "").startswith(("go ", "make ", "curl ")), f"examples readiness {example_id}: verify must be go, make, or curl")
    ports = entry.get("ports") or []
    require(
        all(isinstance(port, int) and 0 < port < 65536 for port in ports),
        f"examples readiness {example_id}: ports must be valid TCP port numbers",
    )
    if entry.get("kind") == "server":
        require(ports, f"examples readiness {example_id}: server examples must declare listening ports")
    else:
        require(not ports, f"examples readiness {example_id}: non-server examples must not declare ports")
    readme_source = str(entry.get("readmeSource") or "")
    require(
        readme_source == "examples/README.md" or readme_source == f"examples/{example_id}/README.md",
        f"examples readiness {example_id}: readmeSource must be examples/README.md or the example README",
    )
    require((root / readme_source).is_file(), f"examples readiness {example_id}: missing {readme_source}")
    require(
        len(str(entry.get("rollbackNote") or "").split()) >= 8,
        f"examples readiness {example_id}: rollbackNote must be actionable",
    )
    gomod = root / "examples" / example_id / "go.mod"
    require(gomod.is_file(), f"examples readiness {example_id}: go.mod is required for copyable examples")
    if gomod.is_file():
        gomod_text = gomod.read_text(encoding="utf-8")
        require(
            f"module github.com/imajinyun/gofly/examples/{example_id}" in gomod_text,
            f"examples readiness {example_id}: go.mod module path mismatch",
        )
        require(
            "replace github.com/imajinyun/gofly => ../.." in gomod_text,
            f"examples readiness {example_id}: go.mod must replace root module for copyable local smoke",
        )
require(
    sorted(readiness_ids) == actual_examples,
    "examples readiness entries must account for every examples/* directory exactly once: "
    f"declared={sorted(readiness_ids)!r}, actual={actual_examples!r}",
)
require(
    len(readiness_ids) == len(set(readiness_ids)),
    "examples readiness entries must not contain duplicate example ids",
)

command_dir = root / "cmd" / "gofly" / "internal" / "command"
families = manifest.get("commandFileFamilies") or []
command_files = sorted(str(path.relative_to(command_dir)) for path in command_dir.rglob("*.go"))
declared_command_files = []
for family in families:
    if not isinstance(family, dict):
        missing.append(f"commandFileFamilies entry must be object: {family!r}")
        continue
    family_id = family.get("id", "")
    prefix = family.get("prefix", "")
    explicit_files = family.get("files") or []
    require(prefix or explicit_files, "commandFileFamilies prefix or files are required")
    require(len(str(family.get("domain") or "").split()) >= 3, f"command family {prefix}: domain must be descriptive")
    if explicit_files and family_id and prefix:
        matching = explicit_files
        for filename in matching:
            require((command_dir / filename).is_file(), f"explicit command file is missing: {filename}")
        declared_command_files.extend(matching)
    elif prefix:
        if prefix.endswith("/"):
            matching = [
                str(path.relative_to(command_dir))
                for path in (command_dir / prefix.rstrip("/")).rglob("*.go")
            ]
        else:
            matching = [
                str(path.relative_to(command_dir))
                for path in command_dir.rglob("*.go")
                if path.name.startswith(prefix)
            ]
        require(matching, f"command family {prefix}: no files match prefix in cmd/gofly/internal/command")
        require(not explicit_files, f"command family {prefix}: prefix families must not also declare explicit files")
        declared_command_files.extend(matching)
    else:
        require(explicit_files, "explicit command family files are required when prefix is empty")
        for filename in explicit_files:
            require(
                not str(filename).startswith("help/"),
                f"explicit shared command file must not include extracted help subpackage file: {filename!r}",
            )
            require((command_dir / filename).is_file(), f"explicit command file is missing: {filename}")
            declared_command_files.append(filename)
require(
    sorted(declared_command_files) == command_files,
    "commandFileFamilies must account for every cmd/gofly/internal/command/**/*.go file exactly once: "
    f"declared={sorted(declared_command_files)!r}, actual={command_files!r}",
)
require(
    len(declared_command_files) == len(set(declared_command_files)),
    "commandFileFamilies must not contain duplicate command files",
)

command_dependency_map_path = root / "docs" / "reference" / "command-family-dependency-map.json"
if command_dependency_map_path.is_file():
    command_dependency_map = json.loads(command_dependency_map_path.read_text(encoding="utf-8"))
else:
    command_dependency_map = {}
    missing.append("docs/reference/command-family-dependency-map.json is missing")
require(
    command_dependency_map.get("schema") == "gofly.command_family_dependency_map.v1",
    "command family dependency map schema mismatch",
)
require(
    command_dependency_map.get("status") == "blocking-contract",
    "command family dependency map status must be blocking-contract",
)
require(
    command_dependency_map.get("acceptanceGate") == "make command-family-dependency-map-check",
    "command family dependency map acceptanceGate mismatch",
)
map_family_ids = [item.get("id") for item in command_dependency_map.get("families") or [] if isinstance(item, dict)]
require(
    sorted(map_family_ids) == [
        "ai",
        "api",
        "config",
        "doctor",
        "help",
        "model",
        "new",
        "plugin",
        "release",
        "rpc",
        "shared",
    ],
    "command family dependency map family ids mismatch",
)
require(
	[item.get("id") for item in command_dependency_map.get("nextCandidates") or [] if isinstance(item, dict)]
	== ["release"],
	"command family dependency map nextCandidates must contain release after P22-15",
)

command_split_readiness_path = root / "docs" / "reference" / "command-split-readiness.json"
if command_split_readiness_path.is_file():
    command_split_readiness = json.loads(command_split_readiness_path.read_text(encoding="utf-8"))
else:
    command_split_readiness = {}
    missing.append("docs/reference/command-split-readiness.json is missing")
require(
    command_split_readiness.get("schema") == "gofly.command_split_readiness.v1",
    "command split readiness schema mismatch",
)
require(
    command_split_readiness.get("status") == "next-family-candidate-refreshed",
    "command split readiness status must be next-family-candidate-refreshed",
)
require(
    command_split_readiness.get("acceptanceGate") == "make command-split-readiness-check",
    "command split readiness acceptanceGate mismatch",
)
require(
	[item.get("id") for item in command_split_readiness.get("candidateFamilies") or [] if isinstance(item, dict)]
	== ["release"],
	"command split readiness candidateFamilies must contain release after P22-15",
)
require(
	{item.get("id") for item in command_split_readiness.get("completedFamilies") or [] if isinstance(item, dict)}
	== {"help", "doctor"},
	"command split readiness completedFamilies must identify help and doctor",
)
require(
    {item.get("id") for item in command_split_readiness.get("blockedFamilies") or [] if isinstance(item, dict)}
    == {"ai", "shared"},
    "command split readiness blockedFamilies must identify ai and shared",
)
require(
	set(command_split_readiness.get("deferredFamilies") or []) == {"api", "rpc", "model", "new", "plugin", "config", "help", "doctor"},
	"command split readiness deferredFamilies mismatch",
)
require(
    (command_split_readiness.get("releaseBlockerFix") or {}).get("regressionTest")
    == "TestGenerateModelFromDDLGORMStyleDoesNotPolluteGoflyRootModule",
    "command split readiness must record the root module pollution regression test",
)
require(
	command_split_readiness.get("nextStep", {}).get("id") == "P22-16-command-release-family-preflight",
	"command split readiness nextStep mismatch",
)
completed_status = {
	item.get("id"): item.get("status")
	for item in command_split_readiness.get("completedFamilies") or []
	if isinstance(item, dict)
}
candidate_status = {
	item.get("id"): item.get("status")
	for item in command_split_readiness.get("candidateFamilies") or []
	if isinstance(item, dict)
}
require(completed_status.get("help") == "physical-split-completed", "command split readiness help family must be completed")
require(completed_status.get("doctor") == "physical-split-completed", "command split readiness doctor family must be completed")
require(candidate_status.get("release") == "candidate-after-json-golden", "command split readiness release family must be next candidate")

command_help_split_path = root / "docs" / "reference" / "command-help-split-dry-run.json"
if command_help_split_path.is_file():
    command_help_split = json.loads(command_help_split_path.read_text(encoding="utf-8"))
else:
    command_help_split = {}
    missing.append("docs/reference/command-help-split-dry-run.json is missing")
require(
    command_help_split.get("schema") == "gofly.command_help_split_dry_run.v1",
    "command help split dry-run schema mismatch",
)
require(
    command_help_split.get("status") == "completed-physical-split",
    "command help split evidence status must be completed-physical-split",
)
require(
    command_help_split.get("acceptanceGate") == "make command-help-split-dry-run-check",
    "command help split dry-run acceptanceGate mismatch",
)
require(command_help_split.get("dryRunOnly") is False, "command help split evidence must no longer be dryRunOnly after P22-12")
require(command_help_split.get("noPhysicalMove") is False, "command help split evidence must allow the completed help physical move")
require(command_help_split.get("physicalSplit") is True, "command help split evidence must record physicalSplit=true")
require(command_help_split.get("helpPackage") == "cmd/gofly/internal/command/help", "command help split evidence helpPackage mismatch")
require(command_help_split.get("commandAdapter") == "help_adapter.go", "command help split evidence commandAdapter mismatch")
require(
    set(command_help_split.get("goldenTopics") or []) == {"doctor", "api", "rpc gen", "plugin run"},
    "command help split dry-run goldenTopics mismatch",
)
require(
    command_help_split.get("physicalSplitAdmission", {}).get("status") == "completed-help-single-family-split",
    "command help split dry-run physicalSplitAdmission mismatch",
)

command_doctor_split_path = root / "docs" / "reference" / "command-doctor-split-dry-run.json"
if command_doctor_split_path.is_file():
    command_doctor_split = json.loads(command_doctor_split_path.read_text(encoding="utf-8"))
else:
    command_doctor_split = {}
    missing.append("docs/reference/command-doctor-split-dry-run.json is missing")
require(
    command_doctor_split.get("schema") == "gofly.command_doctor_split_dry_run.v1",
    "command doctor split dry-run schema mismatch",
)
require(
    command_doctor_split.get("status") == "completed-physical-split",
    "command doctor split dry-run status must be completed-physical-split",
)
require(
    command_doctor_split.get("acceptanceGate") == "make command-doctor-split-dry-run-check",
    "command doctor split dry-run acceptanceGate mismatch",
)
require(command_doctor_split.get("dryRunOnly") is False, "command doctor split dry-run must no longer be dryRunOnly after P22-14")
require(command_doctor_split.get("noPhysicalMove") is False, "command doctor split dry-run must allow completed physical move")
require(
    set(command_doctor_split.get("supportBundleFields") or [])
    == {"supportBundle.schema", "supportBundle.redaction", "supportBundle.commands", "supportBundle.description", "nextActions"},
    "command doctor split dry-run supportBundleFields mismatch",
)
require(
    command_doctor_split.get("physicalSplitAdmission", {}).get("status") == "completed-doctor-single-family-split",
    "command doctor split dry-run physicalSplitAdmission mismatch",
)

command_shared_reduction_path = root / "docs" / "reference" / "command-shared-reduction-plan.json"
if command_shared_reduction_path.is_file():
    command_shared_reduction = json.loads(command_shared_reduction_path.read_text(encoding="utf-8"))
else:
    command_shared_reduction = {}
    missing.append("docs/reference/command-shared-reduction-plan.json is missing")
require(
    command_shared_reduction.get("schema") == "gofly.command_shared_reduction_plan.v1",
    "command shared reduction plan schema mismatch",
)
require(
    command_shared_reduction.get("status") == "completed-preflight",
    "command shared reduction plan status must be completed-preflight",
)
require(
    command_shared_reduction.get("acceptanceGate") == "make command-shared-reduction-plan-check",
    "command shared reduction plan acceptanceGate mismatch",
)
require(command_shared_reduction.get("planningOnly") is True, "command shared reduction plan must be planningOnly")
require(command_shared_reduction.get("noPhysicalMove") is True, "command shared reduction plan must forbid physical move")
require(
    command_shared_reduction.get("recommendedOrder") == ["output-io", "json-envelope", "root-wiring", "path-flags", "template-source"],
    "command shared reduction plan recommendedOrder mismatch",
)
require(
    command_shared_reduction.get("physicalSplitAdmission", {}).get("status") == "blocked-until-adapters",
    "command shared reduction plan physicalSplitAdmission mismatch",
)

command_output_json_adapter_path = root / "docs" / "reference" / "command-output-json-adapter-dry-run.json"
if command_output_json_adapter_path.is_file():
    command_output_json_adapter = json.loads(command_output_json_adapter_path.read_text(encoding="utf-8"))
else:
    command_output_json_adapter = {}
    missing.append("docs/reference/command-output-json-adapter-dry-run.json is missing")
require(
    command_output_json_adapter.get("schema") == "gofly.command_output_json_adapter_dry_run.v1",
    "command output/json adapter dry-run schema mismatch",
)
require(
    command_output_json_adapter.get("status") == "completed-preflight",
    "command output/json adapter dry-run status must be completed-preflight",
)
require(
    command_output_json_adapter.get("acceptanceGate") == "make command-output-json-adapter-dry-run-check",
    "command output/json adapter dry-run acceptanceGate mismatch",
)
require(command_output_json_adapter.get("dryRunOnly") is True, "command output/json adapter dry-run must be dryRunOnly")
require(command_output_json_adapter.get("noPhysicalMove") is True, "command output/json adapter dry-run must forbid physical move")
require(
    set(command_output_json_adapter.get("adapterContracts") or [])
    == {
        "withCommandIO restores output mode and stdout/stderr writers",
        "quiet mode suppresses stdout helpers without suppressing stderr errors",
        "verbose mode writes diagnostic output to stderr",
        "printJSONEnvelope keeps ok command version and data fields",
        "printJSONError keeps error code retryable remediation and nextActions fields",
        "WriteErrorJSON keeps the legacy error envelope",
        "doctor --json stays stdout-only with stable nextActions",
        "bug --json supportBundle stays stdout-only with redaction guidance",
    },
    "command output/json adapter dry-run adapterContracts mismatch",
)
require(
    command_output_json_adapter.get("physicalSplitAdmission", {}).get("status") == "candidate-for-help-doctor-preflight",
    "command output/json adapter dry-run physicalSplitAdmission mismatch",
)

command_help_doctor_preflight_path = root / "docs" / "reference" / "command-help-doctor-split-preflight.json"
if command_help_doctor_preflight_path.is_file():
    command_help_doctor_preflight = json.loads(command_help_doctor_preflight_path.read_text(encoding="utf-8"))
else:
    command_help_doctor_preflight = {}
    missing.append("docs/reference/command-help-doctor-split-preflight.json is missing")
require(
    command_help_doctor_preflight.get("schema") == "gofly.command_help_doctor_split_preflight.v1",
    "command help/doctor split preflight schema mismatch",
)
require(
    command_help_doctor_preflight.get("status") == "help-and-doctor-physical-split-completed",
    "command help/doctor split evidence status must be help-and-doctor-physical-split-completed",
)
require(
    command_help_doctor_preflight.get("acceptanceGate") == "make command-help-doctor-split-preflight-check",
    "command help/doctor split preflight acceptanceGate mismatch",
)
require(command_help_doctor_preflight.get("dryRunOnly") is False, "command help/doctor split evidence must no longer be dryRunOnly after P22-12")
require(command_help_doctor_preflight.get("noPhysicalMove") is False, "command help/doctor split evidence must allow the completed help physical move")
require(command_help_doctor_preflight.get("helpPhysicalSplitDone") is True, "command help/doctor split evidence must record helpPhysicalSplitDone=true")
require(command_help_doctor_preflight.get("helpPackage") == "cmd/gofly/internal/command/help", "command help/doctor split evidence helpPackage mismatch")
require(command_help_doctor_preflight.get("commandAdapter") == "help_adapter.go", "command help/doctor split evidence commandAdapter mismatch")
require(command_help_doctor_preflight.get("doctorPhysicalSplitDone") is True, "command help/doctor split evidence must record doctorPhysicalSplitDone=true")
require(command_help_doctor_preflight.get("doctorPackage") == "cmd/gofly/internal/command/doctor", "command help/doctor split evidence doctorPackage mismatch")
require(command_help_doctor_preflight.get("doctorCommandAdapter") == "doctor_adapter.go", "command help/doctor split evidence doctorCommandAdapter mismatch")
require(command_help_doctor_preflight.get("selectedNextFamily") == "release", "command help/doctor split preflight selectedNextFamily mismatch")
require(command_help_doctor_preflight.get("completedNextFamily") == "doctor", "command help/doctor split preflight completedNextFamily mismatch")
require(command_help_doctor_preflight.get("deferredNextFamily") == "", "command help/doctor split preflight deferredNextFamily mismatch")
require(command_help_doctor_preflight.get("doctorPreflightRefreshed") is True, "command help/doctor split preflight must record doctorPreflightRefreshed=true")
require(
    set(command_help_doctor_preflight.get("preflightContracts") or [])
    == {
        "help remains reachable through root help dispatch and command-specific help routing",
        "help output stays stdout-only through the command output adapter",
        "doctor remains reachable through root command dispatch",
        "doctor --json stays stdout-only with stable nextActions fields",
        "bug --json supportBundle remains available for doctor remediation guidance",
        "only help and doctor files moved into dedicated subpackages; shared files remain in the command package",
    },
    "command help/doctor split preflight contracts mismatch",
)
require(
    command_help_doctor_preflight.get("physicalSplitAdmission", {}).get("status")
    == "completed-help-and-doctor-single-family-splits",
    "command help/doctor split preflight physicalSplitAdmission mismatch",
)

command_next_candidate_path = root / "docs" / "reference" / "command-next-family-candidate-refresh.json"
if command_next_candidate_path.is_file():
    command_next_candidate = json.loads(command_next_candidate_path.read_text(encoding="utf-8"))
else:
    command_next_candidate = {}
    missing.append("docs/reference/command-next-family-candidate-refresh.json is missing")
require(
    command_next_candidate.get("schema") == "gofly.command_next_family_candidate_refresh.v1",
    "command next family candidate refresh schema mismatch",
)
require(
    command_next_candidate.get("status") == "completed-candidate-refresh",
    "command next family candidate refresh status must be completed-candidate-refresh",
)
require(
    command_next_candidate.get("acceptanceGate") == "make command-next-family-candidate-refresh-check",
    "command next family candidate refresh acceptanceGate mismatch",
)
require(command_next_candidate.get("planningOnly") is True, "command next family candidate refresh must be planningOnly")
require(command_next_candidate.get("noPhysicalMove") is True, "command next family candidate refresh must forbid physical movement")
selected_candidate = command_next_candidate.get("selectedCandidate") or {}
require(selected_candidate.get("id") == "release", "command next family candidate refresh selectedCandidate mismatch")
require(selected_candidate.get("status") == "candidate-after-json-golden", "command next family candidate refresh selectedCandidate status mismatch")
require(
    selected_candidate.get("files")
    == [
        "release.go",
        "release_contract_checks.go",
        "release_helpers.go",
        "release_local_checks.go",
        "release_output.go",
        "release_test.go",
        "release_types.go",
    ],
    "command next family candidate refresh release files mismatch",
)
require(not (root / "cmd" / "gofly" / "internal" / "command" / "release").exists(), "release subpackage must not exist during P22-15")
require(
    command_next_candidate.get("nextStep", {}).get("id") == "P22-16-command-release-family-preflight",
    "command next family candidate refresh nextStep mismatch",
)

reference_boundary = manifest.get("referenceFileBoundaries") or {}
require(reference_boundary.get("status") == "blocking-contract", "referenceFileBoundaries.status must be blocking-contract")
require(reference_boundary.get("root") == "docs/reference", "referenceFileBoundaries.root must be docs/reference")
require(
    reference_boundary.get("forbidUnknownReferenceFiles") is True,
    "referenceFileBoundaries must forbid unknown reference files",
)
require(
    "Every tracked file under docs/reference" in str(reference_boundary.get("policy") or ""),
    "referenceFileBoundaries policy must describe tracked docs/reference file handling",
)
tracked_reference_files = sorted(
    rel.removeprefix("docs/reference/")
    for rel in subprocess.run(
        ["git", "ls-files", "docs/reference"],
        cwd=root,
        check=True,
        text=True,
        stdout=subprocess.PIPE,
    ).stdout.splitlines()
    if rel.startswith("docs/reference/")
)
reference_families = reference_boundary.get("families") or []
declared_reference_files = []
allowed_reference_categories = {"contract", "evidence", "golden", "matrix", "roadmap"}
for family in reference_families:
    if not isinstance(family, dict):
        missing.append(f"referenceFileBoundaries family must be object: {family!r}")
        continue
    family_id = family.get("id", "")
    files = family.get("files") or []
    require(family_id, "reference family id is required")
    require(family.get("category") in allowed_reference_categories, f"reference family {family_id}: unknown category {family.get('category')!r}")
    require(len(str(family.get("owner") or "").split()) >= 1, f"reference family {family_id}: owner is required")
    require(len(str(family.get("purpose") or "").split()) >= 8, f"reference family {family_id}: purpose must be descriptive")
    require(gate_is_known(str(family.get("gate") or ""), targets), f"reference family {family_id}: gate is not known")
    require(files, f"reference family {family_id}: files are required")
    for filename in files:
        declared_reference_files.append(filename)
        require("/" not in filename, f"reference family {family_id}: file must be relative to docs/reference root: {filename!r}")
        require((root / "docs" / "reference" / filename).is_file(), f"reference family {family_id}: missing docs/reference/{filename}")
require(
    sorted(declared_reference_files) == tracked_reference_files,
    "referenceFileBoundaries must account for every tracked docs/reference file exactly once: "
    f"declared={sorted(declared_reference_files)!r}, actual={tracked_reference_files!r}",
)
require(
    len(declared_reference_files) == len(set(declared_reference_files)),
    "referenceFileBoundaries must not contain duplicate reference files",
)

convergence_path = root / "docs" / "reference" / "project-layout-convergence-p21.json"
if convergence_path.is_file():
    convergence = json.loads(convergence_path.read_text(encoding="utf-8"))
else:
    convergence = {}
    missing.append("docs/reference/project-layout-convergence-p21.json is missing")
require(
    convergence.get("schema") == "gofly.project_layout_convergence_p21.v1",
    "P21 layout convergence schema mismatch",
)
require(convergence.get("status") == "completed", "P21 layout convergence status must be completed")
require(convergence.get("activeBatch") == "GOFLY-P21", "P21 layout convergence activeBatch mismatch")
require(
    convergence.get("acceptanceGate") == "make project-layout-governance-check",
    "P21 layout convergence acceptanceGate mismatch",
)
expected_p21_commits = [
    "03d2afa",
    "b6720dd",
    "d887636",
    "081cdcb",
    "9475780",
    "46e7fcb",
    "bea64f0",
    "31eb2a1",
    "5c8cb4e",
]
round_commits = convergence.get("roundCommits") or []
require(len(round_commits) == 9, "P21 layout convergence must record 9 round commits")
require(
    [item.get("commit") for item in round_commits if isinstance(item, dict)] == expected_p21_commits,
    "P21 layout convergence commit sequence mismatch",
)
for expected_round, item in enumerate(round_commits, start=1):
    if not isinstance(item, dict):
        missing.append(f"P21 layout convergence round commit must be object: {item!r}")
        continue
    require(item.get("round") == expected_round, f"P21 layout convergence round {expected_round}: round number mismatch")
    require(gate_is_known(str(item.get("gate") or ""), targets), f"P21 layout convergence round {expected_round}: gate is not known")
    require(len(str(item.get("title") or "").split()) >= 2, f"P21 layout convergence round {expected_round}: title is required")
    require(len(str(item.get("coverage") or "").split()) >= 10, f"P21 layout convergence round {expected_round}: coverage must be descriptive")
expected_p21_boundaries = {
    "topLevelDirectoryBoundaries",
    "rootFileBoundaries",
    "scriptFamilyBoundaries",
    "referenceFileBoundaries",
    "commandFileFamilies",
    "deployAssetBoundary",
    "ignoredArtifactBoundary",
    "docsTaxonomyBoundaries",
    "examplesGroupingPlan",
    "testNamingBaseline",
}
require(
    set(convergence.get("coveredBoundaries") or []) == expected_p21_boundaries,
    "P21 layout convergence coveredBoundaries mismatch",
)
verification_commands = {item.get("command") for item in convergence.get("verificationGates") or [] if isinstance(item, dict)}
for command in (
    "make project-layout-governance-check",
    "make governance-boundary-inventory-check",
    "make docs-taxonomy-check",
    "make cloud-native-render-check",
    "GOCACHE=$PWD/.tmp-test/gocache GOTMPDIR=$PWD/.tmp-test/gotmp make examples-smoke",
    "git diff --check",
):
    require(command in verification_commands, f"P21 layout convergence verificationGates missing {command!r}")
deferred_paths = {item.get("path") for item in convergence.get("deferredMigrations") or [] if isinstance(item, dict)}
require(
    deferred_paths == {"examples/", "bin/scripts/", "docs/reference/", "cmd/gofly/internal/command/"},
    "P21 layout convergence deferredMigrations mismatch",
)
for item in convergence.get("deferredMigrations") or []:
    if not isinstance(item, dict):
        missing.append(f"P21 deferred migration entry must be object: {item!r}")
        continue
    require(item.get("status") in {"not-moved", "not-split"}, f"P21 deferred migration {item.get('path')}: status mismatch")
    require(len(str(item.get("reason") or "").split()) >= 12, f"P21 deferred migration {item.get('path')}: reason must be descriptive")
    require(len(str(item.get("futurePolicy") or "").split()) >= 12, f"P21 deferred migration {item.get('path')}: futurePolicy must be descriptive")
admission = convergence.get("examplesPhysicalMigrationAdmission") or {}
require(
    admission.get("status") == "blocked-until-ready",
    "P21 examples physical migration admission status mismatch",
)
require(
    "one family" in str(admission.get("policy") or ""),
    "P21 examples physical migration admission policy must require one-family migration",
)
required_admission_gates = {
    "make project-layout-governance-check",
    "make examples-smoke",
    "make examples-copyable-check",
    "make docs-check",
    "git diff --check",
}
require(
    set(admission.get("requiredGates") or []) == required_admission_gates,
    "P21 examples physical migration requiredGates mismatch",
)
required_signals = admission.get("requiredSignals") or []
require(len(required_signals) >= 8, "P21 examples physical migration admission must require at least 8 signals")
for phrase in ("one examples family", "examples/README.md", "go.mod", "rollback note"):
    require(
        any(phrase in str(signal) for signal in required_signals),
        f"P21 examples physical migration admission missing signal phrase {phrase!r}",
    )
runtime_policy = convergence.get("runtimeIgnoredPolicy") or {}
require(runtime_policy.get("status") == "enforced", "P21 runtimeIgnoredPolicy status mismatch")
for path in expected_ignored_artifacts:
    if path != "docs/*":
        require(path in set(runtime_policy.get("ignoredPaths") or []), f"P21 runtimeIgnoredPolicy missing {path}")
require(
    "P22" in str((convergence.get("nextRecommendation") or {}).get("id") or ""),
    "P21 nextRecommendation must point to P22",
)

contract_index = manifest.get("referenceContractIndex") or []
for item in contract_index:
    if not isinstance(item, dict):
        missing.append(f"referenceContractIndex entry must be object: {item!r}")
        continue
    item_id = item.get("id", "<missing>")
    require(item.get("paths"), f"referenceContractIndex {item_id}: paths are required")
    require(gate_is_known(str(item.get("gate") or ""), targets), f"referenceContractIndex {item_id}: gate is not known")
    for rel in item.get("paths") or []:
        require((root / rel).is_file(), f"referenceContractIndex {item_id}: missing {rel}")

baseline = manifest.get("testNamingBaseline") or {}
require(
    "project-specific" in str(baseline.get("forbiddenSuffixPolicy") or ""),
    "testNamingBaseline forbiddenSuffixPolicy must reject project-specific test suffixes",
)
require(
    "underscore-delimited" in str(baseline.get("forbiddenSuffixPolicy") or ""),
    "testNamingBaseline forbiddenSuffixPolicy must reject underscore-delimited test function names",
)
require(baseline.get("currentOccurrenceCount") == 0, "testNamingBaseline currentOccurrenceCount must be 0")
legacy_unit_suffix = "Bits" + "UT"
legacy_bench_suffix = "Bits" + "Bench"
legacy_suffix_pattern = legacy_unit_suffix + "|" + legacy_bench_suffix
rg = subprocess.run(
    [
        "rg",
        "-n",
        legacy_suffix_pattern,
        ".",
        "--glob",
        "!docs/superpowers/**",
        "--glob",
        "!vendor/**",
        "--glob",
        "!docs/reference/project-layout-governance.json",
        "--glob",
        "!bin/scripts/check-project-layout-governance.sh",
    ],
    cwd=root,
    check=False,
    text=True,
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
)
if rg.returncode not in {0, 1}:
    missing.append(f"rg legacy test suffix scan failed: {rg.stderr.strip()}")
else:
    count = len([line for line in rg.stdout.splitlines() if line.strip()])
    expected_count = baseline.get("currentOccurrenceCount")
    require(count == int(expected_count), f"legacy test suffix occurrence count must be 0, got {count}")
    require("not allowed" in str(baseline.get("policy") or ""), "testNamingBaseline policy must reject reintroduction")

underscore_tests = subprocess.run(
    [
        "rg",
        "-n",
        r"^func (Test|Benchmark|Fuzz)[A-Za-z0-9]+_",
        ".",
        "--glob",
        "*_test.go",
        "--glob",
        "!docs/superpowers/**",
        "--glob",
        "!vendor/**",
    ],
    cwd=root,
    check=False,
    text=True,
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
)
if underscore_tests.returncode not in {0, 1}:
    missing.append(f"rg underscore-delimited test scan failed: {underscore_tests.stderr.strip()}")
else:
    require(
        not [line for line in underscore_tests.stdout.splitlines() if line.strip()],
        "underscore-delimited test or benchmark function names are not allowed",
    )

if missing:
    print("project layout governance check failed:", file=sys.stderr)
    for item in missing:
        print(f"- {item}", file=sys.stderr)
    sys.exit(1)

print("project layout governance OK")
PY
