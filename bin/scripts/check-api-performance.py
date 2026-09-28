#!/usr/bin/env python3
"""Validate and run the pinned goctl API performance comparison."""

from __future__ import annotations

import argparse
import copy
from datetime import datetime, timezone
import hashlib
import json
import os
import platform
from pathlib import Path, PurePosixPath
import re
import shutil
import statistics
import subprocess
import sys
import tempfile
import time


ROOT = Path(__file__).resolve().parents[2]
DEFAULT_POLICY = ROOT / "bench/api-performance-policy.json"
PINNED_GOCTL_COMMIT = "84c92d710b9f2ae11c3cbcee242cea40eec42e70"
POLICY_SCHEMA = "gofly.api_performance_policy.v2"
EVIDENCE_SCHEMA = "gofly.api_performance_evidence.v2"
FIXTURE_SCHEMA = "gofly.api_performance_fixtures.v1"
ALLOWED_CLASSIFICATIONS = {"potential-comparable", "unsupported"}
REQUIRED_POTENTIAL_SURFACES = {"rest-generation"}
REQUIRED_UNSUPPORTED_SURFACES = {
    "parse-format",
    "openapi-documentation",
    "openapi-v3",
}
ALLOWED_PLACEHOLDERS = {"binary", "fixture", "fixture_dir", "output_dir"}
REQUIRED_CLIENT_LANGUAGES = ("javascript", "typescript", "java", "kotlin", "dart")
REQUIRED_BENCHMARK_SUITES = {
    "BenchmarkAPIParse": "parse",
    "BenchmarkAPIGenerate": "rest-generation",
    "BenchmarkAPIOpenAPIExport": "openapi-export",
    "BenchmarkAPIOpenAPIImport": "openapi-import",
}
BENCHMARK_LINE = re.compile(r"^(?P<benchmark>BenchmarkAPI\S+)-[0-9]+\s+")
METRIC_PATTERNS = {
    "medianNsPerOp": re.compile(r"(?:^|\s)([0-9]+(?:\.[0-9]+)?)\s+ns/op(?:\s|$)"),
    "allocatedBytesPerOp": re.compile(r"(?:^|\s)([0-9]+(?:\.[0-9]+)?)\s+B/op(?:\s|$)"),
    "allocsPerOp": re.compile(r"(?:^|\s)([0-9]+(?:\.[0-9]+)?)\s+allocs/op(?:\s|$)"),
    "inputBytesPerOp": re.compile(r"(?:^|\s)([0-9]+(?:\.[0-9]+)?)\s+input-bytes/op(?:\s|$)"),
    "outputBytesPerOp": re.compile(r"(?:^|\s)([0-9]+(?:\.[0-9]+)?)\s+bytes/op(?:\s|$)"),
    "filesPerOp": re.compile(r"(?:^|\s)([0-9]+(?:\.[0-9]+)?)\s+files/op(?:\s|$)"),
}


class ContractError(ValueError):
    """Raised when policy, fixture, or evidence violates the contract."""


def read_json(path: Path) -> dict:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ContractError(f"cannot read JSON {path}: {exc}") from exc
    if not isinstance(value, dict):
        raise ContractError(f"JSON root must be an object: {path}")
    return value


def local_relative_path(value: object, field: str) -> PurePosixPath:
    if not isinstance(value, str) or not value.strip():
        raise ContractError(f"{field} must be a non-empty relative path")
    if "\\" in value:
        raise ContractError(f"{field} must use slash-separated repository paths")
    path = PurePosixPath(value)
    if path.is_absolute() or any(part in {"", ".", ".."} for part in path.parts):
        raise ContractError(f"{field} must stay below the repository root: {value!r}")
    return path


def resolve_repo_path(root: Path, value: object, field: str, *, must_exist: bool) -> Path:
    relative = local_relative_path(value, field)
    root_resolved = root.resolve()
    candidate = root_resolved.joinpath(*relative.parts)
    if must_exist and not candidate.is_file():
        raise ContractError(f"{field} does not name a file: {relative.as_posix()}")
    cursor = root_resolved
    for part in relative.parts:
        cursor = cursor / part
        if cursor.exists() and cursor.is_symlink():
            raise ContractError(f"{field} crosses a symlink: {relative.as_posix()}")
    try:
        candidate.resolve(strict=must_exist).relative_to(root_resolved)
    except (OSError, ValueError) as exc:
        raise ContractError(f"{field} escapes the repository root: {relative.as_posix()}") from exc
    return candidate


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ContractError(message)


def validate_argv_template(value: object, field: str) -> None:
    require(isinstance(value, list) and value, f"{field} must be a non-empty argv array")
    require(all(isinstance(part, str) and part for part in value), f"{field} must contain non-empty strings")
    require(value[0] == "{binary}", f"{field} must start with {{binary}}")
    for part in value:
        if part.startswith("{") and part.endswith("}"):
            placeholder = part[1:-1]
            require(placeholder in ALLOWED_PLACEHOLDERS, f"{field} has unsupported placeholder {part}")


