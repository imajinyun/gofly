#!/usr/bin/env python3
"""Exercise the P2 CI script boundaries without network, Docker, or benchmarks."""

import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile


ROOT = Path(__file__).resolve().parents[2]
SCRIPTS = ROOT / "bin/scripts"
TOOLS = [
    "google.golang.org/protobuf/cmd/protoc-gen-go",
    "google.golang.org/grpc/cmd/protoc-gen-go-grpc",
]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--report", type=Path, required=True)
    args = parser.parse_args()
    checks = []

    def run(name, argv, env=None, expected=0, contains=None):
        result = subprocess.run(
            argv, cwd=ROOT, env=env, text=True, stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT, check=False,
            timeout=30,
        )
        passed = (result.returncode == 0) if expected == 0 else (result.returncode != 0)
        if contains is not None:
            passed = passed and contains in result.stdout
        checks.append({"name": name, "passed": passed, "exitCode": result.returncode,
                       "output": result.stdout})
        return result

    def require(name, condition):
        checks.append({"name": name, "passed": bool(condition)})

    with tempfile.TemporaryDirectory(prefix="gofly-p2-ci-") as directory:
        work = Path(directory)
        budget = json.loads((ROOT / "bench/budget-ratchet.json").read_text())
        (work / "budget-ratchet.json").write_text(json.dumps(budget))
        names = budget["trackedBenchmarks"]

        def samples(exclude=None, count=5, allocs=1):
            return "goos: linux\ngoarch: amd64\ncpu: fixture\n" + "".join(
                f"{name}-8 100 100 ns/op 64 B/op {allocs} allocs/op\n"
                for name in names if name != exclude for _ in range(count)
            )

        base, current = work / "base.txt", work / "current.txt"
        base.write_text(samples())
        current.write_text(samples())
        env = dict(os.environ, BENCH_DIR=str(work), BASELINE_FILE=str(base),
                   CURRENT_FILE=str(current), BENCH_MIN_SAMPLES="5", GO="/usr/bin/false")
        bench = ["bash", str(SCRIPTS / "benchstat.sh")]
        run("all blocking rows present", bench + ["--check-samples"], env)
        run("unknown option fails without starting benchmarks", bench + ["--unknown"], env, 1, "Unknown benchmark option")
        current.write_text(samples(exclude=names[0]))
        run("current row missing", bench + ["--check-samples"], env, 1, names[0])
        run("comparison fails before benchstat", bench + ["--compare"], env, 1, names[0])
        run("regression fails before comparison", bench + ["--regression-check"], env, 1, names[0])
        current.write_text(samples())
        base.write_text(samples(exclude=names[-1]))
        run("baseline row missing", bench + ["--check-samples"], env, 1, names[-1])
        base.write_text(samples())
        current.write_text(samples(count=1))
        run("truncated rounds fail", bench + ["--check-samples"], env, 1, "samples")
        current.write_text(samples().replace("allocs/op", "missing-metric"))
        run("invalid sample is not presence", bench + ["--check-samples"], env, 1)
        current.write_text(samples())
        run("equal allocation regression inputs pass", bench + ["--regression-check"], env)
        current.write_text(samples(allocs=2))
        run("allocation regression remains blocking", bench + ["--regression-check"], env, 1, "exceeds budget")

        scan, counts = work / "trivy.json", work / "counts.json"
        summary = [sys.executable, str(SCRIPTS / "summarize-trivy.py"),
                   str(scan), "--output", str(counts)]
        vulnerabilities = [
            {"Severity": severity, "FixedVersion": "1.2.3"}
            for severity in ["UNKNOWN", "LOW", "MEDIUM", "HIGH", "CRITICAL"]
        ] + [{"Severity": "HIGH"}]
        scan.write_text(json.dumps({"SchemaVersion": 2, "ArtifactName": "fixture:image",
                                    "Results": [{"Vulnerabilities": vulnerabilities}]}))
        result = run("all Trivy severities reported", summary)
        if result.returncode == 0:
            report = json.loads(counts.read_text())
            require("unfixed and UNKNOWN remain visible", report["counts"] == {
                "UNKNOWN": 1, "LOW": 1, "MEDIUM": 1, "HIGH": 2, "CRITICAL": 1})
            require("only fixed HIGH/CRITICAL contribute to policy count", report["blockingCount"] == 2)
        scan.write_text(json.dumps({"SchemaVersion": 2, "Results": []}))
        run("clean scan reports zero counts", summary, contains="UNKNOWN | 0")
        for name, raw in [("invalid JSON", "{"), ("missing results", "{}"),
                          ("invalid severity", json.dumps({"SchemaVersion": 2, "Results": [
                              {"Vulnerabilities": [{"Severity": "UNRECOGNIZED"}]}]}))]:
            scan.write_text(raw)
            run(name, summary, expected=1)

        # A controlled Go executable checks the actual installer and Make recipes.
        fake_go = work / "controlled go"
        fake_go.write_text("#!/usr/bin/env python3\n" + '''import json, os, pathlib, sys
args = sys.argv[1:]
log = pathlib.Path(os.environ["P2_GO_LOG"])
with log.open("a") as out:
    out.write(json.dumps({"args": args, "cache": os.getenv("GOCACHE"), "tmp": os.getenv("GOTMPDIR")}) + "\\n")
if args == ["mod", "edit", "-json"]:
    print(json.dumps({"Tool": [{"Path": p} for p in json.loads(os.environ["P2_TOOLS"])]}))
elif args[:1] == ["env"]:
    print(os.environ.get(args[1], ""))
elif args[:1] == ["install"]:
    assert "-mod=readonly" in args and not any("@" in arg for arg in args)
    assert pathlib.Path(os.environ["GOCACHE"]).is_dir()
    assert pathlib.Path(os.environ["GOTMPDIR"]).is_dir()
    if os.getenv("P2_INSTALL_FAIL"):
        sys.exit(23)
    dest = pathlib.Path(os.environ["GOBIN"])
    dest.mkdir(parents=True, exist_ok=True)
    for package in args[1:]:
        if package.startswith("-"):
            continue
        executable = dest / package.rsplit("/", 1)[-1]
        executable.write_text("#!/bin/sh\\necho " + executable.name + " fixture-pinned\\nexit " + os.getenv("P2_VERSION_EXIT", "0") + "\\n")
        executable.chmod(0o755)
elif args[:1] == ["test"]:
    assert pathlib.Path(os.environ["GOCACHE"]).is_dir()
    assert pathlib.Path(os.environ["GOTMPDIR"]).is_dir()
    assert "-count=1" in args and "-shuffle=on" in args and "-race" in args
    sys.exit(int(os.getenv("P2_TEST_EXIT", "0")))
else:
    sys.exit("unexpected Go command: " + repr(args))
''')
        fake_go.chmod(0o755)
        tool_env = dict(os.environ, GO=str(fake_go), GOBIN=str(work / "tools with spaces"),
                        GOPATH=str(work / "first gopath") + ":" + str(work / "second"),
                        GITHUB_PATH=str(work / "github-path"), P2_TOOLS=json.dumps(TOOLS),
                        P2_GO_LOG=str(work / "go-log"))
        tool_env.pop("GOCACHE", None)
        tool_env.pop("GOTMPDIR", None)
        install = ["make", "--no-print-directory", "protobuf-tools"]
        run("install into GOBIN and print versions", install, tool_env, contains="fixture-pinned")
        if Path(tool_env["GITHUB_PATH"]).exists():
            require("CI exports selected binary directory", tool_env["GOBIN"] in Path(tool_env["GITHUB_PATH"]).read_text().splitlines())
        else:
            require("CI exports selected binary directory", False)
        run("first GOPATH entry fallback", install, dict(tool_env, GOBIN=""), contains="first gopath/bin")
        old_bin = work / "old-bin"
        old_bin.mkdir()
        for package in TOOLS:
            plugin = old_bin / package.rsplit("/", 1)[-1]
            plugin.write_text("#!/bin/sh\necho stale-plugin\nexit 99\n")
            plugin.chmod(0o755)
        run("selected bin overrides stale PATH plugins", install,
            dict(tool_env, PATH=str(old_bin) + os.pathsep + tool_env["PATH"]), contains="fixture-pinned")
        run("missing tool directive", install, dict(tool_env, P2_TOOLS=json.dumps(TOOLS[:1])), 1, TOOLS[1])
        run("missing binary destination fails", install, dict(tool_env, GOBIN="", GOPATH=""), 1, "require GOBIN or GOPATH")
        run("installation failure propagates", install, dict(tool_env, P2_INSTALL_FAIL="1"), 1)
        run("version failure propagates", install, dict(tool_env, P2_VERSION_EXIT="1"), 1)
        run("test recipe passes and cleans cache", ["make", "--no-print-directory", "test"], tool_env)
        run("test recipe failure propagates", ["make", "--no-print-directory", "test"], dict(tool_env, P2_TEST_EXIT="7"), 1)
        log_path = Path(tool_env["P2_GO_LOG"])
        records = [json.loads(line) for line in log_path.read_text().splitlines()] if log_path.exists() else []
        cache_paths = [record[key] for record in records for key in ("cache", "tmp")
                       if record.get(key) and record["args"][0] in ("install", "test")]
        require("temporary caches removed after success and failure", bool(cache_paths) and all(not Path(path).exists() for path in cache_paths))

        shared_cache, shared_tmp = work / "shared-cache", work / "shared-tmp"
        shared_cache.mkdir()
        shared_tmp.mkdir()
        sentinel = shared_cache / "caller-owned"
        sentinel.write_text("preserve")
        shared_env = dict(tool_env, GOCACHE=str(shared_cache), GOTMPDIR=str(shared_tmp))
        run("installer reuses caller cache", install, shared_env)
        run("test reuses caller cache", ["make", "--no-print-directory", "test"], shared_env)
        run("failed test preserves caller cache", ["make", "--no-print-directory", "test"], dict(shared_env, P2_TEST_EXIT="7"), 1)
        records = [json.loads(line) for line in log_path.read_text().splitlines()]
        latest = [record for record in records if record["args"][0] in ("install", "test")][-3:]
        require("caller cache reused and retained", sentinel.read_text() == "preserve" and shared_tmp.is_dir()
                and len(latest) == 3 and all(record["cache"] == str(shared_cache) and record["tmp"] == str(shared_tmp) for record in latest))

    args.report.parent.mkdir(parents=True, exist_ok=True)
    passed = all(check["passed"] for check in checks)
    args.report.write_text(json.dumps({"schema": "gofly.p2_ci_script_check.v1",
                                      "status": "passed" if passed else "failed",
                                      "checks": checks}, indent=2) + "\n")
    for check in checks:
        print(f"{'PASS' if check['passed'] else 'FAIL'} {check['name']}")
        if not check["passed"]:
            print(check.get("output", ""))
    print(f"Evidence: {args.report}")
    return 0 if passed else 1


if __name__ == "__main__":
    sys.exit(main())
