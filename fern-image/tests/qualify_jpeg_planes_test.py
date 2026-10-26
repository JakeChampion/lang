"""Fault controls for independent raw component-plane observations."""

from pathlib import Path
import tempfile
import unittest

import qualify_jpeg_planes as collector


class PlaneProtocolTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        plane = {"id": 201, "horizontal": 2, "vertical": 1, "width": 2, "height": 1, "pixels": [128, 129]}
        self.case = {"name": "two-samples", "encoded_hex": "ffd8ffd9", "input_sha256": "source",
                     "reference": {"width": 2, "height": 1, "planes": [plane]}}
        self.output = self.directory / "case-0.jpg.plane-0.raw"
        self.output.write_bytes(bytes(plane["pixels"]))
        self.stdout = "begin case-0.jpg 2 1 2 1 1 2\nplane 0 201 2 1 2 1 2 2\nend case-0.jpg\n"
        self.stderr = "leakcheck: allocs=13 frees=13 live_bytes=0\n"

    def check(self, stdout=None, stderr=None):
        return collector.check_output(self.stdout if stdout is None else stdout,
                                      self.stderr if stderr is None else stderr,
                                      [self.case], ["case-0.jpg"], self.directory)

    def test_exact_planes(self):
        samples, census = self.check()
        plane = samples[0]["planes"][0]
        self.assertEqual(plane["pixels_sha256"], plane["reference_pixels_sha256"])
        self.assertEqual(census, {"allocs": 13, "frees": 13, "live_bytes": 0})

    def test_visible_one_value_differences(self):
        self.output.write_bytes(bytes([129, 128]))
        samples, _ = self.check()
        self.assertEqual(samples[0]["planes"][0]["different_samples"], 2)
        self.assertEqual(samples[0]["planes"][0]["max_sample_error"], 1)

    def test_wrong_samples(self):
        self.output.write_bytes(bytes([130, 129]))
        with self.assertRaises(ValueError):
            self.check()

    def test_image_metadata(self):
        for value in [self.stdout.replace("2 1 2 1 1 2", "1 2 2 1 1 2"), self.stdout.replace("2 1 2 1 1 2", "2 1 2 1 2 2"),
                      self.stdout.replace("2 1 2 1 1 2", "2 1 2 1 1 3"), self.stdout.replace("2 1 2 1 1 2", "2 1 1 1 1 2")]:
            with self.assertRaises(ValueError):
                self.check(stdout=value)

    def test_component_identity_order_sampling_storage(self):
        for row in ["plane 0 7 2 1 2 1 2 2", "plane 1 201 2 1 2 1 2 2", "plane 0 201 1 2 2 1 2 2",
                    "plane 0 201 2 1 1 2 2 2", "plane 0 201 2 1 2 1 3 2"]:
            with self.assertRaises(ValueError):
                self.check(stdout=self.stdout.replace("plane 0 201 2 1 2 1 2 2", row))

    def test_missing_or_short_output(self):
        for value in [b"", b"\x80", b"\x80\x81\x82"]:
            self.output.write_bytes(value)
            with self.assertRaises(ValueError):
                self.check()
        self.output.unlink()
        with self.assertRaises(ValueError):
            self.check()

    def test_protocol_prefix_and_extra_output(self):
        for value in ["", self.stdout.replace("case-0", "case-1"), self.stdout.replace("end case-0.jpg\n", ""), self.stdout + "extra\n"]:
            with self.assertRaises(ValueError):
                self.check(stdout=value)

    def test_heap_census(self):
        for value in ["", self.stderr * 2, "leakcheck: allocs=13 frees=12 live_bytes=1", "leakcheck: allocs=0 frees=0 live_bytes=0"]:
            with self.assertRaises(ValueError):
                self.check(stderr=value)


if __name__ == "__main__":
    unittest.main()
