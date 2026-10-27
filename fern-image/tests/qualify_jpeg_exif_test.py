"""Fault controls for finite Exif metadata observations and worker cleanup."""

import copy
import hashlib
import struct
import unittest

import qualify_jpeg_exif as collector


class ExifProtocolTests(unittest.TestCase):
    def setUp(self):
        payload = b"Exif\0\0II" + struct.pack("<HIHI", 42, 8, 0, 0)
        semantic = dict(orientation=None, order="II", directories=1, entries=0)
        self.case = dict(name="absent", payload_hex=payload.hex(),
                         payload_sha256=hashlib.sha256(payload).hexdigest(),
                         max_bytes=65533, max_entries=128, cancel_at=0,
                         expected=dict(status="ok", **semantic, polls=2),
                         construction=semantic, pillow=dict(applicable=True, orientation=None))
        self.cancelled = copy.deepcopy(self.case)
        self.cancelled.update(name="cancelled", cancel_at=1,
                              expected=dict(status="error", error="Cancelled", polls=1))
        self.cases = [self.case, self.cancelled]
        self.names = ["case-0.exif-worker", "case-1.exif-worker"]
        self.stdout = "\n".join(collector.protocol_line(n, c["expected"])
                                for c, n in zip(self.cases, self.names, strict=True)) + "\n"
        self.stderr = "leakcheck: allocs=19 frees=19 live_bytes=0\n"

    def check(self, stdout=None, stderr=None, cases=None, names=None, returncode=0):
        return collector.check_output(self.stdout if stdout is None else stdout,
                                      self.stderr if stderr is None else stderr,
                                      self.cases if cases is None else cases,
                                      self.names if names is None else names, returncode)

    def test_exact_success_and_error(self):
        samples, census = self.check()
        self.assertIsNone(samples[0]["observation"]["orientation"])
        self.assertEqual(samples[1]["observation"]["error"], "Cancelled")
        self.assertEqual(census, dict(allocs=19, frees=19, live_bytes=0))

    def test_metadata_and_absence_mismatch(self):
        for old, new in [("ok 0 II 1 0 2", "ok 1 II 1 0 2"),
                         ("ok 0 II 1 0 2", "ok 0 MM 1 0 2"),
                         ("ok 0 II 1 0 2", "ok 0 II 2 0 2"),
                         ("ok 0 II 1 0 2", "ok 0 II 1 1 2"),
                         ("ok 0 II 1 0 2", "ok 0 II 1 0 3")]:
            with self.assertRaises(ValueError):
                self.check(stdout=self.stdout.replace(old, new))

    def test_error_class_and_poll_mismatch(self):
        for text in ["error Directory 1", "error Cancelled 2", "error Unknown 1"]:
            with self.assertRaises(ValueError):
                self.check(stdout=self.stdout.replace("error Cancelled 1", text))

    def test_missing_extra_or_reordered_rows(self):
        lines = self.stdout.splitlines()
        for text in ["", lines[0] + "\n", self.stdout + "extra\n",
                     "\n".join(reversed(lines)) + "\n", self.stdout + "\n"]:
            with self.assertRaises(ValueError):
                self.check(stdout=text)

    def test_worker_case_identity(self):
        for names in [[self.names[1], self.names[0]], self.names[:1], [self.names[0], self.names[0]]]:
            with self.assertRaises(ValueError):
                self.check(names=names)
        with self.assertRaises(ValueError):
            self.check(cases=[self.case, self.case])

    def test_heap_census(self):
        for text in ["", self.stderr * 2, "leakcheck: allocs=19 frees=18 live_bytes=0",
                     "leakcheck: allocs=19 frees=19 live_bytes=1", "leakcheck: allocs=0 frees=0 live_bytes=0"]:
            with self.assertRaises(ValueError):
                self.check(stderr=text)

    def test_nonzero_process_exit(self):
        for code in [1, 3, 137, -9]:
            with self.assertRaises(ValueError):
                self.check(returncode=code)

    def test_reference_identity_and_shape(self):
        for change in [dict(payload_hex="00"), dict(max_bytes=True), dict(cancel_at=-1),
                       dict(expected=dict(self.case["expected"], orientation=9)),
                       dict(expected=dict(self.case["expected"], directories=True))]:
            case = copy.deepcopy(self.case)
            case.update(change)
            with self.assertRaises(ValueError):
                self.check(cases=[case, self.cancelled])


if __name__ == "__main__":
    unittest.main()
