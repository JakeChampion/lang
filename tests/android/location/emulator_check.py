#!/usr/bin/env python3
"""Runs the location test APK on a running Android emulator.

Usage: emulator_check.py APK OUT_DIR

It turns the location of the emulator on, starts the app, and sets the
place of the emulator each second with `adb emu geo fix`. It requires the
lines of the app in logcat: the app has the permission and started, a fix
near the place comes, `last` knows it, and the app stops the fixes. It
fails on a crash. OUT_DIR gets logcat.
"""

import re
import subprocess
import sys
import time

PACKAGE = "dev.fern.locationtest"
LONGITUDE = "-122.084"
LATITUDE = "37.422"
LINES = (
    re.compile(r"locationtest: allowed true, started true"),
    re.compile(r"locationtest: fix near true"),
    re.compile(r"locationtest: last near true"),
    re.compile(r"locationtest: stopped"),
)
CRASH = re.compile(r"Fatal signal|UnsatisfiedLinkError|AbstractMethodError|java\.lang\.\w+Exception|fern: ")


def adb(*args, check=True):
    return subprocess.run(["adb", *args], check=check, capture_output=True, text=True).stdout


def main(apk, out):
    subprocess.run(["mkdir", "-p", out], check=True)
    adb("shell", "cmd", "location", "set-location-enabled", "true", check=False)
    adb("install", "-r", "-g", apk)
    adb("logcat", "-c")
    adb("shell", "am", "start", "-W", "-n", PACKAGE + "/android.app.NativeActivity")
    log = ""
    deadline = time.time() + 60
    while time.time() < deadline:
        adb("emu", "geo", "fix", LONGITUDE, LATITUDE, check=False)
        log = adb("logcat", "-d", "-v", "brief", "fern:I", "AndroidRuntime:E", "*:F")
        if all(p.search(log) for p in LINES):
            break
        time.sleep(1)
    with open(f"{out}/logcat.txt", "w") as f:
        f.write(log)
    print("\n".join(line for line in log.splitlines() if "locationtest" in line), flush=True)
    failures = [f"no line matching {p.pattern!r}" for p in LINES if not p.search(log)]
    failures += ["logcat: " + line for line in log.splitlines() if CRASH.search(line)]
    adb("shell", "am", "force-stop", PACKAGE)
    for f in failures:
        print("FAIL " + f, flush=True)
    if failures:
        sys.exit(1)
    print("ok", flush=True)


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    main(*sys.argv[1:])
