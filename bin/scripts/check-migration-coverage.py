#!/usr/bin/env python3
"""Run the existing migration E2Es in disposable databases and merge coverage."""

import json
import os
import pathlib
import shutil
import signal
import subprocess
import sys
import time


ROOT = pathlib.Path(__file__).resolve().parents[2]
IMAGES = {
    "mysql": "mysql@sha256:0744ee5ef89ce6ccfa13de3e579fe6b9e27f93dd70da9c06d2c908b1b193fb8d",
    "postgres": "postgres@sha256:721873c34ceb9f8d8fc265984940dc982404c105f19ad51be9fdc5970a6080ea",
}


def docker(*arguments):
    return subprocess.check_output(["docker", *arguments], text=True, stderr=subprocess.PIPE).strip()


def main():
    if len(sys.argv) != 3:
        raise ValueError("usage: check-migration-coverage.py BASE MERGED")
    if not shutil.which("docker"):
        raise RuntimeError("full-repository coverage requires Docker for migration E2Es")
    docker("info", "--format", "{{.ServerVersion}}")
    reports = ROOT / ".tmp-test" / "migration-coverage"
    reports.mkdir(parents=True, exist_ok=True)
    baseline_report = reports / "baseline.out"
    if pathlib.Path(sys.argv[1]).resolve() != baseline_report.resolve():
        shutil.copyfile(sys.argv[1], baseline_report)
    profiles = []
    for driver, image in IMAGES.items():
        port = "3306" if driver == "mysql" else "5432"
        variables = (["MYSQL_ROOT_PASSWORD=gofly-test-only", "MYSQL_DATABASE=migration_e2e"]
                     if driver == "mysql" else
                     ["POSTGRES_PASSWORD=gofly-test-only", "POSTGRES_USER=gofly", "POSTGRES_DB=migration_e2e"])
        arguments = ["run", "-d", "--rm", "--label", "gofly.test=migration-coverage",
                     "-p", f"127.0.0.1::{port}"]
        for variable in variables:
            arguments.extend(["-e", variable])
        container = docker(*arguments, image)
        try:
            ready = (["mysqladmin", "ping", "-uroot", "-pgofly-test-only"] if driver == "mysql"
                     else ["pg_isready", "-U", "gofly", "-d", "migration_e2e"])
            deadline = time.monotonic() + 120
            while True:
                # MySQL's initialization server is socket-only; TCP readiness
                # confirms the final server is accepting the mapped connection.
                command = ready + (["-h127.0.0.1", "--protocol=tcp"] if driver == "mysql" else ["-h", "127.0.0.1"])
                result = subprocess.run(["docker", "exec", container, *command],
                                        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)
                if result.returncode == 0:
                    break
                if time.monotonic() >= deadline:
                    raise RuntimeError(f"{driver} migration fixture did not become ready")
                time.sleep(1)
            mapping = docker("port", container, f"{port}/tcp")
            host_port = mapping.rsplit(":", 1)[1]
            dsn = (f"root:gofly-test-only@tcp(127.0.0.1:{host_port})/migration_e2e" if driver == "mysql"
                   else f"postgres://gofly:gofly-test-only@127.0.0.1:{host_port}/migration_e2e?sslmode=disable")
            profile = reports / f"{driver}.out"
            environment = dict(os.environ, GOFLY_MIGRATION_TEST_DRIVER=driver,
                               GOFLY_MIGRATION_TEST_DSN=dsn,
                               GOFLY_MIGRATION_TEST_REPORT=str(reports / f"{driver}.json"))
            print(f"Migration coverage E2E: {driver}", flush=True)
            subprocess.run([os.environ.get("GO", "go"), "test", "-tags=integration", "-count=1",
                            "-shuffle=on", "-covermode=atomic", f"-coverprofile={profile}",
                            "-coverpkg=./cmd/gofly/internal/migration,./cmd/gofly/internal/command", "./cmd/gofly/internal/command",
                            "-run", "^TestMigrationDatabaseLifecycle$", "-v"],
                           cwd=ROOT, env=environment, check=True)
            profiles.append(str(profile))
        finally:
            docker("rm", "-f", container)
    subprocess.run([sys.executable, str(ROOT / "bin/scripts/merge-migration-coverage.py"),
                    sys.argv[2], sys.argv[1], *profiles], check=True)
    combined_report = reports / "combined.out"
    if pathlib.Path(sys.argv[2]).resolve() != combined_report.resolve():
        shutil.copyfile(sys.argv[2], combined_report)
    (reports / "manifest.json").write_text(json.dumps({
        "schema": "gofly.migration_coverage.v1", "images": IMAGES,
        "profiles": profiles, "statementDenominator": "unchanged from baseline",
    }, indent=2) + "\n")


if __name__ == "__main__":
    def terminate(signum, _frame):
        raise SystemExit(128 + signum)

    signal.signal(signal.SIGTERM, terminate)
    try:
        main()
    except (ValueError, OSError, RuntimeError, subprocess.CalledProcessError) as error:
        print(f"migration coverage failed: {error}", file=sys.stderr)
        sys.exit(1)
