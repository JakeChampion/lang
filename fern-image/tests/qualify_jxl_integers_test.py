import struct
import unittest

from qualify_jxl_integers import DEFAULT, check_worker, decode, encoded, fields32, fields64, fixture, make, valid


class IntegerControls(unittest.TestCase):
    def test_independent_u64_goldens(self):
        for value, data, size in [(0, '00', 2), (1, '01', 6), (16, '3d', 6), (17, '0200', 10),
                                  (272, 'fe03', 10), (273, '4704', 15), (4095, 'ff3f', 15), (4096, '03c000', 24)]:
            result = encoded(make('golden', 0, value))
            self.assertEqual((result['data'].hex(), result['bits']), (data, size))

    def test_complete_unsigned_maximum(self):
        result = encoded(make('maximum', 0, 2**64 - 1))
        self.assertEqual((result['data'], result['bits']), (b'\xff' * 9 + b'\x01', 73))

    def test_u64_truncated_stop_bit(self):
        self.assertEqual(decode(b'\xff\x3f', 0, 14, 0, DEFAULT), 'error Truncated')
        self.assertEqual(decode(b'\xff\x3f', 0, 15, 0, DEFAULT), 'ok 4095 15')

    def test_u64_final_group_has_no_stop_bit(self):
        data = b'\xff' * 9 + b'\x01'
        self.assertEqual(decode(data, 0, 73, 0, DEFAULT), f'ok {2**64 - 1} 73')
        self.assertEqual(decode(data, 0, 72, 0, DEFAULT), 'error Truncated')

    def test_u64_long_form_is_legal(self):
        self.assertEqual(decode(b'\x03\x00', 0, 15, 0, DEFAULT), 'ok 0 15')

    def test_u32_shortest_choice_and_first_tie(self):
        profile = [(8, 0), (2, 0), (2, 0), (0, 2)]
        self.assertEqual(fields32(0, profile)['fields'], [(1, 2), (0, 2)])
        self.assertEqual(fields32(2, profile)['fields'], [(3, 2), (0, 0)])
        self.assertEqual(fields32(256, profile), dict(error='Value'))

    def test_u32_modulo_boundary(self):
        self.assertEqual(decode(b'\xfc\xff\xff\xff\x03', 0, 34, 1,
                                [(32, 1), *DEFAULT[1:]]), 'ok 0 34')

    def test_distribution_domain(self):
        for bad in [[*DEFAULT, DEFAULT[0]], DEFAULT[:3], [(-1, 0), *DEFAULT[1:]],
                    [(33, 0), *DEFAULT[1:]], [(0, 2**31), *DEFAULT[1:]], [(1, 2**26), *DEFAULT[1:]]]:
            self.assertFalse(valid(bad))
            self.assertEqual(fields32(0, bad), dict(error='Distribution'))
            self.assertEqual(decode(b'', -1, -1, 1, bad), 'error Distribution')

    def test_logical_and_physical_bounds(self):
        self.assertEqual(decode(b'\x00', 0, 1, 0, DEFAULT), 'error Truncated')
        self.assertEqual(decode(b'\x00', 0, 9, 0, DEFAULT), 'error Bounds')
        self.assertEqual(decode(b'', 0, 0, 1, DEFAULT), 'error Input(Truncated)')

    def test_packet_layout(self):
        case = make('packet', 0, 4096, prefix=7)
        data = fixture(case)
        self.assertEqual(struct.unpack_from('<IQiiII', data),
                         (0, 4096, 12, 7, len(case['queries']), len(case['data'])))
        self.assertEqual(len(data), 60 + 16 * len(case['queries']) + len(case['data']))

    def test_exact_output_and_heap_evidence(self):
        case = make('zero', 0, 0)
        case['queries'] = []
        check_worker('0 pack ok 2 00\n', 'leakcheck: allocs=1 frees=1 live_bytes=0', [case])

    def test_reject_false_outputs(self):
        case = make('zero', 0, 0)
        case['queries'] = []
        for output in ['', '0 pack ok 3 00\n', '0 pack ok 2 01\n', '0 pack ok 2 00\nextra\n']:
            with self.assertRaises(ValueError):
                check_worker(output, 'leakcheck: allocs=1 frees=1 live_bytes=0', [case])

    def test_reject_false_heap_evidence(self):
        case = make('zero', 0, 0)
        case['queries'] = []
        for heap in ['', 'leakcheck: allocs=0 frees=0 live_bytes=0',
                     'leakcheck: allocs=2 frees=1 live_bytes=0', 'leakcheck: allocs=1 frees=1 live_bytes=1']:
            with self.assertRaises(ValueError):
                check_worker('0 pack ok 2 00\n', heap, [case])

    def test_output_budget_and_prefix(self):
        case = make('budget', 0, 4096, prefix=7, budget=3)
        self.assertEqual(encoded(case), dict(error='Limit'))
        self.assertEqual(len(fields64(2**64 - 1)), 16)


if __name__ == '__main__':
    unittest.main()
