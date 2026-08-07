#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "generated-tier-compatibility.json"
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


def gate_target(gate):
    if not isinstance(gate, str):
        return ""
    if gate.startswith("make "):
        parts = gate.removeprefix("make ").split()
        return parts[0] if parts else ""
    return ""


def require_gate(gate, targets, context):
    target = gate_target(gate)
    require(target, f"{context}: gate must be a make target: {gate!r}")
    if target:
        require(target in targets, f"{context}: gate target {target!r} is missing from Makefile")


if manifest_path.is_file():
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
else:
    manifest = {}
    missing.append("docs/reference/generated-tier-compatibility.json is missing")

makefile = read_text(root / "Makefile")
generated_upgrade = read_text(root / "docs" / "reference" / "generated-upgrade-dry-run.json")
scaffold_compat = read_text(root / "docs" / "reference" / "generated-scaffold-long-term-compatibility.json")
service_layout = read_text(root / "docs" / "reference" / "generated-service-layout.md")
goctl_replay = read_text(root / "docs" / "reference" / "goctl-real-project-replay.json")
targets = make_target_names(makefile)
evidence_text = "\n".join((
    generated_upgrade,
    scaffold_compat,
    service_layout,
    goctl_replay,
    read_text(root / "cmd" / "gofly" / "internal" / "generator" / "service_test.go"),
    read_text(root / "cmd" / "gofly" / "internal" / "generator" / "goctl_replay_test.go"),
    read_text(root / "cmd" / "gofly" / "internal" / "generator" / "plugin_test.go"),
    read_text(root / "cmd" / "gofly" / "internal" / "command" / "idl_test.go"),
    read_text(root / "cmd" / "gofly" / "internal" / "command" / "ai_helpers_test.go"),
))

require(manifest.get("schema") == "gofly.generated_tier_compatibility.v1", "schema must be gofly.generated_tier_compatibility.v1")
require(manifest.get("status") == "blocking", "status must be blocking")
require(manifest.get("acceptanceGate") == "make generated-tier-compatibility-check", "acceptanceGate mismatch")
require("generated-tier-compatibility-check" in targets, "Makefile must expose generated-tier-compatibility-check")
require("generated-tier-compatibility-check" in makefile, "Makefile must call generated-tier-compatibility-check")
require("generated-tier-compatibility-check" in makefile.split("contract-docs-check:", 1)[1].split("\n", 1)[0], "contract-docs-check must depend on generated-tier-compatibility-check")
require("generated-tier-compatibility-check" in makefile.split("generated-upgrade-dry-run-check:", 1)[1].split("\n", 1)[0], "generated-upgrade-dry-run-check must depend on generated-tier-compatibility-check")

for source in manifest.get("sourceOfTruth") or []:
    require((root / source).exists(), f"sourceOfTruth path is missing: {source}")
for source in (
    "docs/reference/generated-service-layout.md",
    "docs/reference/generated-scaffold-long-term-compatibility.json",
    "docs/reference/generated-upgrade-dry-run.json",
    "docs/reference/generated-version-compat.md",
    "docs/reference/goctl-real-project-replay.json",
    "docs/reference/goctl-generator-compatibility.json",
    "testdata/generated-compat/matrix.json",
):
    require(source in set(manifest.get("sourceOfTruth") or []), f"sourceOfTruth missing {source!r}")

required_categories = {
    "deterministic-repeat-generation",
    "compatible-addition",
    "formatting-only",
    "breaking-candidate",
}
tier_policy = manifest.get("tierPolicy") or {}
require(set(tier_policy) == {"tier0", "tier1", "tier2"}, f"tierPolicy keys mismatch: {sorted(tier_policy)!r}")
expected_allowed = {
    "tier0": {"deterministic-repeat-generation", "compatible-addition"},
    "tier1": {"deterministic-repeat-generation", "compatible-addition", "formatting-only"},
    "tier2": required_categories,
}
for tier, expected in expected_allowed.items():
    policy = tier_policy.get(tier) or {}
    require(policy.get("name"), f"{tier}: name is required")
    require(len(str(policy.get("compatibility") or "").split()) >= 12, f"{tier}: compatibility policy must be actionable")
    allowed = set(policy.get("allowedDiffs") or [])
    require(allowed == expected, f"{tier}: allowedDiffs mismatch: {sorted(allowed)!r}")
    require(allowed <= required_categories, f"{tier}: allowedDiffs contain unknown categories")
    blocking = set(policy.get("blockingDiffs") or [])
    if tier in {"tier0", "tier1"}:
        require(blocking == {"breaking-candidate"}, f"{tier}: breaking-candidate must block")
    else:
        require(blocking == set(), "tier2: blockingDiffs must be empty")
    for gate in policy.get("requiredGates") or []:
        require_gate(gate, targets, f"{tier}.requiredGates")
    require(len(str(policy.get("rollbackOrEscalation") or "").split()) >= 12, f"{tier}: rollbackOrEscalation must be actionable")

