#!/usr/bin/env python3
"""Compile and execute generated API clients with available toolchains."""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import shutil
import subprocess
import sys
import time

LANGUAGES = ("javascript", "typescript", "java", "kotlin", "dart")
TOOLS = {
    "javascript": ("node", ["node", "--version"]),
    "typescript": ("tsc", ["tsc", "--version"]),
    "java": ("javac", ["javac", "-version"]),
    "kotlin": ("kotlinc", ["kotlinc", "-version"]),
    "dart": ("dart", ["dart", "--version"]),
}
MAX_OUTPUT = 32768


def parse_bool(value: str) -> bool:
    normalized = value.strip().lower()
    if normalized in {"1", "true", "yes", "on"}:
        return True
    if normalized in {"0", "false", "no", "off", ""}:
        return False
    raise ValueError(f"invalid boolean value {value!r}")


def bounded(value: str) -> str:
    if len(value) <= MAX_OUTPUT:
        return value
    return value[:MAX_OUTPUT] + f"\n... truncated {len(value) - MAX_OUTPUT} characters"


def run(
    command: list[str],
    *,
    cwd: pathlib.Path,
    timeout: int = 180,
    env_overrides: dict[str, str] | None = None,
) -> dict[str, object]:
    started = time.monotonic()
    env = os.environ.copy()
    if env_overrides:
        env.update(env_overrides)
    try:
        completed = subprocess.run(
            command,
            cwd=cwd,
            env=env,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            timeout=timeout,
            check=False,
        )
        return {
            "command": command,
            "exitCode": completed.returncode,
            "durationMs": round((time.monotonic() - started) * 1000),
            "output": bounded(completed.stdout),
        }
    except subprocess.TimeoutExpired as error:
        output = error.stdout or ""
        if isinstance(output, bytes):
            output = output.decode("utf-8", errors="replace")
        return {
            "command": command,
            "exitCode": None,
            "durationMs": round((time.monotonic() - started) * 1000),
            "output": bounded(output),
            "error": f"timed out after {timeout}s",
        }
    except OSError as error:
        return {
            "command": command,
            "exitCode": None,
            "durationMs": round((time.monotonic() - started) * 1000),
            "output": "",
            "error": str(error),
        }


def detect_tool(language: str, root: pathlib.Path) -> tuple[str | None, dict[str, object] | None]:
    executable, version_command = TOOLS[language]
    resolved = shutil.which(executable)
    if resolved is None:
        return None, None
    version_command = [resolved, *version_command[1:]]
    version = run(version_command, cwd=root, timeout=30)
    if version["exitCode"] != 0:
        return None, version
    return resolved, version


def generate(root: pathlib.Path, work: pathlib.Path, language: str) -> tuple[pathlib.Path, dict[str, object]]:
    output = work / "generated" / language
    output.mkdir(parents=True, exist_ok=True)
    result = run(
        [
            os.environ.get("GO", "go"),
            "run",
            "./cmd/gofly",
            "api",
            "client",
            "--file",
            str(root / "testdata/goctl-api-semantic/contract.api"),
            "--dir",
            str(output),
            "--language",
            language,
            "--base-url",
            "http://127.0.0.1:18080",
        ],
        cwd=root,
    )
    return output, result


def verify_javascript(root: pathlib.Path, output: pathlib.Path, tool: str) -> dict[str, object]:
    source = output / "contract_client.js"
    module = output / "contract_client.mjs"
    shutil.copyfile(source, module)
    return run([tool, str(root / "testdata/api-client-toolchain/javascript-runtime.mjs"), str(module)], cwd=output)


def verify_typescript(output: pathlib.Path, tool: str) -> dict[str, object]:
    return run(
        [
            tool,
            "--noEmit",
            "--strict",
            "--target",
            "ES2022",
            "--module",
            "ES2022",
            "--moduleResolution",
            "bundler",
            "--lib",
            "ES2022,DOM",
            str(output / "contract_client.ts"),
        ],
        cwd=output,
    )


def verify_java(root: pathlib.Path, work: pathlib.Path, output: pathlib.Path, tool: str) -> dict[str, object]:
    classes = work / "classes" / "java"
    classes.mkdir(parents=True, exist_ok=True)
    return run(
        [
            tool,
            "-d",
            str(classes),
            str(root / "testdata/api-client-toolchain/java/com/fasterxml/jackson/databind/ObjectMapper.java"),
            str(output / "APIClient.java"),
        ],
        cwd=output,
    )