def validate_policy(policy: dict, root: Path, *, require_fixture: bool) -> Path:
    require(policy.get("schema") == POLICY_SCHEMA, f"policy schema must be {POLICY_SCHEMA}")
    require(policy.get("acceptanceGate") == "make api-performance-check", "acceptanceGate mismatch")

    oracle = policy.get("oracle") or {}
    require(oracle.get("repository") == "zeromicro/go-zero", "oracle.repository mismatch")
    require(oracle.get("commit") == PINNED_GOCTL_COMMIT, "oracle.commit must stay pinned")
    require(oracle.get("toolPath") == "tools/goctl", "oracle.toolPath mismatch")

    measurement = policy.get("measurement") or {}
    minimum = measurement.get("minimumPublishableSamples")
    default = measurement.get("defaultSamples")
    require(isinstance(minimum, int) and minimum >= 10, "minimumPublishableSamples must be at least 10")
    require(isinstance(default, int) and default >= minimum, "defaultSamples must meet the publishable minimum")
    require(measurement.get("ordering") == "alternating", "measurement.ordering must be alternating")
    require(measurement.get("sameRunner") is True, "measurement.sameRunner must be true")
    require(measurement.get("sameGoToolchain") is True, "measurement.sameGoToolchain must be true")
    require(measurement.get("prebuildEachBinaryOnce") is True, "both binaries must be prebuilt exactly once")
    require(measurement.get("singleSampleMode") == "smoke-only-non-publishable", "single-sample policy mismatch")
    require(isinstance(measurement.get("commandTimeoutSeconds"), int) and measurement["commandTimeoutSeconds"] > 0, "command timeout must be positive")
    require(isinstance(measurement.get("benchmarkTimeoutSeconds"), int) and measurement["benchmarkTimeoutSeconds"] >= 600, "benchmark timeout must be at least 600 seconds")
    require(isinstance(measurement.get("outputLimitBytes"), int) and measurement["outputLimitBytes"] >= 1024, "output limit is too small")

    fixture = policy.get("fixture") or {}
    manifest_path = resolve_repo_path(root, fixture.get("manifest"), "fixture.manifest", must_exist=require_fixture)
    require(fixture.get("schema") == FIXTURE_SCHEMA, f"fixture.schema must be {FIXTURE_SCHEMA}")
    require(fixture.get("digest") == "sha256", "fixture.digest must be sha256")

    surfaces = policy.get("surfaces")
    require(isinstance(surfaces, list) and surfaces, "surfaces must be a non-empty list")
    ids: set[str] = set()
    potential: set[str] = set()
    unsupported: set[str] = set()
    for index, surface in enumerate(surfaces):
        field = f"surfaces[{index}]"
        require(isinstance(surface, dict), f"{field} must be an object")
        surface_id = surface.get("id")
        require(isinstance(surface_id, str) and surface_id, f"{field}.id is required")
        require(surface_id not in ids, f"duplicate surface id: {surface_id}")
        ids.add(surface_id)
        classification = surface.get("classification")
        require(classification in ALLOWED_CLASSIFICATIONS, f"{field}.classification is invalid")
        require(surface.get("latencyMode") == "report-only", f"{surface_id} latency must remain report-only")
        commands = surface.get("commands") or {}
        if classification == "potential-comparable":
            potential.add(surface_id)
            require(set(commands) == {"gofly", "goctl"}, f"{surface_id} must define both command templates")
            validate_argv_template(commands["gofly"], f"{field}.commands.gofly")
            validate_argv_template(commands["goctl"], f"{field}.commands.goctl")
            require(not surface.get("unsupportedReason"), f"{surface_id} cannot have unsupportedReason")
        else:
            unsupported.add(surface_id)
            require(not commands, f"unsupported surface {surface_id} must not define commands")
            reason = surface.get("unsupportedReason")
            require(isinstance(reason, str) and len(reason.split()) >= 5, f"unsupported surface {surface_id} needs an actionable reason")

    require(potential == REQUIRED_POTENTIAL_SURFACES, "only REST generation may be potential-comparable")
    require(REQUIRED_UNSUPPORTED_SURFACES <= unsupported, "known unsupported API surfaces are not explicit")

    latency = policy.get("crossFrameworkLatency") or {}
    require(latency.get("mode") == "report-only", "cross-framework latency must remain report-only")
    require(latency.get("blocking") is False, "cross-framework latency cannot block")

    benchmarks = policy.get("goflyBenchmarks") or {}
    require(benchmarks.get("framework") == "gofly", "gofly benchmark framework mismatch")
    require(benchmarks.get("mode") == "publishable-when-complete", "gofly benchmark mode mismatch")
    require(benchmarks.get("minimumSamples") == minimum, "gofly benchmark sample minimum mismatch")
    require(benchmarks.get("package") == "./cmd/gofly/internal/generator", "gofly benchmark package mismatch")
    require(
        benchmarks.get("benchmark") == "^BenchmarkAPI(Parse|Generate|OpenAPIExport|OpenAPIImport|ClientGenerate)$",
        "gofly benchmark pattern mismatch",
    )
    require(benchmarks.get("expectedRowCount") == 27, "gofly benchmark row count must remain 27")
    require(tuple(benchmarks.get("clientLanguages") or ()) == REQUIRED_CLIENT_LANGUAGES, "gofly benchmark client languages mismatch")

    allocation = policy.get("goflyAllocation") or {}
    require(allocation.get("mode") == "blocking-when-available", "gofly allocation mode mismatch")
    require(allocation.get("framework") == "gofly", "only gofly allocation can be blocking")
    require(allocation.get("minimumSamples") >= 10, "gofly allocation blocking requires at least 10 samples")
    require(allocation.get("missingMetricClassification") == "unsupported", "missing allocations must be explicit")
    require(allocation.get("package") == "./cmd/gofly/internal/generator", "gofly allocation package mismatch")
    require(allocation.get("benchmark") == "^BenchmarkAPIGenerate$", "gofly allocation benchmark mismatch")
    budgets = allocation.get("budgets")
    require(isinstance(budgets, list) and len(budgets) == 3, "gofly allocation budgets must cover all three fixtures")
    budget_fixtures: set[str] = set()
    for index, budget in enumerate(budgets):
        require(isinstance(budget, dict), f"goflyAllocation.budgets[{index}] must be an object")
        fixture_name = budget.get("fixture")
        require(isinstance(fixture_name, str) and fixture_name, f"goflyAllocation.budgets[{index}].fixture is required")
        require(fixture_name not in budget_fixtures, f"duplicate allocation budget for {fixture_name}")
        budget_fixtures.add(fixture_name)
        for field in ("maxBytesPerOp", "maxAllocsPerOp"):
            value = budget.get(field)
            require(isinstance(value, (int, float)) and value >= 0, f"{fixture_name} {field} must be non-negative")
    require(budget_fixtures == {"small", "medium", "large"}, "gofly allocation budgets must cover small, medium, and large")

    evidence = policy.get("evidence") or {}
    resolve_repo_path(root, evidence.get("trackedPath"), "evidence.trackedPath", must_exist=False)
    required_fields = set(evidence.get("requiredFields") or [])
    require(
        {
            "sha",
            "goVersion",
            "os",
            "arch",
            "cpu",
            "fixtureDigest",
            "argv",
            "sampleCount",
            "median",
            "allocatedBytesPerOp",
            "allocsPerOp",
            "inputBytesPerOp",
            "outputBytesPerOp",
            "filesPerOp",
            "exitCode",
            "classification",
        }
        <= required_fields,
        "evidence.requiredFields is incomplete",
    )
    return manifest_path


def validate_sample_mode(policy: dict, sample_count: int, *, publish: bool, smoke: bool) -> None:
    require(sample_count > 0, "sample count must be positive")
    minimum = policy["measurement"]["minimumPublishableSamples"]
    if smoke:
        require(sample_count == 1, "smoke mode must use exactly one sample")
        require(not publish, "smoke evidence cannot be published")
        return
    require(sample_count >= minimum, f"publishable runs require at least {minimum} samples")


def alternating_schedule(sample_count: int, surface_ids: list[str]) -> list[dict]:
    schedule = []
    for round_number in range(1, sample_count + 1):
        framework_order = ["gofly", "goctl"] if round_number % 2 else ["goctl", "gofly"]
        for surface_id in surface_ids:
            for framework in framework_order:
                schedule.append({"round": round_number, "surface": surface_id, "framework": framework})
    return schedule


def safe_child(root: Path, relative_value: object, field: str, *, must_exist: bool = True) -> Path:
    relative = local_relative_path(relative_value, field)
    root_resolved = root.resolve()
    candidate = root_resolved.joinpath(*relative.parts)
    cursor = root_resolved
    for part in relative.parts:
        cursor = cursor / part
        if cursor.exists() and cursor.is_symlink():
            raise ContractError(f"{field} crosses a symlink: {relative.as_posix()}")
    if must_exist and not candidate.is_file():
        raise ContractError(f"{field} does not name a file: {relative.as_posix()}")
    try:
        candidate.resolve(strict=must_exist).relative_to(root_resolved)
    except (OSError, ValueError) as exc:
        raise ContractError(f"{field} escapes fixture root: {relative.as_posix()}") from exc
    return candidate


