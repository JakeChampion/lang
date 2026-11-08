"""Require exact bit results, resource errors and complete heap evidence."""

import struct
import unittest

import qualify_jxl_bits as qualification


class BitEvidenceTests(unittest.TestCase):
    heap = 'leakcheck: allocs=3 frees=3 live_bytes=0\n'
    case = dict(fields=[(5, 3), (26, 5)], budget=1, data=b'\xd5', queries=[(0, 3, 8), (3, 5, 8)])
    output = ('0 pack ok 8 d5\n0 query 0 ok 5 3\n0 align 0 ok 0\n'
              '0 query 1 ok 26 8\n0 align 1 error Padding\n')

    def test_known_complete_results(self):
        qualification.check_worker(self.output, self.heap, [self.case])

    def test_exact_low_bit_equations(self):
        self.assertEqual(qualification.pack([(5, 3), (26, 5)], 1), dict(bits=8, data=b'\xd5'))
        self.assertEqual(qualification.read(b'\xd5\x6a', 4, 8, 16), 'ok 173 12')

    def test_partial_byte_zeroes_unused_bits(self):
        self.assertEqual(qualification.pack([(3, 2)], 1), dict(bits=2, data=b'\x03'))

    def test_every_value_boundary(self):
        for width in range(57):
            with self.subTest(width=width):
                self.assertEqual(qualification.pack([(1 << width, width)], 100), dict(error='Value'))

    def test_empty_and_zero_width_fields(self):
        self.assertEqual(qualification.pack([], 0), dict(bits=0, data=b''))
        self.assertEqual(qualification.pack([(0, 0)], 0), dict(bits=0, data=b''))
        self.assertEqual(qualification.read(b'', 0, 0, 0), 'ok 0 0')

    def test_width_and_budget_errors(self):
        self.assertEqual(qualification.pack([(0, 57)], 100), dict(error='Width'))
        self.assertEqual(qualification.pack([(0, -1)], 100), dict(error='Width'))
        self.assertEqual(qualification.pack([], -1), dict(error='Limit'))
        self.assertEqual(qualification.pack([(1, 1)], 0), dict(error='Limit'))

    def test_bounds_precede_sample_access(self):
        for position, extent in [(-1, 8), (9, 8), (0, -1), (0, 9), (2**63 - 1, 8)]:
            with self.subTest(position=position, extent=extent):
                self.assertEqual(qualification.read(b'\xff', position, 0, extent), 'error Bounds')

    def test_logical_extent_excludes_physical_padding(self):
        self.assertEqual(qualification.read(b'\xff', 7, 1, 7), 'error Truncated')
        self.assertEqual(qualification.read(b'\xff', 7, 1, 8), 'ok 1 8')

    def test_alignment_checks_full_extent_and_value(self):
        self.assertEqual(qualification.align(b'\0', 3, 8), 'ok 8')
        self.assertEqual(qualification.align(b'\0', 3, 7), 'error Truncated')
        self.assertEqual(qualification.align(b'\x80', 3, 8), 'error Padding')
        self.assertEqual(qualification.align(b'', 0, 0), 'ok 0')

    def test_binary_fixture_layout(self):
        expected = (struct.pack('<iIII', 1, 2, 2, 1) + struct.pack('<iQ', 3, 5) + struct.pack('<iQ', 5, 26)
                    + struct.pack('<qiq', 0, 3, 8) + struct.pack('<qiq', 3, 5, 8) + b'\xd5')
        self.assertEqual(qualification.fixture(self.case), expected)

    def test_missing_or_extra_result_fails(self):
        for output in ['', self.output + 'extra\n', self.output.splitlines()[0] + '\n']:
            with self.subTest(output=output), self.assertRaises(ValueError):
                qualification.check_worker(output, self.heap, [self.case])

    def test_missing_or_bad_heap_fails(self):
        for heap in ['', 'leakcheck: allocs=0 frees=0 live_bytes=0', 'leakcheck: allocs=3 frees=2 live_bytes=0',
                     'leakcheck: allocs=3 frees=3 live_bytes=1']:
            with self.subTest(heap=heap), self.assertRaises(ValueError):
                qualification.check_worker(self.output, heap, [self.case])

    def test_false_position_value_or_padding_fails(self):
        for before, after in [('ok 5 3', 'ok 4 3'), ('ok 26 8', 'ok 26 9'), ('error Padding', 'ok 8')]:
            with self.subTest(before=before), self.assertRaises(ValueError):
                qualification.check_worker(self.output.replace(before, after), self.heap, [self.case])

    def test_exact_error_class_required(self):
        case = dict(fields=[(1, 0)], budget=1, data=b'', queries=[])
        qualification.check_worker('0 pack error Value\n', self.heap, [case])
        with self.assertRaises(ValueError):
            qualification.check_worker('0 pack error Width\n', self.heap, [case])


if __name__ == '__main__':
    unittest.main()