def verify_kotlin(root: pathlib.Path, work: pathlib.Path, output: pathlib.Path, tool: str) -> dict[str, object]:
    artifact = work / "classes" / "kotlin.jar"
    artifact.parent.mkdir(parents=True, exist_ok=True)
    return run(
        [
            tool,
            str(root / "testdata/api-client-toolchain/kotlin/Serialization.kt"),
            str(root / "testdata/api-client-toolchain/kotlin/Json.kt"),
            str(output / "APIClient.kt"),
            "-d",
            str(artifact),
        ],
        cwd=output,
    )


def verify_dart(root: pathlib.Path, work: pathlib.Path, output: pathlib.Path, tool: str) -> dict[str, object]:
    project = work / "dart-project"
    (project / "lib").mkdir(parents=True, exist_ok=True)
    shutil.copyfile(output / "contract_client.dart", project / "lib/generated_client.dart")
    pubspec = (
        "name: gofly_api_client_check\n"
        "environment:\n  sdk: '>=3.9.0 <4.0.0'\n"
        "dependencies:\n  http:\n    path: "
        + str(root / "testdata/api-client-toolchain/dart/http")
        + "\n"
    )
    (project / "pubspec.yaml").write_text(pubspec, encoding="utf-8")
    dart_home = work / "dart-home"
    pub_cache = work / "dart-pub-cache"
    dart_home.mkdir(parents=True, exist_ok=True)
    pub_cache.mkdir(parents=True, exist_ok=True)
    dart_env = {
        "HOME": str(dart_home),
        "PUB_CACHE": str(pub_cache),
        "DART_SUPPRESS_ANALYTICS": "true",
    }
    pub_get = run([tool, "pub", "get", "--offline"], cwd=project, env_overrides=dart_env)
    if pub_get["exitCode"] != 0:
        return pub_get
    analyze = run(
        [tool, "analyze", "--fatal-infos", "--fatal-warnings", "lib/generated_client.dart"],
        cwd=project,
        env_overrides=dart_env,
    )
    analyze["setupCommand"] = pub_get["command"]
    analyze["setupOutput"] = pub_get["output"]
    return analyze


def verify(language: str, root: pathlib.Path, work: pathlib.Path, output: pathlib.Path, tool: str) -> dict[str, object]:
    if language == "javascript":
        return verify_javascript(root, output, tool)
    if language == "typescript":
        return verify_typescript(output, tool)
    if language == "java":
        return verify_java(root, work, output, tool)
    if language == "kotlin":
        return verify_kotlin(root, work, output, tool)
    return verify_dart(root, work, output, tool)


