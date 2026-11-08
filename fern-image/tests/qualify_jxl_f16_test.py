import json
from pathlib import Path
import struct
import unittest

from qualify_jxl_bits import digest
from qualify_jxl_f16 import boundary_words, cases, check_worker, decode, encoded, encode_word, fixture, half_bits, reference_queries


class FiniteMetadataControls(unittest.TestCase):
    def test_exact_read_goldens(self):
        for word, value in [(0, 0), (0x8000, 0x80000000), (1, 0x33800000), (0x3ff, 0x387fc000),
                            (0x400, 0x38800000), (0x3c00, 0x3f800000), (0x7bff, 0x477fe000)]:
            self.assertEqual(half_bits(word), value)

    def test_nonfinite_half_words(self):
        for word in [0x7c00, 0xfc00, 0x7c01, 0x7e00, 0xffff]:
            self.assertIsNone(half_bits(word))
            self.assertEqual(decode(struct.pack('<H', word), 0, 16), 'error Value')

    def test_signed_zero_policy(self):
        self.assertEqual(decode(b'\x00\x80', 0, 16), 'value 2147483648 16')
        self.assertEqual(encode_word(0x80000000), 0)

    def test_tiny_signed_values(self):
        for word in [1, 0x80000001, 0x337fffff, 0xb37fffff]:
            self.assertEqual(encode_word(word), 0)

    def test_subnormal_boundary(self):
        self.assertEqual(encode_word(0x33800000), 1)
        self.assertEqual(encode_word(0xb3800000), 0x8001)
        self.assertEqual(encode_word(0x387fffff), 0x3ff)
        self.assertEqual(encode_word(0x38800000), 0x400)

    def test_truncation_above_midpoint(self):
        self.assertEqual(encode_word(0x3f801001), 0x3c00)
        self.assertEqual(encode_word(0x3f803000), 0x3c01)
        self.assertEqual(encode_word(0xbf801001), 0xbc00)

    def test_finite_limit(self):
        self.assertEqual(encode_word(0x477fe000), 0x7bff)
        self.assertEqual(encode_word(0xc77fe000), 0xfbff)
        self.assertIsNone(encode_word(0x477fe001))
        self.assertIsNone(encode_word(0xc77fe001))

    def test_nonfinite_binary32(self):
        for word in [0x7f800000, 0xff800000, 0x7fc00000, 0x7f800001, 0xffffffff]:
            self.assertIsNone(encode_word(word))

    def test_closed_extent(self):
        for cut in range(16):
            self.assertEqual(decode(b'\x00\x3c', 0, cut), 'error Input(Truncated)')
        self.assertEqual(decode(b'\x00\x3c', 0, 16), 'value 1065353216 16')

    def test_malformed_bounds(self):
        for p, e in [(-1, 16), (0, -1), (0, 17), (17, 16), (2**63 - 1, 16)]:
            self.assertEqual(decode(b'\x00\x3c', p, e), 'error Input(Bounds)')
        self.assertEqual(decode(b'', 0, 0), 'error Input(Truncated)')

    def test_offset_bytes(self):
        result = encoded(0x3f800000, 7, 3)
        self.assertEqual(result, dict(bits=23, data=bytes.fromhex('00001e')))
        self.assertEqual(decode(result['data'], 7, 23), 'value 1065353216 23')

    def test_output_admission(self):
        self.assertEqual(encoded(0, 0, -1), dict(error='Limit'))
        self.assertEqual(encoded(0, 7, 2), dict(error='Limit'))
        self.assertEqual(encoded(0, 57, 9), dict(error='Width'))
        self.assertEqual(encoded(0x7f800000, 57, -1), dict(error='Value'))

    def test_packet_layout(self):
        case = dict(data=b'\x00\x3c', queries=[(0, 16)], encodes=[(0x3f800000, 0, 2)])
        actual = fixture(case)
        self.assertEqual(struct.unpack_from('<IIIqqIii', actual), (1, 1, 2, 0, 16, 0x3f800000, 0, 2))
        self.assertEqual(len(actual), 42)

    def test_worker_exact_results(self):
        case = dict(data=b'\x00\x3c', queries=[(0, 16)], encodes=[(0x3f800000, 0, 2)])
        check_worker('0 query 0 value 1065353216 16\n0 encode 0 ok 16 003c\n',
            'leakcheck: allocs=1 frees=1 live_bytes=0', [case])

    def test_worker_rejects_false_results(self):
        case = dict(data=b'', queries=[], encodes=[(0x3f800000, 0, 2)])
        for output in ['', '0 encode 0 ok 15 003c\n', '0 encode 0 ok 16 013c\n', '0 encode 0 error Value\n',
                       '0 encode 0 ok 16 003c\nextra\n']:
            with self.assertRaises(ValueError):
                check_worker(output, 'leakcheck: allocs=1 frees=1 live_bytes=0', [case])

    def test_worker_rejects_false_heap(self):
        case = dict(data=b'', queries=[], encodes=[(0x3f800000, 0, 2)])
        for heap in ['', 'leakcheck: allocs=0 frees=0 live_bytes=0', 'leakcheck: allocs=2 frees=1 live_bytes=0',
                     'leakcheck: allocs=1 frees=1 live_bytes=1']:
            with self.assertRaises(ValueError):
                check_worker('0 encode 0 ok 16 003c\n', heap, [case])

    def test_exhaustive_word_packets(self):
        words = [case for case in cases() if case['name'].startswith('words-')]
        self.assertEqual(len(words), 256)
        self.assertEqual(sum(len(case['queries']) for case in words), 65536)
        self.assertEqual(sum(len(case['encodes']) for case in words), 63488)

    def test_binary32_interval_boundaries(self):
        words = boundary_words()
        self.assertIn(0x3f801000, words)
        self.assertIn(0x3f801fff, words)
        self.assertIn(0x33800000, words)
        self.assertIn(0x477fe001, words)
        self.assertEqual(words, sorted(set(words)))

    def test_reference_query_protocol(self):
        case = dict(data=b'\x00\x3c', queries=[(0, 16), (0, 0), (1, 16)], encodes=[(0x3f800000, 0, 2), (0x7f800000, 0, 2)])
        request, expected, count = reference_queries(case)
        self.assertEqual(request, b'read 15360\nencode 1065353216\nencode 2139095040\n')
        self.assertEqual(expected, ['read 1065353216 16', 'encoded 15360 16', 'value'])
        self.assertEqual(count, 1)

    def test_real_metadata_and_provenance(self):
        root = Path(__file__).parent
        record = json.loads((root / 'fixtures/jxl_f16_reference.json').read_text())
        self.assertEqual(record['reference_version'], '0.12.0')
        self.assertEqual(record['generator_sha256'], digest(root / 'generate_jxl_f16.py'))
        self.assertEqual(record['adapter_sha256'], digest(root / 'generate_jxl_f16.c'))
        self.assertEqual(len(record['reference_dependencies']), 8)
        actual = [case for case in cases() if case['name'].startswith('real-')]
        self.assertEqual(len(actual), 5)
        for case, row in zip(actual, record['images'], strict=True):
            for position, output in zip(row['positions'], row['output_bits'], strict=True):
                self.assertEqual(decode(case['data'], position, len(case['data']) * 8), f'value {output} {position + 16}')


if __name__ == '__main__':
    unittest.main()
