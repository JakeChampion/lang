"""Fault controls for scalar inverse-DCT observations."""

import copy
import unittest

import qualify_jpeg_idct as collector


class IdctProtocolTests(unittest.TestCase):
    def setUp(self):
        self.case = {"name": "zero", "input_sha256": "input", "samples_decimal": ["0"] * 64, "ideal_pixels": [128] * 64}
        self.stdout = "begin case-0.bin\nsamples " + " ".join(["0"] * 64) + "\npixels " + " ".join(["128"] * 64) + "\n"
        self.stderr = "leakcheck: allocs=10 frees=10 live_bytes=0\n"

    def check(self, stdout=None, stderr=None, case=None):
        return collector.check_output(self.stdout if stdout is None else stdout,
                                      self.stderr if stderr is None else stderr,
                                      [self.case if case is None else case], ["case-0.bin"])

    def test_exact_numeric_and_sample_output(self):
        samples, census = self.check()
        self.assertEqual(samples[0]["max_absolute_error"], "0")
        self.assertEqual(census, {"allocs": 10, "frees": 10, "live_bytes": 0})

    def test_wrong_mathematical_value(self):
        with self.assertRaises(ValueError):
            self.check(stdout=self.stdout.replace("samples 0 ", "samples 0.00001 ", 1))

    def test_nonfinite_numeric_value(self):
        for value in ["nan", "inf", "-inf"]:
            with self.assertRaises(ValueError):
                self.check(stdout=self.stdout.replace("samples 0 ", "samples " + value + " ", 1))

    def test_wrong_rounding(self):
        with self.assertRaises(ValueError):
            self.check(stdout=self.stdout.replace("pixels 128 ", "pixels 129 ", 1))

    def test_wrong_codec_observation(self):
        case = copy.deepcopy(self.case)
        case["codec_pixels"] = [None] * 64
        case["codec_pixels"][0] = 125
        with self.assertRaises(ValueError):
            self.check(case=case)

    def test_codec_rounding_difference_remains_visible(self):
        case = copy.deepcopy(self.case)
        case["codec_pixels"] = [None] * 64
        case["codec_pixels"][0] = 127
        samples, _ = self.check(case=case)
        self.assertEqual(samples[0]["codec_visible_samples"], 1)
        self.assertEqual(samples[0]["codec_pixel_differences"], 1)
        self.assertEqual(samples[0]["max_codec_pixel_error"], 1)

    def test_invalid_heap_census(self):
        for value in ["", self.stderr * 2, "leakcheck: allocs=10 frees=9 live_bytes=1", "leakcheck: allocs=0 frees=0 live_bytes=0"]:
            with self.assertRaises(ValueError):
                self.check(stderr=value)

    def test_case_order_shape_and_extra_output(self):
        for value in [self.stdout.replace("case-0.bin", "case-1.bin"), self.stdout.replace("samples 0 ", "samples ", 1), self.stdout + "extra\n"]:
            with self.assertRaises(ValueError):
                self.check(stdout=value)


if __name__ == "__main__":
    unittest.main()
