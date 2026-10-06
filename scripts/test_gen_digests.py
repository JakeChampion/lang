"""Exercise the generated-source gate, including its refusal paths."""
import pathlib
import subprocess
import sys
import tempfile
import unittest

import gen_digests


class DigestGenerationTest(unittest.TestCase):
    def check_source(self, text, expected_status):
        with tempfile.TemporaryDirectory() as directory:
            target = pathlib.Path(directory) / "crypto.fern"
            target.write_text(text, encoding="utf-8")
            before = target.read_bytes()
            result = subprocess.run(
                [sys.executable, gen_digests.__file__, "--check", "--target", str(target)],
                capture_output=True, text=True, check=False,
            )
            self.assertEqual(result.returncode, expected_status, result.stderr)
            self.assertEqual(target.read_bytes(), before, "--check modified its input")
            return result

    def test_current_output_and_handwritten_surroundings(self):
        self.check_source("// hand-written prefix\n" + gen_digests.generate() + "// suffix\n", 0)

    def test_generated_edit_fails_without_writing(self):
        text = gen_digests.generate().replace("function", "function /* drift */", 1)
        result = self.check_source(text, 1)
        self.assertIn("generated digests are stale", result.stderr)

    def test_invalid_markers_fail(self):
        for text in ["", gen_digests.BEGIN, gen_digests.generate() * 2,
                     gen_digests.END + "\n" + gen_digests.BEGIN]:
            with self.subTest(text=text[:80]):
                self.check_source(text, 1)

    def test_regeneration_preserves_surrounding_bytes(self):
        text = "// prefix\r\n" + gen_digests.BEGIN + "\nstale\n" + gen_digests.END + "\r\n// suffix"
        expected = "// prefix\r\n" + gen_digests.generate().rstrip("\n") + "\r\n// suffix"
        self.assertEqual(gen_digests.regenerated(text), expected)


if __name__ == "__main__":
    unittest.main()