def api_imports(source: bytes) -> list[str]:
    imports: list[str] = []
    in_block = False
    for raw_line in source.decode("utf-8").splitlines():
        line = raw_line.strip()
        if not in_block and line.startswith("import ("):
            in_block = True
            continue
        if in_block:
            if line == ")":
                in_block = False
                continue
            match = re.fullmatch(r'"([^"\n]+)"', line)
            if match:
                imports.append(match.group(1))
            continue
        match = re.fullmatch(r'import\s+"([^"\n]+)"', line)
        if match:
            imports.append(match.group(1))
    require(not in_block, "fixture contains an unterminated import block")
    return imports


def fixture_graph(fixture_root: Path, entry: str) -> tuple[dict[str, bytes], int, str]:
    files: dict[str, bytes] = {}
    import_count = 0

    def visit(relative: str) -> None:
        nonlocal import_count
        path = safe_child(fixture_root, relative, "fixture import")
        rel = path.relative_to(fixture_root.resolve()).as_posix()
        if rel in files:
            return
        require(path.suffix == ".api", f"fixture import must reference .api file: {rel}")
        data = path.read_bytes()
        files[rel] = data
        imports = api_imports(data)
        import_count += len(imports)
        for imported in imports:
            imported_path = (PurePosixPath(rel).parent / PurePosixPath(imported)).as_posix()
            visit(imported_path)

    visit(entry)
    digest = hashlib.sha256()
    for relative in sorted(files):
        digest.update(relative.encode("utf-8"))
        digest.update(b"\0")
        digest.update(files[relative])
        digest.update(b"\0")
    return files, import_count, digest.hexdigest()


def validate_fixture_manifest(manifest_path: Path) -> tuple[dict, list[dict]]:
    manifest = read_json(manifest_path)
    require(manifest.get("schema") == FIXTURE_SCHEMA, f"fixture manifest schema must be {FIXTURE_SCHEMA}")
    raw_fixtures = manifest.get("fixtures")
    require(isinstance(raw_fixtures, list) and raw_fixtures, "fixture manifest must contain fixtures")
    fixture_root = manifest_path.parent.resolve()
    fixtures: list[dict] = []
    names: set[str] = set()
    routes: set[int] = set()
    for index, raw in enumerate(raw_fixtures):
        field = f"fixtures[{index}]"
        require(isinstance(raw, dict), f"{field} must be an object")
        name = raw.get("name")
        require(isinstance(name, str) and re.fullmatch(r"[a-z0-9][a-z0-9-]*", name) is not None, f"{field}.name is invalid")
        require(name not in names, f"duplicate fixture name: {name}")
        names.add(name)
        route_count = raw.get("routeCount")
        require(isinstance(route_count, int) and route_count > 0, f"{field}.routeCount must be positive")
        require(route_count not in routes, f"duplicate fixture routeCount: {route_count}")
        routes.add(route_count)
        require(isinstance(raw.get("typeCount"), int) and raw["typeCount"] > 0, f"{field}.typeCount must be positive")
        require(isinstance(raw.get("importCount"), int) and raw["importCount"] >= 0, f"{field}.importCount is invalid")
        require(isinstance(raw.get("expectedGeneratedFileCount"), int) and raw["expectedGeneratedFileCount"] > 0, f"{field}.expectedGeneratedFileCount must be positive")
        entry = local_relative_path(raw.get("file"), f"{field}.file").as_posix()
        files, import_count, digest = fixture_graph(fixture_root, entry)
        require(import_count == raw["importCount"], f"{name} importCount mismatch: got {import_count}, want {raw['importCount']}")
        require(digest == raw.get("sha256"), f"{name} sha256 mismatch: got {digest}, want {raw.get('sha256')}")
        fixtures.append({**raw, "file": entry, "graph": files, "fixtureDigest": digest})
    return manifest, fixtures


def run_command(argv: list[str], cwd: Path, env: dict[str, str], timeout: int, output_limit: int) -> dict:
    started = time.perf_counter_ns()
    timed_out = False
    with tempfile.TemporaryFile() as output:
        try:
            completed = subprocess.run(
                argv,
                cwd=cwd,
                env=env,
                stdin=subprocess.DEVNULL,
                stdout=output,
                stderr=subprocess.STDOUT,
                check=False,
                timeout=timeout,
            )
            exit_code = completed.returncode
        except subprocess.TimeoutExpired:
            exit_code = 124
            timed_out = True
        duration_ns = time.perf_counter_ns() - started
        output.seek(0)
        captured = output.read(output_limit + 1)
    truncated = len(captured) > output_limit
    captured = captured[:output_limit]
    return {
        "argv": argv,
        "cwd": str(cwd),
        "durationNs": duration_ns,
        "exitCode": exit_code,
        "timedOut": timed_out,
        "output": captured.decode("utf-8", errors="replace"),
        "outputTruncated": truncated,
    }


def command_evidence(result: dict, *, include_duration: bool = True) -> dict:
    evidence = {
        "argv": result["argv"],
        "cwd": result["cwd"],
        "exitCode": result["exitCode"],
        "timedOut": result["timedOut"],
        "output": result["output"],
        "outputTruncated": result["outputTruncated"],
    }
    if include_duration:
        evidence["durationNs"] = result["durationNs"]
    return evidence


def git_sha(repo: Path, env: dict[str, str], timeout: int, output_limit: int) -> tuple[str | None, dict]:
    result = run_command(["git", "-C", str(repo), "rev-parse", "HEAD"], repo, env, timeout, output_limit)
    sha = result["output"].strip() if result["exitCode"] == 0 else None
    return sha, command_evidence(result, include_duration=False)


def cpu_name() -> str:
    value = platform.processor().strip()
    if value:
        return value
    if sys.platform == "darwin":
        result = subprocess.run(
            ["sysctl", "-n", "machdep.cpu.brand_string"],
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            check=False,
        )
        if result.returncode == 0 and result.stdout.strip():
            return result.stdout.strip()
    cpuinfo = Path("/proc/cpuinfo")
    if cpuinfo.is_file():
        for line in cpuinfo.read_text(encoding="utf-8", errors="replace").splitlines():
            if line.lower().startswith("model name") and ":" in line:
                return line.split(":", 1)[1].strip()
    return "unknown"


def resolve_gozero_root(policy: dict) -> Path:
    variable = policy["oracle"]["checkoutEnvironment"]
    configured = os.environ.get(variable, "").strip()
    candidates = [Path(configured)] if configured else [ROOT / "_reference/gozero", ROOT.parent / "gozero"]
    for candidate in candidates:
        resolved = candidate.expanduser().resolve()
        if (resolved / "go.mod").is_file() and (resolved / policy["oracle"]["toolPath"] / "go.mod").is_file():
            return resolved
    if configured:
        raise ContractError(f"{variable} is not a go-zero checkout: {configured}")
    raise ContractError(f"pinned go-zero checkout is missing; set {variable} or check out _reference/gozero")


def write_graph(destination: Path, graph: dict[str, bytes]) -> None:
    destination.mkdir(parents=True, exist_ok=False)
    for relative, data in graph.items():
        target = destination.joinpath(*PurePosixPath(relative).parts)
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)


