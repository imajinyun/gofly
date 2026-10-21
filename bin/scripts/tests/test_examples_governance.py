"""Exercise example gate selection and security inventories at process boundaries."""

import json
import pathlib
import shutil
import subprocess
import tempfile
import unittest


SCRIPTS = pathlib.Path(__file__).resolve().parents[1]


class ExamplesGovernanceTest(unittest.TestCase):
    def test_independent_example_remains_in_catalog_but_leaves_root_execution(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            (root / "examples").mkdir()
            catalog = {"examples": [
                {"path": "examples/recipe"},
                {"path": "examples/service", "rootGate": False},
            ]}
            (root / "examples/catalog.json").write_text(json.dumps(catalog))
            result = subprocess.run(
                ["sh", "-c", '. "$1"; examples_catalog_paths "$2"',
                 "test", str(SCRIPTS / "examples-lib.sh"), str(root)],
                capture_output=True, text=True, check=False,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.splitlines(), ["examples/recipe"])

    def test_root_inventory_ignores_all_examples_without_catalog(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            script = root / "bin/scripts/gosec-exception-inventory.sh"
            script.parent.mkdir(parents=True)
            shutil.copyfile(SCRIPTS / script.name, script)
            (root / "examples").mkdir()
            for relative in ("core/source.go", "examples/service/source.go", "examples/recipe/source.go"):
                source = root / relative
                source.parent.mkdir(parents=True, exist_ok=True)
                source.write_text("package example\n// #nosec G101 -- protocol header\n")
            result = subprocess.run(["sh", str(script)], capture_output=True,
                                    text=True, check=False)
            self.assertEqual(result.returncode, 0, result.stderr)
            report = json.loads(result.stdout)
            self.assertEqual([entry["file"] for entry in report["entries"]], ["core/source.go"])


if __name__ == "__main__":
    unittest.main()
