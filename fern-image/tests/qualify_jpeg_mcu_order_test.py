"""Fault controls for the MCU geometry reference."""

import itertools
import unittest

import qualify_jpeg_mcu_order as collector


class McuEvidenceTests(unittest.TestCase):
    def setUp(self):
        self.cases = [(17, 9, 12, ((2, 2), (1, 1), (1, 1)), 1), (0, 1, -1, (), 1)]
        self.stdout = "\n".join(line for i, case in enumerate(self.cases) for line in collector.expected(case, i)) + "\n"
        self.stderr = "leakcheck: allocs=10 frees=10 live_bytes=0\n"

    def test_literal_order(self):
        lines = self.stdout.splitlines()
        self.assertEqual(lines[0], "plan 0 2 1 2 6 12 2 2")
        self.assertEqual(lines[1], "block 0 0 0 0 0 0 17 9 0")
        self.assertEqual(lines[5], "block 0 0 4 1 0 0 9 5 0")
        self.assertEqual(lines[8], "block 0 1 1 0 3 0 17 9 1")
        self.assertEqual(lines[-1], "error 1 Dimensions")
        self.assertEqual(collector.check_output(self.stdout, self.stderr, self.cases)["lines"], 14)

    def test_wrong_order_and_protocol(self):
        lines = self.stdout.splitlines()
        lines[1], lines[2] = lines[2], lines[1]
        for stdout in ["\n".join(lines), "", self.stdout + "extra\n", self.stdout.replace("17 9 1", "17 9 0")]:
            with self.assertRaises(ValueError):
                collector.check_output(stdout, self.stderr, self.cases)

    def test_invalid_heap_evidence(self):
        for stderr in ["", self.stderr * 2, "leakcheck: allocs=0 frees=0 live_bytes=0\n",
                       "leakcheck: allocs=10 frees=9 live_bytes=0\n", "leakcheck: allocs=10 frees=10 live_bytes=1\n"]:
            with self.assertRaises(ValueError):
                collector.check_output(self.stdout, stderr, self.cases)

    def test_error_precedence_and_exact_limit(self):
        for case, error in [((0, 1, -1, (), 1), "Dimensions"), ((1, 1, -1, (), 1), "Components"),
                            ((1, 1, -1, ((0, 1),), 1), "Sampling"), ((1, 1, -1, ((1, 1),), 1), "Limit")]:
            self.assertEqual(collector.expected(case, 0), [f"error 0 {error}"])
        self.assertEqual(collector.expected((65535, 65535, 83886080, ((4, 1), (1, 2), (2, 2)), 0), 0),
                         ["plan 0 2048 4096 8388608 10 83886080 4 2"])
        self.assertEqual(collector.expected((65535, 65535, 83886079, ((4, 1), (1, 2), (2, 2)), 0), 0), ["error 0 Limit"])

    def test_single_component_factors(self):
        for h, v in itertools.product(range(1, 5), repeat=2):
            lines = collector.expected((17, 9, 6, ((h, v),), 1), 0)
            self.assertEqual(lines[0], f"plan 0 3 2 6 1 6 {h} {v}")
            self.assertEqual(lines[-1], "block 0 5 0 0 2 1 17 9 0")

    def test_complete_factor_domain(self):
        cases = list(collector.cases())
        for count in range(1, 5):
            actual = {case[3] for case in cases if case[:3] == (17, 19, 2147483647) and len(case[3]) == count}
            wanted = set(itertools.product(tuple(itertools.product(range(1, 5), repeat=2)), repeat=count))
            self.assertEqual(actual, wanted)


if __name__ == "__main__":
    unittest.main()