def prepare_invocation(work: Path, fixture: dict, framework: str, sequence: str, module: str) -> tuple[Path, Path]:
    invocation = work / sequence / framework
    source = invocation / "source"
    output = invocation / "output"
    write_graph(source, fixture["graph"])
    output.mkdir(parents=True, exist_ok=False)
    (output / "go.mod").write_text(f"module {module}\n\ngo 1.26.7\n", encoding="utf-8")
    return source, output


def render_argv(template: list[str], values: dict[str, str]) -> list[str]:
    argv = []
    for part in template:
        if part.startswith("{") and part.endswith("}"):
            key = part[1:-1]
            require(key in values, f"missing argv placeholder: {part}")
            argv.append(values[key])
        else:
            argv.append(part)
    return argv


def output_snapshot(root: Path) -> dict:
    files: list[tuple[str, bytes]] = []
    for path in sorted(root.rglob("*")):
        if path.is_symlink():
            raise ContractError(f"generated output contains a symlink: {path.relative_to(root)}")
        if path.is_file():
            files.append((path.relative_to(root).as_posix(), path.read_bytes()))
    digest = hashlib.sha256()
    for relative, data in files:
        digest.update(relative.encode("utf-8"))
        digest.update(b"\0")
        digest.update(data)
        digest.update(b"\0")
    generated = [relative for relative, _ in files if relative != "go.mod"]
    return {
        "fileCount": len(generated),
        "goFileCount": sum(relative.endswith(".go") for relative in generated),
        "sha256": digest.hexdigest(),
        "preexistingFiles": ["go.mod"],
    }


def run_rest_invocation(
    policy: dict,
    surface: dict,
    fixture: dict,
    framework: str,
    binary: Path,
    work: Path,
    sequence: str,
    env: dict[str, str],
) -> dict:
    source, output = prepare_invocation(
        work,
        fixture,
        framework,
        sequence,
        surface["freshOutput"]["goModule"],
    )
    fixture_path = source.joinpath(*PurePosixPath(fixture["file"]).parts)
    argv = render_argv(
        surface["commands"][framework],
        {
            "binary": str(binary),
            "fixture": str(fixture_path),
            "fixture_dir": str(source),
            "output_dir": str(output),
        },
    )
    result = run_command(
        argv,
        ROOT,
        env,
        policy["measurement"]["commandTimeoutSeconds"],
        policy["measurement"]["outputLimitBytes"],
    )
    result["outputSnapshot"] = output_snapshot(output)
    _, _, digest_after = fixture_graph(source, fixture["file"])
    result["fixtureDigestAfter"] = digest_after
    return result


def median(values: list[int | float]) -> int | float | None:
    if not values:
        return None
    value = statistics.median(values)
    return int(value) if float(value).is_integer() else value


def benchmark_row_contracts(fixtures: list[dict]) -> list[dict]:
    contracts = []
    for benchmark, surface in REQUIRED_BENCHMARK_SUITES.items():
        for fixture in fixtures:
            contracts.append({
                "benchmark": f"{benchmark}/routes={fixture['routeCount']}",
                "suite": benchmark,
                "surface": surface,
                "fixture": fixture["name"],
                "fixtureDigest": fixture["fixtureDigest"],
                "language": None,
                "requiredMetrics": [
                    "medianNsPerOp",
                    "allocatedBytesPerOp",
                    "allocsPerOp",
                    "inputBytesPerOp" if benchmark == "BenchmarkAPIParse" else "outputBytesPerOp",
                    *([] if benchmark == "BenchmarkAPIParse" else ["filesPerOp"]),
                ],
            })
    for language in REQUIRED_CLIENT_LANGUAGES:
        for fixture in fixtures:
            contracts.append({
                "benchmark": f"BenchmarkAPIClientGenerate/{language}/routes={fixture['routeCount']}",
                "suite": "BenchmarkAPIClientGenerate",
                "surface": "client-generation",
                "fixture": fixture["name"],
                "fixtureDigest": fixture["fixtureDigest"],
                "language": language,
                "requiredMetrics": [
                    "medianNsPerOp",
                    "allocatedBytesPerOp",
                    "allocsPerOp",
                    "outputBytesPerOp",
                    "filesPerOp",
                ],
            })
    return contracts


def parse_benchmark_output(output: str, fixtures: list[dict]) -> dict[str, dict[str, list[float]]]:
    contracts = benchmark_row_contracts(fixtures)
    samples: dict[str, dict[str, list[float]]] = {
        contract["benchmark"]: {metric: [] for metric in METRIC_PATTERNS} for contract in contracts
    }
    for line in output.splitlines():
        match = BENCHMARK_LINE.match(line)
        if not match:
            continue
        benchmark = match.group("benchmark")
        if benchmark not in samples:
            continue
        for metric, pattern in METRIC_PATTERNS.items():
            metric_match = pattern.search(line)
            if metric_match:
                samples[benchmark][metric].append(float(metric_match.group(1)))
    return samples


def observed_benchmark_rows(output: str) -> set[str]:
    return {
        match.group("benchmark")
        for line in output.splitlines()
        if (match := BENCHMARK_LINE.match(line)) is not None
    }


def metric_samples(metrics: dict[str, list[float]], count: int) -> list[dict]:
    return [
        {
            metric: values[index] if index < len(values) else None
            for metric, values in metrics.items()
        }
        for index in range(count)
    ]


