import pathlib
import subprocess
import sys
import tempfile
import unittest


SCRIPT = pathlib.Path(__file__).resolve().parents[1] / "merge-migration-coverage.py"


class MigrationCoverageMergeTest(unittest.TestCase):
    def run_merge(self, base, extra):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / "base.out").write_text(base)
            (root / "extra.out").write_text(extra)
            result = subprocess.run(
                [sys.executable, str(SCRIPT), str(root / "merged.out"),
                 str(root / "base.out"), str(root / "extra.out")],
                text=True, capture_output=True, check=False,
            )
            output = root / "merged.out"
            return result.returncode, output.read_text() if output.exists() else ""

    def test_union_preserves_statement_denominator(self):
        code, output = self.run_merge(
            "mode: atomic\nexample/a.go:1.1,2.1 3 1\nexample/a.go:3.1,4.1 7 0\n",
            "mode: atomic\nexample/a.go:1.1,2.1 3 2\nexample/a.go:3.1,4.1 7 1\n",
        )
        self.assertEqual(code, 0)
        self.assertEqual(output, "mode: atomic\nexample/a.go:1.1,2.1 3 3\nexample/a.go:3.1,4.1 7 1\n")

    def test_unknown_block_is_rejected(self):
        code, output = self.run_merge("mode: atomic\nexample/a.go:1.1,2.1 3 1\n",
                                      "mode: atomic\nexample/b.go:1.1,2.1 3 1\n")
        self.assertNotEqual(code, 0)
        self.assertEqual(output, "")

    def test_changed_statement_count_is_rejected(self):
        code, _ = self.run_merge("mode: atomic\nexample/a.go:1.1,2.1 3 1\n",
                                 "mode: atomic\nexample/a.go:1.1,2.1 4 1\n")
        self.assertNotEqual(code, 0)

    def test_mode_mismatch_is_rejected(self):
        code, _ = self.run_merge("mode: atomic\nexample/a.go:1.1,2.1 3 1\n",
                                 "mode: set\nexample/a.go:1.1,2.1 3 1\n")
        self.assertNotEqual(code, 0)


if __name__ == "__main__":
    unittest.main()
