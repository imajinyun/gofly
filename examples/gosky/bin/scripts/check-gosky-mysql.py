#!/usr/bin/env python3
"""Run Gosky's required HTTP/MySQL E2Es in a disposable, migrated database."""

import hashlib
import json
import os
import pathlib
import re
import secrets
import signal
import subprocess
import sys
import tempfile
import time


GOSKY = pathlib.Path(__file__).resolve().parents[2]
FRAMEWORK = GOSKY.parents[1]
# Same reviewed MySQL digest as the migration coverage fixture.
MYSQL_IMAGE = "mysql@sha256:0744ee5ef89ce6ccfa13de3e579fe6b9e27f93dd70da9c06d2c908b1b193fb8d"
TESTS = (
    "TestMySQLIdentityBoundaryLifecycleIntegration",
    "TestMySQLIdentityRejectsInvalidStoredHashIntegration",
    "TestMySQLProjectPersistenceBoundaryIntegration",
    "TestMySQLSmartWebRoleMembersAndScopesIntegration",
    "TestMySQLSmartWebRoleMenuRoundTripIntegration",
    "TestMySQLExplicitAdminPermissionsIntegration",
    "TestMySQLDynamicRoleLifecycleIntegration",
    "TestMySQLDynamicMenuCRUDIntegration",
    "TestMySQLUserAdminLifecycleIntegration",
    "TestMySQLRBACLastAdministratorLockoutIntegration",
    "TestMySQLRBACTenantIsolationIntegration",
    "TestMySQLOrganizationManagementIntegration",
)


def run(arguments, *, cwd, env, diagnostic, output=None, timeout=300, check=True):
    """Bound child lifetime, including compiler/test processes, without a shell."""
    with subprocess.Popen(arguments, cwd=cwd, env=env, stdout=output or subprocess.PIPE,
                          stderr=diagnostic, text=True, start_new_session=True) as process:
        try:
            text, _ = process.communicate(timeout=timeout)
        except BaseException:
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            process.wait()
            raise
        if check and process.returncode:
            # Do not print argv: database credentials may occur in Docker args.
            raise RuntimeError(f"command exited with status {process.returncode}; see commands.log")
        return process.returncode, (text or "").strip()


def test_results(path):
    states = dict.fromkeys(TESTS, "missing")
    rejected = []
    package_passed = False
    with path.open() as stream:
        for line in stream:
            event = json.loads(line)
            action, test = event.get("Action"), event.get("Test")
            if test in states and action in ("pass", "fail", "skip"):
                states[test] = action
            if action in ("fail", "skip"):
                rejected.append(test or event.get("Package", "package"))
            if action == "pass" and not test:
                package_passed = True
    return states, rejected, package_passed


