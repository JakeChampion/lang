#!/usr/bin/env python3
"""Drives the counter APK on a running Android device or emulator.

Usage: emulator_check.py APK OUT_DIR

It installs the APK, which fern -apk wrote, and starts its NativeActivity.
It requires the button in the primary color of the theme on the screen,
which the GPU painter of std/android/ui draws. It taps the button three
times and requires that the screen changes. Then it presses the back key,
and requires the line of the app in logcat with a count of 3. It fails on a
crash in logcat. OUT_DIR gets the frames and logcat.
"""

from pathlib import Path
import subprocess
import sys
import time

sys.path.insert(0, str(Path(__file__).resolve().parents[3] / "tests" / "android"))
from emulator import adb, crashes, fail, failures, finish, logcat, screen, wait_for  # noqa: E402

PACKAGE = "dev.fern.counter"
# The primary color of the light theme and of the dark theme.
PRIMARY = ((103, 80, 164), (208, 188, 255))
# The "Add one" button covers some thousands of pixels on any screen.
MIN_BUTTON_PIXELS = 2000


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


def main(apk, out):
    subprocess.run(["mkdir", "-p", out], check=True)
    adb("install", "-r", apk)
    adb("logcat", "-c")
    adb("shell", "am", "start", "-W", "-n", PACKAGE + "/android.app.NativeActivity")

    x = y = n = 0
    deadline = time.time() + 30
    while time.time() < deadline and n < MIN_BUTTON_PIXELS:
        time.sleep(1)
        width, height, first = screen(out, "zero")
        x, y, n = button(first, width, height)
    print(f"zero: {width}x{height}, button at {x},{y} with {n} pixels", flush=True)
    if n < MIN_BUTTON_PIXELS:
        fail("the button is not on the screen in the primary color")
    else:
        for _ in range(3):
            adb("shell", "input", "tap", str(x), str(y))
            time.sleep(0.5)
        time.sleep(1)
        _, _, after = screen(out, "three")
        if after == first:
            fail("three taps did not change the screen")

    adb("shell", "input", "keyevent", "4")
    m = wait_for(r"counter: closed at (\d+)")
    if not m:
        fail("the back key did not close the app")
    elif m.group(1) != "3":
        fail(f"the counter closed at {m.group(1)}, not at 3")

    log = logcat()
    with open(f"{out}/logcat.txt", "w") as f:
        f.write(log)
    for line in crashes(log):
        fail("logcat: " + line)

    if failures:
        print(f"{len(failures)} failures", flush=True)
    finish()


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    main(*sys.argv[1:])
