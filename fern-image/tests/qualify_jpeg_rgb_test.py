"""Fault controls for exact RGB composition and finite independent references."""
from fractions import Fraction
from pathlib import Path
import tempfile
import unittest

import qualify_jpeg_rgb as collector
from jpeg_rgb_reference import component_sample, rounded
from generate_jpeg_rgb import profile_outcome


class RgbProtocolTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        planes = [{'id': i + 1, 'horizontal': 1, 'vertical': 1, 'width': 1, 'height': 1, 'pixels': [128]} for i in range(3)]
        self.case = {'name': 'neutral', 'encoded_hex': 'ffd8ffd9', 'input_sha256': 'source', 'outcome': 'accepted',
                     'raw_reference': {'width': 1, 'height': 1, 'planes': planes}, 'nearest_hex': '808080',
                     'bilinear_hex': '808080', 'codec_reference': {'pixels': [128, 128, 128]}}
        for index in range(2):
            (self.directory / f'case-0.jpg.rgb-{index}.raw').write_bytes(bytes([128] * 3))
        for index in range(3):
            (self.directory / f'case-0.jpg.plane-{index}.raw').write_bytes(bytes([128]))
        self.stdout = 'rgb case-0.jpg 0 1 1 3 3 4 3 6 16\nrgb case-0.jpg 1 1 1 3 3 4 3 6 16\nplanes case-0.jpg 3\n'
        self.stdout += ''.join(f'plane {i} {i + 1} 1 1 1 1 1 1\n' for i in range(3)) + 'end case-0.jpg\n'
        self.stderr = 'leakcheck: allocs=23 frees=23 live_bytes=0\n'

    def check(self, stdout=None, stderr=None):
        return collector.check_output(self.stdout if stdout is None else stdout, self.stderr if stderr is None else stderr,
                                      [self.case], ['case-0.jpg'], self.directory)

    def test_complete_exact_observation(self):
        samples, census = self.check()
        self.assertEqual(census, {'allocs': 23, 'frees': 23, 'live_bytes': 0})
        self.assertEqual(samples[0]['filters'][0]['sha256'], samples[0]['filters'][0]['exact_composition_sha256'])

    def test_raw_sample_allowance_keeps_exact_composition(self):
        (self.directory / 'case-0.jpg.plane-0.raw').write_bytes(bytes([129]))
        for index in range(2):
            (self.directory / f'case-0.jpg.rgb-{index}.raw').write_bytes(bytes([129] * 3))
        samples, _ = self.check()
        self.assertEqual(samples[0]['filters'][0]['max_sample_error'], 1)
        self.assertEqual(samples[0]['planes'][0]['max_sample_error'], 1)

    def test_color_must_match_own_raw_planes_exactly(self):
        (self.directory / 'case-0.jpg.rgb-0.raw').write_bytes(bytes([129, 128, 128]))
        with self.assertRaisesRegex(ValueError, 'exact rational'):
            self.check()

    def test_component_and_codec_reference_conditions(self):
        self.case['codec_reference']['pixels'] = [123] * 3
        with self.assertRaisesRegex(ValueError, 'supplemental C'):
            self.check()
        self.case['codec_reference']['pixels'] = [128] * 3
        (self.directory / 'case-0.jpg.plane-1.raw').write_bytes(bytes([130]))
        with self.assertRaisesRegex(ValueError, 'component reference'):
            self.check()

    def test_metadata_and_logical_plan(self):
        for value in [self.stdout.replace('3 6 16', '3 6 15'), self.stdout.replace('0 1 1 3 3', '0 1 1 4 4'),
                      self.stdout.replace('plane 0 1', 'plane 0 7'), self.stdout.replace('planes case-0.jpg 3', 'planes case-0.jpg 2')]:
            with self.assertRaises(ValueError):
                self.check(stdout=value)

    def test_missing_short_or_extra_output(self):
        output = self.directory / 'case-0.jpg.rgb-0.raw'
        for data in [b'', b'\x80', b'\x80' * 4]:
            output.write_bytes(data)
            with self.assertRaises(ValueError):
                self.check()
        output.unlink()
        with self.assertRaises(ValueError):
            self.check()

    def test_protocol_heap_and_unexpected_file(self):
        for stdout in ['', self.stdout + 'extra\n', self.stdout.replace('end case-0.jpg\n', '')]:
            with self.assertRaises(ValueError):
                self.check(stdout=stdout)
        for stderr in ['', self.stderr * 2, 'leakcheck: allocs=0 frees=0 live_bytes=0', 'leakcheck: allocs=23 frees=22 live_bytes=1']:
            with self.assertRaises(ValueError):
                self.check(stderr=stderr)
        (self.directory / 'unrequested.raw').write_bytes(b'')
        with self.assertRaisesRegex(ValueError, 'unexpected worker file'):
            self.check()

    def test_rejected_profiles_are_explicit_and_have_no_output(self):
        for path in self.directory.iterdir():
            path.unlink()
        self.case['outcome'] = 'profile'
        stdout = 'reject case-0.jpg 0 profile\nreject case-0.jpg 1 profile\nend case-0.jpg\n'
        samples, _ = self.check(stdout=stdout)
        self.assertEqual(samples[0]['outcome'], 'profile')
        with self.assertRaisesRegex(ValueError, 'status mismatch'):
            self.check(stdout=stdout.replace('profile', 'unsupported'))
        (self.directory / 'case-0.jpg.rgb-0.raw').write_bytes(b'')
        with self.assertRaisesRegex(ValueError, 'unexpected worker file'):
            self.check(stdout=stdout)

    def test_rational_rounding_and_centered_nearest_tie(self):
        self.assertEqual([rounded(Fraction(x, 2)) for x in [-3, -1, 1, 3]], [-1, 0, 1, 2])
        plane = {'horizontal': 2, 'vertical': 1, 'width': 2, 'height': 1, 'pixels': [0, 1]}
        self.assertEqual([component_sample(plane, x, 0, 3, 1, False) for x in range(3)], [0, 1, 1])
        self.assertEqual([component_sample(plane, x, 0, 3, 1, True) for x in range(3)], [0, 1, 1])

    def test_declared_component_ids_select_the_expected_profile(self):
        encoded = bytes.fromhex('ffd8ffe000104a46494600010100000100010000')
        self.assertEqual(profile_outcome(encoded, [1, 2, 3]), 'accepted')
        self.assertEqual(profile_outcome(encoded, [1]), 'unsupported')
        for ids in [[201], [3, 2, 1], [17, 201, 55]]:
            self.assertEqual(profile_outcome(encoded, ids), 'profile')
        self.assertEqual(profile_outcome(bytes.fromhex('ffd8ffdb'), [1]), 'profile')


if __name__ == '__main__':
    unittest.main()
