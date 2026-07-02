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
command_files = sorted(path.name for path in command_dir.glob("*.go"))
declared_command_files = []
for family in families:
    if not isinstance(family, dict):
        missing.append(f"commandFileFamilies entry must be object: {family!r}")
        continue
    prefix = family.get("prefix", "")
    explicit_files = family.get("files") or []
    require(prefix or explicit_files, "commandFileFamilies prefix or files are required")
    require(len(str(family.get("domain") or "").split()) >= 3, f"command family {prefix}: domain must be descriptive")
    if prefix:
        matching = [path.name for path in command_dir.glob(f"{prefix}*.go")]
        require(matching, f"command family {prefix}: no files match prefix in cmd/gofly/internal/command")
        require(not explicit_files, f"command family {prefix}: prefix families must not also declare explicit files")
        declared_command_files.extend(matching)
    else:
        require(explicit_files, "explicit command family files are required when prefix is empty")
        for filename in explicit_files:
            require("/" not in filename, f"explicit command file must be relative to command root: {filename!r}")
            require((command_dir / filename).is_file(), f"explicit command file is missing: {filename}")
            declared_command_files.append(filename)
require(
    sorted(declared_command_files) == command_files,
    "commandFileFamilies must account for every cmd/gofly/internal/command/*.go file exactly once: "
    f"declared={sorted(declared_command_files)!r}, actual={command_files!r}",
)
require(
    len(declared_command_files) == len(set(declared_command_files)),
    "commandFileFamilies must not contain duplicate command files",
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
