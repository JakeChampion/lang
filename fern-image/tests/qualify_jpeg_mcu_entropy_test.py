"""Controls for exact MCU entropy and complete coefficient observations."""

import copy
import unittest

import qualify_jpeg_mcu_entropy as collector


class McuEntropyTests(unittest.TestCase):
    def base(self):
        values = []
        for value in [1, 2, 3]:
            values.extend([value] + [0] * 63)
        return {'mcus': 1, 'restart': 0, 'limit': 1000000, 'components': [(1, 1, 0, 0)] * 3,
                'dc': [collector.table(False, 8)], 'ac': [collector.table(True)], 'values': values}

    def test_literal_component_predictors(self):
        self.assertEqual(collector.reference(self.base()).hex(), '01800140005807')

    def test_restart_resets_every_predictor(self):
        case = self.base()
        case.update(mcus=19, restart=1, values=case['values'] * 19)
        unit = bytes.fromhex('01800140005807')
        wanted = unit + b''.join(bytes([255, 208 + i % 8]) + unit for i in range(18))
        self.assertEqual(collector.reference(case), wanted)

    def test_exact_limits_include_markers(self):
        case = self.base()
        case.update(mcus=2, restart=1, values=case['values'] * 2)
        size = len(collector.reference(case))
        self.assertTrue(collector.expected({**case, 'limit': size}).startswith('ok '))
        self.assertEqual(collector.expected({**case, 'limit': size - 1}), 'error Limit')

    def test_admission_and_error_precedence(self):
        case = self.base()
        for changes, error in [({'mcus': 0}, 'Shape'), ({'components': []}, 'Shape'),
                               ({'components': [(1, 1, 2, 0)]}, 'Table'),
                               ({'mcus': 2147483647}, 'Limit'), ({'values': []}, 'Shape'),
                               ({'values': [0] * 63}, 'Shape'), ({'values': [1024] + [0] * 191}, 'Coefficient')]:
            self.assertEqual(collector.expected({**case, **changes}), 'error ' + error)

    def test_single_component_factors_and_frame(self):
        case = self.base()
        case.update(mcus=3, components=[(4, 4, 0, 0)])
        self.assertEqual(collector.membership(case), [0])
        self.assertEqual(collector.coefficient_lines(case)[0:2], ['frame 24 8 1', 'component 0 3 1'])
        frame = collector.frame(case, collector.reference(case))
        self.assertTrue(frame.startswith(b'\xff\xd8') and frame.endswith(b'\xff\xd9'))

    def test_distinct_table_selectors_change_bytes(self):
        case = self.base()
        changed = copy.deepcopy(case)
        changed['dc'].append(collector.table(False, 8))
        changed['dc'][1]['symbols'].reverse()
        changed['components'][1] = (1, 1, 1, 0)
        self.assertNotEqual(collector.reference(case), collector.reference(changed))

    def test_worker_protocol(self):
        case = self.base()
        stdout = 'case.raw ' + collector.expected(case) + '\n'
        stderr = 'leakcheck: allocs=10 frees=10 live_bytes=0\n'
        self.assertEqual(collector.check_output(stdout, stderr, [case], ['case.raw'])['allocs'], 10)
        for broken in ['', stdout + 'extra\n', stdout.replace('0180', '0280'), stdout.replace('case.raw', 'wrong.raw')]:
            with self.assertRaises(ValueError):
                collector.check_output(broken, stderr, [case], ['case.raw'])

    def test_worker_heap_evidence(self):
        case = self.base()
        stdout = 'case.raw ' + collector.expected(case) + '\n'
        for stderr in ['', 'leakcheck: allocs=0 frees=0 live_bytes=0\n', 'leakcheck: allocs=10 frees=9 live_bytes=0\n',
                       'leakcheck: allocs=10 frees=10 live_bytes=1\n', 'leakcheck: allocs=10 frees=10 live_bytes=0\n' * 2]:
            with self.assertRaises(ValueError):
                collector.check_output(stdout, stderr, [case], ['case.raw'])

    def test_missing_worker_name(self):
        with self.assertRaises(ValueError):
            collector.check_output('', 'leakcheck: allocs=1 frees=1 live_bytes=0\n', [self.base()], [])


if __name__ == '__main__':
    unittest.main()
