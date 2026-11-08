"""Controls for complete entropy input, mathematical bounds and worker reports."""

import copy
import decimal
import unittest

import qualify_jpeg_component_planes as reference


class Controls(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        decimal.getcontext().prec = 90
        reference.basis_matrix = reference.basis()

    def test_complete_entropy_components_and_restarts(self):
        for pattern in [((4, 4),), ((2, 2), (1, 1), (1, 1)), ((3, 1), (2, 2)), ((2, 2), (1, 1), (1, 1), (1, 1))]:
            for restart in [0, 1, 2, 7]:
                case = reference.frames.make(pattern, 35, 17, restart, 1)
                output = reference.frames.reference(case)
                self.assertEqual(reference.decode(case, output), case['values'])

    def test_marker_truncation_and_extra_bytes(self):
        case = reference.frames.make(((1, 1),), 8, 8)
        output = reference.frames.reference(case)
        for bad in [output[1:], output[:-1], output[:-2], output + b'\0', output[:-2] + b'\0' + output[-2:]]:
            with self.assertRaises(ValueError):
                reference.decode(case, bad)

    def test_stuffing_and_alignment(self):
        bits = reference.Bits(b'\xff\0')
        self.assertEqual(bits.read(8), 255)
        with self.assertRaises(ValueError):
            bits.read(1)
        for data in [b'\xff', b'\xff\xd0', b'']:
            with self.assertRaises(ValueError):
                reference.Bits(data).read(8)
        bits = reference.Bits(b'\0')
        bits.read(3)
        with self.assertRaises(ValueError):
            bits.align()

    def test_wrong_restart_sequence(self):
        case = reference.frames.make(((2, 2), (1, 1), (1, 1)), 35, 17, 1, 1)
        output = reference.frames.reference(case)
        self.assertIn(b'\xff\xd0', output)
        with self.assertRaises(ValueError):
            reference.decode(case, output.replace(b'\xff\xd0', b'\xff\xd1', 1))

    def test_constant_math_requires_exact_integer(self):
        case = reference.make(((1, 1),), 1, 1)
        q = case['quantization'][case['components'][0][3]][0]
        dc = int((decimal.Decimal((17 - 128) * 8) / q).to_integral_value(rounding=decimal.ROUND_HALF_UP))
        values = [dc] + [0] * 63
        self.assertEqual(reference.check_math(case, values), 0)
        for at in [0, 1, 63]:
            bad = values.copy()
            bad[at] += 1
            with self.assertRaises(ValueError):
                reference.check_math(case, bad)

    def test_nonuniform_math_rejects_coefficient_error(self):
        case = reference.make(((1, 1),), 8, 8, 1)
        values = list(reference.mathematical(reference.sample_block(case['planes'][0], 0, 0)))
        result = [0] * 64
        q = case['quantization'][case['components'][0][3]]
        for at, natural in enumerate(reference.frames.scan.permutation()):
            result[natural] = int((values[natural] / q[at]).to_integral_value(rounding=decimal.ROUND_HALF_UP))
        reference.check_math(case, result)
        result[1] += 5
        with self.assertRaises(ValueError):
            reference.check_math(case, result)

    def test_edge_extension_and_stride(self):
        p = {'width': 1, 'height': 1, 'stride': 3, 'samples': [17, 231, 231], 'edge': 0, 'fill': 67}
        self.assertEqual(reference.sample_block(p, 0, 0), bytes([17] * 64))
        p['edge'] = 1
        self.assertEqual(reference.sample_block(p, 0, 0), bytes([17] + [67] * 63))
        self.assertEqual(reference.sample_block(p, 1, 1), bytes([67] * 64))

    def test_admission_preserves_inputs(self):
        case = reference.make(((2, 2), (1, 1), (1, 1)), 17, 17)
        before = copy.deepcopy(case)
        self.assertIsNone(reference.preflight(case))
        self.assertEqual(case, before)
        case['planes'][1]['stride'] = 2147483647
        self.assertEqual(reference.preflight(case), 'Block.Storage')
        case['max_blocks'] = 23
        self.assertEqual(reference.preflight(case), 'Order.Limit')
        case['width'] = 0
        self.assertEqual(reference.preflight(case), 'Order.Dimensions')

    def test_positive_balanced_worker_heap(self):
        lines, census = reference.check_worker('case ok 00\n', 'leakcheck: allocs=1 frees=1 live_bytes=0\n', 1)
        self.assertEqual(lines, ['case ok 00'])
        self.assertEqual(census, {'allocs': 1, 'frees': 1, 'live_bytes': 0})
        for text in ['', 'leakcheck: allocs=0 frees=0 live_bytes=0\n', 'leakcheck: allocs=1 frees=0 live_bytes=0\n', 'leakcheck: allocs=1 frees=1 live_bytes=1\n', 'leakcheck: allocs=1 frees=1 live_bytes=0\nextra\n']:
            with self.assertRaises(ValueError):
                reference.check_worker('case ok 00\n', text, 1)
        with self.assertRaises(ValueError):
            reference.check_worker('', 'leakcheck: allocs=1 frees=1 live_bytes=0\n', 1)


if __name__ == '__main__':
    unittest.main()
