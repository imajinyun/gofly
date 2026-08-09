#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import filecmp
import json
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "goctl-oracle-replay.json"
missing = []

expected_categories = {
    "same-contract",
    "compatible-addition",
    "layout-difference",
    "model-layout-difference",
    "generated-cache-template",
    "missing-capability",
    "generation-error",
}


def require(condition, message):
    if not condition:
        missing.append(message)


def read_json(path):
    if not path.is_file():
        missing.append(f"{path.relative_to(root)} is missing")
        return {}
    return json.loads(path.read_text(encoding="utf-8"))


def read_text(path):
    if not path.is_file():
        return ""
    return path.read_text(encoding="utf-8", errors="replace")


def make_target_names(makefile):
    out = set()
    for line in makefile.splitlines():
        if ":" not in line or line.startswith("\t") or line.startswith("#"):
            continue
        name = line.split(":", 1)[0]
        if name and all(ch.isalnum() or ch in "_-" for ch in name):
            out.add(name)
    return out


def make_target_deps(makefile, target):
    for line in makefile.splitlines():
        if line.startswith(target + ":"):
            return set(line.split(":", 1)[1].split("##", 1)[0].split())
    return set()


def run(cmd, cwd, env, timeout=240):
    return subprocess.run(
        cmd,
        cwd=cwd,
        env=env,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        timeout=timeout,
        check=False,
    )


def copy_fixture_source(fixture_dir, fixture, destination):
    destination.mkdir(parents=True, exist_ok=True)
    for field in ("api", "config", "ddl"):
        rel = pathlib.PurePosixPath(fixture[field])
        src = fixture_dir / rel
        dst = destination / rel
        dst.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(src, dst)
    for rel in fixture.get("extraFiles", []):
        src = fixture_dir / rel
        dst = destination / rel
        dst.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(src, dst)
    for path in fixture_dir.rglob("*.api"):
        rel = path.relative_to(fixture_dir)
        dst = destination / rel
        dst.parent.mkdir(parents=True, exist_ok=True)
        if not dst.exists():
            shutil.copy2(path, dst)


def write_go_mod(out_dir, module):
    out_dir.mkdir(parents=True, exist_ok=True)
    (out_dir / "go.mod").write_text(f"module {module}\n\ngo 1.24\n", encoding="utf-8")


def append_replace(out_dir):
    go_mod = out_dir / "go.mod"
    text = go_mod.read_text(encoding="utf-8")
    if "replace github.com/imajinyun/gofly =>" not in text:
        text += f"\nreplace github.com/imajinyun/gofly => {root}\n"
    go_mod.write_text(text, encoding="utf-8")


def file_set(base):
    result = set()
    for path in base.rglob("*"):
        if path.is_file():
            rel = path.relative_to(base).as_posix()
            if rel.startswith(".gofly/"):
                continue
            result.add(rel)
    return result


def compare_common_files(goctl_dir, gofly_dir):
    same = []
    different = []
    for rel in sorted(file_set(goctl_dir) & file_set(gofly_dir)):
        if filecmp.cmp(goctl_dir / rel, gofly_dir / rel, shallow=False):
            same.append(rel)
        else:
            different.append(rel)
    return same, different


def classify(goctl_dir, gofly_dir, fixture):
    categories = set()
    goctl_files = file_set(goctl_dir)
    gofly_files = file_set(gofly_dir)
    same, different = compare_common_files(goctl_dir, gofly_dir)
    missing_in_gofly = sorted(goctl_files - gofly_files)
    additional_in_gofly = sorted(gofly_files - goctl_files)

    if same:
        categories.add("same-contract")
    if additional_in_gofly:
        categories.add("compatible-addition")
    if different or missing_in_gofly:
        categories.add("layout-difference")
    if any(path.startswith("model/") for path in missing_in_gofly + additional_in_gofly + different):
        categories.add("model-layout-difference")
    if fixture.get("cache"):
        categories.add("generated-cache-template")
    if not categories:
        categories.add("same-contract")

    return {
        "categories": sorted(categories),
        "goctlFileCount": len(goctl_files),
        "goflyFileCount": len(gofly_files),
        "commonFileCount": len(goctl_files & gofly_files),
        "sameCommonFiles": same[:20],
        "differentCommonFiles": different[:20],
        "missingInGofly": missing_in_gofly[:30],
        "additionalInGofly": additional_in_gofly[:30],
    }


def validate_contracts(out_dir, fixture):
    problems = []
    go_mod = read_text(out_dir / "go.mod")
    if f"module {fixture['module']}" not in go_mod:
        problems.append("go.mod module")
    for rel in ("internal/api/http/routes.go", "internal/types/types.go"):
        if not (out_dir / rel).is_file():
            problems.append(rel)
    if not any(path.as_posix().endswith(".go") for path in (out_dir / "internal" / "app").rglob("*.go")):
        problems.append("app files")
    if not any(path.as_posix().endswith(".go") for path in (out_dir / "model").rglob("*.go")):
        problems.append("model output")
    routes = read_text(out_dir / "internal" / "api" / "http" / "routes.go")
    if "RegisterHandlers" not in routes:
        problems.append("route registration")
    return problems


