#!/usr/bin/env python3
"""Drives the to-do APK on a running Android device or emulator.

Usage: emulator_check.py APK OUT_DIR

It installs the APK, which fern -apk wrote, and starts its NativeActivity.
It finds the Add button by the primary color of the theme, and taps the
text field at its left. It types a word with key events and presses the
enter key, then types a second word and taps the Add button. Then it
presses the back key, and requires the line of the app in logcat with the
two items. It fails on a crash in logcat. OUT_DIR gets the frames and
logcat.
"""

from pathlib import Path
import subprocess
import sys
import time

sys.path.insert(0, str(Path(__file__).resolve().parents[3] / "tests" / "android"))
from emulator import adb, crashes, fail, failures, finish, logcat, screen, wait_for  # noqa: E402

PACKAGE = "dev.fern.todo"
# The primary color of the light theme and of the dark theme.
PRIMARY = ((103, 80, 164), (208, 188, 255))
# The Add button covers some hundreds of pixels on any screen.
MIN_BUTTON_PIXELS = 400


def button(pixels, width, height):
    """The middle of the pixels in the primary color, and how many there
    are: the Add button. It reads one pixel in each four."""
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
        width, height, pixels = screen(out, "empty")
        x, y, n = button(pixels, width, height)
    print(f"empty: {width}x{height}, button at {x},{y} with {n} pixels", flush=True)
    if n < MIN_BUTTON_PIXELS:
        fail("the Add button is not on the screen in the primary color")
    else:
        # The field is at the left of the button, on the same row.
        adb("shell", "input", "tap", str(width // 4), str(y))
        time.sleep(2)
        adb("shell", "input", "text", "Milk")
        time.sleep(1)
        adb("shell", "input", "keyevent", "66")
        time.sleep(1)
        adb("shell", "input", "tap", str(width // 4), str(y))
        time.sleep(2)
        adb("shell", "input", "text", "Eggs")
        time.sleep(1)
        screen(out, "typed")
        adb("shell", "input", "tap", str(x), str(y))
        time.sleep(1)
        screen(out, "two")

    # The first back key takes the keyboard away when it shows.
    m = None
    for _ in range(3):
        adb("shell", "input", "keyevent", "4")
        m = wait_for(r"todo: closed with (\d+) items, (\d+) left: (.*)", timeout=5)
        if m:
            break
    if not m:
        fail("the back key did not close the app")
    elif (m.group(1), m.group(2), m.group(3).strip()) != ("2", "2", "Milk | Eggs"):
        fail(f"the app closed with {m.group(0)!r}, not with Milk and Eggs")

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
