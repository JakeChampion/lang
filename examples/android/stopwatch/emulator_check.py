#!/usr/bin/env python3
"""Drives the stopwatch APK on a running Android device or emulator.

Usage: emulator_check.py APK OUT_DIR [REPORT]

It installs the APK, which fern -apk wrote, and starts its NativeActivity.
The app draws its own pixels, so the check reads the log lines of the app
and the colours of the screen. It requires the dial on the screen, a tap
that starts the stopwatch and shows the red dot of the ring, a long tap
that resets it after two seconds or more, the menu, a settings card that
changes a setting, and the back key that goes back and then closes the
app. It fails on a crash in logcat. OUT_DIR gets the frames and logcat.

REPORT is tests/android/report.fern built for the device. The check runs it
and requires its std/log record at priority W and its abort at priority F
in logcat, with the tag "fern".
"""

from pathlib import Path
import re
import subprocess
import sys
import time

sys.path.insert(0, str(Path(__file__).resolve().parents[3] / "tests" / "android"))
from emulator import adb, count, crashes, fail, failures, logcat, screen, wait_for  # noqa: E402

PACKAGE = "dev.fern.stopwatch"
# The dark disc of the dial, and the red dot of a running stopwatch at the
# default opacity, 13 of 20, over black.
SURFACE = (26, 26, 26)
RED_DOT = (215 * 13 // 20, 25 * 13 // 20, 33 * 13 // 20)
# The disc of the dial covers more than this part of the screen.
MIN_DIAL_PART = 0.05
REPORT_LINES = (
    re.compile(r" W fern\s*: \[WARN\] about to index past the end$"),
    re.compile(r" F fern\s*: fern: array index out of range$"),
)



def frame(out, name):
    """The share of the screen in the surface colour, and the count of the
    pixels of the red dot, in one screen frame."""
    width, height, pixels = screen(out, name)
    part = count(pixels, SURFACE) / (width * height)
    red = count(pixels, RED_DOT)
    print(f"{name}: {width}x{height}, surface {part:.3f}, red dot {red} pixels", flush=True)
    return part, red


def dp(width, height, n):
    """n dp as screen.fern counts it: 1/400 of the short side."""
    return n * min(width, height) // 400


def tap(x, y):
    adb("shell", "input", "tap", str(x), str(y))


def check_report(report, out):
    adb("logcat", "-c")
    adb("push", report, "/data/local/tmp/fern-report")
    adb("shell", "chmod", "755", "/data/local/tmp/fern-report")
    status = subprocess.run(["adb", "shell", "/data/local/tmp/fern-report"], capture_output=True, text=True).returncode
    if status != 134:
        fail(f"the report program exited {status}, not 134")
    time.sleep(1)
    log = adb("logcat", "-d", "-s", "fern")
    with open(f"{out}/logcat-report.txt", "w") as f:
        f.write(log)
    lines = log.splitlines()
    for want in REPORT_LINES:
        if not any(want.search(line) for line in lines):
            fail(f"logcat has no line that matches {want.pattern!r}")


def settings(out, width, height):
    """Opens the settings from the menu, and turns the clock off and on with
    a tap on its card."""
    tap(width // 2, dp(width, height, 50))
    time.sleep(1)
    tap(width - dp(width, height, 24 + 30), dp(width, height, 28 + 22))
    if not wait_for(r"stopwatch: menu true"):
        fail("a tap on MENU did not open the menu")
        return
    tap(width - dp(width, height, 24 + 100), dp(width, height, 28 + 44 + 12 + 12 + 22))
    if not wait_for(r"stopwatch: page settings"):
        fail("a tap on SETTINGS did not open the settings")
        return
    time.sleep(1)
    frame(out, "3-settings")
    section = dp(width, height, 24) + 7 * max(1, dp(width, height, 22) // 10) + dp(width, height, 10)
    card = dp(width, height, 28 + 44 + 12) + section + dp(width, height, 20)
    tap(width // 2, card)
    if not wait_for(r"stopwatch: setting clock_enabled 0"):
        fail("a tap on the first card did not turn the clock off")
    tap(width // 2, card)
    if not wait_for(r"stopwatch: setting clock_enabled 1"):
        fail("a second tap on the first card did not turn the clock on")
    adb("shell", "input", "keyevent", "4")
    if not wait_for(r"stopwatch: page main"):
        fail("the back key did not go back to the stopwatch")


def main(apk, out, report=None):
    subprocess.run(["mkdir", "-p", out], check=True)
    adb("install", "-r", apk)
    adb("shell", "pm", "clear", PACKAGE)
    adb("logcat", "-c")
    adb("shell", "am", "start", "-W", "-n", PACKAGE + "/android.app.NativeActivity")

    m = wait_for(r"stopwatch: window (\d+)x(\d+)")
    if not m:
        fail("no window line from the app in logcat")
        width, height = map(int, re.findall(r"\d+", adb("shell", "wm", "size"))[-2:])
    else:
        width, height = int(m.group(1)), int(m.group(2))
    time.sleep(2)
    part, _ = frame(out, "1-start")
    if part < MIN_DIAL_PART:
        fail(f"the dial covers {part:.3f} of the screen, not {MIN_DIAL_PART} or more")

    # The dial is below the clock, and it covers the centre of the screen.
    tap(width // 2, height // 2)
    if not wait_for(r"stopwatch: started at 0 ms"):
        fail("a tap did not start the stopwatch")
    time.sleep(2.5)
    _, red = frame(out, "2-running")
    if red == 0:
        fail("the ring of the running stopwatch has no red dot")
    adb("shell", "input", "swipe", str(width // 2), str(height // 2), str(width // 2), str(height // 2), "1500")
    m = wait_for(r"stopwatch: reset at (\d+) ms")
    if not m:
        fail("a long tap did not reset the stopwatch")
    elif int(m.group(1)) < 2000:
        fail(f"the stopwatch reset at {m.group(1)} ms, before 2 s")
    if re.search(r"stopwatch: paused at", logcat()):
        fail("the long tap also paused the stopwatch")

    settings(out, width, height)

    adb("shell", "input", "keyevent", "4")
    if not wait_for(r"stopwatch: destroyed"):
        fail("the back key on the stopwatch did not close the app")

    log = logcat()
    with open(f"{out}/logcat.txt", "w") as f:
        f.write(log)
    for line in crashes(log):
        fail("logcat: " + line)
    if report is not None:
        check_report(report, out)

    if failures:
        print(f"{len(failures)} check(s) failed", flush=True)
        return 1
    print("ok", flush=True)
    return 0


if __name__ == "__main__":
    if len(sys.argv) not in (3, 4):
        sys.exit(__doc__)
    sys.exit(main(*sys.argv[1:4]))
