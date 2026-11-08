"""Check frozen Adam7 fixtures and the independent decoder boundary."""

import io
from pathlib import Path
import unittest

from PIL import Image
from fixtures import generate_png_adam7 as reference


class Adam7ReferenceTests(unittest.TestCase):
    def test_frozen_samples_match_independent_pixels(self):
        self.assertEqual(Path(reference.__file__).with_name('png_adam7.fern').read_text(), reference.source())

    def test_wide_packed_and_multichannel_rows(self):
        for depth, color in [(1, 0), (2, 0), (4, 3), (8, 0), (8, 2), (8, 4), (8, 6)]:
            with self.subTest(depth=depth, color=color):
                value = reference.vector(depth, color, 257, 17, 1)
                self.assertEqual(len(value['boundaries']), 7)
                self.assertEqual(len(value['expected']), 257 * 17 * value['channels'])

    def test_reference_rejects_invalid_filter_in_every_pass(self):
        for data, _ in list(reference.malformed())[:7]:
            with self.assertRaises(OSError):
                with Image.open(io.BytesIO(data)) as image:
                    image.load()


if __name__ == '__main__':
    unittest.main()
