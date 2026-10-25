"""Exercise the huffman table gate, including its refusal path."""
import os
import pathlib
import re
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPT = os.path.join(HERE, "gen-huffman-table.py")
TABLE = os.path.join(HERE, "..", "internal", "stdlib", "std", "http", "huffman.fern.md")


class HuffmanTableGateTest(unittest.TestCase):
    def check(self, target):
        before = pathlib.Path(target).read_bytes()
        result = subprocess.run(
            [sys.executable, SCRIPT, "--check", "--target", target],
            capture_output=True, text=True, check=False,
        )
        self.assertEqual(pathlib.Path(target).read_bytes(), before, "--check modified its input")
        return result

    def test_committed_tables_pass(self):
        self.assertEqual(self.check(TABLE).returncode, 0, self.check(TABLE).stderr)

    def test_drifted_huff_fails(self):
        text = pathlib.Path(TABLE).read_text(encoding="utf-8")
        # Flip the first HUFF entry so it no longer matches HUFF_CODE/HUFF_LEN,
        # leaving HUFF_CODE/HUFF_LEN (which encode/decode read) consistent so
        # the drift, not a round-trip failure, is what the gate catches.
        m = re.search(r"(const HUFF: i64\[\] = \[\s*)(\d+)", text)
        self.assertIsNotNone(m, "could not find HUFF in the table")
        drifted = text[: m.start(2)] + str(int(m.group(2)) + 1) + text[m.end(2) :]
        with tempfile.TemporaryDirectory() as directory:
            target = os.path.join(directory, "huffman.fern.md")
            pathlib.Path(target).write_text(drifted, encoding="utf-8")
            result = self.check(target)
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("out of date", result.stderr)


if __name__ == "__main__":
    unittest.main()
