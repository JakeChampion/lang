#!/usr/bin/env python3
"""Runs tests/android/tz/zone.fern on a running Android device or emulator.

Usage: emulator_check.py ZONE_BIN OUT_DIR

It sets the zone of the device to ZONE, when adb can get root, and runs
the program in the adb shell. The program must print the value that
getprop gives for persist.sys.timezone, and the offset that date gives for
2026-01-01T00:00:00Z. OUT_DIR gets the output of the program.
"""

import subprocess
import sys
import time

ZONE = "Asia/Kolkata"
INSTANT = 1767225600


def adb(*args, check=True):
    return subprocess.run(["adb", *args], check=check, capture_output=True, text=True)


def offset_seconds(text):
    """The seconds of a date +%z offset, for example +0530."""
    sign = -1 if text.startswith("-") else 1
    digits = text.lstrip("+-")
    return sign * (int(digits[:2]) * 3600 + int(digits[2:4]) * 60)


def main(binary, out):
    subprocess.run(["mkdir", "-p", out], check=True)
    if adb("root", check=False).returncode == 0:
        adb("wait-for-device")
        time.sleep(1)
        adb("shell", "setprop", "persist.sys.timezone", ZONE, check=False)
    adb("push", binary, "/data/local/tmp/fern-zone")
    adb("shell", "chmod", "755", "/data/local/tmp/fern-zone")
    got = adb("shell", "/data/local/tmp/fern-zone").stdout
    with open(f"{out}/zone.txt", "w") as f:
        f.write(got)
    name = adb("shell", "getprop", "persist.sys.timezone").stdout.strip()
    offset = offset_seconds(adb("shell", "date", "-d", f"@{INSTANT}", "+%z").stdout.strip())
    want = f"property {name}\noffset {offset}\n"
    print(f"getprop: {name!r}, date: {offset}", flush=True)
    print(got, end="", flush=True)
    failures = []
    if not name:
        failures.append("the device has no persist.sys.timezone")
    if got.replace("\r\n", "\n") != want:
        failures.append(f"the program printed {got!r}, not {want!r}")
    for f in failures:
        print("FAIL " + f, flush=True)
    if failures:
        sys.exit(1)
    print("ok", flush=True)


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    main(*sys.argv[1:])
