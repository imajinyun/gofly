#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
missing = []


def read(path):
    if not path.is_file():
        missing.append(f"{path} is missing")
        return ""
    return path.read_text(encoding="utf-8")


def require(condition, message):
    if not condition:
        missing.append(message)


doc = read(root / "docs" / "reference" / "rest-middleware-profiles.md")
makefile = read(root / "Makefile")
rest_tests = read(root / "rest" / "server_test.go")
app_tests = read(root / "app" / "service_conf_test.go")
generator_tests = read(root / "cmd" / "gofly" / "internal" / "generator" / "service_test.go")
generator_output = read(root / "bin" / "scripts" / "check-generated-output-governance.sh")

for marker in (
    "schema: gofly.rest_middleware_profiles.v1",
    "`minimal`",
    "`standard`",
    "`production`",
    "rest.preset",
    "make rest-profile-check",
):
    require(marker in doc, f"rest middleware profiles doc missing {marker!r}")

targets = set(re.findall(r"^([A-Za-z0-9_-]+):", makefile, re.M))
require("rest-profile-check" in targets, "Makefile must expose rest-profile-check")
require("rest-profile-check" in makefile.split("generated-output-governance:", 1)[1].split("\n", 1)[0], "generated-output-governance must depend on rest-profile-check")
require("check-rest-profiles.sh" in makefile, "Makefile must call check-rest-profiles.sh")

for marker in (
    "TestNewServerMiddlewareProfiles",
    "TestServiceConfRESTConfigPreservesExplicitMiddlewareProfiles",
    "TestGeneratedRESTMiddlewareProfilesByStyle",
):
    require(marker in rest_tests + app_tests + generator_tests, f"missing test marker {marker}")

for marker in (
    "PresetMinimal",
    "PresetStandard",
    "PresetProduction",
):
    require(marker in read(root / "rest" / "config.go"), f"rest config missing {marker}")

require("check-rest-profiles.sh" in generator_output, "generated-output governance must invoke check-rest-profiles.sh")

if missing:
    print("rest profile check failed:", file=sys.stderr)
    for item in missing:
        print(f"- {item}", file=sys.stderr)
    raise SystemExit(1)
print("rest profile check OK")
PY
