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

examples_plan = manifest.get("examplesGroupingPlan") or {}
require(examples_plan.get("status") == "planned-only", "examplesGroupingPlan.status must be planned-only")
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
