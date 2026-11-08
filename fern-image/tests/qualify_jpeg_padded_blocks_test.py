"""Fault controls for complete padded component blocks."""

import unittest

import qualify_jpeg_padded_blocks as collector


class PaddedEvidenceTests(unittest.TestCase):
    def setUp(self):
        self.cases = [(1, 1, 1, 2, 2, 1, 1, 0, 0, b"\x11"), (1, 1, 1, 2, 2, 1, 1, 1, 128, b"\x11")]
        self.names = ["one.raw", "two.raw"]
        self.stdout = "\n".join(name + " " + collector.expected(case) for name, case in zip(self.names, self.cases)) + "\n"
        self.stderr = "leakcheck: allocs=10 frees=10 live_bytes=0\n"

    def test_literal_complete_blocks(self):
        self.assertEqual(collector.expected(self.cases[0]), "ok " + "11" * 64)
        self.assertEqual(collector.expected(self.cases[1]), "ok " + "80" * 64)
        self.assertEqual(collector.check_output(self.stdout, self.stderr, self.cases, self.names)["allocs"], 10)

    def test_protocol_and_samples(self):
        for stdout in ["", self.stdout + "extra\n", self.stdout.replace("1111", "1211", 1), self.stdout.replace("one.raw", "other.raw")]:
            with self.assertRaises(ValueError):
                collector.check_output(stdout, self.stderr, self.cases, self.names)
        with self.assertRaises(ValueError):
            collector.check_output(self.stdout, self.stderr, self.cases, self.names[:-1])

    def test_invalid_heap_evidence(self):
        for stderr in ["", self.stderr * 2, "leakcheck: allocs=0 frees=0 live_bytes=0\n",
                       "leakcheck: allocs=10 frees=9 live_bytes=0\n", "leakcheck: allocs=10 frees=10 live_bytes=1\n"]:
            with self.assertRaises(ValueError):
                collector.check_output(self.stdout, stderr, self.cases, self.names)

    def test_error_precedence(self):
        for case, error in [((0, 1, 1, 0, 0, -1, -1, 0, 0, b""), "Layout"),
                            ((1, 1, 1, 0, 0, -1, -1, 0, 0, b""), "Storage"),
                            ((1, 1, 1, 0, 0, -1, -1, 0, 0, b"\x11"), "Layout"),
                            ((1, 1, 1, 1, 1, -1, -1, 0, 0, b"\x11"), "Bounds")]:
            self.assertEqual(collector.expected(case), "error " + error)

    def test_wide_and_partial_edge(self):
        case = (1, 1, 2147483647, 2147483647, 2147483647, 2147483646, 2147483646, 0, 0, b"\x11")
        self.assertEqual(collector.expected(case), "ok " + "11" * 64)
        case = (1, 2, 2, 2, 2, 1, 0, 0, 0, b"\x11\xdd\x22")
        self.assertEqual(collector.expected(case), "ok " + "11" * 8 + "22" * 56)

    def test_all_constant_bytes(self):
        inputs = list(collector.cases())
        self.assertEqual({c[8] for c in inputs if c[:8] == (1, 1, 1, 2, 2, 1, 1, 1)}, set(range(256)))


if __name__ == "__main__":
    unittest.main()
