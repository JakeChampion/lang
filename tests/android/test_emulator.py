"""Tests of tests/android/emulator.py, with a fake adb on PATH."""
import os
import struct
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import emulator  # noqa: E402

FAKE_ADB = """#!/bin/sh
case "$*" in
  "logcat -d"*) cat "$FAKE_LOGCAT" ;;
  "exec-out screencap") cat "$FAKE_SCREEN" ;;
  "root") cat "$FAKE_ROOT" ;;
  # The old adbd answers the first calls, then adbd is down for one call.
  "shell id -u")
    echo x >> "$FAKE_CALLS"
    case "$(wc -l < "$FAKE_CALLS" | tr -d ' ')" in
      1|2) echo 2000 ;;
      3) exit 1 ;;
      *) echo 0 ;;
    esac ;;
esac
"""


class EmulatorTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        d = Path(self.dir.name)
        (d / "adb").write_text(FAKE_ADB)
        (d / "adb").chmod(0o755)
        self.logcat = d / "logcat.txt"
        self.screen = d / "screen.raw"
        self.env = dict(os.environ)
        os.environ["PATH"] = str(d) + os.pathsep + os.environ["PATH"]
        os.environ["FAKE_LOGCAT"] = str(self.logcat)
        os.environ["FAKE_SCREEN"] = str(self.screen)
        self.root = d / "root.txt"
        self.calls = d / "calls.txt"
        os.environ["FAKE_ROOT"] = str(self.root)
        os.environ["FAKE_CALLS"] = str(self.calls)
        emulator.failures.clear()

    def tearDown(self):
        os.environ.clear()
        os.environ.update(self.env)
        self.dir.cleanup()

    def test_wait_for(self):
        self.logcat.write_text("I/fern ( 12): paper: drawn 1080x2400\n")
        m = emulator.wait_for(r"paper: drawn (\d+)x(\d+)", timeout=1)
        self.assertEqual(m.groups(), ("1080", "2400"))
        self.assertIsNone(emulator.wait_for(r"paper: layer 1", timeout=0.6))

    def test_root_waits_for_the_new_adbd(self):
        self.root.write_text("restarting adbd as root\n")
        self.assertTrue(emulator.root(timeout=5))
        self.assertEqual(len(self.calls.read_text().splitlines()), 4)

    def test_root_refused(self):
        self.root.write_text("adbd cannot run as root in production builds\n")
        self.assertFalse(emulator.root(timeout=5))
        self.assertFalse(self.calls.exists())

    def test_crashes(self):
        log = "I/fern ( 1): ok\nF/libc ( 1): Fatal signal 11 (SIGSEGV)\nE/AndroidRuntime ( 1): FATAL EXCEPTION: main\n"
        self.assertEqual(len(emulator.crashes(log)), 2)

    def test_screen_and_count(self):
        # A frame of 2 by 2 pixels after a header of 16 bytes: three paper
        # pixels and one black pixel.
        paper = bytes([255, 244, 230, 255])
        self.screen.write_bytes(struct.pack("<IIII", 2, 2, 1, 0) + paper * 3 + bytes([0, 0, 0, 255]))
        width, height, pixels = emulator.screen(self.dir.name, "s")
        self.assertEqual((width, height), (2, 2))
        self.assertEqual(emulator.count(pixels, (255, 244, 230)), 3)
        self.assertEqual(emulator.count(pixels, (255, 244, 230), step=2), 2)

    def test_fail_and_finish(self):
        emulator.finish()
        emulator.fail("the home screen did not draw")
        with self.assertRaises(SystemExit):
            emulator.finish()


if __name__ == "__main__":
    unittest.main()
