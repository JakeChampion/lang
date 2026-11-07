"""Reject false entropy and heap observations before qualification."""

import copy
import unittest

from qualify_jpeg_entropy_writer import cases, heap, observation, reference


class ObservationTests(unittest.TestCase):
    def setUp(self):
        self.case = next(cases())[1]
        self.line = "ok " + reference(self.case).hex()

    def test_exact_bytes(self):
        self.assertEqual(observation(self.case, self.line), self.line)

    def test_false_bytes_and_results(self):
        for value in ("", "ok", self.line + "00", "ok 01", "error Table"):
            with self.subTest(value=value), self.assertRaises(ValueError):
                observation(self.case, value)

    def test_wrong_error(self):
        case = copy.deepcopy(self.case)
        case["values"] = []
        self.assertEqual(observation(case, "error Shape"), "error Shape")
        for value in (self.line, "error Coefficient", "error Shape extra"):
            with self.subTest(value=value), self.assertRaises(ValueError):
                observation(case, value)

    def test_heap_controls(self):
        valid = "leakcheck: allocs=10 frees=10 live_bytes=0\n"
        heap(valid)
        for value in ("", valid * 2, "leakcheck: allocs=0 frees=0 live_bytes=0\n",
                      valid.replace("frees=10", "frees=9"), valid.replace("bytes=0", "bytes=1")):
            with self.subTest(value=value), self.assertRaises(ValueError):
                heap(value)


if __name__ == "__main__":
    unittest.main()
