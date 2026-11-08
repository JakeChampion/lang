import struct
import unittest

from qualify_jxl_bits import pack
from qualify_jxl_f16 import float_bits
from qualify_jxl_tone import DEFAULT, check_worker, decode, encoded, fields, fixture, original_fields, raw_fields, reference_queries


class ToneControls(unittest.TestCase):
    def test_default_golden(self):
        self.assertEqual(encoded(DEFAULT), dict(bits=1, data=b'\x01'))
        self.assertEqual(decode(b'\x01', 0, 1), 'tone 1132396544 0 0 0 1')

    def test_explicit_golden(self):
        values = (float_bits(1000), float_bits(0.5), 1, float_bits(0.25))
        self.assertEqual(encoded(values), dict(bits=50, data=bytes.fromhex('a0c7007002d000')))
        self.assertEqual(decode(bytes.fromhex('a0c7007002d000'), 0, 50), 'tone 1148846080 1056964608 1 1048576000 50')

    def test_unreadable_original_intensity(self):
        values = (float_bits(1e-8), 0, 0, 0)
        self.assertEqual(fields(values), dict(error='Value'))
        original = pack(original_fields(values)['fields'], 7)
        self.assertEqual(original, dict(bits=50, data=bytes(7)))
        self.assertEqual(decode(original['data'], 0, 50), 'error Value')

    def test_retained_finite_minimum(self):
        values = (DEFAULT[0], float_bits(1e-7), 0, 0)
        self.assertEqual(original_fields(values), dict(fields=[(1, 1)]))
        self.assertEqual(fields(values), dict(fields=raw_fields(0x5bf8, 1, 0, 0)))
        output = encoded(values)
        self.assertEqual(decode(output['data'], 0, 50), 'tone 1132396544 864026624 0 0 50')

    def test_invalid_partial_default(self):
        values = (DEFAULT[0], float_bits(-1e-7), 1, float_bits(1000))
        self.assertEqual(original_fields(values), dict(fields=[(1, 1)]))
        self.assertEqual(fields(values), dict(error='Value'))

    def test_negative_linear_default(self):
        values = (DEFAULT[0], 0, 0, float_bits(-1e-7))
        self.assertEqual(original_fields(values), dict(fields=[(1, 1)]))
        self.assertEqual(fields(values), dict(error='Value'))

    def test_semantic_relations(self):
        for values in [(0, 0, 0, 0), (float_bits(1), float_bits(2), 0, 0),
                       (float_bits(1), 0, 1, float_bits(2)), (float_bits(1), float_bits(-1), 0, 0)]:
            self.assertEqual(fields(values), dict(error='Value'))
        self.assertIn('fields', fields((float_bits(1), 0, 0, float_bits(2))))

    def test_finite_domain(self):
        self.assertEqual(fields((0x7f800000, 0, 0, 0)), dict(error='Float(Value)'))
        self.assertEqual(fields((0x7fc00000, 0, 0, 0)), dict(error='Value'))
        self.assertEqual(fields((0x477fe001, 0, 0, 0)), dict(error='Float(Value)'))

    def test_default_zero_normalization(self):
        self.assertEqual(fields((DEFAULT[0], 0x80000000, 0, 0x80000000)), dict(fields=[(1, 1)]))

    def test_signed_zero_reader(self):
        data = pack(raw_fields(0x3c00, 0x8000, 0, 0x8000), 7)['data']
        self.assertEqual(decode(data, 0, 50), 'tone 1065353216 2147483648 0 2147483648 50')

    def test_every_bit_cut(self):
        data = bytes.fromhex('a0c7007002d000')
        for cut in range(50):
            self.assertTrue(decode(data, 0, cut).startswith('error'))
        self.assertFalse(decode(data, 0, 50).startswith('error'))

    def test_first_error_order(self):
        self.assertEqual(decode(bytes(7), 0, 17), 'error Value')
        self.assertEqual(decode(bytes(7), 0, 16), 'error Float(Input(Truncated))')
        data = pack(raw_fields(0x3c00, 0x7c00, 0, 0), 7)['data']
        self.assertEqual(decode(data, 0, 50), 'error Float(Value)')

    def test_bounds(self):
        for p, e in [(-1, 1), (0, -1), (0, 9), (2**63 - 1, 1)]:
            self.assertEqual(decode(b'\x01', p, e), 'error Input(Bounds)')
        self.assertEqual(decode(b'', 0, 0), 'error Input(Truncated)')

    def test_output_budget(self):
        values = (float_bits(1000), 0, 0, 0)
        self.assertEqual(encoded(values, 7, 7), dict(error='Limit'))
        self.assertEqual(encoded(DEFAULT, 0, 0), dict(error='Limit'))
        self.assertEqual(encoded(DEFAULT, 57, 14), dict(error='Width'))
        self.assertEqual(encoded((0, 0, 0, 0), 57, -1), dict(error='Value'))

    def test_packet_layout(self):
        case = dict(data=b'\x01', queries=[(0, 1)], encodes=[(DEFAULT, 0, 1)])
        data = fixture(case)
        self.assertEqual(struct.unpack_from('<IIIqq4Iii', data), (1, 1, 1, 0, 1, *DEFAULT, 0, 1))
        self.assertEqual(len(data), 53)

    def test_exact_worker_output(self):
        case = dict(data=b'\x01', queries=[(0, 1)], encodes=[(DEFAULT, 0, 1)])
        check_worker('0 query 0 tone 1132396544 0 0 0 1\n0 encode 0 ok 1 01\n',
            'leakcheck: allocs=1 frees=1 live_bytes=0', [case])

    def test_false_worker_output(self):
        case = dict(data=b'', queries=[], encodes=[(DEFAULT, 0, 1)])
        for output in ['', '0 encode 0 ok 2 01\n', '0 encode 0 ok 1 00\n', '0 encode 0 ok 1 01\nextra\n']:
            with self.assertRaises(ValueError):
                check_worker(output, 'leakcheck: allocs=1 frees=1 live_bytes=0', [case])

    def test_false_heap(self):
        case = dict(data=b'', queries=[], encodes=[(DEFAULT, 0, 1)])
        for heap in ['', 'leakcheck: allocs=0 frees=0 live_bytes=0', 'leakcheck: allocs=2 frees=1 live_bytes=0',
                     'leakcheck: allocs=1 frees=1 live_bytes=1']:
            with self.assertRaises(ValueError):
                check_worker('0 encode 0 ok 1 01\n', heap, [case])

    def test_safe_reference_read_protocol(self):
        case = dict(data=b'\x01', queries=[(0, 1)], encodes=[(DEFAULT, 0, 1)])
        command, wanted, counts = reference_queries(case)
        self.assertEqual(command, b'input 01\nread 0\nencode 1132396544 0 0 0\ninput 01\nread 0\n')
        self.assertEqual(wanted, ['tone 1132396544 0 0 0 1', 'encoded 1 01', 'tone 1132396544 0 0 0 1',
            'tone 1132396544 0 0 0 1'])
        self.assertEqual(counts['safe_reads'], 1)

    def test_discrepancy_categories(self):
        case = dict(data=b'', queries=[], encodes=[((float_bits(1e-8), 0, 0, 0), 0, 7),
            ((DEFAULT[0], float_bits(1e-7), 0, 0), 0, 7), ((DEFAULT[0], float_bits(-1e-7), 1, float_bits(1000)), 0, 7)])
        _, _, counts = reference_queries(case)
        self.assertEqual((counts['refused_unreadable'], counts['exact_default'], counts['refused_invalid']), (1, 1, 1))


if __name__ == '__main__':
    unittest.main()