def generate_with_goctl(gozero_root, fixture_dir, fixture, workdir, env):
    source_dir = workdir / "goctl-source"
    out_dir = workdir / "goctl-out"
    copy_fixture_source(fixture_dir, fixture, source_dir)
    write_go_mod(out_dir, fixture["module"])
    api_file = source_dir / fixture["api"]
    ddl_file = source_dir / fixture["ddl"]

    api_result = run(
        ["go", "run", ".", "api", "go", "--api", str(api_file), "--dir", str(out_dir), "--style", "gozero"],
        gozero_root,
        env,
    )
    if api_result.returncode != 0:
        return out_dir, {"step": "goctl api go", "output": api_result.stdout}

    model_result = run(
        [
            "go",
            "run",
            ".",
            "model",
            "mysql",
            "ddl",
            "--src",
            str(ddl_file),
            "--dir",
            str(out_dir / "model"),
            "--style",
            "gozero",
            "--database",
            "go_zero",
            "--cache",
            "--prefix",
            "cache",
        ],
        gozero_root,
        env,
    )
    if model_result.returncode != 0:
        return out_dir, {"step": "goctl model mysql ddl", "output": model_result.stdout}
    return out_dir, None


def generate_with_gofly(fixture_dir, fixture, workdir, env):
    source_dir = workdir / "gofly-source"
    out_dir = workdir / "gofly-out"
    copy_fixture_source(fixture_dir, fixture, source_dir)
    write_go_mod(out_dir, fixture["module"])
    config_src = source_dir / fixture["config"]
    config_dst = out_dir / fixture["config"]
    config_dst.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(config_src, config_dst)

    scaffold = run(
        [
            "go",
            "run",
            "./cmd/gofly",
            "new",
            "api",
            fixture["serviceName"],
            "--module",
            fixture["module"],
            "--dir",
            str(out_dir),
            "--style",
            fixture["style"],
            "--profile",
            fixture["profile"],
            "--api-spec=false",
        ],
        root,
        env,
    )
    if scaffold.returncode != 0:
        return out_dir, {"step": "gofly new api", "output": scaffold.stdout}

    api_result = run(
        [
            "go",
            "run",
            "./cmd/gofly",
            "api",
            "gen",
            "--file",
            str(source_dir / fixture["api"]),
            "--dir",
            str(out_dir),
            "--package",
            "api",
            "--profile",
            fixture["profile"],
        ],
        root,
        env,
    )
    if api_result.returncode != 0:
        return out_dir, {"step": "gofly api gen", "output": api_result.stdout}

    model_result = run(
        [
            "go",
            "run",
            "./cmd/gofly",
            "model",
            "gen",
            "--src",
            str(source_dir / fixture["ddl"]),
            "--dir",
            str(out_dir),
            "--package",
            "model",
            "--module",
            fixture["module"],
            "--style",
            "go_zero",
            "--database",
            "go_zero",
            "--strict",
            "--cache",
        ],
        root,
        env,
    )
    if model_result.returncode != 0:
        return out_dir, {"step": "gofly model gen", "output": model_result.stdout}
    append_replace(out_dir)
    return out_dir, None


manifest = read_json(manifest_path)
makefile = read_text(root / "Makefile")
targets = make_target_names(makefile)
require(manifest.get("schema") == "gofly.goctl_oracle_replay.v1", "schema mismatch")
require(manifest.get("acceptanceGate") == "make goctl-oracle-replay-check", "acceptanceGate mismatch")
require(manifest.get("mode") == "report-only", "oracle replay must start in report-only mode")
require("goctl-oracle-replay-check" in targets, "Makefile must expose goctl-oracle-replay-check")
require("check-goctl-oracle-replay.sh" in makefile, "Makefile must call check-goctl-oracle-replay.sh")
require("goctl-oracle-replay-check" in make_target_deps(makefile, "goctl-real-project-replay-check"), "goctl-real-project-replay-check must depend on goctl-oracle-replay-check")
require("goctl-oracle-replay-check" in make_target_deps(makefile, "docs-check"), "docs-check must depend on goctl-oracle-replay-check")
require(set(manifest.get("diffCategories") or []) == expected_categories, "diffCategories drifted")
for source in manifest.get("sourceOfTruth") or []:
    if not source.startswith("../"):
        require((root / source).exists(), f"sourceOfTruth missing {source}")
for field in ("positioning", "generationPolicy", "failurePolicy"):
    require(len(str((manifest.get("oraclePolicy") or {}).get(field) or "").split()) >= 8, f"oraclePolicy.{field} must be actionable")
require("nativeFixturePolicy" in manifest, "nativeFixturePolicy is required")
native_fixtures = set(manifest.get("nativeFixtures") or [])
require(native_fixtures, "at least one native fixture is required")

if missing:
    print("goctl oracle replay check failed:", file=sys.stderr)
    for item in missing:
        print(f"- {item}", file=sys.stderr)
    sys.exit(1)

