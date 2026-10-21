"""Verify root CI command planning works without any example checkout."""

import os
import pathlib
import shutil
import subprocess
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[3]


class RootCIBoundaryTest(unittest.TestCase):
    def test_ci_fast_plans_only_toolkit_checks_without_examples(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            shutil.copyfile(ROOT / "Makefile", root / "Makefile")
            result = subprocess.run(["make", "-n", "ci-fast"], cwd=root,
                                    capture_output=True, text=True, check=False)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertNotIn("examples", result.stdout + result.stderr)
            self.assertIn("go vet", result.stdout)
            self.assertIn("-race", result.stdout)
            self.assertIn("check-mod-tidy.sh", result.stdout)

    def test_governance_engineering_smoke_does_not_start_examples(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            fake_go = root / "go"
            fake_go.write_text('#!/bin/sh\nprintf "%s\\n" "$*" > "$TRACE"\n')
            fake_go.chmod(0o755)
            # Exercise the actual round implementation without rerunning
            # unrelated full-repository tests or starting Docker fixtures.
            script = (ROOT / "bin/scripts/governance-10-rounds.sh").read_text()
            function = script.split("round_engineering_smoke() {", 1)[1].split("\n}", 1)[0]
            command = 'set -eu\nround_engineering_smoke() {' + function + '\n}\nround_engineering_smoke'
            trace = root / "trace"
            env = dict(os.environ, root=str(root), go_cmd=str(fake_go),
                       testflags="-count=1 -shuffle=on", TRACE=str(trace))
            result = subprocess.run(["sh", "-c", command], cwd=root, env=env,
                                    capture_output=True, text=True, check=False)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(trace.read_text().strip(), "test -count=1 -shuffle=on ./bench/...")


if __name__ == "__main__":
    unittest.main()
