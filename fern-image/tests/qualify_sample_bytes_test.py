"""Fault controls for exact raw sample word evidence."""

import unittest

import qualify_sample_bytes as collector


class SampleEvidenceTests(unittest.TestCase):
    def setUp(self):
        self.cases = [(2, 0, 2, 7, 0x1234), (2, 1, 2, 7, 0x1234), (9, 0, -1, 0, 0)]
        wanted = [collector.expected(case, i) for i, case in enumerate(self.cases)]
        self.stdout = "\n".join(line for line, _ in wanted) + "\n"
        self.output = b"".join(data for _, data in wanted)
        self.stderr = "leakcheck: allocs=10 frees=10 live_bytes=0\n"

    def test_independent_bytes(self):
        self.assertEqual(self.output, b"\xa5\xa5\x12\x34\xa5\xa5\xa5\xa5\xa5\x34\x12\xa5\xa5\xa5")
        self.assertEqual(collector.check_output(self.stdout, self.stderr, self.output, self.cases)["allocs"], 10)

    def test_wrong_value_or_protocol(self):
        for stdout in [self.stdout.replace("4660", "4661"), "", self.stdout + "extra\n", self.stdout.replace("error 2 Width", "error 2 Bounds")]:
            with self.assertRaises(ValueError):
                collector.check_output(stdout, self.stderr, self.output, self.cases)

    def test_wrong_bytes_or_length(self):
        for output in [b"", self.output[:-1], self.output + b"\0", bytes([0]) + self.output[1:]]:
            with self.assertRaises(ValueError):
                collector.check_output(self.stdout, self.stderr, output, self.cases)

    def test_invalid_heap_evidence(self):
        for stderr in ["", self.stderr * 2, "leakcheck: allocs=0 frees=0 live_bytes=0\n", "leakcheck: allocs=10 frees=9 live_bytes=0\n", "leakcheck: allocs=10 frees=10 live_bytes=1\n"]:
            with self.assertRaises(ValueError):
                collector.check_output(self.stdout, stderr, self.output, self.cases)

    def test_error_precedence(self):
        self.assertEqual(collector.expected((0, 0, -1, 0, 0), 0)[0], "error 0 Width")
        self.assertEqual(collector.expected((1, 0, -1, 0, 256), 0)[0], "error 0 Bounds")
        self.assertEqual(collector.expected((1, 0, 0, 1, 256), 0)[0], "error 0 Value")

    def test_complete_sixteen_bit_domain(self):
        cases = list(collector.cases())
        for order in range(2):
            values = {case[4] for case in cases if case[:4] == (2, order, 2, 7)}
            self.assertEqual(values.intersection(range(65536)), set(range(65536)))


if __name__ == "__main__":
    unittest.main()
