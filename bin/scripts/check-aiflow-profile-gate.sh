#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import pathlib
import sys

root = pathlib.Path(".").resolve()
config = root / "aiflow.yaml"
missing = []

if not config.is_file():
    missing.append("aiflow.yaml is missing")
    text = ""
else:
    text = config.read_text(encoding="utf-8")


def require(condition, message):
    if not condition:
        missing.append(message)


profile_prefix = "gofly gateway profile validate"
aggregation_prefix = "gofly gateway aggregation validate"
release_gate = "gofly release check --json"

require("docs/superpowers" in text, "aiflow.yaml must keep docs/superpowers ignored")
require(f"- {profile_prefix}" in text, "aiflow.yaml must allow gateway profile validate command")
require(f"- {aggregation_prefix}" in text, "aiflow.yaml must allow gateway aggregation validate command")
require(f"- {release_gate}" in text, "aiflow.yaml acceptance must include release check JSON gate")

lines = text.splitlines()
sections = {}
path = []
for raw in lines:
    if not raw.strip() or raw.lstrip().startswith("#"):
        continue
    indent = len(raw) - len(raw.lstrip(" "))
    stripped = raw.strip()
    if stripped.endswith(":"):
        level = indent // 2
        path = path[:level] + [stripped[:-1]]
        sections[".".join(path)] = []
        continue
    if stripped.startswith("- "):
        sections.setdefault(".".join(path), []).append(stripped[2:])

require(profile_prefix in sections.get("workspace.command_allowlist", []), "workspace.command_allowlist must include gateway profile validate")
require(aggregation_prefix in sections.get("workspace.command_allowlist", []), "workspace.command_allowlist must include gateway aggregation validate")
require(profile_prefix in sections.get("commands.allowlist", []), "commands.allowlist must include gateway profile validate")
require(aggregation_prefix in sections.get("commands.allowlist", []), "commands.allowlist must include gateway aggregation validate")
require(release_gate in sections.get("commands.release", []), "commands.release must include release check JSON")
require(release_gate in sections.get("acceptance.required_commands", []), "acceptance.required_commands must include release check JSON")

if missing:
    for item in missing:
        print("aiflow profile gate check:", item, file=sys.stderr)
    sys.exit(1)
print("aiflow profile gate check: ok")
PY
