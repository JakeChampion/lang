import pathlib
import subprocess
import tempfile
import urllib.error
import urllib.request
import unittest
from unittest.mock import patch

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


class DownloadTests(unittest.TestCase):
    def test_locates_the_holdable_poster_among_duplicate_labels(self):
        xml = ('<hierarchy><node package="dev.fern.plex" text="Square Tone" bounds="[0,0][2,2]" />'
               '<node package="dev.fern.plex" content-desc="Square Tone" long-clickable="true" '
               'bounds="[4,10][100,200]" /></hierarchy>')
        self.assertEqual(check.node_center(xml, "Square Tone", long_click=True), ("52", "105"))
        self.assertIsNone(check.node_center(xml, "Download"))
        self.assertIsNone(check.node_center("", "Download"))

    def test_download_route_requires_the_token_and_serves_the_file(self):
        with tempfile.TemporaryDirectory() as directory:
            file = pathlib.Path(directory) / "tone.wav"
            file.write_bytes(b"fixture")
            with patch.object(check, "requests", []):
                httpd = check.server({check.DOWNLOAD_PATH.split("?")[0]: file})
                try:
                    url = f"http://127.0.0.1:{httpd.server_port}" + check.DOWNLOAD_PATH
                    with self.assertRaises(urllib.error.HTTPError) as refusal:
                        urllib.request.urlopen(url, timeout=5)
                    self.assertEqual(refusal.exception.code, 403)
                    refusal.exception.close()
                    req = urllib.request.Request(url, headers={"X-Plex-Token": "srv"})
                    with urllib.request.urlopen(req, timeout=5) as response:
                        self.assertEqual(response.read(), b"fixture")
                finally:
                    httpd.shutdown()
                    httpd.server_close()

    def test_previous_get_cannot_satisfy_new_download(self):
        failures = []
        with patch.multiple(check, start=lambda port: None, download_node=lambda *a, **kw: ("1", "2"),
                            adb=lambda *args: "", wait_for=lambda test: test(), fail=failures.append,
                            requests=["GET " + check.DOWNLOAD_PATH]):
            check.check_download(".", 1234)
        self.assertEqual(failures, ["Android did not request the track from the download URL"])

    def run_download(self, remove):
        failures = []
        calls = [("2", "3"), ("4", "5"), ("2", "3"), remove]
        taps = []

        def adb(*args):
            if args[2] == "tap":
                taps.append(args[3:])
                check.requests.append("GET " + check.DOWNLOAD_PATH)
        with patch.multiple(check, start=lambda port: None, download_node=lambda *a, **kw: calls.pop(0),
                            adb=adb, wait_for=lambda test: test(), fail=failures.append, requests=[]):
            check.check_download(".", 1234)
        return failures, taps

    def test_new_request_without_completion_fails(self):
        failures, taps = self.run_download(None)
        self.assertEqual(failures, ["Android did not complete the track download"])
        self.assertEqual(taps, [("4", "5")])

    def test_completed_fixture_download_is_removed(self):
        failures, taps = self.run_download(("6", "7"))
        self.assertEqual(failures, [])
        self.assertEqual(taps, [("4", "5"), ("6", "7")])


if __name__ == "__main__":
    unittest.main()
