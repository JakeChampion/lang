import copy
import subprocess
import unittest

import qualify_jpeg_component_frames as reference


class ComponentFrameReferenceTests(unittest.TestCase):
    def case(self):
        return reference.make(((1, 1),), 1, 1)

    def test_literal_frame_and_scan_markers(self):
        case = self.case()
        frame = reference.reference(case)
        self.assertTrue(frame.startswith(bytes.fromhex('ffd8ffdb004300')))
        self.assertIn(bytes.fromhex('ffc0000b080001000101001100'), frame)
        self.assertIn(bytes.fromhex('ffda0008010000003f00'), frame)
        self.assertTrue(frame.endswith(b'\xff\xd9'))
        self.assertNotIn(b'JFIF\0', frame)

    def test_single_component_factors_do_not_multiply_mcus(self):
        case = reference.make(((4, 4),), 17, 9)
        self.assertEqual(reference.geometry(case)[:3], (3, 2, 1))
        self.assertEqual(len(case['values']), 6 * 64)
        self.assertEqual(reference.coefficient_lines(case)[1], 'component 0 3 2')

    def test_physical_grids_discard_whole_dummy_blocks(self):
        case = reference.make(((2, 2), (1, 1), (1, 1)), 17, 17)
        for i in range(len(case['values']) // 64):
            case['values'][i * 64] = i
        rows = reference.coefficient_lines(case)
        y = [row for row in rows if row.startswith('block 0 ')]
        self.assertEqual([int(row.split()[4]) for row in y], [0, 1, 6, 2, 3, 8, 12, 13, 18])
        self.assertIn('component 1 2 2', rows)
        self.assertIn('component 2 2 2', rows)
        self.assertEqual(len([row for row in rows if row.startswith('block ')]), 17)

    def test_distinct_component_and_table_selectors(self):
        case = reference.make(((1, 1),) * 4, 8, 8, variant=1)
        beginning = reference.prefix(case)
        self.assertIn(bytes.fromhex('ffc00014080008000804001103ff11022a1101801100'), beginning)
        self.assertIn(bytes.fromhex('ffda000e040001ff102a018010003f00'), beginning)
        for destination in range(4):
            self.assertIn(bytes([255, 219, 0, 67, destination]), beginning)

    def test_exact_limits_include_every_marker_and_eoi(self):
        case = self.case()
        output = reference.reference(case)
        for limit in [0, 1, len(reference.prefix(case)), len(output) - 1]:
            with self.assertRaisesRegex(reference.scan.Rejected, '^Limit$'):
                reference.reference({**case, 'limit': limit})
        self.assertEqual(reference.reference({**case, 'limit': len(output)}), output)

    def test_structural_admission_precedes_byte_limits(self):
        case = self.case()
        with self.assertRaisesRegex(reference.scan.Rejected, '^Dimensions$'):
            reference.reference({**case, 'width': 0, 'quantization': [], 'limit': -1})
        with self.assertRaisesRegex(reference.scan.Rejected, '^Shape$'):
            reference.reference({**case, 'quantization': [], 'limit': -1})
        with self.assertRaisesRegex(reference.scan.Rejected, '^Table$'):
            reference.reference({**case, 'quantization': [[0] * 64], 'limit': -1})

    def test_entropy_errors_retain_the_frame_boundary(self):
        case = self.case()
        case['values'][0] = 1024
        with self.assertRaisesRegex(reference.scan.Rejected, '^Entropy.Coefficient$'):
            reference.reference(case)
        case = self.case()
        case['dc'] = [{'counts': [0] * 16, 'symbols': []}]
        with self.assertRaisesRegex(reference.scan.Rejected, '^Entropy.Table$'):
            reference.reference(case)
        with self.assertRaisesRegex(reference.scan.Rejected, '^Limit$'):
            reference.reference({**case, 'limit': 0})

    def test_worker_protocol_rejects_missing_extra_or_reordered_lines(self):
        case = self.case()
        stdout = 'first ' + reference.expected(case) + '\n'
        heap = 'leakcheck: allocs=1 frees=1 live_bytes=0\n'
        reference.check_output(stdout, heap, [case], ['first'])
        for changed in ['', stdout + stdout, stdout.replace('first ', 'other ', 1)]:
            with self.assertRaises(ValueError):
                reference.check_output(changed, heap, [case], ['first'])

    def test_oracle_limits_are_explicit_and_other_failures_remain_errors(self):
        case = {**self.case(), 'width': 65535}
        stderr = 'Maximum supported image dimension is 65500 pixels\n'
        result = subprocess.CompletedProcess([], 1, '', stderr)
        self.assertEqual(reference.check_oracle(result, case), {'status': 'dimension-limit', 'maximum': 65500, 'stderr': stderr})
        with self.assertRaises(ValueError):
            reference.check_oracle(result, {**case, 'width': 65500})
        for output in [subprocess.CompletedProcess([], 1, 'unexpected', stderr), subprocess.CompletedProcess([], 2, '', stderr),
                       subprocess.CompletedProcess([], 1, '', 'invalid JPEG'), subprocess.CompletedProcess([], 0, '', '')]:
            with self.assertRaises(ValueError):
                reference.check_oracle(output, case)

    def test_heap_protocol_rejects_zero_unbalanced_or_live_counters(self):
        case = self.case()
        stdout = 'first ' + reference.expected(case) + '\n'
        for heap in ['', 'leakcheck: allocs=0 frees=0 live_bytes=0\n', 'leakcheck: allocs=2 frees=1 live_bytes=0\n',
                     'leakcheck: allocs=1 frees=1 live_bytes=1\n', 'leakcheck: allocs=1 frees=1 live_bytes=0\nunexpected\n']:
            with self.assertRaises(ValueError):
                reference.check_output(stdout, heap, [copy.deepcopy(case)], ['first'])


if __name__ == '__main__':
    unittest.main()
