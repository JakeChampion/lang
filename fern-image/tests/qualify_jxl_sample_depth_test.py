import struct
import unittest

from qualify_jxl_bits import pack
from qualify_jxl_sample_depth import check_worker, choices, decode, encoded, fields, fixture, make, valid


class PrecisionControls(unittest.TestCase):
    def test_exact_goldens(self):
        for floating, precision, exponent, data, size in [(False, 8, 0, '00', 3), (False, 10, 0, '02', 3),
                (False, 12, 0, '04', 3), (False, 31, 0, 'f600', 9), (True, 16, 5, '23', 7),
                (True, 24, 7, '35', 7), (True, 32, 8, '39', 7)]:
            result = encoded(make('golden', floating, precision, exponent))
            self.assertEqual((result['data'].hex(), result['bits']), (data, size))

    def test_integer_semantic_limit(self):
        self.assertEqual(fields(False, 32, 0), dict(error='Value'))
        self.assertEqual(fields(False, 0, 0), dict(error='Value'))
        self.assertTrue(valid(False, 31, 0))

    def test_integer_exponent_normalization(self):
        self.assertEqual(fields(False, 8, 2**32 - 1), fields(False, 8, 0))
        self.assertEqual(decode(b'\x00', 0, 3), 'depth 0 8 0 3')

    def test_four_exponent_bits(self):
        self.assertEqual(decode(b'\x39', 0, 6), 'error Input(Truncated)')
        self.assertEqual(decode(b'\x39', 0, 7), 'depth 1 32 8 7')

    def test_mantissa_boundaries(self):
        for exponent in range(2, 9):
            self.assertTrue(valid(True, exponent + 3, exponent))
            self.assertTrue(valid(True, exponent + 24, exponent))
            self.assertFalse(valid(True, exponent + 2, exponent))
            self.assertFalse(valid(True, exponent + 25, exponent))

    def test_exponent_domains(self):
        for exponent in [0, 1, 9, 16, 2**32 - 1]:
            self.assertEqual(fields(True, 16, exponent), dict(error='Value'))

    def test_noncanonical_integer(self):
        data = pack([(0, 1), (3, 2), (7, 6)], 2)['data']
        self.assertEqual(decode(data, 0, 9), 'depth 0 8 0 9')
        self.assertEqual(encoded(make('canonical', False, 8, 0))['bits'], 3)

    def test_full_raw_exponent_domain(self):
        for raw in range(16):
            data = pack([(1, 1), (1, 2), (raw, 4)], 2)['data']
            expected = f'depth 1 16 {raw + 1} 7' if 1 <= raw <= 7 else 'error Value'
            self.assertEqual(decode(data, 0, 7), expected)

    def test_logical_and_physical_bounds(self):
        self.assertEqual(decode(b'\x00', -1, 3), 'error Input(Bounds)')
        self.assertEqual(decode(b'\x00', 0, 9), 'error Input(Bounds)')
        self.assertEqual(decode(b'', 0, 0), 'error Input(Truncated)')

    def test_complete_packet_layout(self):
        case = make('packet', True, 16, 5, prefix=7)
        data = fixture(case)
        self.assertEqual(struct.unpack_from('<3IiiII', data), (1, 16, 5, 4, 7, len(case['queries']), len(case['data'])))
        self.assertEqual(len(data), 28 + 16 * len(case['queries']) + len(case['data']))

    def test_exact_output_and_heap(self):
        case = make('integer', False, 8, 0)
        case['queries'] = []
        check_worker('0 pack ok 3 00\n', 'leakcheck: allocs=1 frees=1 live_bytes=0', [case])

    def test_reject_false_output(self):
        case = make('integer', False, 8, 0)
        case['queries'] = []
        for output in ['', '0 pack ok 2 00\n', '0 pack ok 3 01\n', '0 pack ok 3 00\nextra\n']:
            with self.assertRaises(ValueError):
                check_worker(output, 'leakcheck: allocs=1 frees=1 live_bytes=0', [case])

    def test_reject_false_heap(self):
        case = make('integer', False, 8, 0)
        case['queries'] = []
        for heap in ['', 'leakcheck: allocs=0 frees=0 live_bytes=0', 'leakcheck: allocs=2 frees=1 live_bytes=0',
                     'leakcheck: allocs=1 frees=1 live_bytes=1']:
            with self.assertRaises(ValueError):
                check_worker('0 pack ok 3 00\n', heap, [case])

    def test_budget_and_field_width(self):
        self.assertEqual(encoded(make('budget', False, 31, 0, budget=1)), dict(error='Limit'))
        self.assertEqual(encoded(make('prefix', False, 8, 0, prefix=57)), dict(error='Width'))

    def test_unsigned_field_distributions(self):
        self.assertEqual(choices(False), [(0, 8), (0, 10), (0, 12), (6, 1)])
        self.assertEqual(choices(True), [(0, 32), (0, 16), (0, 24), (6, 1)])

    def test_real_fixture_declarations(self):
        from qualify_jxl_sample_depth import cases
        actual = [case for case in cases() if case['name'].startswith('real-')]
        self.assertEqual(len(actual), 8)
        for case in actual:
            f, precision, exponent = case['values']
            position, extent = case['queries'][-1]
            expected_end = position + sum(width for _, width in fields(f, precision, exponent)['fields'])
            self.assertEqual(decode(case['data'], position, extent), f'depth {f} {precision} {exponent} {expected_end}')

    def test_fixture_provenance(self):
        import json
        from pathlib import Path
        from qualify_jxl_bits import digest
        root = Path(__file__).parent
        record = json.loads((root / 'fixtures/jxl_sample_depth_reference.json').read_text())
        self.assertEqual(record['reference_version'], '0.12.0')
        self.assertEqual(record['generator_sha256'], digest(root / 'generate_jxl_sample_depth.py'))
        self.assertEqual(record['adapter_sha256'], digest(root / 'generate_jxl_sample_depth.c'))
        self.assertEqual(len(record['reference_dependencies']), 8)

    def test_reference_box_extraction(self):
        from generate_jxl_sample_depth import codestream
        signature = bytes.fromhex('0000000c4a584c200d0a870a')
        raw = b'\xff\x0a\x12\x34'
        self.assertEqual(codestream(raw), raw)
        self.assertEqual(codestream(signature + struct.pack('>I4s', 12, b'jxlc') + raw), raw)
        self.assertEqual(codestream(signature + struct.pack('>I4sQ', 1, b'jxlc', 20) + raw), raw)
        self.assertEqual(codestream(signature + struct.pack('>I4s', 0, b'jxlc') + raw), raw)
        for data in [b'', signature, signature + b'\0', signature + struct.pack('>I4s', 7, b'jxlc'),
                     signature + struct.pack('>I4s', 1, b'jxlc'), signature + struct.pack('>I4s', 13, b'jxlc') + raw]:
            with self.assertRaises(ValueError):
                codestream(data)


if __name__ == '__main__':
    unittest.main()
