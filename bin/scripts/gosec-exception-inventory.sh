#!/usr/bin/env sh
set -eu

root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
out="${GOSEC_INVENTORY_OUT:-}"

python3 - "$root" "$out" <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(sys.argv[1])
out = sys.argv[2]
pattern = re.compile(r"#nosec\s+([A-Z0-9\s]+)(?:--|:)\s*(.*)$")

def classify(file_path, rules, rationale):
    text = f"{file_path} {rationale}".lower()
    if "exec" in text or "command" in text or "plugin" in text or "git" in text or "go install" in text or "protoc" in text:
        trust = "explicit CLI/operator external process boundary"
    elif "template" in text or "generated" in text or "contract" in text or "config" in text or "file" in text or "path" in text:
        trust = "explicit CLI/operator filesystem boundary"
    elif "sql" in text or "table name" in text or "placeholder" in text:
        trust = "validated SQL identifier boundary"
    elif "tls" in text or "oauth" in text or "csrf" in text or "websocket" in text:
        trust = "protocol compatibility or explicit security configuration boundary"
    elif "rand" in text or "jitter" in text or "sampling" in text:
        trust = "non-cryptographic randomness boundary"
    else:
        trust = "documented internal or protocol-required boundary"

    protections = []
    if any(rule in rules for rule in ("G204", "G702")):
        protections.extend(["argv-separated execution", "no shell expansion", "explicit CLI/operator opt-in"])
        if "timeout" in text or "plugin" in text:
            protections.append("bounded execution or output")
    if any(rule in rules for rule in ("G304", "G301", "G306", "G703", "G122")):
        protections.extend(["root/path scope validation", "parent symlink rejection where applicable", "leaf symlink rejection where applicable", "explicit file-mode policy"])
    if any(rule in rules for rule in ("G201", "G202")):
        protections.extend(["identifier validation", "values use placeholders"])
    if "G404" in rules:
        protections.append("non-security randomness only")
    if any(rule in rules for rule in ("G401", "G505", "G117", "G124", "G402", "G101", "G115")):
        protections.append("protocol-required or bounds-checked exception with inline rationale")
    if not protections:
        protections.append("inline rationale documents the false-positive boundary")

    coverage = []
    if "cmd/gofly/internal/generator/plugin.go" in file_path:
        coverage.extend(["cmd/gofly/internal/generator/plugin_test.go", "cmd/gofly/internal/command/idl_test.go"])
    elif "cmd/gofly/internal/generator/service.go" in file_path or "generated_file.go" in file_path:
        coverage.extend(["cmd/gofly/internal/generator/service_test.go", "cmd/gofly/internal/generator/service_scaffold_test.go"])
    elif "cmd/gofly/internal/command/new.go" in file_path:
        coverage.append("cmd/gofly/internal/command/ai_helpers_test.go")
    elif "cmd/gofly/internal/command/idl.go" in file_path:
        coverage.append("cmd/gofly/internal/command/idl_test.go")
    elif "cmd/gofly/internal/command/release.go" in file_path:
        coverage.append("cmd/gofly/internal/command/release_test.go")
    else:
        coverage.append("package-local *_test.go or governance scan")

    helper = "none"
    if any(rule in rules for rule in ("G304", "G301", "G306", "G703", "G122")):
        if "cmd/gofly/internal/generator" in file_path or "cmd/gofly/internal/command/new.go" in file_path:
            helper = "central generated path helper in generated_file.go"
        else:
            helper = "candidate for future root-constrained filesystem helper if writes become generator-scoped"
    elif any(rule in rules for rule in ("G204", "G702")):
        helper = "candidate for explicit command allow-list wrapper if command surface grows"

    return trust, sorted(set(protections)), sorted(set(coverage)), helper

entries = []
for path in sorted(root.rglob("*.go")):
    rel = path.relative_to(root).as_posix()
    if "/vendor/" in f"/{rel}/" or "/testdata/" in f"/{rel}/":
        continue
    for number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), start=1):
        if "#nosec" not in line:
            continue
        match = pattern.search(line)
        if match:
            rules = re.findall(r"G\d+", match.group(1))
            rationale = match.group(2).strip()
        else:
            rules = re.findall(r"G\d+", line)
            rationale = line.split("#nosec", 1)[1].strip(" -:")
        trust, protections, coverage, helper = classify(rel, rules, rationale)
        entries.append({
            "file": rel,
            "line": number,
            "rules": rules,
            "rationale": rationale,
            "trust_boundary": trust,
            "current_protection": protections,
            "coverage_tests": coverage,
            "replaceable_helper": helper,
        })

by_rule = {}
for entry in entries:
    for rule in entry["rules"] or ["unscoped"]:
        by_rule.setdefault(rule, []).append({"file": entry["file"], "line": entry["line"]})

report = {
    "schema": "gofly.gosec_exception_inventory.v1",
    "total_exceptions": len(entries),
    "summary_by_rule": {rule: len(locations) for rule, locations in sorted(by_rule.items())},
    "entries": entries,
}

payload = json.dumps(report, indent=2, sort_keys=True) + "\n"
if out:
    pathlib.Path(out).write_text(payload, encoding="utf-8")
else:
    sys.stdout.write(payload)
PY
