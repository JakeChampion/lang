#!/usr/bin/env python3
"""Runs the Keystore device test APK two times on a running Android device
or emulator.

Usage: emulator_check.py APK OUT_DIR

The first run must make a key and an envelope for the plain alias and for
the copy in the data directory. The second run must open both envelopes.
An emulator has no StrongBox and no screen lock, so the check requires only
that the app reports the other aliases. README.md is the procedure for a
phone. OUT_DIR gets logcat of each run.
"""

import re
import subprocess
import sys
import time

PACKAGE = "dev.fern.keystoretest"
FIRST = (
    re.compile(r"keystore-device: api \d+"),
    re.compile(r"keystore-device: restored key false open none seal ok"),
    re.compile(r"keystore-device: plain key true open none seal ok"),
    re.compile(r"keystore-device: strongbox key false open none "),
    re.compile(r"keystore-device: auth key false open none "),
    re.compile(r"keystore-device: done"),
)
SECOND = (
    re.compile(r"keystore-device: restored key true open ok"),
    re.compile(r"keystore-device: plain key true open ok"),
    re.compile(r"keystore-device: done"),
)
CRASH = re.compile(r"Fatal signal|UnsatisfiedLinkError|AndroidRuntime|fern: ")


def adb(*args):
    return subprocess.run(["adb", *args], check=True, capture_output=True, text=True).stdout


def run(lines, out, name):
    adb("logcat", "-c")
    adb("shell", "am", "start", "-W", "-n", PACKAGE + "/android.app.NativeActivity")
    log = ""
    deadline = time.time() + 40
    while time.time() < deadline:
        log = adb("logcat", "-d", "-v", "brief", "fern:I", "AndroidRuntime:E", "*:F")
        if "keystore-device: done" in log:
            break
        time.sleep(0.5)
    with open(f"{out}/{name}.txt", "w") as f:
        f.write(log)
    print("\n".join(line for line in log.splitlines() if "keystore-device" in line), flush=True)
    failures = [f"{name}: no line matching {p.pattern!r}" for p in lines if not p.search(log)]
    failures += [f"{name}: logcat: " + line for line in log.splitlines() if CRASH.search(line)]
    adb("shell", "am", "force-stop", PACKAGE)
    return failures


def main(apk, out):
    subprocess.run(["mkdir", "-p", out], check=True)
    subprocess.run(["adb", "uninstall", PACKAGE], capture_output=True)
    adb("install", "-r", "-g", apk)
    failures = run(FIRST, out, "first")
    failures += run(SECOND, out, "second")
    for f in failures:
        print("FAIL " + f, flush=True)
    if failures:
        sys.exit(1)
    print("ok", flush=True)


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    main(sys.argv[1], sys.argv[2])
