"""Reject invalid benchmark evidence before any performance comparison."""

import unittest

from benchmark_qoi import observation


class ObservationTests(unittest.TestCase):
    value = bytes([2, 7, 4])
    stdout = '100 27 3 18'
    stderr = '  1048576 maximum resident set size\nleakcheck: allocs=8 frees=8 live_bytes=0\n'

    def test_valid_and_resolution_limited_samples(self):
        result = observation(self.stdout, self.stderr, self.value, 3, 1000, True)
        self.assertEqual(result['inner_ns'], 100)
        self.assertFalse(result['timer_resolution_limited'])
        result = observation('0 27 3 18', self.stderr, self.value, 3, 1000, True)
        self.assertTrue(result['timer_resolution_limited'])

    def test_reject_corrupt_loop_and_timer_records(self):
        for text in ['100 28 3 18', '100 27 4 18', '100 27 3 19',
                     '100 27 3', '100 27 3 18 0', 'nan 27 3 18',
                     '-1 27 3 18', '1000 27 3 18', '1001 27 3 18']:
            with self.subTest(stdout=text), self.assertRaises(ValueError):
                observation(text, self.stderr, self.value, 3, 1000, True)

    def test_reject_missing_duplicate_and_zero_memory(self):
        for text in ['', self.stderr + self.stderr, '0 maximum resident set size\n']:
            with self.subTest(stderr=text), self.assertRaises(ValueError):
                observation(self.stdout, text, self.value, 3, 1000, False)

    def test_reject_missing_duplicate_or_unbalanced_heap(self):
        for census in ['', 'allocs=0 frees=0 live_bytes=0', 'allocs=8 frees=7 live_bytes=0',
                       'allocs=8 frees=8 live_bytes=1', 'allocs=8 frees=8 live_bytes=0\nleakcheck: allocs=8 frees=8 live_bytes=0']:
            text = '1048576 maximum resident set size\nleakcheck: ' + census
            with self.subTest(census=census), self.assertRaises(ValueError):
                observation(self.stdout, text, self.value, 3, 1000, True)


if __name__ == '__main__':
    unittest.main()
