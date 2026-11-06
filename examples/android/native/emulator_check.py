#!/usr/bin/env python3
"""Drives the native stopwatch APK on a running Android device or emulator.

Usage: emulator_check.py APK PACKAGE OUT_DIR [LIBS]

It installs the APK, which fern -apk wrote, and starts its NativeActivity.
LIBS is "mapped" for stored libraries, which Android maps from the APK, or
"extracted" for the libraries of -apk-compress, which Android extracts at
install. The check requires the library in the lib directory of the app
for "extracted" only.
It requires the window line of the app in logcat, the grey face of a paused
stopwatch, a tap that starts it and turns the face green, a second tap that
pauses it after two seconds or more, and the back key that closes it. It
fails on a crash in logcat. OUT_DIR gets the frames and logcat.
"""

import re
import struct
import subprocess
import sys
import time

CRASH = re.compile(r"Fatal signal|SIGSYS|UnsatisfiedLinkError|fern: |Unable to load native library")
GREY = (110, 110, 110)
GREEN = (80, 200, 120)
# The face of the stopwatch covers some thousands of pixels on any screen.
MIN_FACE_PIXELS = 2000

failures = []


def adb(*args, check=True):
    return subprocess.run(["adb", *args], check=check, capture_output=True, text=True).stdout


def fail(message):
    failures.append(message)
    print("FAIL " + message, flush=True)


def logcat():
    return adb("logcat", "-d", "-v", "brief", "fern:I", "AndroidRuntime:E", "DEBUG:*", "libc:*", "*:F")


def wait_for(pattern, timeout=30):
    deadline = time.time() + timeout
    while time.time() < deadline:
        m = re.search(pattern, logcat())
        if m:
            return m
        time.sleep(0.5)
    return None


def frame(out, name):
    """The colour counts of one raw screen frame: RGBA_8888 after a header."""
    raw = subprocess.run(["adb", "exec-out", "screencap"], check=True, capture_output=True).stdout
    with open(f"{out}/{name}.raw", "wb") as f:
        f.write(raw)
    width, height, _ = struct.unpack_from("<III", raw)
    head = len(raw) - width * height * 4
    counts = {GREY: 0, GREEN: 0}
    for at in range(head, len(raw), 16):
        px = (raw[at], raw[at + 1], raw[at + 2])
        if px in counts:
            counts[px] += 1
    print(f"{name}: {width}x{height}, grey {counts[GREY]}, green {counts[GREEN]} (one pixel in four)", flush=True)
    return width, height, counts


def check_libs(package, libs):
    """The library is in the lib directory of the app when Android extracted
    it, and not there when Android maps it from the APK."""
    if subprocess.run(["adb", "root"], capture_output=True).returncode == 0:
        adb("wait-for-device")
    base = adb("shell", "pm", "path", package).strip().removeprefix("package:")
    listing = subprocess.run(["adb", "shell", "ls", base.rsplit("/", 1)[0] + "/lib/x86_64"], capture_output=True, text=True)
    print(f"lib/x86_64: {listing.stdout.strip()!r} {listing.stderr.strip()!r}", flush=True)
    found = "libapp.so" in listing.stdout
    if libs == "extracted" and not found:
        fail("Android did not extract the deflated library")
    if libs == "mapped" and found:
        fail("Android extracted the stored library, which it can map from the APK")


def main(apk, package, out, libs="mapped"):
    subprocess.run(["mkdir", "-p", out], check=True)
    adb("install", "-r", apk)
    check_libs(package, libs)
    adb("logcat", "-c")
    adb("shell", "am", "start", "-W", "-n", package + "/android.app.NativeActivity")

    if not wait_for(r"stopwatch: window \d+x\d+"):
        fail("no window line from the app in logcat")
    time.sleep(1)
    width, height, counts = frame(out, "paused")
    if counts[GREY] * 4 < MIN_FACE_PIXELS:
        fail("the paused face is not grey on the screen")

    adb("shell", "input", "tap", str(width // 2), str(height // 2))
    if not wait_for(r"stopwatch: started at 0 ms"):
        fail("a tap did not start the stopwatch")
    time.sleep(2.5)
    _, _, counts = frame(out, "running")
    if counts[GREEN] * 4 < MIN_FACE_PIXELS:
        fail("the running face is not green on the screen")

    adb("shell", "input", "tap", str(width // 2), str(height // 2))
    m = wait_for(r"stopwatch: paused at (\d+) ms")
    if not m:
        fail("a second tap did not pause the stopwatch")
    elif int(m.group(1)) < 2000:
        fail(f"the stopwatch paused at {m.group(1)} ms, before 2 s")

    adb("shell", "input", "keyevent", "4")
    if not wait_for(r"stopwatch: destroyed at \d+ ms"):
        fail("the back key did not close the app")

    log = logcat()
    with open(f"{out}/logcat.txt", "w") as f:
        f.write(log)
    for line in log.splitlines():
        if CRASH.search(line):
            fail("logcat: " + line)

    if failures:
        print(f"{len(failures)} failures", flush=True)
        sys.exit(1)
    print("ok", flush=True)


if __name__ == "__main__":
    if len(sys.argv) not in (4, 5) or sys.argv[4:] not in ([], ["mapped"], ["extracted"]):
        sys.exit(__doc__)
    main(*sys.argv[1:])
