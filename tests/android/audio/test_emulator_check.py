"""Check that timestamp diagnostics preserve their availability distinction."""
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('audio_emulator', Path(__file__).with_name('emulator_check.py'))
audio = importlib.util.module_from_spec(spec)
spec.loader.exec_module(audio)


PATTERNS = ((audio.TIMESTAMP, 'timestamp'), (audio.MUSIC_TIMESTAMP, 'music timestamp'))


class TimestampDiagnostics(unittest.TestCase):
    def test_available(self):
        for pattern, name in PATTERNS:
            for values in ('0 monotonic_ns 0', '48000 monotonic_ns 123456789',
                           '1099511627776 monotonic_ns 1125899906842624'):
                with self.subTest(name=name, values=values):
                    line = 'W/fern (123): audiotest: ' + name + ' frames ' + values + '\n'
                    self.assertIsNotNone(pattern.search(line))

    def test_unavailable(self):
        for pattern, name in PATTERNS:
            self.assertIsNotNone(pattern.search('W/fern (123): audiotest: ' + name + ' unavailable\n'))

    def test_invalid_or_missing(self):
        for pattern, name in PATTERNS:
            for text in ('', 'frames -1 monotonic_ns 1', 'frames 1 monotonic_ns -1',
                         'frames 1', 'available', 'unavailable extra', 'frames 1 monotonic_ns 2 extra'):
                with self.subTest(name=name, text=text):
                    self.assertIsNone(pattern.search('audiotest: ' + name + ' ' + text + '\n'))


if __name__ == '__main__':
    unittest.main()
