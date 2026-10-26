"""Fault controls for whole-image grayscale observations."""

from pathlib import Path
import tempfile
import unittest

import qualify_jpeg_grayscale as collector


class GrayscaleProtocolTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.case = {"name": "two-pixels", "encoded_hex": "ffd8ffd9", "input_sha256": "input", "status": "accepted",
                     "reference": {"width": 2, "height": 1, "pixels": [7, 130]}}
        self.path = self.directory / "case-0.jpg.rgb.raw"
        self.path.write_bytes(bytes([7, 7, 7, 130, 130, 130]))
        self.stdout = "accepted case-0.jpg 2 1 6 6\n"
        self.stderr = "leakcheck: allocs=10 frees=10 live_bytes=0\n"

    def check(self, stdout=None, stderr=None, case=None):
        return collector.check_output(self.stdout if stdout is None else stdout,
                                      self.stderr if stderr is None else stderr,
                                      [self.case if case is None else case], ["case-0.jpg"], self.directory)

    def test_exact_image(self):
        samples, census = self.check()
        self.assertEqual(samples[0]["pixels_sha256"], samples[0]["reference_rgb_sha256"])
        self.assertEqual(census, {"allocs": 10, "frees": 10, "live_bytes": 0})

    def test_rounding_difference_remains_visible(self):
        self.path.write_bytes(bytes([8, 8, 8, 130, 130, 130]))
        samples, _ = self.check()
        self.assertEqual(samples[0]["different_samples"], 1)
        self.assertEqual(samples[0]["max_sample_error"], 1)

    def test_wrong_samples(self):
        self.path.write_bytes(bytes([10, 10, 10, 130, 130, 130]))
        with self.assertRaises(ValueError):
            self.check()

    def test_channel_replication(self):
        self.path.write_bytes(bytes([7, 8, 7, 130, 130, 130]))
        with self.assertRaises(ValueError):
            self.check()

    def test_metadata_or_storage_mismatch(self):
        for output in [self.stdout.replace("2 1", "1 2"), self.stdout.replace("6 6", "7 6"), "error case-0.jpg\n"]:
            with self.assertRaises(ValueError):
                self.check(stdout=output)
        self.path.write_bytes(b"short")
        with self.assertRaises(ValueError):
            self.check()
        self.path.unlink()
        with self.assertRaises(ValueError):
            self.check()

    def test_unsupported_profile(self):
        case = {**self.case, "status": "unsupported"}
        with self.assertRaises(ValueError):
            self.check(stdout="unsupported case-0.jpg\n", case=case)
        self.path.unlink()
        samples, _ = self.check(stdout="unsupported case-0.jpg\n", case=case)
        self.assertEqual(samples[0]["status"], "unsupported")
        with self.assertRaises(ValueError):
            self.check(case=case)

    def test_heap_census(self):
        for value in ["", self.stderr * 2, "leakcheck: allocs=10 frees=9 live_bytes=1", "leakcheck: allocs=0 frees=0 live_bytes=0"]:
            with self.assertRaises(ValueError):
                self.check(stderr=value)

    def test_case_order_and_extra_output(self):
        for value in [self.stdout.replace("case-0.jpg", "case-1.jpg"), self.stdout + "extra\n", ""]:
            with self.assertRaises(ValueError):
                self.check(stdout=value)


if __name__ == "__main__":
    unittest.main()
