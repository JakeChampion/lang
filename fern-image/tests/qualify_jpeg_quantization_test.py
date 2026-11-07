"""Fault controls for quantizer reference observations."""

import unittest

from qualify_jpeg_quantization import check_output


class OutputTests(unittest.TestCase):
    def setUp(self):
        self.wanted = [["one.raw", "ok", *(["0"] * 64)], ["two.raw", "error", "Coefficient"]]
        self.stdout = "\n".join(" ".join(row) for row in self.wanted) + "\n"
        self.stderr = "leakcheck: allocs=10 frees=10 live_bytes=0\n"

    def test_exact_success_and_error(self):
        self.assertEqual(check_output(self.stdout, self.stderr, self.wanted), dict(allocs=10, frees=10, live_bytes=0))

    def test_wrong_values_or_errors(self):
        for text in [self.stdout.replace("ok 0", "ok 1", 1), self.stdout.replace("Coefficient", "Quantizer")]:
            with self.subTest(text=text), self.assertRaises(ValueError):
                check_output(text, self.stderr, self.wanted)

    def test_shape_order_and_extra_output(self):
        rows = self.stdout.splitlines()
        for text in [rows[0] + "\n", "\n".join(reversed(rows)), self.stdout + "extra\n",
                     self.stdout.replace("ok 0 ", "ok ", 1), self.stdout.replace("one.raw", "other.raw")]:
            with self.subTest(text=text), self.assertRaises(ValueError):
                check_output(text, self.stderr, self.wanted)

    def test_heap_faults(self):
        for text in ["", self.stderr * 2, "leakcheck: allocs=0 frees=0 live_bytes=0\n",
                     "leakcheck: allocs=10 frees=9 live_bytes=0\n", "leakcheck: allocs=10 frees=10 live_bytes=1\n"]:
            with self.subTest(text=text), self.assertRaises(ValueError):
                check_output(self.stdout, text, self.wanted)


if __name__ == "__main__":
    unittest.main()