def main():
    reports = GOSKY / ".tmp-test/gosky-mysql"
    # Reports are repository-owned; reject a parent symlink escaping the repo.
    reports.resolve().relative_to(GOSKY)
    reports.mkdir(parents=True, exist_ok=True)
    report_dir = pathlib.Path(tempfile.mkdtemp(prefix="run-", dir=reports))
    report = {"schema": "gofly.gosky_mysql_e2e.v1", "status": "failed", "stage": "docker",
              "image": MYSQL_IMAGE, "tests": dict.fromkeys(TESTS, "missing"), "cleanup": "not_started"}
    started = time.monotonic()
    nonce = secrets.token_hex(8)
    container_name = "gofly-gosky-" + nonce
    report["containerName"] = container_name
    attempted, container = False, ""
    try:
        with tempfile.TemporaryDirectory(prefix="gosky-mysql-") as temporary, (report_dir / "commands.log").open("w") as diagnostic:
            scratch = pathlib.Path(temporary)
            for directory in ("gocache", "gotmp"):
                (scratch / directory).mkdir()
            env = dict(os.environ, GOCACHE=str(scratch / "gocache"), GOTMPDIR=str(scratch / "gotmp"),
                       GOFLAGS=os.environ.get("GOFLAGS", "") + " -count=1",
                       GOPROXY="direct", GOFLY_CACHE_DISABLED="true")
            go = os.environ.get("GO") or "go"

            def command(arguments, **options):
                return run(arguments, cwd=options.pop("cwd", GOSKY), env=env,
                           diagnostic=diagnostic, **options)

            try:
                command(["docker", "info", "--format", "{{.ServerVersion}}"], timeout=15)
                report["goVersion"] = command([go, "version"], timeout=30)[1]
                report["stage"] = "container_start"
                password = secrets.token_hex(16)
                attempted = True
                print("Gosky MySQL E2E: starting disposable database", flush=True)
                _, container = command(["docker", "run", "-d", "--rm", "--name", container_name,
                                        "--label", "gofly.gosky-e2e=" + nonce,
                                        "-p", "127.0.0.1::3306", "-e", "MYSQL_ROOT_PASSWORD=" + password,
                                        "-e", "MYSQL_DATABASE=gosky_e2e", MYSQL_IMAGE], timeout=180)
                report["stage"] = "database_ready"
                deadline = time.monotonic() + 120
                while True:
                    code, _ = command(["docker", "exec", "-e", "MYSQL_PWD=" + password, container_name,
                                       "mysqladmin", "ping", "-uroot", "-h127.0.0.1", "--protocol=tcp"],
                                      timeout=10, check=False)
                    if code == 0:
                        break
                    if time.monotonic() >= deadline:
                        raise RuntimeError("MySQL did not become TCP-ready before the deadline")
                    time.sleep(1)
                mapping = command(["docker", "port", container_name, "3306/tcp"], timeout=15)[1]
                match = re.fullmatch(r"127\.0\.0\.1:(\d+)", mapping)
                if not match or not 0 < int(match[1]) < 65536:
                    raise RuntimeError("invalid loopback port mapping")
                env["GOSKY_MYSQL_DSN"] = f"root:{password}@tcp(127.0.0.1:{match[1]})/gosky_e2e?parseTime=true&charset=utf8mb4"

                report["stage"] = "migration"
                report["migrations"] = {p.name: hashlib.sha256(p.read_bytes()).hexdigest()
                                         for p in sorted((GOSKY / "internal/migrations").glob("*.up.sql"))}
                if not report["migrations"]:
                    raise RuntimeError("no Gosky migrations found")
                print("Gosky MySQL E2E: applying migrations", flush=True)
                with (report_dir / "migration.json").open("w") as output:
                    command([go, "run", "./cmd/gofly", "--output", "json", "migrate", "up", "--driver", "mysql",
                             "--dir", str(GOSKY / "internal/migrations"), "--dsn-env", "GOSKY_MYSQL_DSN"],
                            cwd=FRAMEWORK, output=output)
                if json.loads((report_dir / "migration.json").read_text()).get("ok") is not True:
                    raise RuntimeError("migration returned a non-success envelope")

                report["stage"] = "tests"
                print("Gosky MySQL E2E: running required suites with race detection", flush=True)
                transcript = report_dir / "tests.jsonl"
                with transcript.open("w") as output:
                    code, _ = command([go, "test", "-json", "-race", "-count=1", "-shuffle=on", "-tags=integration",
                                       "./internal/routes", "-run", "^(" + "|".join(TESTS) + ")$"],
                                      cwd=GOSKY, output=output, timeout=600, check=False)
                states, rejected, package_passed = test_results(transcript)
                report.update(tests=states, rejected=rejected, packagePassed=package_passed)
                if code or rejected or not package_passed or any(state != "pass" for state in states.values()):
                    raise RuntimeError("required MySQL suites did not all pass; see tests.jsonl")
                report["status"] = "passed"
                report["stage"] = "complete"
            except Exception as error:
                report["error"] = str(error) if isinstance(error, RuntimeError) else type(error).__name__
            finally:
                if attempted:
                    try:
                        # The random label proves ownership even if docker run
                        # created the container before returning an error.
                        code, label = command(["docker", "inspect", "--format",
                                               '{{ index .Config.Labels "gofly.gosky-e2e" }}', container_name],
                                              timeout=15, check=False)
                        if code == 0 and label == nonce:
                            command(["docker", "rm", "-f", "-v", container_name], timeout=30)
                            report["cleanup"] = "passed"
                        elif code and not container:
                            report["cleanup"] = "not_created"
                        else:
                            raise RuntimeError("cannot confirm ownership of the fixture container")
                    except Exception:
                        report.update(status="failed", stage="cleanup", cleanup="failed")
                        report["error"] = "fixture cleanup failed; inspect commands.log"
    except Exception as error:
        report.update(status="failed", stage="runner")
        report["error"] = type(error).__name__
    finally:
        report["durationSeconds"] = round(time.monotonic() - started, 3)
        (report_dir / "summary.json").write_text(json.dumps(report, indent=2) + "\n")
        print(f"Gosky MySQL E2E report: {report_dir / 'summary.json'}", flush=True)
    if report["status"] != "passed":
        print(f"Gosky MySQL E2E failed at {report['stage']}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    def terminate(signum, _frame):
        raise InterruptedError(f"interrupted by signal {signum}")

    signal.signal(signal.SIGTERM, terminate)
    signal.signal(signal.SIGINT, terminate)
    sys.exit(main())