def gofly_benchmark_evidence(
    policy: dict,
    fixtures: list[dict],
    env: dict[str, str],
    *,
    samples: int,
    allow_blocking: bool,
) -> tuple[dict, bool]:
    benchmark_policy = policy["goflyBenchmarks"]
    allocation = policy["goflyAllocation"]
    count = samples
    argv = [
        env.get("GO", "go"),
        "test",
        "-run",
        "^$",
        "-bench",
        benchmark_policy["benchmark"],
        "-benchmem",
        "-count",
        str(count),
        benchmark_policy["package"],
    ]
    result = run_command(
        argv,
        ROOT,
        env,
        policy["measurement"]["benchmarkTimeoutSeconds"],
        max(policy["measurement"]["outputLimitBytes"], 1_048_576),
    )
    parsed = parse_benchmark_output(result["output"], fixtures)
    expected_rows = set(parsed)
    observed_rows = observed_benchmark_rows(result["output"])
    unknown_rows = sorted(observed_rows - expected_rows)
    missing_rows = sorted(expected_rows - observed_rows)
    budgets = {item["fixture"]: item for item in allocation["budgets"]}
    rows = []
    blocking_failure = (
        result["exitCode"] != 0
        or result["outputTruncated"]
        or bool(unknown_rows)
        or bool(missing_rows)
    )
    for contract in benchmark_row_contracts(fixtures):
        metrics = parsed[contract["benchmark"]]
        required_metrics = contract["requiredMetrics"]
        sample_counts = {metric: len(metrics[metric]) for metric in required_metrics}
        available = all(sample_counts[metric] == count for metric in required_metrics)
        budget = budgets.get(contract["fixture"]) if contract["suite"] == "BenchmarkAPIGenerate" else None
        row_sample_count = min(sample_counts.values()) if sample_counts else 0
        row = {
            "benchmark": contract["benchmark"],
            "suite": contract["suite"],
            "surface": contract["surface"],
            "fixture": contract["fixture"],
            "fixtureDigest": contract["fixtureDigest"],
            "language": contract["language"],
            "sampleCount": row_sample_count,
            "medianNsPerOp": median(metrics["medianNsPerOp"]),
            "allocatedBytesPerOp": median(metrics["allocatedBytesPerOp"]),
            "allocsPerOp": median(metrics["allocsPerOp"]),
            "inputBytesPerOp": median(metrics["inputBytesPerOp"]),
            "outputBytesPerOp": median(metrics["outputBytesPerOp"]),
            "filesPerOp": median(metrics["filesPerOp"]),
            "samples": metric_samples(metrics, max((len(values) for values in metrics.values()), default=0)),
            "exitCode": result["exitCode"],
            "classification": "measurement-failure" if not available else "publishable",
            "budget": budget,
        }
        if not available:
            row["reason"] = f"Expected exactly {count} samples for every required metric; got {sample_counts}."
            blocking_failure = True
        elif budget is not None and allow_blocking:
            exceeded = row["allocatedBytesPerOp"] > budget["maxBytesPerOp"] or row["allocsPerOp"] > budget["maxAllocsPerOp"]
            row["classification"] = "blocking-regression" if exceeded else "blocking-pass"
            blocking_failure = blocking_failure or exceeded
        elif budget is not None:
            row["classification"] = "smoke-only-non-blocking"
        elif not allow_blocking:
            row["classification"] = "smoke-only-non-publishable"
        rows.append(row)
    evidence = command_evidence(result, include_duration=False)
    evidence["expectedRowCount"] = benchmark_policy["expectedRowCount"]
    evidence["rowCount"] = len(observed_rows & expected_rows)
    evidence["unknownRows"] = unknown_rows
    evidence["missingRows"] = missing_rows
    evidence["sampleCount"] = min((row["sampleCount"] for row in rows), default=0)
    evidence["rows"] = rows
    if blocking_failure:
        evidence["classification"] = "blocking-failure"
    elif allow_blocking:
        evidence["classification"] = "publishable"
    else:
        evidence["classification"] = "smoke-only-non-publishable"
    return evidence, blocking_failure


def allocation_evidence_from_benchmarks(benchmarks: dict) -> dict:
    rows = []
    for benchmark_row in benchmarks["rows"]:
        if benchmark_row["suite"] != "BenchmarkAPIGenerate":
            continue
        rows.append({
            "fixture": benchmark_row["fixture"],
            "benchmark": benchmark_row["benchmark"],
            "sampleCount": benchmark_row["sampleCount"],
            "medianNsPerOp": benchmark_row["medianNsPerOp"],
            "bytesPerOp": benchmark_row["allocatedBytesPerOp"],
            "allocsPerOp": benchmark_row["allocsPerOp"],
            "exitCode": benchmark_row["exitCode"],
            "classification": benchmark_row["classification"],
            "budget": benchmark_row["budget"],
        })
    return {
        "source": "goflyBenchmarks",
        "argv": benchmarks["argv"],
        "sampleCount": min((row["sampleCount"] for row in rows), default=0),
        "rows": rows,
        "classification": "blocking-failure" if benchmarks["classification"] == "blocking-failure" else "pass",
    }


def evidence_output_path(policy: dict, configured: Path | None) -> Path:
    if configured is None:
        return resolve_repo_path(ROOT, policy["evidence"]["trackedPath"], "evidence.trackedPath", must_exist=False)
    candidate = configured.expanduser()
    if not candidate.is_absolute():
        candidate = ROOT / candidate
    candidate = candidate.resolve(strict=False)
    if candidate.exists() and candidate.is_symlink():
        raise ContractError(f"evidence path must not be a symlink: {candidate}")
    require(candidate.parent.is_dir(), f"evidence parent directory must already exist: {candidate.parent}")
    require(not candidate.parent.is_symlink(), f"evidence parent directory must not be a symlink: {candidate.parent}")
    return candidate


def write_json_atomic(path: Path, value: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary_name = tempfile.mkstemp(prefix=path.name + ".", suffix=".tmp", dir=path.parent)
    temporary = Path(temporary_name)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as output:
            json.dump(value, output, indent=2, sort_keys=True)
            output.write("\n")
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, path)
    finally:
        if temporary.exists():
            temporary.unlink()


