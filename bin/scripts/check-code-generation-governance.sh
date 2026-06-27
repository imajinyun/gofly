#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import os
import pathlib
import re
import subprocess
import sys
import tempfile

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "code-generation-governance.json"
missing = []


def read_text(path):
    if not path.is_file():
        missing.append(f"{path.relative_to(root)} is missing")
        return ""
    return path.read_text(encoding="utf-8")


def require(condition, message):
    if not condition:
        missing.append(message)


try:
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
except FileNotFoundError:
    manifest = {}
    missing.append("docs/reference/code-generation-governance.json is missing")
except json.JSONDecodeError as exc:
    manifest = {}
    missing.append(f"docs/reference/code-generation-governance.json is invalid JSON: {exc}")

require(manifest.get("schema") == "gofly.code_generation_governance.v1", "code generation governance schema mismatch")
require(manifest.get("aiflowTask") == "GOFLY-GOV-10R3-04", "code generation governance aiflowTask mismatch")
require(manifest.get("acceptanceGate") == "make code-generation-governance-check", "code generation governance acceptanceGate mismatch")
require(manifest.get("aggregateGate") == "make generated-upgrade-dry-run-check", "code generation governance aggregateGate mismatch")

makefile = read_text(root / "Makefile")
generator_tests = "\n".join(path.read_text(encoding="utf-8") for path in (root / "cmd" / "gofly" / "internal" / "generator").glob("*_test.go"))
command_tests = "\n".join(path.read_text(encoding="utf-8") for path in (root / "cmd" / "gofly" / "internal" / "command").glob("*_test.go"))
test_corpus = generator_tests + "\n" + command_tests
upgrade_manifest = read_text(root / "docs" / "reference" / "generated-upgrade-dry-run.json")
version_doc = read_text(root / "docs" / "reference" / "generated-version-compat.md")

require("code-generation-governance-check" in makefile, "Makefile must expose code-generation-governance-check")
upgrade_deps = next((line for line in makefile.splitlines() if line.startswith("generated-upgrade-dry-run-check:")), "")
require("code-generation-governance-check" in upgrade_deps, "generated-upgrade-dry-run-check must depend on code-generation-governance-check")
require("generated-output-governance" in upgrade_deps, "generated-upgrade-dry-run-check must retain generated-output-governance dependency")

for rel in manifest.get("sourceCode") or []:
    require((root / rel).exists(), f"code generation source is missing: {rel}")

policy = manifest.get("policy") or {}
for key in (
    "noPathEscape",
    "repeatGenerationMustBeDeterministic",
    "rootModuleDependencyHygiene",
    "generatedProjectDependenciesStayInGeneratedGoMod",
    "upgradeDiffsMustBeClassified",
    "dryRunMustNotWriteRuntimeArtifacts",
):
    require(policy.get(key) is True, f"policy.{key} must be true")

surfaces = manifest.get("surfaces") or []
surface_ids = {item.get("id") for item in surfaces if isinstance(item, dict)}
required_surfaces = {
    "service-scaffold",
    "api-generator",
    "rpc-generator",
    "model-generator",
    "template-sync",
    "plugin-generated-output",
}
require(surface_ids == required_surfaces, f"code generation surfaces mismatch: {sorted(surface_ids)!r}")

for item in surfaces:
    if not isinstance(item, dict):
        missing.append(f"surface entry must be an object: {item!r}")
        continue
    surface = item.get("id", "<missing>")
    for field in ("commands", "risk", "gate", "tests", "evidence"):
        require(item.get(field), f"surface {surface}: {field} is required")
    gate = str(item.get("gate") or "")
    require(gate.startswith("make ") or gate.startswith("go test "), f"surface {surface}: gate must be runnable")
    for test_name in item.get("tests") or []:
        require(test_name in test_corpus, f"surface {surface}: test {test_name} is missing")
    for evidence in item.get("evidence") or []:
        require((root / evidence).exists(), f"surface {surface}: evidence path is missing: {evidence}")

profile_matrix = manifest.get("profileMatrix") or {}
require(profile_matrix.get("source") == "docs/reference/generated-upgrade-dry-run.json", "profileMatrix.source mismatch")
require(profile_matrix.get("versionCompat") == "docs/reference/generated-version-compat.md", "profileMatrix.versionCompat mismatch")
require(set(profile_matrix.get("profiles") or []) == {"old", "current", "future"}, "profileMatrix.profiles must cover old/current/future")
for profile in ("old", "current", "future"):
    require(f'"profile": "{profile}"' in upgrade_manifest, f"generated-upgrade-dry-run.json missing profile {profile!r}")
    require(profile in version_doc, f"generated-version-compat.md missing profile {profile!r}")

diff_categories = set(manifest.get("diffCategories") or [])
required_categories = {
    "deterministic-repeat-generation",
    "compatible-addition",
    "formatting-only",
    "breaking-candidate",
}
require(diff_categories == required_categories, f"diffCategories mismatch: {sorted(diff_categories)!r}")
for category in required_categories:
    require(category in upgrade_manifest, f"generated-upgrade-dry-run.json missing diff category {category!r}")

test_cmd = [
    "go",
    "test",
    "-count=1",
    "./cmd/gofly/internal/generator",
    "./cmd/gofly/internal/command",
    "-run",
    "TestGeneratedFileSafe|TestBuildServiceScaffoldIR.*Profile|TestExecuteAPINew(WithGoZeroCompatibleProfile|UsesConfigProfileDefault|RejectsUnknownProfile)$|TestExecuteAPINewAcceptsGoctlReservedFlags|TestIDLGenerateCommandsEmitJSONEnvelope|TestGenerateModelFromDDLGORMStyle|TestGenerateModelFromDDLGoZeroStyleDoesNotRequireGORM|TestGenerateMongoModelDriverStyle|TestApplyTemplateExtensionRejectsSymlinkTemplate|TestCopyDirRejectsSymlinkSourceEntry|TestPluginResponseWriteFilesRejectsEscapingPaths|TestPluginResponseRejectsSymlinkParentTraversal|TestPluginResponseRejectsSymlinkLeaf|TestPluginSymlinkParentBoundaries|TestAINewGeneratedArtifactsAreDeterministicAndIdempotent|TestAINewGeneratedProjectVerificationMatrix|TestNewServiceGeneratedProjectSmokeMatrix",
]
go_tmp = pathlib.Path(tempfile.mkdtemp(prefix="gofly-codegen-go-"))
go_cache = go_tmp / "gocache"
go_tmpdir = go_tmp / "gotmp"
go_cache.mkdir(parents=True, exist_ok=True)
go_tmpdir.mkdir(parents=True, exist_ok=True)
env = os.environ.copy()
env.setdefault("GOCACHE", str(go_cache))
env.setdefault("GOTMPDIR", str(go_tmpdir))
test = subprocess.run(test_cmd, cwd=root, check=False, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, env=env)
if test.returncode != 0:
    missing.append("targeted code generation governance tests failed:\n" + test.stdout)

if missing:
    print("code generation governance check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("code generation governance ok")
PY
