import importlib.machinery
import importlib.util
import json
from pathlib import Path
import struct
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("security-smoke")
LOADER = importlib.machinery.SourceFileLoader("security_smoke", str(SCRIPT))
SPEC = importlib.util.spec_from_loader(LOADER.name, LOADER)
SMOKE = importlib.util.module_from_spec(SPEC)
LOADER.exec_module(SMOKE)


def image(code_flags=5, stack_flags=6, data_address=0x402000):
    data = bytearray(4096)
    data[:7] = b"\x7fELF\x02\x01\x01"
    struct.pack_into("<H", data, 18, 62)
    struct.pack_into("<Q", data, 32, 64)
    struct.pack_into("<HH", data, 54, 56, 3)
    struct.pack_into("<IIQQQQQQ", data, 64, 1, code_flags, 0, 0x400000, 0, 4096, 4096, 4096)
    struct.pack_into("<IIQQQQQQ", data, 120, 1, 6, 4096, data_address, 0, 0, 4096, 4096)
    struct.pack_into("<IIQQQQQQ", data, 176, 0x6474E551, stack_flags, 0, 0, 0, 0, 0, 16)
    return data


class BinaryInspection(unittest.TestCase):
    def test_valid_separate_code_data_and_nx_stack(self):
        self.assertTrue(SMOKE.elf_properties(image(), 62)["nx_stack"])

    def test_truncated_header_and_program_table(self):
        for size in (0, 63, 100, 231):
            with self.subTest(size=size), self.assertRaises(RuntimeError):
                SMOKE.elf_properties(image()[:size], 62)

    def test_wrong_architecture(self):
        with self.assertRaisesRegex(RuntimeError, "machine"):
            SMOKE.elf_properties(image(), 183)

    def test_writable_code(self):
        with self.assertRaisesRegex(RuntimeError, "writable executable"):
            SMOKE.elf_properties(image(code_flags=7), 62)

    def test_executable_stack(self):
        with self.assertRaisesRegex(RuntimeError, "executable ELF stack"):
            SMOKE.elf_properties(image(stack_flags=7), 62)

    def test_distinct_segments_cannot_share_a_writable_executable_page(self):
        with self.assertRaisesRegex(RuntimeError, "pages overlap"):
            SMOKE.elf_properties(image(data_address=0x400800), 62)

    def test_missing_stack_header(self):
        data = image()
        struct.pack_into("<I", data, 176, 0)
        with self.assertRaisesRegex(RuntimeError, "missing NX"):
            SMOKE.elf_properties(data, 62)

    def test_segment_outside_file(self):
        data = image()
        struct.pack_into("<Q", data, 64 + 32, 8192)
        with self.assertRaisesRegex(RuntimeError, "segment exceeds"):
            SMOKE.elf_properties(data, 62)


class PrerequisiteFailure(unittest.TestCase):
    def test_missing_compiler_fails_and_writes_evidence(self):
        with tempfile.TemporaryDirectory() as temp:
            report = Path(temp) / "report.json"
            result = subprocess.run(
                [sys.executable, SCRIPT, "--compiler", str(Path(temp) / "missing"), "--report", report],
                capture_output=True, timeout=10,
            )
            self.assertNotEqual(result.returncode, 0)
            evidence = json.loads(report.read_text())
            self.assertEqual(evidence["result"], "failed")
            self.assertEqual(evidence["cases"], [])
            self.assertIn("required executable is missing", evidence["error"])
            self.assertTrue(all(state == "unverified" for state in evidence["hardware_enforcement"].values()))


if __name__ == "__main__":
    unittest.main()