gozero_root = root.parent / "gozero" / "tools" / "goctl"
report = {
    "schema": "gofly.goctl_oracle_replay_report.v1",
    "mode": manifest.get("mode"),
    "goctlAvailable": (gozero_root / "go.mod").is_file(),
    "fixtures": [],
    "summary": {
        "total": 0,
        "compared": 0,
        "goflyGenerated": 0,
        "skipped": 0,
        "goctlGenerationErrors": 0,
        "goflyGenerationErrors": 0,
        "missingContracts": 0,
    },
}

if not report["goctlAvailable"]:
    report["summary"]["skipped"] = len(manifest.get("fixtures") or [])
    print(json.dumps(report, indent=2, sort_keys=True))
    sys.exit(0)

base_env = os.environ.copy()
base_env.setdefault("GOFLAGS", "-count=1")
base_env.setdefault("GOSUMDB", "off")
with tempfile.TemporaryDirectory(prefix="gofly-goctl-oracle-") as tmp:
    tmp_root = pathlib.Path(tmp)
    base_env["GOCACHE"] = os.environ.get("GOCACHE", str(tmp_root / "gocache"))
    base_env["GOTMPDIR"] = os.environ.get("GOTMPDIR", str(tmp_root / "gotmp"))
    base_env["GOMODCACHE"] = os.environ.get("GOMODCACHE", str(tmp_root / "gomodcache"))
    pathlib.Path(base_env["GOCACHE"]).mkdir(parents=True, exist_ok=True)
    pathlib.Path(base_env["GOTMPDIR"]).mkdir(parents=True, exist_ok=True)
    pathlib.Path(base_env["GOMODCACHE"]).mkdir(parents=True, exist_ok=True)

    for fixture_id in manifest.get("fixtures") or []:
        report["summary"]["total"] += 1
        fixture_dir = root / "testdata" / "goctl-replay" / fixture_id.replace("-goctl-replay", "").replace("-imported-multigroup-replay", "").replace("-transitive-import-replay", "").replace("-crud-query-replay", "").replace("-admin-delete-replay", "")
        if not fixture_dir.is_dir():
            candidates = [p for p in (root / "testdata" / "goctl-replay").iterdir() if p.is_dir() and (p / "replay.json").is_file() and json.loads((p / "replay.json").read_text(encoding="utf-8")).get("id") == fixture_id]
            fixture_dir = candidates[0] if candidates else fixture_dir
        fixture = read_json(fixture_dir / "replay.json")
        item = {"id": fixture_id}
        is_native = fixture_id in native_fixtures or fixture.get("nativeOracle") is True
        item["nativeOracle"] = is_native
        workdir = tmp_root / fixture_id
        workdir.mkdir(parents=True, exist_ok=True)

        goctl_dir, goctl_error = generate_with_goctl(gozero_root, fixture_dir, fixture, workdir, base_env)
        gofly_dir, gofly_error = generate_with_gofly(fixture_dir, fixture, workdir, base_env)
        if gofly_error:
            item["categories"] = ["generation-error"]
            item["goflyError"] = gofly_error
            item["goctlError"] = goctl_error
            report["summary"]["goflyGenerationErrors"] += 1
            report["fixtures"].append(item)
            continue
        report["summary"]["goflyGenerated"] += 1

        gofly_contract_problems = validate_contracts(gofly_dir, fixture)
        if goctl_error:
            item["categories"] = ["generation-error"]
            item["goctlError"] = goctl_error
            item["goflyError"] = None
            item["contracts"] = {
                "goflyMissing": gofly_contract_problems,
                "goctlMissing": ["goctl generation failed before contract validation"],
            }
            report["summary"]["goctlGenerationErrors"] += 1
            if gofly_contract_problems:
                item["categories"] = sorted(set(item["categories"]) | {"missing-capability"})
                report["summary"]["missingContracts"] += 1
            if is_native:
                report["summary"].setdefault("nativeGoctlGenerationErrors", 0)
                report["summary"]["nativeGoctlGenerationErrors"] += 1
            report["fixtures"].append(item)
            continue

        item.update(classify(goctl_dir, gofly_dir, fixture))
        goctl_contract_problems = validate_contracts(goctl_dir, fixture)
        item["contracts"] = {
            "goflyMissing": gofly_contract_problems,
            "goctlMissing": goctl_contract_problems,
        }
        if gofly_contract_problems:
            item["categories"] = sorted(set(item["categories"]) | {"missing-capability"})
            report["summary"]["missingContracts"] += 1
        report["summary"]["compared"] += 1
        if is_native:
            report["summary"].setdefault("nativeCompared", 0)
            report["summary"]["nativeCompared"] += 1
        report["fixtures"].append(item)

print(json.dumps(report, indent=2, sort_keys=True))
if report["summary"]["goflyGenerationErrors"] or report["summary"]["missingContracts"] or report["summary"].get("nativeGoctlGenerationErrors") or not report["summary"].get("nativeCompared"):
    sys.exit(1)
PY
