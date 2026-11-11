#!/usr/bin/env python3
"""Drives the music player APK on a running Android device or emulator.

Usage: emulator_check.py APK OUT_DIR

It installs the APK, which fern -apk wrote, and starts its NativeActivity.
The player makes its three tunes at the first start, and reads their
files into its library. The check taps a track in the list, lets it play
for some seconds, and presses the back key. Then it requires the line of
the app in logcat: the library has the three tunes or more, a track plays,
and its time went on. So the decoder
gave its samples to the sound of the device in time. It fails on a crash
in logcat. OUT_DIR gets the frames and logcat.
"""

from pathlib import Path
import subprocess
import sys
import time

sys.path.insert(0, str(Path(__file__).resolve().parents[3] / "tests" / "android"))
from emulator import adb, crashes, fail, failures, finish, logcat, screen, wait_for  # noqa: E402

PACKAGE = "dev.fern.tune"


def main(apk, out):
    subprocess.run(["mkdir", "-p", out], check=True)
    adb("install", "-r", apk)
    adb("logcat", "-c")
    adb("shell", "am", "start", "-W", "-n", PACKAGE + "/android.app.NativeActivity")

    # The app makes its tunes and reads them, and Android can ask for the
    # permission to read the music of the phone.
    time.sleep(8)
    adb("shell", "input", "keyevent", "111")
    time.sleep(2)
    width, height, _ = screen(out, "library")
    # The first rows of the list are below the title, the search field and
    # the tabs.
    adb("shell", "input", "tap", str(width // 2), str(height * 45 // 100))
    time.sleep(4)
    screen(out, "playing")

    adb("shell", "input", "keyevent", "4")
    m = wait_for(r"tune: closed with (\d+) tracks at track (-?\d+), (\d+):(\d\d) of (\d+):(\d\d), playing (\w+)", timeout=10)
    if not m:
        fail("the back key did not close the app")
    else:
        track, played = int(m.group(2)), int(m.group(3)) * 60 + int(m.group(4))
        print(f"closed: {m.group(0)}", flush=True)
        if int(m.group(1)) < 3:
            fail(f"the library does not have its 3 tunes: {m.group(0)!r}")
        elif track < 0 or m.group(7) != "true":
            fail(f"no track plays: {m.group(0)!r}")
        elif played < 1:
            fail(f"the time of the track did not go on: {m.group(0)!r}")

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
