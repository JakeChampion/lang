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
    struct.pack_into("<HHI", data, 16, 2, 62, 1)
    struct.pack_into("<Q", data, 24, 0x400100)
    struct.pack_into("<Q", data, 32, 64)
    struct.pack_into("<H", data, 52, 64)
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
            SMOKE.elf_properties(image(data_address=0x400000), 62)

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

    def test_invalid_executable_header(self):
        for offset, fmt, value in ((16, "<H", 1), (20, "<I", 0), (52, "<H", 63)):
            data = image()
            struct.pack_into(fmt, data, offset, value)
            with self.subTest(offset=offset), self.assertRaisesRegex(RuntimeError, "header"):
                SMOKE.elf_properties(data, 62)

    def test_program_header_offset_cannot_wrap(self):
        data = image()
        struct.pack_into("<Q", data, 32, (1 << 64) - 56)
        with self.assertRaisesRegex(RuntimeError, "program header"):
            SMOKE.elf_properties(data, 62)

    def test_load_alignment_must_be_a_power_of_two_and_congruent(self):
        for align, address in ((3, 0x400000), (4096, 0x400001)):
            data = image()
            struct.pack_into("<Q", data, 64 + 16, address)
            struct.pack_into("<Q", data, 64 + 48, align)
            with self.subTest(align=align, address=address), self.assertRaisesRegex(RuntimeError, "alignment"):
                SMOKE.elf_properties(data, 62)

    def test_virtual_load_range_cannot_wrap(self):
        data = image()
        struct.pack_into("<Q", data, 64 + 16, (1 << 64) - 4096)
        struct.pack_into("<Q", data, 64 + 40, 8192)
        with self.assertRaisesRegex(RuntimeError, "virtual range"):
            SMOKE.elf_properties(data, 62)

    def test_entry_must_be_in_file_backed_executable_memory(self):
        for entry in (0, 0x401000, 0x402000, (1 << 64) - 1):
            data = image()
            struct.pack_into("<Q", data, 24, entry)
            with self.subTest(entry=entry), self.assertRaisesRegex(RuntimeError, "entry"):
                SMOKE.elf_properties(data, 62)
        data = image()
        struct.pack_into("<Q", data, 64 + 32, 0)
        with self.assertRaisesRegex(RuntimeError, "entry"):
            SMOKE.elf_properties(data, 62)

    def test_empty_load_has_no_pages_to_overlap(self):
        data = image(data_address=0x400800)
        struct.pack_into("<Q", data, 120 + 40, 0)
        struct.pack_into("<Q", data, 120 + 48, 1)
        self.assertTrue(SMOKE.elf_properties(data, 62)["wx"])

    def test_both_native_machines_and_position_independent_images(self):
        for machine in (62, 183):
            for kind in (2, 3):
                for align in (0, 1, 4096):
                    data = image()
                    struct.pack_into("<HH", data, 16, kind, machine)
                    struct.pack_into("<Q", data, 64 + 48, align)
                    with self.subTest(machine=machine, kind=kind, align=align):
                        self.assertTrue(SMOKE.elf_properties(data, machine)["nx_stack"])


class PrerequisiteFailure(unittest.TestCase):
    def test_missing_qemu_fails_before_any_case(self):
        with tempfile.TemporaryDirectory() as temp:
            report = Path(temp) / "report.json"
            result = subprocess.run(
                [sys.executable, SCRIPT, "--compiler", sys.executable,
                 "--qemu", str(Path(temp) / "missing-qemu"), "--report", report],
                capture_output=True, timeout=10,
            )
            self.assertNotEqual(result.returncode, 0)
            evidence = json.loads(report.read_text())
            self.assertEqual(evidence["cases"], [])
            self.assertIn("missing-qemu", evidence["error"])

    def test_compile_fail_case_refuses_success_and_unrelated_errors(self):
        with tempfile.TemporaryDirectory() as temp:
            for compiler in ("/bin/true", "/bin/false"):
                with self.subTest(compiler=compiler), self.assertRaisesRegex(RuntimeError, "wrong compile-fail"):
                    SMOKE.run_case(compiler, "unused", "unused", "x86-64-linux",
                                   "diag_e051", Path(temp), 5, [])

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
