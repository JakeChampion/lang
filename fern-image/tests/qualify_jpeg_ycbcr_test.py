"""Fault controls for exact explicit sample conversion observations."""

import hashlib
from pathlib import Path
import tempfile
import unittest

import qualify_jpeg_ycbcr as collector


class ConversionProtocolTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.expected = bytes([254, 0, 0, 0, 255, 0])
        self.case = {"name": "primary-pair", "count": 2, "input_sha256": "source",
                     "rgb_hex": self.expected.hex()}
        self.output = self.directory / "row-0.ycbcr.rgb.raw"
        self.output.write_bytes(self.expected)
        self.stdout = "converted row-0.ycbcr 2\n"
        self.stderr = "leakcheck: allocs=19 frees=19 live_bytes=0\n"

    def check(self, stdout=None, stderr=None):
        return collector.check_output(self.stdout if stdout is None else stdout,
                                      self.stderr if stderr is None else stderr,
                                      [self.case], ["row-0.ycbcr"], self.directory)

    def test_exact_result(self):
        samples, census = self.check()
        self.assertEqual(samples[0]["rgb_sha256"], hashlib.sha256(self.expected).hexdigest())
        self.assertEqual(census, {"allocs": 19, "frees": 19, "live_bytes": 0})

    def test_one_value_difference_rejects(self):
        self.output.write_bytes(bytes([255, 0, 0, 0, 255, 0]))
        with self.assertRaises(ValueError):
            self.check()

    def test_swapped_channels_reject(self):
        self.output.write_bytes(bytes([0, 0, 254, 0, 255, 0]))
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

    def test_protocol_status_name_count(self):
        for value in [self.stdout.replace("converted", "error"), self.stdout.replace("row-0", "row-1"),
                      self.stdout.replace(" 2", " 1"), self.stdout + "extra\n", ""]:
            with self.assertRaises(ValueError):
                self.check(stdout=value)

    def test_heap_census(self):
        for value in ["", self.stderr * 2, "leakcheck: allocs=19 frees=18 live_bytes=1", "leakcheck: allocs=0 frees=0 live_bytes=0"]:
            with self.assertRaises(ValueError):
                self.check(stderr=value)

    def test_missing_or_reordered_rows(self):
        for cases, names in [([self.case, self.case], ["row-0.ycbcr", "row-1.ycbcr"]),
                             ([self.case], ["row-1.ycbcr"])]:
            with self.assertRaises(ValueError):
                collector.check_output(self.stdout, self.stderr, cases, names, self.directory)


if __name__ == "__main__":
    unittest.main()
