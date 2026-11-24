#!/usr/bin/env python3
"""Drives the Press APK on a running Android device or emulator.

Usage: emulator_check.py APK OUT_DIR

It installs the APK, which fern -apk wrote, and starts its activity. It
requires the "Open an image" button in the primary color of the theme,
which the GPU painter of std/android/ui draws. A tap on the button must
start the document picker of Android. The back key closes the picker, and
the @jni onActivityResult of the app must give the loop the empty result:
the app logs that no image was chosen. A second back key closes the app.
It fails on a crash in logcat. OUT_DIR gets the frames and logcat.
"""

from pathlib import Path
import re
import subprocess
import sys
import time

sys.path.insert(0, str(Path(__file__).resolve().parents[3] / "tests" / "android"))
from emulator import adb, crashes, fail, failures, finish, logcat, screen, wait_for  # noqa: E402

PACKAGE = "dev.fern.press"
# The primary color of the light theme and of the dark theme.
PRIMARY = ((103, 80, 164), (208, 188, 255))
# The button covers some thousands of pixels on any screen.
MIN_BUTTON_PIXELS = 2000
# The packages of the document picker of AOSP and of Google.
PICKERS = ("com.android.documentsui", "com.google.android.documentsui")


def button(pixels, width, height):
    """The middle of the pixels in the primary color, and how many there
    are: the filled button. It reads one pixel in each four."""
    n = sx = sy = 0
    for y in range(0, height, 2):
        row = y * width * 4
        for x in range(0, width, 2):
            at = row + x * 4
            if (pixels[at], pixels[at + 1], pixels[at + 2]) in PRIMARY:
                n += 1
                sx += x
                sy += y
    if n == 0:
        return 0, 0, 0
    return sx // n, sy // n, n * 4


def resumed():
    """The package of the activity that has the focus."""
    out = adb("shell", "dumpsys", "activity", "activities", check=False)
    m = re.search(r"(?:topResumedActivity|mResumedActivity)[^\n]*? ([\w.]+)/", out)
    return m.group(1) if m else ""


def main(apk, out):
    subprocess.run(["mkdir", "-p", out], check=True)
    adb("install", "-r", apk)
    adb("logcat", "-c")
    adb("shell", "monkey", "-p", PACKAGE, "-c", "android.intent.category.LAUNCHER", "1")

    x = y = n = 0
    width = height = 0
    deadline = time.time() + 30
    while time.time() < deadline and n < MIN_BUTTON_PIXELS:
        time.sleep(1)
        width, height, first = screen(out, "welcome")
        x, y, n = button(first, width, height)
    print(f"welcome: {width}x{height}, button at {x},{y} with {n} pixels", flush=True)
    if n < MIN_BUTTON_PIXELS:
        fail("the button is not on the screen in the primary color")
    else:
        adb("shell", "input", "tap", str(x), str(y))
        top = ""
        deadline = time.time() + 20
        while time.time() < deadline and top not in PICKERS:
            time.sleep(1)
            top = resumed()
        screen(out, "picker")
        if top not in PICKERS:
            fail(f"the button did not start the document picker: {top or 'no activity'} has the focus")
        else:
            adb("shell", "input", "keyevent", "4")
            if not wait_for(r"press: no image chosen"):
                fail("the app did not get the empty result of the picker")

    adb("shell", "input", "keyevent", "4")
    if not wait_for(r"press: closed"):
        fail("the back key did not close the app")

    log = logcat()
    with open(f"{out}/logcat.txt", "w") as f:
        f.write(log)
    for line in crashes(log):
        fail("logcat: " + line)

    if failures:
        print(f"{len(failures)} failures", flush=True)
    finish(out)


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    main(*sys.argv[1:])