def run_comparison(policy: dict, manifest_path: Path, evidence_path: Path, *, samples: int, smoke: bool) -> int:
    validate_sample_mode(policy, samples, publish=not smoke, smoke=smoke)
    manifest, fixtures = validate_fixture_manifest(manifest_path)
    gozero_root = resolve_gozero_root(policy)
    timeout = policy["measurement"]["commandTimeoutSeconds"]
    output_limit = policy["measurement"]["outputLimitBytes"]
    base_env = os.environ.copy()
    go_command = base_env.get("GO", "go")

    with tempfile.TemporaryDirectory(prefix="gofly-api-performance-") as directory:
        work = Path(directory)
        base_env.setdefault("GOCACHE", str(work / "gocache"))
        base_env.setdefault("GOTMPDIR", str(work / "gotmp"))
        base_env.setdefault("GOPROXY", "direct")
        Path(base_env["GOCACHE"]).mkdir(parents=True, exist_ok=True)
        Path(base_env["GOTMPDIR"]).mkdir(parents=True, exist_ok=True)
        base_env["GO"] = go_command

        gofly_sha, gofly_git = git_sha(ROOT, base_env, timeout, output_limit)
        gozero_sha, gozero_git = git_sha(gozero_root, base_env, timeout, output_limit)
        require(gozero_sha == PINNED_GOCTL_COMMIT, f"go-zero oracle commit mismatch: got {gozero_sha}, want {PINNED_GOCTL_COMMIT}")
        go_version_result = run_command([go_command, "version"], ROOT, base_env, timeout, output_limit)
        require(go_version_result["exitCode"] == 0, "go version failed")

        binaries = {"gofly": work / "bin/gofly", "goctl": work / "bin/goctl"}
        binaries["gofly"].parent.mkdir(parents=True, exist_ok=True)
        build_specs = {
            "gofly": ([go_command, "build", "-trimpath", "-o", str(binaries["gofly"]), "./cmd/gofly"], ROOT),
            "goctl": ([go_command, "build", "-trimpath", "-o", str(binaries["goctl"]), "."], gozero_root / policy["oracle"]["toolPath"]),
        }
        builds = {}
        build_failed = False
        for framework in ("gofly", "goctl"):
            argv, cwd = build_specs[framework]
            result = run_command(argv, cwd, base_env, timeout * 3, output_limit)
            builds[framework] = command_evidence(result)
            build_failed = build_failed or result["exitCode"] != 0 or not binaries[framework].is_file()

        manifest_digest = hashlib.sha256(manifest_path.read_bytes()).hexdigest()
        evidence = {
            "schema": EVIDENCE_SCHEMA,
            "generatedAt": datetime.now(timezone.utc).isoformat(),
            "mode": "smoke" if smoke else "publishable-candidate",
            "publishable": False,
            "classification": "build-failure" if build_failed else "pending-preflight",
            "policy": {
                "path": policy["evidence"]["trackedPath"].replace("evidence.json", "policy.json"),
                "sha256": hashlib.sha256(DEFAULT_POLICY.read_bytes()).hexdigest(),
            },
            "oracle": {
                "repository": policy["oracle"]["repository"],
                "commit": PINNED_GOCTL_COMMIT,
                "actualSHA": gozero_sha,
                "git": gozero_git,
            },
            "sampleCount": 0,
            "requestedSampleCount": samples,
            "environment": {
                "sha": gofly_sha,
                "goVersion": go_version_result["output"].strip(),
                "os": platform.system().lower(),
                "arch": platform.machine(),
                "cpu": cpu_name(),
                "goflyGit": gofly_git,
            },
            "fixture": {
                "manifest": manifest_path.relative_to(ROOT).as_posix(),
                "schema": manifest["schema"],
                "fixtureDigest": manifest_digest,
                "fixtures": [
                    {
                        "name": fixture["name"],
                        "file": fixture["file"],
                        "routeCount": fixture["routeCount"],
                        "fixtureDigest": fixture["fixtureDigest"],
                    }
                    for fixture in fixtures
                ],
            },
            "builds": builds,
            "surfaces": [],
            "goflyBenchmarks": None,
            "allocation": None,
            "publishableScope": [],
            "exitCode": 1 if build_failed else 0,
        }
        if build_failed:
            write_json_atomic(evidence_path, evidence)
            print(f"api performance evidence: {evidence_path}")
            return 1

        surface = next(item for item in policy["surfaces"] if item["id"] == "rest-generation")
        rest_evidence = {
            "id": "rest-generation",
            "policyClassification": "potential-comparable",
            "classification": "potential-comparable",
            "latencyMode": "report-only",
            "fixtureResults": [],
        }
        admitted: list[dict] = []
        blocking_failure = False
        for fixture in fixtures:
            preflight = {}
            for framework in ("gofly", "goctl"):
                result = run_rest_invocation(
                    policy,
                    surface,
                    fixture,
                    framework,
                    binaries[framework],
                    work / "preflight",
                    fixture["name"],
                    base_env,
                )
                preflight[framework] = command_evidence(result)
                preflight[framework]["outputSnapshot"] = result["outputSnapshot"]
                preflight[framework]["fixtureDigestAfter"] = result["fixtureDigestAfter"]
                repeated = run_rest_invocation(
                    policy,
                    surface,
                    fixture,
                    framework,
                    binaries[framework],
                    work / "preflight-repeat",
                    fixture["name"],
                    base_env,
                )
                preflight[framework]["repeat"] = command_evidence(repeated)
                preflight[framework]["repeat"]["outputSnapshot"] = repeated["outputSnapshot"]
                preflight[framework]["repeat"]["fixtureDigestAfter"] = repeated["fixtureDigestAfter"]
                preflight[framework]["deterministic"] = (
                    result["exitCode"] == 0
                    and repeated["exitCode"] == 0
                    and result["outputSnapshot"] == repeated["outputSnapshot"]
                )

            gofly_ok = preflight["gofly"]["exitCode"] == 0 and preflight["gofly"]["outputSnapshot"]["goFileCount"] > 0
            goctl_ok = preflight["goctl"]["exitCode"] == 0 and preflight["goctl"]["outputSnapshot"]["goFileCount"] > 0
            source_stable = all(
                item["fixtureDigestAfter"] == fixture["fixtureDigest"]
                and item["repeat"]["fixtureDigestAfter"] == fixture["fixtureDigest"]
                for item in preflight.values()
            )
            gofly_deterministic = preflight["gofly"]["deterministic"]
            goctl_deterministic = preflight["goctl"]["deterministic"]
            row = {
                "fixture": fixture["name"],
                "fixtureDigest": fixture["fixtureDigest"],
                "argv": {
                    "gofly": preflight["gofly"]["argv"],
                    "goctl": preflight["goctl"]["argv"],
                },
                "preflight": preflight,
                "sampleCount": 0,
                "median": {"goflyNs": None, "goctlNs": None, "ratio": None},
                "bytesPerOp": None,
                "allocsPerOp": None,
                "exitCode": max(preflight["gofly"]["exitCode"], preflight["goctl"]["exitCode"]),
                "classification": "potential-comparable",
            }
            if not goctl_ok:
                row["classification"] = "unsupported"
                row["reason"] = "Pinned goctl rejected the unchanged fixture or produced no Go files at the same fresh minimal go.mod scaffold boundary."
            elif not gofly_ok:
                row["classification"] = "gofly-generation-error"
                row["reason"] = "gofly failed the direct REST generation preflight that pinned goctl accepted."
                blocking_failure = True
            elif not source_stable:
                row["classification"] = "unsupported"
                row["reason"] = "At least one generator modified the copied fixture graph, so the measured input boundary is not equivalent."
            elif not gofly_deterministic:
                row["classification"] = "nondeterministic-output"
                row["reason"] = "gofly produced different output snapshots across repeated preflight generation."
                blocking_failure = True
            elif not goctl_deterministic:
                row["classification"] = "unsupported"
                row["reason"] = "Pinned goctl produced different output snapshots across repeated preflight generation."
            else:
                row["classification"] = "comparable"
                admitted.append({"fixture": fixture, "row": row})
            rest_evidence["fixtureResults"].append(row)

        if admitted:
            for round_number in range(1, samples + 1):
                order = ("gofly", "goctl") if round_number % 2 else ("goctl", "gofly")
                for admitted_fixture in admitted:
                    fixture = admitted_fixture["fixture"]
                    row = admitted_fixture["row"]
                    row.setdefault("samples", [])
                    for framework in order:
                        result = run_rest_invocation(
                            policy,
                            surface,
                            fixture,
                            framework,
                            binaries[framework],
                            work / "samples",
                            f"round-{round_number:02d}-{fixture['name']}",
                            base_env,
                        )
                        row["samples"].append({
                            "round": round_number,
                            "framework": framework,
                            **command_evidence(result),
                            "outputSnapshot": result["outputSnapshot"],
                            "fixtureDigestAfter": result["fixtureDigestAfter"],
                        })
                        if result["exitCode"] != 0 or result["fixtureDigestAfter"] != fixture["fixtureDigest"]:
                            blocking_failure = True
                    if blocking_failure:
                        break
                if blocking_failure:
                    break

            for admitted_fixture in admitted:
                row = admitted_fixture["row"]
                gofly_samples = [item["durationNs"] for item in row.get("samples", []) if item["framework"] == "gofly" and item["exitCode"] == 0]
                goctl_samples = [item["durationNs"] for item in row.get("samples", []) if item["framework"] == "goctl" and item["exitCode"] == 0]
                snapshots = {
                    framework: {
                        json.dumps(item["outputSnapshot"], sort_keys=True)
                        for item in row.get("samples", [])
                        if item["framework"] == framework and item["exitCode"] == 0
                    }
                    for framework in ("gofly", "goctl")
                }
                if len(snapshots["gofly"]) > 1:
                    row["classification"] = "nondeterministic-output"
                    row["reason"] = "gofly produced different output snapshots across measured rounds."
                    blocking_failure = True
                elif len(snapshots["goctl"]) > 1:
                    row["classification"] = "unsupported"
                    row["reason"] = "Pinned goctl produced different output snapshots across measured rounds."
                gofly_median = median(gofly_samples)
                goctl_median = median(goctl_samples)
                row["sampleCount"] = min(len(gofly_samples), len(goctl_samples))
                row["median"] = {
                    "goflyNs": gofly_median,
                    "goctlNs": goctl_median,
                    "ratio": round(gofly_median / goctl_median, 6) if gofly_median is not None and goctl_median else None,
                }
                row["exitCode"] = 0 if row["sampleCount"] == samples else 1
                if row["exitCode"] != 0:
                    row["classification"] = "measurement-failure"
                    blocking_failure = True

        comparable_rows = [item["row"] for item in admitted if item["row"]["classification"] == "comparable"]
        all_rest_rows_comparable = len(comparable_rows) == len(fixtures)
        if not comparable_rows:
            rest_evidence["classification"] = "unsupported"
        elif blocking_failure:
            rest_evidence["classification"] = "measurement-failure"
        elif not all_rest_rows_comparable:
            rest_evidence["classification"] = "partial-comparable"
        else:
            rest_evidence["classification"] = "comparable"
        evidence["surfaces"].append(rest_evidence)

        for unsupported_surface in policy["surfaces"]:
            if unsupported_surface["classification"] != "unsupported":
                continue
            evidence["surfaces"].append({
                "id": unsupported_surface["id"],
                "policyClassification": "unsupported",
                "classification": "unsupported",
                "reason": unsupported_surface["unsupportedReason"],
                "argv": {"gofly": None, "goctl": None},
                "sampleCount": 0,
                "median": {"goflyNs": None, "goctlNs": None, "ratio": None},
                "bytesPerOp": None,
                "allocsPerOp": None,
                "exitCode": None,
            })

        benchmarks, benchmark_failed = gofly_benchmark_evidence(
            policy,
            fixtures,
            base_env,
            samples=1 if smoke else samples,
            allow_blocking=not smoke,
        )
        evidence["goflyBenchmarks"] = benchmarks
        evidence["allocation"] = allocation_evidence_from_benchmarks(benchmarks)
        blocking_failure = blocking_failure or benchmark_failed
        comparison_fully_sampled = all_rest_rows_comparable and all(row["sampleCount"] >= samples for row in comparable_rows)
        benchmarks_fully_sampled = (
            benchmarks["rowCount"] == policy["goflyBenchmarks"]["expectedRowCount"]
            and benchmarks["sampleCount"] >= policy["goflyBenchmarks"]["minimumSamples"]
            and benchmarks["classification"] == "publishable"
        )
        evidence["sampleCount"] = benchmarks["sampleCount"]
        evidence["publishableScope"] = ["gofly-benchmarks"] if benchmarks_fully_sampled else []
        if comparison_fully_sampled:
            evidence["publishableScope"].append("cross-framework-rest-generation-report-only")
        evidence["publishable"] = not smoke and benchmarks_fully_sampled and not blocking_failure
        if blocking_failure:
            evidence["classification"] = "blocking-failure"
        elif smoke:
            evidence["classification"] = "smoke-only"
        elif not comparable_rows:
            evidence["classification"] = "publishable-gofly-only-cross-framework-unsupported"
        elif not all_rest_rows_comparable:
            evidence["classification"] = "publishable-gofly-with-partial-report-only-cross-framework"
        else:
            evidence["classification"] = "publishable-with-report-only-cross-framework"
        evidence["exitCode"] = 1 if blocking_failure else 0
        validate_evidence_shape(evidence, policy)
        write_json_atomic(evidence_path, evidence)
        print(f"api performance evidence: {evidence_path}")
        print(f"api performance classification: {evidence['classification']}")
        return evidence["exitCode"]


