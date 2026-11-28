"""Reject byte, boundary, offline CMM, protocol, reference, process, and heap faults."""
import copy
import unittest
import qualify_icc_srgb_destination as c
from generate_icc_srgb_destination import bits, expected


class DestinationProtocolTests(unittest.TestCase):
    def setUp(self):
        self.case = dict(name='white', input_bits=[bits(63190/65536), bits(1.0), bits(54060/65536)], cancel_at=0, control=True, lcms_applicable=True)
        self.case['expected'] = expected(self.case['input_bits'], 0)
        self.case['lcms_bytes'] = [255, 255, 255]
        self.case['payload_sha256'] = c.sha(c.packet(self.case))
        self.error = dict(name='cancelled', input_bits=[bits(0.5)]*3, cancel_at=1, control=False, lcms_applicable=False)
        self.error['expected'] = expected(self.error['input_bits'], 1)
        self.error['payload_sha256'] = c.sha(c.packet(self.error))
        self.boundary = dict(self.case, name='boundary', control=False)
        self.boundary['expected'] = dict(status='ok', polls=2, bytes=[255, 128, 255], allowed=[[255], [127, 128], [255]], boundary_gaps=[0.0, 1e-12, 0.0])
        self.boundary['lcms_bytes'] = [255, 127, 255]
        self.cases = [self.case, self.error]
        self.names = ['case-0.icc-worker', 'case-1.icc-worker']
        self.stdout = f'encode {self.names[0]} ok 2 255 255 255\nencode {self.names[1]} error Cancelled 1\n'
        self.stderr = 'leakcheck: allocs=19 frees=19 live_bytes=0\n'

    def check(self, stdout=None, stderr=None, cases=None, names=None, code=0):
        return c.check_output(self.stdout if stdout is None else stdout, self.stderr if stderr is None else stderr,
                              self.cases if cases is None else cases, self.names if names is None else names, code)

    def test_reference_white(self):
        self.assertEqual(self.case['expected']['bytes'], [255, 255, 255])
        self.assertEqual(expected([bits(-0.0)]*3, 0)['bytes'], [0, 0, 0])

    def test_success_and_error(self):
        samples, census = self.check()
        self.assertEqual(samples[0]['observation']['lcms_differences'], [0]*3)
        self.assertEqual(samples[0]['observation']['boundary_differences'], [])
        self.assertEqual(census['live_bytes'], 0)

    def test_exact_bytes(self):
        for text in ['254 255 255', '255 255 256', '255 255 -1', '255 255 0255']:
            with self.assertRaises(ValueError):
                self.check(stdout=self.stdout.replace('255 255 255', text))

    def test_boundary_allowance_is_recorded(self):
        cases = [self.boundary, self.error]
        for byte in ['127', '128']:
            samples, _ = self.check(cases=cases, stdout=self.stdout.replace('255 255 255', f'255 {byte} 255'))
            self.assertEqual(len(samples[0]['observation']['boundary_differences']), 1 if byte == '127' else 0)
        with self.assertRaises(ValueError):
            self.check(cases=cases, stdout=self.stdout.replace('255 255 255', '255 129 255'))

    def test_offline_cmm_within_one(self):
        cases = copy.deepcopy(self.cases)
        cases[0]['lcms_bytes'] = [254, 255, 255]
        self.check(cases=cases)
        cases[0]['lcms_bytes'] = [253, 255, 255]
        with self.assertRaises(ValueError):
            self.check(cases=cases)

    def test_errors_and_polls(self):
        for text in ['error Input 1', 'error Cancelled 2', 'error Unknown 1']:
            with self.assertRaises(ValueError):
                self.check(stdout=self.stdout.replace('error Cancelled 1', text))
        with self.assertRaises(ValueError):
            self.check(stdout=self.stdout.replace('ok 2', 'ok 1'))
        for inputs, error in [([bits(float('inf'))] + [bits(0.5)]*2, 'Input'), ([bits(2.0) + 1] + [bits(0.5)]*2, 'Unsupported')]:
            self.assertEqual(expected(inputs, 1), dict(status='error', error=error, polls=0))

    def test_missing_extra_reordered_and_duplicate(self):
        lines = self.stdout.splitlines()
        for text in ['', lines[0] + '\n', self.stdout + 'extra\n', '\n'.join(reversed(lines)) + '\n']:
            with self.assertRaises(ValueError):
                self.check(stdout=text)
        for names in [self.names[::-1], self.names[:1], [self.names[0]]*2]:
            with self.assertRaises(ValueError):
                self.check(names=names)

    def test_heap_requirements(self):
        for text in ['', self.stderr*2, 'leakcheck: allocs=19 frees=18 live_bytes=0', 'leakcheck: allocs=19 frees=19 live_bytes=1', 'leakcheck: allocs=0 frees=0 live_bytes=0']:
            with self.assertRaises(ValueError):
                self.check(stderr=text)

    def test_process_and_sanitizer(self):
        for code in [1, 3, 137, -9]:
            with self.assertRaises(ValueError):
                self.check(code=code)
        with self.assertRaises(ValueError):
            self.check(stderr=self.stderr + 'fern-sanitizer: fault\n')

    def test_reference_identity_and_shape(self):
        for change in [dict(input_bits=[bits(0.75)]*3), dict(cancel_at=True), dict(lcms_applicable=False),
                       dict(expected=dict(status='ok', polls=2, bytes=[256]*3, allowed=[[256]]*3, boundary_gaps=[0.0]*3)),
                       dict(expected=dict(status='ok', polls=2, bytes=[1]*3, allowed=[[1, 2]]*3, boundary_gaps=[0.0]*3)),
                       dict(expected=dict(status='error', error='Numeric', polls=0))]:
            case = copy.deepcopy(self.case)
            case.update(change)
            with self.assertRaises(ValueError):
                self.check(cases=[case, self.error])


if __name__ == '__main__':
    unittest.main()