commands = manifest.get("commandBoundaries") or []
require(len(commands) >= 10, "commandBoundaries must cover at least 10 generated command surfaces")
by_command = {item.get("command"): item for item in commands if isinstance(item, dict)}
for command in (
    "gofly new service",
    "gofly quickstart",
    "gofly new api",
    "gofly api gen",
    "gofly new rpc",
    "gofly rpc gen",
    "gofly model gen",
    "gofly gateway",
    "gofly ai new",
    "gofly template",
    "gofly plugin",
):
    require(command in by_command, f"commandBoundaries missing {command!r}")

for item in commands:
    if not isinstance(item, dict):
        missing.append(f"command boundary must be an object: {item!r}")
        continue
    command = item.get("command", "<missing>")
    tier = item.get("tier")
    require(tier in {"tier0", "tier1", "tier2"}, f"{command}: tier must be tier0, tier1, or tier2")
    for field in ("surface", "stabilityContract", "verification", "requiredArtifacts", "rollbackOrEscalation"):
        require(item.get(field) not in ("", None, []), f"{command}: {field} is required")
    contract = item.get("stabilityContract")
    if contract:
        require((root / contract).exists(), f"{command}: stabilityContract path is missing: {contract}")
    for verification in item.get("verification") or []:
        if verification.startswith("make "):
            require_gate(verification, targets, f"{command}.verification")
        else:
            require(verification in evidence_text, f"{command}: verification marker {verification!r} is not backed by tracked evidence")
    require(len(str(item.get("rollbackOrEscalation") or "").split()) >= 8, f"{command}: rollbackOrEscalation must be actionable")

tier0_commands = {item.get("command") for item in commands if item.get("tier") == "tier0"}
tier1_commands = {item.get("command") for item in commands if item.get("tier") == "tier1"}
tier2_commands = {item.get("command") for item in commands if item.get("tier") == "tier2"}
require({"gofly new service", "gofly quickstart"} <= tier0_commands, "tier0 must include new service and quickstart")
require({"gofly new api", "gofly api gen", "gofly new rpc", "gofly rpc gen", "gofly model gen", "gofly gateway"} <= tier1_commands, "tier1 must include API/RPC/model/gateway generated surfaces")
require({"gofly ai new", "gofly template", "gofly plugin"} <= tier2_commands, "tier2 must include preview AI/template/plugin surfaces")

release_rules = manifest.get("releaseRules") or {}
for field in (
    "tier0BreakingChange",
    "tier1BreakingChange",
    "tier2BreakingChange",
    "rootDependencyPolicy",
    "runtimeArtifactPolicy",
):
    require(len(str(release_rules.get(field) or "").split()) >= 8, f"releaseRules.{field} must be actionable")
require("block release" in release_rules.get("tier0BreakingChange", ""), "tier0BreakingChange must block release")
require("block release" in release_rules.get("tier1BreakingChange", ""), "tier1BreakingChange must block release")
require("generated-only dependencies" in release_rules.get("rootDependencyPolicy", ""), "rootDependencyPolicy must mention generated-only dependencies")
require("must not be committed" in release_rules.get("runtimeArtifactPolicy", ""), "runtimeArtifactPolicy must reject committed runtime output")

for needle in (
    "Tier 0 Golden Path",
    "make generated-service-layout-check",
):
    require(needle in service_layout, f"generated-service-layout.md missing {needle!r}")
for needle in (
    "generated_tier_compatibility",
    "generated-tier-compatibility-check",
):
    require(needle in json.dumps(manifest), f"generated tier manifest missing {needle!r}")

if missing:
    print("generated tier compatibility check failed:", file=sys.stderr)
    for item in missing:
        print(f"- {item}", file=sys.stderr)
    sys.exit(1)

print("generated tier compatibility OK")
PY