def validate_evidence_shape(evidence: dict, policy: dict) -> None:
    require(evidence.get("schema") == EVIDENCE_SCHEMA, f"evidence schema must be {EVIDENCE_SCHEMA}")
    require(evidence.get("oracle", {}).get("commit") == PINNED_GOCTL_COMMIT, "evidence oracle commit mismatch")
    sample_count = evidence.get("sampleCount")
    require(isinstance(sample_count, int) and sample_count >= 0, "evidence sampleCount is invalid")
    if evidence.get("publishable"):
        validate_sample_mode(policy, sample_count, publish=True, smoke=False)
        benchmarks = evidence.get("goflyBenchmarks") or {}
        require(benchmarks.get("classification") == "publishable", "publishable evidence requires complete gofly benchmarks")
        require(benchmarks.get("rowCount") == policy["goflyBenchmarks"]["expectedRowCount"], "publishable evidence row count mismatch")
        require(benchmarks.get("sampleCount") >= policy["goflyBenchmarks"]["minimumSamples"], "publishable evidence benchmark samples are incomplete")
        rows = benchmarks.get("rows") or []
        require(len(rows) == policy["goflyBenchmarks"]["expectedRowCount"], "publishable evidence benchmark rows are incomplete")
        require(len({row.get("benchmark") for row in rows}) == len(rows), "publishable evidence benchmark rows must be unique")
        require(
            all(row.get("sampleCount", 0) >= policy["goflyBenchmarks"]["minimumSamples"] for row in rows),
            "publishable evidence contains undersampled benchmark rows",
        )
        require("gofly-benchmarks" in evidence.get("publishableScope", []), "publishable evidence scope is incomplete")
    if sample_count == 1:
        require(evidence.get("mode") == "smoke", "single-sample evidence must be smoke")
        require(evidence.get("publishable") is False, "single-sample evidence cannot be publishable")
    for field in ("environment", "fixture", "surfaces", "goflyBenchmarks", "classification"):
        require(field in evidence, f"evidence.{field} is required")


def dry_run(policy: dict, manifest_path: Path) -> int:
    potential = [surface["id"] for surface in policy["surfaces"] if surface["classification"] == "potential-comparable"]
    unsupported = [surface["id"] for surface in policy["surfaces"] if surface["classification"] == "unsupported"]
    result = {
        "schema": "gofly.api_performance_dry_run.v1",
        "status": "ready-for-fixture" if not manifest_path.is_file() else "ready",
        "fixtureManifest": manifest_path.relative_to(ROOT).as_posix(),
        "fixturePresent": manifest_path.is_file(),
        "oracleCommit": PINNED_GOCTL_COMMIT,
        "sampleCount": policy["measurement"]["defaultSamples"],
        "potentialComparableSurfaces": potential,
        "unsupportedSurfaces": unsupported,
        "publishable": False,
    }
    print(json.dumps(result, indent=2, sort_keys=True))
    return 0


