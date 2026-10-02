#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import hashlib
import http.client
import json
import os
import pathlib
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import time
from urllib.parse import urlsplit

root = pathlib.Path(".").resolve()
oracle_path = root / "docs" / "reference" / "goctl-oracle-replay.json"
sources_path = root / "testdata" / "migration" / "goctl-replay" / "sources.json"
errors = []


def read_json(path):
    if not path.is_file():
        errors.append(f"missing required JSON file: {path.relative_to(root)}")
        return {}
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        errors.append(f"invalid JSON {path.relative_to(root)}: {exc}")
        return {}


def require(condition, message):
    if not condition:
        errors.append(message)


def sha256(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def safe_relative(value, description):
    path = pathlib.PurePosixPath(value)
    if path.is_absolute() or ".." in path.parts:
        errors.append(f"{description} must be a relative path: {value!r}")
        return None
    return path


def fixture_digest(fixture_dir, fixture_name, files):
    rows = []
    for item in sorted(files, key=lambda item: item.get("path", "")):
        rel = safe_relative(item.get("path", ""), f"{fixture_name} source file")
        if rel is None:
            continue
        path = fixture_dir / rel
        if not path.is_file():
            errors.append(f"{fixture_name}: missing recorded fixture source {rel}")
            continue
        actual = sha256(path)
        if actual != item.get("sha256"):
            errors.append(f"{fixture_name}: digest mismatch for {rel}: got {actual}")
        rows.append(f"{fixture_name}/{rel.as_posix()}:{actual}\n")
    return hashlib.sha256("".join(rows).encode("utf-8")).hexdigest()


def command(cmd, cwd, env, timeout=300):
    try:
        result = subprocess.run(
            cmd,
            cwd=cwd,
            env=env,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            timeout=timeout,
            check=False,
        )
        return {
            "ok": result.returncode == 0,
            "command": cmd,
            "exitCode": result.returncode,
            "output": result.stdout[-8000:],
        }
    except (OSError, subprocess.TimeoutExpired) as exc:
        return {"ok": False, "command": cmd, "error": str(exc)}


def git_commit(path):
    result = command(["git", "-C", str(path), "rev-parse", "HEAD"], root, os.environ.copy(), timeout=30)
    if result.get("ok"):
        result["commit"] = result.get("output", "").strip()
    return result


def resolve_gozero(expected_commit):
    configured = os.environ.get("GOZERO_ROOT", "").strip()
    candidates = [("GOZERO_ROOT", pathlib.Path(configured).expanduser())] if configured else [("../gozero", root.parent / "gozero")]
    for source, candidate in candidates:
        repo = candidate.resolve()
        goctl = repo / "tools" / "goctl"
        if not ((repo / "go.mod").is_file() and (goctl / "go.mod").is_file()):
            if configured:
                errors.append(f"GOZERO_ROOT is not a go-zero checkout: {repo}")
            continue
        commit = git_commit(repo)
        if not commit.get("ok"):
            errors.append(f"cannot resolve go-zero commit at {repo}: {commit.get('output') or commit.get('error')}")
            return None
        if commit["commit"] != expected_commit:
            message = f"go-zero checkout {repo} is {commit['commit']}, expected pinned {expected_commit}"
            if configured:
                errors.append(message)
            return None
        return {"source": source, "repo": repo, "goctl": goctl, "commit": commit["commit"]}
    return None


def copy_fixture(fixture_dir, destination):
    shutil.copytree(fixture_dir, destination, dirs_exist_ok=True)


def write_goctl_mod(out_dir, module, gozero_root):
    out_dir.mkdir(parents=True, exist_ok=True)
    (out_dir / "go.mod").write_text(
        f"module {module}\n\ngo 1.25.0\n\nrequire github.com/zeromicro/go-zero v1.10.2\n\nreplace github.com/zeromicro/go-zero => {gozero_root}\n",
        encoding="utf-8",
    )


def append_gofly_replace(out_dir):
    path = out_dir / "go.mod"
    text = path.read_text(encoding="utf-8")
    replacement = f"replace github.com/imajinyun/gofly => {root}\n"
    if replacement not in text:
        path.write_text(text.rstrip() + "\n\n" + replacement, encoding="utf-8")


def generate_goctl(gozero, fixture_dir, fixture, out_dir, env):
    source_dir = out_dir.parent / "goctl-source"
    copy_fixture(fixture_dir, source_dir)
    write_goctl_mod(out_dir, fixture["module"], gozero["repo"])
    api = source_dir / fixture["api"]
    ddl = source_dir / fixture["ddl"]
    steps = []
    api_result = command(
        ["go", "run", ".", "api", "go", "--api", str(api), "--dir", str(out_dir), "--style", "gozero"],
        gozero["goctl"], env,
    )
    steps.append({"name": "goctl api go", **api_result})
    if not api_result["ok"]:
        return steps
    model_result = command(
        ["go", "run", ".", "model", "mysql", "ddl", "--src", str(ddl), "--dir", str(out_dir / "model"), "--style", "gozero", "--database", "go_zero", "--cache", "--prefix", "cache"],
        gozero["goctl"], env,
    )
    steps.append({"name": "goctl model mysql ddl", **model_result})
    if model_result["ok"]:
        config_path = out_dir / fixture["config"]
        config_path.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(fixture_dir / fixture["config"], config_path)
    return steps


def generate_gofly(fixture_dir, fixture, out_dir, env):
    source_dir = out_dir.parent / "gofly-source"
    copy_fixture(fixture_dir, source_dir)
    out_dir.mkdir(parents=True, exist_ok=True)
    steps = []
    scaffold = command(
        ["go", "run", "./cmd/gofly", "new", "api", fixture["serviceName"], "--module", fixture["module"], "--dir", str(out_dir), "--style", fixture["style"], "--profile", fixture["profile"], "--api-spec=false"],
        root, env,
    )
    steps.append({"name": "gofly new api", **scaffold})
    if not scaffold["ok"]:
        return steps
    api = command(
        ["go", "run", "./cmd/gofly", "api", "gen", "--file", str(source_dir / fixture["api"]), "--dir", str(out_dir), "--package", "api", "--profile", fixture["profile"]],
        root, env,
    )
    steps.append({"name": "gofly api gen", **api})
    if not api["ok"]:
        return steps
    model = command(
        ["go", "run", "./cmd/gofly", "model", "gen", "--src", str(source_dir / fixture["ddl"]), "--dir", str(out_dir), "--package", "model", "--module", fixture["module"], "--style", "go_zero", "--database", "go_zero", "--strict", "--cache"],
        root, env,
    )
    steps.append({"name": "gofly model gen", **model})
    if model["ok"]:
        append_gofly_replace(out_dir)
    return steps


def tree_digest(directory):
    rows = []
    for path in sorted(directory.rglob("*")):
        if not path.is_file():
            continue
        rel = path.relative_to(directory).as_posix()
        if rel in {"go.sum"} or rel.startswith(".gofly/"):
            continue
        rows.append(f"{rel}:{sha256(path)}\n")
    return hashlib.sha256("".join(rows).encode("utf-8")).hexdigest()


def compile_module(out_dir, env):
    return [
        {"name": "go mod tidy", **command(["go", "mod", "tidy"], out_dir, env)},
        {"name": "go test ./...", **command(["go", "test", "./..."], out_dir, env)},
    ]


def read_text(path):
    return path.read_text(encoding="utf-8", errors="replace") if path.is_file() else ""


def expected_route_parts(route):
    method, path = route.split(" ", 1)
    return method.upper(), path


def extract_routes(path):
    text = read_text(path)
    prefix_match = re.search(r'WithPrefix\("([^"]+)"\)', text)
    prefix = prefix_match.group(1).rstrip("/") if prefix_match else ""
    found = set()
    for method, route in re.findall(r'Method:\s*http\.Method([A-Za-z]+),\s*Path:\s*"([^"]+)"', text):
        normalized = re.sub(r":([A-Za-z_][A-Za-z0-9_]*)", r"{\1}", route)
        found.add((method.upper(), prefix + normalized))
    return found


def contract_evidence(side, out_dir, fixture):
    if side == "goctl":
        routes_file = out_dir / "internal" / "handler" / "routes.go"
        types_file = out_dir / "internal" / "types" / "types.go"
        config_file = out_dir / fixture["config"]
    else:
        routes_file = out_dir / "internal" / "routes" / "routes.go"
        types_file = out_dir / "internal" / "model" / "types.go"
        config_file = out_dir / "etc" / f"{fixture['serviceName']}.json"
    found_routes = extract_routes(routes_file)
    route_results = []
    for route in fixture.get("expectedRoutes", []):
        method, path = expected_route_parts(route)
        route_results.append({"route": route, "present": (method, path) in found_routes})
    types = read_text(types_file)
    type_results = [{"type": name, "present": bool(re.search(r"\btype\s+" + re.escape(name) + r"\b", types))} for name in fixture.get("expectedTypes", [])]
    config = read_text(config_file).lower()
    config_results = [{"key": key, "present": key.lower() in config} for key in fixture.get("expectedConfigKeys", [])]
    missing = [f"route:{item['route']}" for item in route_results if not item["present"]]
    missing += [f"type:{item['type']}" for item in type_results if not item["present"]]
    missing += [f"config:{item['key']}" for item in config_results if not item["present"]]
    return {"routes": route_results, "types": type_results, "config": config_results, "missing": missing}


def file_set(directory):
    return {
        path.relative_to(directory).as_posix()
        for path in directory.rglob("*")
        if path.is_file() and path.name != "go.sum" and not path.relative_to(directory).as_posix().startswith(".gofly/")
    }


def classify_path(path, direction):
    if path.startswith(("model/", "repo/")):
        return "model-layout-difference"
    if path.startswith("etc/"):
        return "config-layout-difference"
    if path.startswith(("internal/handler/", "internal/logic/", "internal/types/", "internal/routes/", "internal/api/", "internal/app/", "internal/model/", "cmd/")) or path.endswith(".go"):
        return "layout-difference"
    return "compatible-addition" if direction == "gofly" else "layout-difference"


def classify_diff(goctl_dir, gofly_dir, contracts):
    goctl_files = file_set(goctl_dir)
    gofly_files = file_set(gofly_dir)
    missing = sorted(goctl_files - gofly_files)
    additional = sorted(gofly_files - goctl_files)
    entries = [{"path": path, "side": "goctl", "classification": classify_path(path, "goctl")} for path in missing]
    entries += [{"path": path, "side": "gofly", "classification": classify_path(path, "gofly")} for path in additional]
    categories = {entry["classification"] for entry in entries}
    if not contracts["goctl"]["missing"] and not contracts["gofly"]["missing"]:
        categories.update({"route-contract", "type-contract", "config-contract"})
    return {"categories": sorted(categories), "entries": entries, "unclassified": []}


def update_yaml_port(path, port):
    text = read_text(path)
    updated, count = re.subn(r"(?mi)^port:\s*.*$", f"Port: {port}", text)
    if count != 1:
        raise ValueError(f"expected one port key in {path}")
    path.write_text(updated, encoding="utf-8")


def update_gofly_port(path, port):
    data = json.loads(path.read_text(encoding="utf-8"))
    data.setdefault("rest", {})["host"] = "127.0.0.1"
    data["rest"]["port"] = port
    path.write_text(json.dumps(data, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def smoke_http(side, out_dir, fixture, port, env):
    try:
        if side == "goctl":
            config = out_dir / fixture["config"]
            update_yaml_port(config, port)
            build = command(["go", "build", "-o", str(out_dir / "smoke-server"), "."], out_dir, env)
            start = [str(out_dir / "smoke-server"), "-f", str(config)]
        else:
            config = out_dir / "etc" / f"{fixture['serviceName']}.json"
            update_gofly_port(config, port)
            build = command(["go", "build", "-o", str(out_dir / "smoke-server"), f"./cmd/{fixture['serviceName']}"] , out_dir, env)
            start = [str(out_dir / "smoke-server")]
        if not build["ok"]:
            return {"ok": False, "build": build, "error": "smoke build failed"}

        log_path = out_dir / "http-smoke.log"
        with log_path.open("w", encoding="utf-8") as log:
            process = subprocess.Popen(start, cwd=out_dir, env=env, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
            try:
                smoke = fixture["httpSmoke"]
                parsed = urlsplit(smoke["path"])
                for _ in range(100):
                    if process.poll() is not None:
                        break
                    try:
                        connection = http.client.HTTPConnection("127.0.0.1", port, timeout=1)
                        connection.request(smoke["method"], parsed.path + (f"?{parsed.query}" if parsed.query else ""))
                        response = connection.getresponse()
                        body = response.read(2048).decode("utf-8", errors="replace")
                        connection.close()
                        return {"ok": response.status == smoke["expectStatus"], "method": smoke["method"], "path": smoke["path"], "expectedStatus": smoke["expectStatus"], "actualStatus": response.status, "body": body}
                    except OSError:
                        time.sleep(0.1)
                return {"ok": False, "error": "server did not accept the HTTP smoke request", "log": read_text(log_path)[-4000:]}
            finally:
                if process.poll() is None:
                    os.killpg(process.pid, signal.SIGTERM)
                    try:
                        process.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        os.killpg(process.pid, signal.SIGKILL)
                process.wait(timeout=5)
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        return {"ok": False, "error": str(exc)}


oracle = read_json(oracle_path)
sources = read_json(sources_path)
expected_categories = {"route-contract", "type-contract", "config-contract", "layout-difference", "model-layout-difference", "config-layout-difference", "compatible-addition"}
require(oracle.get("schema") == "gofly.goctl_oracle_replay.v2", "oracle schema must be gofly.goctl_oracle_replay.v2")
require(oracle.get("acceptanceGate") == "make goctl-oracle-replay-check", "oracle acceptanceGate drifted")
require(oracle.get("mode") == "pinned-dual-runtime-migration-proof", "oracle mode drifted")
require(oracle.get("sourceManifest") == "testdata/migration/goctl-replay/sources.json", "oracle sourceManifest drifted")
require(set(oracle.get("diffCategories") or []) == expected_categories, "oracle diffCategories drifted")
require(sources.get("schema") == "gofly.goctl_migration_replay_sources.v1", "sources schema drifted")
provenance = sources.get("provenance") or {}
external = oracle.get("externalReference") or {}
require(provenance.get("pinnedCommit") == external.get("pinnedCommit"), "oracle and sources pinned commit differ")
require(provenance.get("goctlVersion") == external.get("goctlVersion"), "oracle and sources goctl version differ")
require(len(sources.get("failureModeInventory") or []) >= 7, "sources must record the migration-proof failure-mode inventory")
fixtures = sources.get("fixtures") or []
require(len(fixtures) == 2, "migration proof needs exactly two representative fixtures")

fixture_rows = []
for source in fixtures:
    rel = safe_relative(source.get("directory", ""), f"{source.get('id', '<missing>')} directory")
    if rel is None:
        continue
    fixture_dir = root / rel
    replay_path = fixture_dir / "replay.json"
    replay = read_json(replay_path)
    require(replay.get("schema") == "gofly.goctl_migration_proof_fixture.v1", f"{source.get('id')}: fixture schema drifted")
    require(replay.get("id") == source.get("id"), f"{source.get('id')}: source and replay IDs differ")
    require(replay.get("profile") == "gozero-compatible", f"{source.get('id')}: profile must be gozero-compatible")
    require(replay.get("style") == "minimal", f"{source.get('id')}: style must be minimal")
    require(bool(replay.get("rollback")), f"{source.get('id')}: rollback is required")
    require(bool(replay.get("supportedSurface")) and bool(replay.get("excludedSurface")), f"{source.get('id')}: supported and excluded surfaces are required")
    require(bool(source.get("httpSmoke")), f"{source.get('id')}: HTTP smoke definition is required")
    digest = fixture_digest(fixture_dir, fixture_dir.name, source.get("files") or [])
    require(digest == source.get("sourceDigest"), f"{source.get('id')}: aggregate source digest mismatch: got {digest}")
    fixture = dict(replay)
    fixture["httpSmoke"] = source["httpSmoke"]
    fixture_rows.append((source, fixture, fixture_dir, digest))

if errors:
    print("goctl migration proof contract check failed:", file=sys.stderr)
    for item in errors:
        print(f"- {item}", file=sys.stderr)
    sys.exit(1)

base_env = os.environ.copy()
base_env["GOFLAGS"] = base_env.get("GOFLAGS", "-count=1")
base_env.setdefault("GOPROXY", "direct")
root_mod_before = sha256(root / "go.mod")
root_sum_before = sha256(root / "go.sum")
gozero = resolve_gozero(provenance["pinnedCommit"])
if errors:
    print("goctl migration proof checkout check failed:", file=sys.stderr)
    for item in errors:
        print(f"- {item}", file=sys.stderr)
    sys.exit(1)

report = {
    "schema": "gofly.goctl_oracle_replay_report.v2",
    "mode": oracle["mode"],
    "accepted": False,
    "environment": {"gofly": git_commit(root), "goVersion": command(["go", "version"], root, base_env, timeout=30)},
    "provenance": {"sourceManifest": str(sources_path.relative_to(root)), "externalReference": provenance, "gozero": None},
    "fixtures": [],
    "summary": {"total": len(fixture_rows), "passed": 0, "failed": 0, "skipped": 0},
}
if gozero is None:
    report["provenance"]["gozero"] = {"available": False, "reason": "pinned GOZERO_ROOT or ../gozero checkout unavailable"}
    report["fixtures"] = [
        {
            "id": fixture["id"],
            "sourceDigest": digest,
            "accepted": False,
            "status": "skipped-external-source-unavailable",
        }
        for _, fixture, _, digest in fixture_rows
    ]
    report["summary"]["skipped"] = len(fixture_rows)
    print(json.dumps(report, indent=2, sort_keys=True))
    sys.exit(0)

report["provenance"]["gozero"] = {"available": True, "source": gozero["source"], "path": str(gozero["repo"]), "commit": gozero["commit"], "version": command(["go", "run", ".", "--version"], gozero["goctl"], base_env, timeout=120)}

with tempfile.TemporaryDirectory(prefix="gofly-goctl-migration-proof-") as tmp:
    tmp_root = pathlib.Path(tmp)
    env = base_env.copy()
    env["GOCACHE"] = os.environ.get("GOCACHE", str(tmp_root / "gocache"))
    env["GOTMPDIR"] = os.environ.get("GOTMPDIR", str(tmp_root / "gotmp"))
    pathlib.Path(env["GOCACHE"]).mkdir(parents=True, exist_ok=True)
    pathlib.Path(env["GOTMPDIR"]).mkdir(parents=True, exist_ok=True)
    if os.environ.get("GOVERNANCE_ISOLATE_GOMODCACHE") == "true":
        env["GOMODCACHE"] = str(tmp_root / "gomodcache")
        pathlib.Path(env["GOMODCACHE"]).mkdir(parents=True, exist_ok=True)

    for index, (source, fixture, fixture_dir, digest) in enumerate(fixture_rows):
        item = {"id": fixture["id"], "sourceDigest": digest, "supportedSurface": fixture["supportedSurface"], "excludedSurface": fixture["excludedSurface"]}
        workdir = tmp_root / fixture["id"]
        goctl_out = workdir / "goctl"
        gofly_out = workdir / "gofly"
        goctl_repeat = workdir / "goctl-repeat"
        gofly_repeat = workdir / "gofly-repeat"
        item["generation"] = {
            "goctl": generate_goctl(gozero, fixture_dir, fixture, goctl_out, env),
            "gofly": generate_gofly(fixture_dir, fixture, gofly_out, env),
        }
        generated = all(step.get("ok") for side in item["generation"].values() for step in side)
        if generated:
            goctl_repeat_steps = generate_goctl(gozero, fixture_dir, fixture, goctl_repeat, env)
            gofly_repeat_steps = generate_gofly(fixture_dir, fixture, gofly_repeat, env)
            item["repeatGeneration"] = {"goctl": goctl_repeat_steps, "gofly": gofly_repeat_steps}
            repeated = all(step.get("ok") for side in item["repeatGeneration"].values() for step in side)
            item["deterministic"] = {
                "goctl": repeated and tree_digest(goctl_out) == tree_digest(goctl_repeat),
                "gofly": repeated and tree_digest(gofly_out) == tree_digest(gofly_repeat),
            }
            item["compilation"] = {"goctl": compile_module(goctl_out, env), "gofly": compile_module(gofly_out, env)}
            compiled = all(step.get("ok") for side in item["compilation"].values() for step in side)
            item["contracts"] = {"goctl": contract_evidence("goctl", goctl_out, fixture), "gofly": contract_evidence("gofly", gofly_out, fixture)}
            item["diff"] = classify_diff(goctl_out, gofly_out, item["contracts"])
            item["httpSmoke"] = {
                "goctl": smoke_http("goctl", goctl_out, fixture, 19080 + index * 10, env),
                "gofly": smoke_http("gofly", gofly_out, fixture, 19081 + index * 10, env),
            }
            contracts_ok = not item["contracts"]["goctl"]["missing"] and not item["contracts"]["gofly"]["missing"]
            smoke_ok = item["httpSmoke"]["goctl"].get("ok") and item["httpSmoke"]["gofly"].get("ok")
            item["accepted"] = bool(compiled and all(item["deterministic"].values()) and contracts_ok and smoke_ok and not item["diff"]["unclassified"])
        else:
            item["accepted"] = False
        report["fixtures"].append(item)
        if item["accepted"]:
            report["summary"]["passed"] += 1
        else:
            report["summary"]["failed"] += 1

root_module_unchanged = root_mod_before == sha256(root / "go.mod") and root_sum_before == sha256(root / "go.sum")
report["summary"]["rootModuleUnchanged"] = root_module_unchanged
report["accepted"] = report["summary"]["failed"] == 0 and root_module_unchanged

report_path = os.environ.get("GOCTL_ORACLE_REPORT", "").strip()
if report_path:
    destination = pathlib.Path(report_path).expanduser()
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
print(json.dumps(report, indent=2, sort_keys=True))
sys.exit(0 if report["accepted"] else 1)
PY
