"""Fault controls for exact image bytes, metadata, failure state, and heap evidence."""

import copy
import hashlib
from pathlib import Path
import tempfile
import unittest

import qualify_jpeg_orientation as collector


class OrientationProtocolTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        raw = b"\1\2\3"
        digest = hashlib.sha256(raw).hexdigest()
        self.case = dict(name="identity", data_hex=raw.hex(), data_sha256=digest, output_hex=raw.hex(), pillow=True,
                         config=dict(width=1, height=1, stride=3, claimed_bytes=3, channels=3, orientation=1,
                                     max_pixels=1, max_bytes=3, max_planned_bytes=6, cancel_at=0),
                         expected=dict(status="ok", width=1, height=1, stride=3, channels=3, bytes=3,
                                       input_bytes=3, planned_bytes=6, polls=4, data_sha256=digest))
        cancelled = copy.deepcopy(self.case)
        cancelled["name"] = "cancelled"
        cancelled["config"]["cancel_at"] = 2
        cancelled["expected"] = dict(status="error", error="Cancelled", polls=2)
        self.cases = [self.case, cancelled]
        self.names = ["case-0", "case-1"]
        self.stdout = "\n".join(collector.protocol_line(n, c["expected"])
                                for n, c in zip(self.names, self.cases, strict=True)) + "\n"
        self.stderr = "leakcheck: allocs=19 frees=19 live_bytes=0\n"
        self.pixels = self.directory / "case-0.pixels.raw"
        self.pixels.write_bytes(raw)

    def check(self, stdout=None, stderr=None, cases=None, names=None, returncode=0):
        return collector.check_output(self.stdout if stdout is None else stdout,
                                      self.stderr if stderr is None else stderr,
                                      self.cases if cases is None else cases,
                                      self.names if names is None else names, self.directory, returncode)

    def test_exact_success_and_cancellation(self):
        samples, heap = self.check()
        self.assertEqual(samples[0]["observation"]["data_sha256"], self.case["data_sha256"])
        self.assertEqual(samples[1]["observation"]["error"], "Cancelled")
        self.assertEqual(heap, dict(allocs=19, frees=19, live_bytes=0))

    def test_metadata_error_and_poll_mismatch(self):
        for old, new in [("ok 1 1 3", "ok 2 1 3"), ("ok 1 1 3", "ok 1 1 4"),
                         ("3 3 6 4", "3 3 7 4"), ("Cancelled 2", "Limit 2"), ("Cancelled 2", "Cancelled 3")]:
            with self.assertRaises(ValueError):
                self.check(stdout=self.stdout.replace(old, new))

    def test_exact_pixels_and_missing_output(self):
        for raw in [b"", b"\1\2", b"\1\2\4", b"\1\2\3\4"]:
            self.pixels.write_bytes(raw)
            with self.assertRaises(ValueError):
                self.check()
        self.pixels.unlink()
        with self.assertRaises((ValueError, FileNotFoundError)):
            self.check()

    def test_no_partial_output_on_error(self):
        (self.directory / "case-1.pixels.raw").write_bytes(b"\0")
        with self.assertRaises(ValueError):
            self.check()

    def test_nonzero_exit_and_sanitizer(self):
        for status in [1, 3, 137, -9]:
            with self.assertRaises(ValueError):
                self.check(returncode=status)
        with self.assertRaises(ValueError):
            self.check(stderr=self.stderr + "fern-sanitizer: bounds\n")

    def test_heap_census(self):
        for text in ["", self.stderr * 2, "leakcheck: allocs=19 frees=18 live_bytes=0",
                     "leakcheck: allocs=19 frees=19 live_bytes=1", "leakcheck: allocs=0 frees=0 live_bytes=0"]:
            with self.assertRaises(ValueError):
                self.check(stderr=text)

    def test_missing_extra_reordered_or_duplicate_cases(self):
        lines = self.stdout.splitlines()
        for text in ["", lines[0] + "\n", self.stdout + "extra\n", "\n".join(reversed(lines)) + "\n", self.stdout + "\n"]:
            with self.assertRaises(ValueError):
                self.check(stdout=text)
        for names in [self.names[:1], [self.names[0], self.names[0]], list(reversed(self.names))]:
            with self.assertRaises(ValueError):
                self.check(names=names)
        with self.assertRaises(ValueError):
            self.check(cases=[self.case, self.case])

    def test_reference_identity_and_shape(self):
        for patch in [dict(data_hex="00"), dict(pillow=False),
                      dict(expected=dict(self.case["expected"], bytes=True)),
                      dict(config=dict(self.case["config"], cancel_at=-1)),
                      dict(config=dict(self.case["config"], orientation=9))]:
            case = copy.deepcopy(self.case)
            case.update(patch)
            with self.assertRaises(ValueError):
                self.check(cases=[case, self.cases[1]])


if __name__ == "__main__":
    unittest.main()
