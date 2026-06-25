#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import pathlib
import sys

checks = {
    pathlib.Path("docs/reference/plugin-conformance.md"): [
        "gofly.plugin_conformance.v1",
        "Publishing contract",
        "registry JSON schema",
        "plugin manifest schema",
        "`name`, `remote`, `version`",
        "`compatibleVersions`, `capabilities`, `permissions`, and",
        "make plugin-conformance-check",
        "go test -C examples/plugin-ecosystem ./...",
        "go run -C examples/plugin-ecosystem .",
        "protocol compatibility",
        "digest provenance",
        "permission rationale",
        "template contract",
        "rollback and failure isolation behavior",
        "digest",
        "least permission",
        "compatibility runner",
        "failure isolation",
        "old protocol",
        "current protocol",
        "future protocol",
        "malicious path",
        "digest mismatch",
        "permission escape",
    ],
    pathlib.Path("examples/plugin-ecosystem/main.go"): [
        "Publishing",
        "publishingSummary",
        "ManifestFields",
        "RegistryFields",
        "RequiredGates",
        "ReleaseNotes",
        "requiresDryRun",
        "digest provenance",
        "old-protocol",
        "current-protocol",
        "future-plus-current",
        "future-only",
        "digest-mismatch",
        "malicious-path",
        "permission-escape",
        "failure-isolation",
    ],
    pathlib.Path("cmd/gofly/internal/generator/plugin.go"): [
        "PluginProtocolSchema",
        "PluginRegistryEntry",
        "validatePluginRegistryChecksum",
        "PluginPermissionWriteRelative",
    ],
    pathlib.Path("cmd/gofly/internal/generator/plugin_test.go"): [
        "TestPluginProtocolCompatibilityMatrix",
        "TestPluginProtocolSchemaContract",
        "TestPluginRegistryIndexValidationAndFiltering",
    ],
    pathlib.Path("examples/plugin-ecosystem/main_test.go"): [
        "Publishing.ManifestFields",
        "Publishing.RequiredGates",
        "Publishing.ReleaseNotes",
    ],
}

missing = []
for path, needles in checks.items():
    if not path.is_file():
        missing.append(f"{path}: file is missing")
        continue
    text = path.read_text(encoding="utf-8")
    for needle in needles:
        if needle not in text:
            missing.append(f"{path}: missing {needle!r}")

if missing:
    print("plugin conformance check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("plugin conformance governance ok")
PY