def self_test(policy: dict) -> int:
    checks: list[tuple[str, bool]] = []

    def check(name: str, condition: bool) -> None:
        checks.append((name, condition))

    def rejects(name: str, action) -> None:
        try:
            action()
        except ContractError:
            checks.append((name, True))
        else:
            checks.append((name, False))

    with tempfile.TemporaryDirectory(prefix="gofly-api-performance-self-test-") as directory:
        test_root = Path(directory)
        manifest = test_root / "fixtures/manifest.json"
        manifest.parent.mkdir(parents=True)
        manifest.write_text(json.dumps({"schema": FIXTURE_SCHEMA}) + "\n", encoding="utf-8")
        test_policy = copy.deepcopy(policy)
        test_policy["fixture"]["manifest"] = "fixtures/manifest.json"

        validate_policy(test_policy, test_root, require_fixture=True)
        check("valid fixture-independent policy", True)

        fixture_root = test_root / "fixture-graph"
        (fixture_root / "types").mkdir(parents=True)
        (fixture_root / "contract.api").write_text('syntax = "v1"\nimport "types/common.api"\n', encoding="utf-8")
        (fixture_root / "types/common.api").write_text('syntax = "v1"\n', encoding="utf-8")
        graph, import_count, fixture_digest = fixture_graph(fixture_root, "contract.api")
        check("fixture graph follows local imports", sorted(graph) == ["contract.api", "types/common.api"] and import_count == 1)
        check("fixture graph emits sha256", re.fullmatch(r"[0-9a-f]{64}", fixture_digest) is not None)

        rejects(
            "repository path traversal is rejected",
            lambda: resolve_repo_path(test_root, "../outside.json", "fixture.manifest", must_exist=False),
        )

        wrong_commit = copy.deepcopy(test_policy)
        wrong_commit["oracle"]["commit"] = "main"
        rejects("mutable oracle revision is rejected", lambda: validate_policy(wrong_commit, test_root, require_fixture=True))

        unsupported_command = copy.deepcopy(test_policy)
        unsupported_surface = next(item for item in unsupported_command["surfaces"] if item["classification"] == "unsupported")
        unsupported_surface["commands"] = {"goctl": ["{binary}", "api", "swagger"]}
        rejects(
            "unsupported surface cannot execute",
            lambda: validate_policy(unsupported_command, test_root, require_fixture=True),
        )

        rejects(
            "single sample cannot be published",
            lambda: validate_sample_mode(test_policy, 1, publish=True, smoke=False),
        )
        validate_sample_mode(test_policy, 1, publish=False, smoke=True)
        check("single sample is smoke-only", True)

        surface_ids = sorted(REQUIRED_POTENTIAL_SURFACES)
        schedule = alternating_schedule(10, surface_ids)
        expected_runs = 10 * len(surface_ids) * 2
        check("ten rounds schedule both frameworks", len(schedule) == expected_runs)
        check(
            "framework order alternates",
            schedule[0]["framework"] == "gofly"
            and schedule[1]["framework"] == "goctl"
            and schedule[len(surface_ids) * 2]["framework"] == "goctl",
        )

        benchmark_output = "\n".join(
            [
                "BenchmarkAPIParse/routes=20-8  1  101 ns/op  5836 input-bytes/op  71 B/op  2 allocs/op",
                "BenchmarkAPIGenerate/routes=20-8  1  123 ns/op  456 bytes/op  7 files/op  89 B/op  3 allocs/op",
                "BenchmarkAPIClientGenerate/typescript/routes=20-8  1  145 ns/op  678 bytes/op  1 files/op  90 B/op  4 allocs/op",
                "BenchmarkAPIUnexpected/routes=20-8  1  167 ns/op  91 B/op  5 allocs/op",
            ]
        ) + "\n"
        parsed = parse_benchmark_output(
            benchmark_output,
            [{"name": "small", "routeCount": 20, "fixtureDigest": "0" * 64}],
        )
        check("one fixture defines nine benchmark rows", len(parsed) == 9)
        observed_rows = observed_benchmark_rows(benchmark_output)
        check(
            "unknown and missing benchmark rows remain detectable",
            "BenchmarkAPIUnexpected/routes=20" in observed_rows
            and len(set(parsed) - observed_rows) == 6,
        )
        check(
            "all benchmark metrics are parsed without confusing custom bytes/op",
            parsed["BenchmarkAPIParse/routes=20"]["medianNsPerOp"] == [101.0]
            and parsed["BenchmarkAPIParse/routes=20"]["inputBytesPerOp"] == [5836.0]
            and parsed["BenchmarkAPIGenerate/routes=20"]["outputBytesPerOp"] == [456.0]
            and parsed["BenchmarkAPIGenerate/routes=20"]["filesPerOp"] == [7.0]
            and parsed["BenchmarkAPIGenerate/routes=20"]["allocatedBytesPerOp"] == [89.0]
            and parsed["BenchmarkAPIGenerate/routes=20"]["allocsPerOp"] == [3.0]
            and parsed["BenchmarkAPIClientGenerate/typescript/routes=20"]["outputBytesPerOp"] == [678.0],
        )

        smoke_evidence = {
            "schema": EVIDENCE_SCHEMA,
            "mode": "smoke",
            "publishable": False,
            "classification": "smoke-only",
            "oracle": {"commit": PINNED_GOCTL_COMMIT},
            "sampleCount": 1,
            "environment": {},
            "fixture": {},
            "surfaces": [],
            "goflyBenchmarks": {},
        }
        validate_evidence_shape(smoke_evidence, test_policy)
        check("smoke evidence is non-publishable", True)

    failed = [name for name, passed in checks if not passed]
    for name, passed in checks:
        print(f"{'PASS' if passed else 'FAIL'} {name}")
    if failed:
        raise ContractError("self-test failures: " + ", ".join(failed))
    print(f"api performance self-test OK ({len(checks)} checks)")
    return 0


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--policy", type=Path, default=DEFAULT_POLICY)
    parser.add_argument(
        "--evidence",
        type=Path,
        default=Path(os.environ["API_PERFORMANCE_EVIDENCE"]) if os.environ.get("API_PERFORMANCE_EVIDENCE") else None,
        help="write evidence to this file; parent must already exist outside the repository",
    )
    parser.add_argument("--samples", type=int, help="override the publishable sample count")
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--self-test", action="store_true", help="exercise fixture-independent boundary checks")
    mode.add_argument("--dry-run", action="store_true", help="validate policy and print the planned comparison")
    mode.add_argument("--smoke", action="store_true", help="run one non-publishable sample")
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(sys.argv[1:] if argv is None else argv)
    policy_path = args.policy.resolve()
    policy = read_json(policy_path)
    manifest_path = validate_policy(policy, ROOT, require_fixture=not (args.self_test or args.dry_run))
    if args.self_test:
        return self_test(policy)
    if args.dry_run:
        return dry_run(policy, manifest_path)
    samples = 1 if args.smoke else (args.samples or policy["measurement"]["defaultSamples"])
    output_path = evidence_output_path(policy, args.evidence)
    return run_comparison(policy, manifest_path, output_path, samples=samples, smoke=args.smoke)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except ContractError as exc:
        print(f"api performance check failed: {exc}", file=sys.stderr)
        sys.exit(1)
