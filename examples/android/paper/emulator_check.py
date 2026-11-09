#!/usr/bin/env python3
"""Drives the Paper APK on a running Android device or emulator.

Usage: emulator_check.py APK OUT_DIR

It installs the APK, which fern -apk wrote, makes Paper the home app, and
presses the home key. The app draws its own pixels, so the check reads the
log lines of the app and the colours of the screen. It requires the home
screen in the paper colour above the wallpaper, a swipe up that opens the
app list in the paper colour, a search for "pap" that finds Paper and opens
it, the back key that goes back to the home screen, a frame section in
the system trace while the list slides, and no crash in logcat. OUT_DIR
gets the frames, logcat and the trace.
"""

from pathlib import Path
import re
import subprocess
import sys
import time

sys.path.insert(0, str(Path(__file__).resolve().parents[3] / "tests" / "android"))
from emulator import adb, count, crashes, fail, finish, logcat, screen, wait_for  # noqa: E402

PACKAGE = "dev.fern.paper"
ACTIVITY = PACKAGE + "/.Home"
PAPER = (255, 244, 230)



def paper_part(out, name):
    """The share of the screen in the paper colour, from one pixel of each
    four."""
    time.sleep(1.0)
    width, height, pixels = screen(out, name)
    part = count(pixels, PAPER, 4) * 4 / (width * height)
    print(f"{name}: {width}x{height}, paper {part:.3f}", flush=True)
    return width, height, part


def main(apk, out):
    subprocess.run(["mkdir", "-p", out], check=True)
    adb("install", "-r", "-g", apk)
    adb("logcat", "-c")
    adb("shell", "cmd", "package", "set-home-activity", ACTIVITY, check=False)
    adb("shell", "input", "keyevent", "KEYCODE_HOME")
    if not wait_for(r"paper: drawn \d+x\d+"):
        adb("shell", "am", "start", "-W", "-n", ACTIVITY)
        if not wait_for(r"paper: drawn \d+x\d+"):
            fail("the home screen did not draw")
    if not wait_for(r"paper: apps \d+, Paper listed true"):
        fail("the apps did not load with Paper among them")
    width, height, part = paper_part(out, "home")
    if part < 0.2 or part > 0.7:
        fail(f"the home screen has {part:.3f} of the paper colour, not paper above the wallpaper")

    adb("shell", "atrace", "--async_start", "-c", "-b", "4096", "-a", PACKAGE, check=False)
    adb("shell", "input", "swipe", str(width // 2), str(height * 3 // 4), str(width // 2), str(height // 4), "150")
    if not wait_for(r"paper: layer 1"):
        fail("a swipe up did not open the app list")
    trace = adb("shell", "atrace", "--async_stop", check=False)
    with open(f"{out}/trace.txt", "w") as f:
        f.write(trace)
    if not re.search(r"B\|\d+\|frame", trace):
        fail("the system trace has no frame section of the app")
    _, _, part = paper_part(out, "drawer")
    if part < 0.6:
        fail(f"the app list has {part:.3f} of the paper colour")

    adb("shell", "input", "text", "pap")
    if not wait_for(r"paper: launch dev\.fern\.paper/dev\.fern\.paper\.Home"):
        fail("a search for pap did not open Paper")
    adb("shell", "input", "keyevent", "KEYCODE_HOME")
    time.sleep(1.5)

    adb("shell", "input", "swipe", str(width // 2), str(height * 3 // 4), str(width // 2), str(height // 4), "150")
    time.sleep(1.0)
    early = logcat()
    adb("logcat", "-c")
    adb("shell", "input", "keyevent", "KEYCODE_BACK")
    if not wait_for(r"paper: layer 0"):
        fail("the back key did not go back to the home screen")
    paper_part(out, "back")

    log = early + logcat()
    with open(f"{out}/logcat.txt", "w") as f:
        f.write(log)
    for line in crashes(log):
        fail("logcat: " + line)
    adb("shell", "cmd", "package", "set-home-activity", "com.android.launcher3/.uioverrides.QuickstepLauncher", check=False)
    finish()


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    main(*sys.argv[1:])