def write_report(path: pathlib.Path, report: dict[str, object]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def validate_contract(root: pathlib.Path) -> list[str]:
    errors: list[str] = []
    contract_path = root / "docs/reference/api-client-toolchains.json"
    fixture_path = root / "testdata/goctl-api-semantic/contract.api"
    if not contract_path.is_file():
        return ["docs/reference/api-client-toolchains.json is missing"]
    if not fixture_path.is_file():
        errors.append("testdata/goctl-api-semantic/contract.api is missing")
    try:
        contract = json.loads(contract_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        return [f"cannot read toolchain contract: {error}"]
    if contract.get("schema") != "gofly.api_client_toolchains.v1":
        errors.append("toolchain contract schema mismatch")
    rows = contract.get("languages") or []
    if not isinstance(rows, list):
        return ["toolchain contract languages must be a list"]
    ids = [row.get("id") for row in rows if isinstance(row, dict)]
    if ids != list(LANGUAGES):
        errors.append(f"toolchain languages drifted: got {ids!r}, want {list(LANGUAGES)!r}")
    for row in rows:
        if not isinstance(row, dict):
            errors.append(f"toolchain language row must be an object: {row!r}")
            continue
        language = row.get("id")
        if language in TOOLS and row.get("tool") != TOOLS[language][0]:
            errors.append(f"{language}: tool must be {TOOLS[language][0]}")
        for key in ("ciVersion", "verification", "evidence"):
            if not row.get(key):
                errors.append(f"{language}: {key} is required")
    for relative in (
        "testdata/api-client-toolchain/javascript-runtime.mjs",
        "testdata/api-client-toolchain/java/com/fasterxml/jackson/databind/ObjectMapper.java",
        "testdata/api-client-toolchain/kotlin/Serialization.kt",
        "testdata/api-client-toolchain/kotlin/Json.kt",
        "testdata/api-client-toolchain/dart/http/pubspec.yaml",
        "testdata/api-client-toolchain/dart/http/lib/http.dart",
    ):
        if not (root / relative).is_file():
            errors.append(f"{relative} is missing")
    return errors


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", required=True, type=pathlib.Path)
    parser.add_argument("--work", required=True, type=pathlib.Path)
    parser.add_argument("--report", required=True, type=pathlib.Path)
    parser.add_argument("--language", default="all")
    parser.add_argument("--required", default="false")
    args = parser.parse_args()

    root = args.root.resolve()
    work = args.work.resolve()
    report_path = args.report.resolve()
    try:
        required = parse_bool(args.required)
    except ValueError as error:
        write_report(report_path, {
            "schema": "gofly.api_client_toolchain_report.v1",
            "status": "fail",
            "error": str(error),
            "results": [],
        })
        print(error, file=sys.stderr)
        return 2
    if args.language == "all":
        selected = list(LANGUAGES)
    elif args.language in LANGUAGES:
        selected = [args.language]
    else:
        write_report(report_path, {
            "schema": "gofly.api_client_toolchain_report.v1",
            "required": required,
            "status": "fail",
            "error": f"unsupported API client toolchain language {args.language!r}",
            "results": [],
        })
        print(f"unsupported API client toolchain language {args.language!r}", file=sys.stderr)
        return 2

    contract_errors = validate_contract(root)
    if contract_errors:
        report = {
            "schema": "gofly.api_client_toolchain_report.v1",
            "fixture": "testdata/goctl-api-semantic/contract.api",
            "required": required,
            "selected": selected,
            "status": "fail",
            "contractErrors": contract_errors,
            "results": [],
        }
        write_report(report_path, report)
        for error in contract_errors:
            print(f"api client toolchain contract: {error}", file=sys.stderr)
        return 1
    contract = json.loads((root / "docs/reference/api-client-toolchains.json").read_text(encoding="utf-8"))
    contract_rows = {row["id"]: row for row in contract["languages"]}

    results: list[dict[str, object]] = []
    failed = False
    unavailable = False
    for language in selected:
        contract_row = contract_rows[language]
        entry: dict[str, object] = {
            "language": language,
            "expectedCiVersion": contract_row["ciVersion"],
            "evidence": contract_row["evidence"],
        }
        try:
            output, generation = generate(root, work, language)
        except OSError as error:
            output = work / "generated" / language
            generation = {
                "command": [],
                "exitCode": None,
                "durationMs": 0,
                "output": "",
                "error": str(error),
            }
        entry["generation"] = generation
        if generation["exitCode"] != 0:
            entry["status"] = "fail"
            entry["reason"] = "client generation failed"
            failed = True
            results.append(entry)
            continue
        tool, version = detect_tool(language, root)
        if tool is None:
            entry["status"] = "fail" if required else "unavailable"
            entry["reason"] = f"{TOOLS[language][0]} is not available"
            if version is not None:
                entry["toolProbe"] = version
            unavailable = True
            failed = failed or required
            results.append(entry)
            continue
        entry["tool"] = tool
        entry["toolVersion"] = version
        version_output = str(version.get("output", ""))
        expected_version = str(contract_row["ciVersion"])
        entry["expectedVersionMatched"] = expected_version in version_output
        if required and not entry["expectedVersionMatched"]:
            entry["status"] = "fail"
            entry["reason"] = f"tool version does not match required CI version {expected_version}"
            failed = True
            results.append(entry)
            continue
        try:
            verification = verify(language, root, work, output, tool)
        except (OSError, ValueError) as error:
            verification = {
                "command": [],
                "exitCode": None,
                "durationMs": 0,
                "output": "",
                "error": str(error),
            }
        entry["verification"] = verification
        if verification["exitCode"] == 0:
            entry["status"] = "pass"
        else:
            entry["status"] = "fail"
            entry["reason"] = "toolchain verification failed"
            failed = True
        results.append(entry)

    status = "fail" if failed or (required and unavailable) else ("partial" if unavailable else "pass")
    report = {
        "schema": "gofly.api_client_toolchain_report.v1",
        "contract": "docs/reference/api-client-toolchains.json",
        "fixture": "testdata/goctl-api-semantic/contract.api",
        "required": required,
        "selected": selected,
        "status": status,
        "results": results,
    }
    write_report(report_path, report)
    for entry in results:
        print(f"api client toolchain {entry['language']}: {entry['status']}")
        if entry["status"] == "fail":
            verification = entry.get("verification") or entry.get("generation") or {}
            output = str(verification.get("output", ""))
            if output:
                print(output, file=sys.stderr)
    if required and unavailable:
        print("required API client toolchain is unavailable", file=sys.stderr)
    return 1 if status == "fail" else 0


if __name__ == "__main__":
    raise SystemExit(main())
