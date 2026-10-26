"""Fault controls for the coefficient comparison protocol."""

import copy
import unittest

import qualify_jpeg_coefficients as collector


class CoefficientProtocolTests(unittest.TestCase):
    def setUp(self):
        self.case = {"name": "one-block", "encoded_hex": "ffd8ffd9", "oracle_stdout_sha256": "reference",
                     "reference": {"width": 8, "height": 8, "components": [{"id": 7, "horizontal": 1, "vertical": 1,
                                    "columns": 1, "rows": 1, "blocks": [list(range(64))]}]}}
        self.block = "block 7 0 0 " + " ".join(map(str, range(64)))
        self.stdout = "begin case-0.jpg\n" + self.block + "\nok 8 8 1 1 1 4\n"
        self.stderr = "leakcheck: allocs=10 frees=10 live_bytes=0\n"

    def check(self, stdout=None, stderr=None, case=None):
        return collector.check_output(self.stdout if stdout is None else stdout,
                                      self.stderr if stderr is None else stderr,
                                      [self.case if case is None else case], ["case-0.jpg"])

    def test_exact_block(self):
        samples, census = self.check()
        self.assertEqual(len(samples), 1)
        self.assertEqual(samples[0]["coefficient_sha256"], samples[0]["reference_coefficient_sha256"])
        self.assertEqual(census, {"allocs": 10, "frees": 10, "live_bytes": 0})

    def test_wrong_coefficient(self):
        with self.assertRaises(ValueError):
            self.check(stdout=self.stdout.replace(" 63\n", " 62\n"))

    def test_wrong_coordinate(self):
        with self.assertRaises(ValueError):
            self.check(stdout=self.stdout.replace("block 7 0 0", "block 7 1 0"))

    def test_missing_or_duplicate_block(self):
        for output in [self.stdout.replace(self.block + "\n", ""), self.stdout.replace(self.block, self.block + "\n" + self.block)]:
            with self.assertRaises(ValueError):
                self.check(stdout=output)

    def test_consumption_and_plan(self):
        for output in [self.stdout.replace("1 1 1 4", "1 1 1 3"), self.stdout.replace("8 8 1", "9 8 1")]:
            with self.assertRaises(ValueError):
                self.check(stdout=output)

    def test_invalid_census(self):
        for census in ["", self.stderr * 2, "leakcheck: allocs=10 frees=9 live_bytes=4", "leakcheck: allocs=0 frees=0 live_bytes=0"]:
            with self.assertRaises(ValueError):
                self.check(stderr=census)

    def test_worker_rejection_or_extra_output(self):
        for output in [self.stdout.replace("ok 8 8 1 1 1 4", "error EntropyMarker"), self.stdout + "extra\n"]:
            with self.assertRaises(ValueError):
                self.check(stdout=output)

    def test_reference_schedule_uses_component_identifiers(self):
        case = copy.deepcopy(self.case)
        case["reference"]["components"][0]["id"] = 201
        with self.assertRaises(ValueError):
            self.check(case=case)
        samples, _ = self.check(stdout=self.stdout.replace("block 7", "block 201"), case=case)
        self.assertEqual(samples[0]["output_blocks"], 1)


if __name__ == "__main__":
    unittest.main()
