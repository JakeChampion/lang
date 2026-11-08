import struct
import unittest

from qualify_jxl_headers import check_worker, decode, encoded, fields, make


class HeaderControls(unittest.TestCase):
    def test_dimension_goldens(self):
        for mode, width, height, data, size in [(0, 8, 8, '4100', 9), (0, 9, 8, '8100', 9),
                                               (0, 1, 1, '0010', 15), (1, 8, 8, '0501', 11), (1, 9, 8, '3804', 12)]:
            result = encoded(make('golden', mode, (width, height, 0, 0)))
            self.assertEqual((result['data'].hex(), result['bits']), (data, size))

    def test_legal_preview_fallback(self):
        result = encoded(make('fallback', 1, (5440, 5440, 0, 0)))
        self.assertEqual((result['data'], result['bits']), (b'\xfe\xff\x00', 18))
        self.assertEqual(fields(1, (5440, 5440, 0, 0), fallback=False), dict(error='Value'))
        self.assertEqual(decode(b'\xfe\xff\x00', 0, 18, 1), 'dimensions 5440 5440 18')

    def test_inferred_width_beyond_signed_storage(self):
        case = make('wide', 0, (2**31, 2**30, 0, 0))
        result = encoded(case)
        self.assertEqual(decode(result['data'], 0, result['bits'], 0), f'dimensions {2**31} {2**30} {result["bits"]}')

    def test_ratio_truncation_and_first_tie(self):
        self.assertEqual(fields(0, (1, 1, 0, 0))['fields'][-1], (1, 3))
        self.assertEqual(decode(b'\x81\x00', 0, 9, 0), 'dimensions 9 8 9')

    def test_dimension_domains(self):
        for width, height in [(0, 1), (1, 0), (-1, 1), (1, -1), (2**32, 1), (1, 2**32)]:
            for mode in [0, 1]:
                self.assertEqual(fields(mode, (width, height, 0, 0)), dict(error='Dimension'))
        self.assertEqual(fields(1, (1, 5441, 0, 0)), dict(error='Value'))
        self.assertEqual(fields(0, (1, 2**30 + 1, 0, 0)), dict(error='Value'))

    def test_animation_goldens(self):
        for values, expected in [((100, 1, 0, 0), b'\x00'), ((100, 1, 0, 1), b'\x40'), ((1000, 1001, 0, 0), b'\x05')]:
            result = encoded(make('animation', 2, values))
            self.assertEqual((result['data'], result['bits']), (expected, 7))
            self.assertEqual(decode(expected, 0, 7, 2), 'animation ' + ' '.join(map(str, values)) + ' 7')

    def test_unsigned_loop_count(self):
        case = make('loops', 2, (100, 1, 2**32 - 1, 1))
        result = encoded(case)
        self.assertEqual(decode(result['data'], 0, result['bits'], 2), f'animation 100 1 {2**32 - 1} 1 {result["bits"]}')

    def test_animation_domains(self):
        for n, d in [(0, 1), (1, 0), (2**30 + 1, 1), (1, 1025)]:
            self.assertEqual(fields(2, (n, d, 0, 0)), dict(error='Value'))

    def test_logical_and_physical_bounds(self):
        self.assertEqual(decode(b'\x41\x00', 0, 8, 0), 'error Input(Truncated)')
        self.assertEqual(decode(b'\x41\x00', -1, 16, 0), 'error Input(Bounds)')
        self.assertEqual(decode(b'\x00', 0, 9, 2), 'error Input(Bounds)')

    def test_complete_packet_layout(self):
        from qualify_jxl_headers import fixture
        case = make('packet', 0, (9, 8, 0, 0), prefix=7)
        data = fixture(case)
        self.assertEqual(struct.unpack_from('<I4QiiII', data), (0, 9, 8, 0, 0, 12, 7, len(case['queries']), len(case['data'])))
        self.assertEqual(len(data), 52 + 16 * len(case['queries']) + len(case['data']))

    def test_exact_output_and_heap(self):
        case = make('square', 0, (8, 8, 0, 0))
        case['queries'] = []
        check_worker('0 pack ok 9 4100\n', 'leakcheck: allocs=1 frees=1 live_bytes=0', [case])

    def test_reject_false_output(self):
        case = make('square', 0, (8, 8, 0, 0))
        case['queries'] = []
        for output in ['', '0 pack ok 8 4100\n', '0 pack ok 9 4101\n', '0 pack ok 9 4100\nextra\n']:
            with self.assertRaises(ValueError):
                check_worker(output, 'leakcheck: allocs=1 frees=1 live_bytes=0', [case])

    def test_reject_false_heap(self):
        case = make('square', 0, (8, 8, 0, 0))
        case['queries'] = []
        for heap in ['', 'leakcheck: allocs=0 frees=0 live_bytes=0', 'leakcheck: allocs=2 frees=1 live_bytes=0',
                     'leakcheck: allocs=1 frees=1 live_bytes=1']:
            with self.assertRaises(ValueError):
                check_worker('0 pack ok 9 4100\n', heap, [case])

    def test_output_budget_and_field_width(self):
        self.assertEqual(encoded(make('budget', 0, (8, 8, 0, 0), budget=1)), dict(error='Limit'))
        self.assertEqual(encoded(make('prefix', 0, (8, 8, 0, 0), prefix=57)), dict(error='Width'))


if __name__ == '__main__':
    unittest.main()
