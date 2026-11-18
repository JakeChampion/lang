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

    def pattern(self, filters):
        width, height = 320, 640
        pixels = subprocess.run([
            "ffmpeg", "-v", "error", "-f", "lavfi", "-i",
            "testsrc=size=320x240:rate=25", "-vf", filters,
            "-frames:v", "1", "-pix_fmt", "rgba", "-f", "rawvideo", "-",
        ], check=True, capture_output=True, timeout=30).stdout
        self.assertEqual(len(pixels), width * height * 4)
        return check.frame_fault(width, height, pixels)

    def test_letterboxed_upright_pattern_passes(self):
        self.assertEqual(self.pattern("pad=320:640:0:200:black"), "")

    def test_upside_down_pattern_fails(self):
        self.assertIn("upside down", self.pattern("vflip,pad=320:640:0:200:black"))

    def test_stretched_pattern_fails(self):
        self.assertIn("not 4:3", self.pattern("scale=320:640"))

    def test_black_screen_fails(self):
        self.assertEqual(check.frame_fault(320, 640, bytes((0, 0, 0, 255)) * 320 * 640), "the screen is black")

    def test_blank_solid_and_poster_screens_fail(self):
        width, height = 320, 640
        for color in ((0, 0, 0, 255), (255, 255, 255, 255), (*check.POSTER, 255)):
            with self.subTest(color=color):
                pixels = bytes(color) * width * height
                self.assertLess(check.video_colors(width, height, pixels), 12)



class DumpTests(unittest.TestCase):
    def test_accessible_names_read_text_and_descriptions(self):
        xml = '<node text="" content-desc="Test Pattern (2026)" /><node text="Square Tone" content-desc="" />'
        self.assertEqual(check.accessible_names(xml), {"Test Pattern (2026)", "Square Tone"})

    def test_pinned_needs_the_app_in_the_pinned_mode(self):
        pip = "  * Task{a1 #12 type=standard A=10123:dev.fern.plex U=0 visible=true mode=pinned sz=1}"
        full = "  * Task{a1 #12 type=standard A=10123:dev.fern.plex U=0 visible=true mode=fullscreen sz=1}"
        other = "  * Task{b2 #13 type=standard A=10124:com.other U=0 visible=true mode=pinned sz=1}"
        self.assertTrue(check.pinned(full + "\n" + pip))
        self.assertFalse(check.pinned(full + "\n" + other))


if __name__ == "__main__":
    unittest.main()
