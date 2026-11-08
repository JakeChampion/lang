"""Reject false Adam7 worker success and incomplete heap evidence."""

import unittest

from qualify_png_adam7 import check_worker


class Adam7WorkerTests(unittest.TestCase):
    def setUp(self):
        self.case = dict(shape=(1, 1, 3), pixels=b'\x01\x02\x03')
        self.heap = 'leakcheck: allocs=2 frees=2 live_bytes=0\n'

    def test_exact_image_and_error_results(self):
        check_worker('0 ok 1 1 3 010203\n1 error Scanline\n', self.heap, [self.case, dict(error='Scanline')])

    def test_pixels_dimensions_and_result_indices(self):
        for text in ['0 ok 1 1 3 010204', '0 ok 1 2 3 010203', '1 ok 1 1 3 010203', '0 ok 1 1 4 010203', '0 error Scanline']:
            with self.subTest(text=text), self.assertRaises(ValueError):
                check_worker(text, self.heap, [self.case])

    def test_incomplete_or_extra_results(self):
        for text in ['', '0 ok 1 1 3 010203\n0 ok 1 1 3 010203']:
            with self.assertRaises(ValueError):
                check_worker(text, self.heap, [self.case])

    def test_exact_error_category(self):
        for text in ['0 error Compressed', '0 ok 1 1 3 010203']:
            with self.assertRaises(ValueError):
                check_worker(text, self.heap, [dict(error='Scanline')])

    def test_missing_zero_or_unbalanced_heap(self):
        for heap in ['', 'leakcheck: allocs=0 frees=0 live_bytes=0', 'leakcheck: allocs=2 frees=1 live_bytes=0', 'leakcheck: allocs=2 frees=2 live_bytes=1']:
            with self.assertRaises(ValueError):
                check_worker('0 ok 1 1 3 010203', heap, [self.case])


if __name__ == '__main__':
    unittest.main()
