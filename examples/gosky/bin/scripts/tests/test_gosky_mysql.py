"""GOV-001..004: exercise the runner through its actual process boundary."""

import json
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile
import unittest


SCRIPT = pathlib.Path(__file__).resolve().parents[1] / "check-gosky-mysql.py"
TESTS = [
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
]

FAKE_TOOL = r'''
import json, os, pathlib, sys
args = sys.argv[1:]
kind = pathlib.Path(sys.argv[0]).name
state = pathlib.Path(os.environ["FAKE_STATE"])
with open(os.environ["FAKE_TRACE"], "a") as stream:
    stream.write(json.dumps({"tool":kind,"args":args,"cwd":os.getcwd(),
        "dsn":os.environ.get("GOSKY_MYSQL_DSN"),"gocache":os.environ.get("GOCACHE"),
        "gotmpdir":os.environ.get("GOTMPDIR")}) + "\n")
mode = os.environ.get("FAKE_MODE", "pass")
if kind == "docker":
    if args[0] == "info": print("29.0.0")
    elif args[0] == "run":
        label = next(x for x in args if x.startswith("gofly.gosky-e2e="))
        state.write_text(label.split("=",1)[1])
        if mode == "start_failure": sys.exit(1)
        print("fixture-container")
    elif args[0] == "inspect":
        if not state.exists(): sys.exit(1)
        print(state.read_text())
    elif args[0] == "port": print("127.0.0.1:45678")
    elif args[0] == "rm":
        if mode == "cleanup_failure": sys.exit(1)
        state.unlink()
    elif args[0] != "exec": sys.exit(2)
else:
    if args[0] == "version": print("go version go1.26.8 linux/amd64")
    elif "migrate" in args:
        if mode == "migration_failure": sys.exit(1)
        print(json.dumps({"ok":True,"data":{}}))
    elif args[0] == "test":
        names=json.loads(os.environ["FAKE_TESTS"])
        for index,name in enumerate(names):
            if mode == "missing_test" and index == 0: continue
            action = "skip" if mode == "skipped_test" and index == 0 else "pass"
            print(json.dumps({"Action":action,"Package":"gosky/internal/routes","Test":name}))
        print(json.dumps({"Action":"pass","Package":"gosky/internal/routes"}))
        if mode == "test_failure": sys.exit(1)
    else: sys.exit(2)
'''


class GoskyMySQLRunnerTest(unittest.TestCase):
    def run_gate(self, mode="pass"):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            runner = root / "examples/gosky/bin/scripts/check-gosky-mysql.py"
            runner.parent.mkdir(parents=True)
            shutil.copyfile(SCRIPT, runner)
            migrations = root / "examples/gosky/internal/migrations"
            migrations.mkdir(parents=True)
            (migrations / "20261001000000_init.up.sql").write_text("CREATE TABLE example(id INT);\n")
            fakebin = root / "fakebin"
            fakebin.mkdir()
            for name in ("docker", "go"):
                path = fakebin / name
                path.write_text("#!" + sys.executable + "\n" + FAKE_TOOL)
                path.chmod(0o755)
            trace = root / "trace.jsonl"
            env = dict(os.environ, PATH=str(fakebin) + os.pathsep + os.environ["PATH"],
                       GO=str(fakebin / "go"), FAKE_MODE=mode, FAKE_STATE=str(root / "state"),
                       FAKE_TRACE=str(trace), FAKE_TESTS=json.dumps(TESTS),
                       GOSKY_MYSQL_DSN="root:untouched@tcp(127.0.0.1:3306)/existing")
            result = subprocess.run([sys.executable, str(runner)], env=env, text=True,
                                    capture_output=True, cwd=root / "fakebin", timeout=20, check=False)
            reports = list((root / "examples/gosky/.tmp-test/gosky-mysql").glob("*/summary.json"))
            self.assertEqual(len(reports), 1, result.stderr)
            report = json.loads(reports[0].read_text())
            events = [json.loads(line) for line in trace.read_text().splitlines()]
            for event in events:
                if event["tool"] == "go":
                    self.assertFalse(pathlib.Path(event["gocache"]).exists(), "temporary build cache leaked")
                    self.assertFalse(pathlib.Path(event["gotmpdir"]).exists(), "temporary work directory leaked")
            return result, report, events

    def test_pass_requires_fresh_database_migrations_race_and_all_tests(self):
        result, report, events = self.run_gate()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(report["status"], "passed")
        self.assertEqual(report["cleanup"], "passed")
        self.assertEqual(report["tests"], dict.fromkeys(TESTS, "pass"))
        start = next(e for e in events if e["tool"] == "docker" and e["args"][0] == "run")
        self.assertIn("127.0.0.1::3306", start["args"])
        self.assertRegex(start["args"][-1], r"^mysql@sha256:[0-9a-f]{64}$")
        migration = next(e for e in events if e["tool"] == "go" and "migrate" in e["args"] and "up" in e["args"])
        self.assertIn("--dsn-env", migration["args"])
        test_root = next(e["cwd"] for e in events if e["tool"] == "go" and e["args"][0] == "test")
        self.assertEqual(pathlib.Path(migration["cwd"]), pathlib.Path(test_root).parents[1])
        migration_dir = migration["args"][migration["args"].index("--dir") + 1]
        self.assertEqual(pathlib.Path(migration_dir), pathlib.Path(test_root) / "internal/migrations")
        self.assertNotIn("existing", migration["dsn"])
        self.assertIn("127.0.0.1:45678", migration["dsn"])
        test = next(e for e in events if e["tool"] == "go" and e["args"][0] == "test")
        for flag in ("-json", "-race", "-count=1", "-shuffle=on", "-tags=integration"):
            self.assertIn(flag, test["args"])
        self.assertEqual(pathlib.Path(test["cwd"]).name, "gosky")
        self.assertEqual(test["dsn"], migration["dsn"])
        self.assertNotIn("untouched", json.dumps(report))

    def test_failures_cannot_pass_and_always_attempt_cleanup(self):
        for mode in ("start_failure", "migration_failure", "test_failure", "missing_test", "skipped_test", "cleanup_failure"):
            with self.subTest(mode=mode):
                result, report, events = self.run_gate(mode)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(report["status"], "failed")
                self.assertTrue(any(e["tool"] == "docker" and e["args"][0] == "rm" for e in events))
                self.assertEqual(report["cleanup"], "failed" if mode == "cleanup_failure" else "passed")


if __name__ == "__main__":
    unittest.main()
