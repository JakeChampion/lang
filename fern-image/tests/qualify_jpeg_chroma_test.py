"""Fault controls for exact mathematical component-row observations."""

from pathlib import Path
import tempfile
import unittest

import qualify_jpeg_chroma as collector


class ChromaProtocolTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.expected = bytes([0, 25, 75, 100])
        self.case = {"name": "centered-ramp", "width": 4, "mode": "bilinear", "input_sha256": "source",
                     "samples_hex": self.expected.hex()}
        self.output = self.directory / "row-0.plane.row.raw"
        self.output.write_bytes(self.expected)
        self.stdout = "row row-0.plane 4\n"
        self.stderr = "leakcheck: allocs=17 frees=17 live_bytes=0\n"

    def check(self, stdout=None, stderr=None):
        return collector.check_output(self.stdout if stdout is None else stdout,
                                      self.stderr if stderr is None else stderr,
                                      [self.case], ["row-0.plane"], self.directory)

    def test_exact_result(self):
        samples, census = self.check()
        self.assertEqual(samples[0]["samples_sha256"], samples[0]["reference_samples_sha256"])
        self.assertEqual(samples[0]["mode"], "bilinear")
        self.assertEqual(census, {"allocs": 17, "frees": 17, "live_bytes": 0})

    def test_one_value_difference_rejects(self):
        self.output.write_bytes(bytes([0, 26, 75, 100]))
        with self.assertRaises(ValueError):
            self.check()

    def test_wrong_sample_order(self):
        self.output.write_bytes(self.expected[::-1])
        with self.assertRaises(ValueError):
            self.check()

    def test_storage_length(self):
        for data in [self.expected[:-1], self.expected + b"\0"]:
            self.output.write_bytes(data)
            with self.assertRaises(ValueError):
                self.check()

    def test_missing_output(self):
        self.output.unlink()
        with self.assertRaises(ValueError):
            self.check()

    def test_protocol_status_name_width(self):
        for value in [self.stdout.replace("row ", "error ", 1), self.stdout.replace("row-0", "row-1"),
                      self.stdout.replace(" 4", " 3"), self.stdout + "extra\n", ""]:
            with self.assertRaises(ValueError):
                self.check(stdout=value)

    def test_heap_census(self):
        for value in ["", self.stderr * 2, "leakcheck: allocs=17 frees=16 live_bytes=1", "leakcheck: allocs=0 frees=0 live_bytes=0"]:
            with self.assertRaises(ValueError):
                self.check(stderr=value)

    def test_missing_or_reordered_rows(self):
        for cases, names in [([self.case, self.case], ["row-0.plane", "row-1.plane"]), ([self.case], ["row-1.plane"])]:
            with self.assertRaises(ValueError):
                collector.check_output(self.stdout, self.stderr, cases, names, self.directory)


if __name__ == "__main__":
    unittest.main()
