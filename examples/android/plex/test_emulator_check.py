import subprocess
import unittest

import emulator_check as check


class VideoScreenTests(unittest.TestCase):
    def test_real_pattern_includes_the_gradient(self):
        width, height = 320, 640
        pixels = subprocess.run([
            "ffmpeg", "-v", "error", "-f", "lavfi", "-i",
            "testsrc=size=320x240:rate=25", "-vf", "scale=320:640",
            "-frames:v", "1", "-pix_fmt", "rgba", "-f", "rawvideo", "-",
        ], check=True, capture_output=True, timeout=30).stdout
        self.assertEqual(len(pixels), width * height * 4)
        old = check.colors_in(width, height, pixels,
                              (width // 4, height // 3, width * 3 // 4, height * 2 // 3))
        self.assertLess(old, 12)
        self.assertGreaterEqual(check.video_colors(width, height, pixels), 12)

    def test_blank_solid_and_poster_screens_fail(self):
        width, height = 320, 640
        for color in ((0, 0, 0, 255), (255, 255, 255, 255), (*check.POSTER, 255)):
            with self.subTest(color=color):
                pixels = bytes(color) * width * height
                self.assertLess(check.video_colors(width, height, pixels), 12)


if __name__ == "__main__":
    unittest.main()
