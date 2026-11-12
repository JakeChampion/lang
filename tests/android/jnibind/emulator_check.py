#!/usr/bin/env python3
"""Runs the typed JNI bindings test APK on a running Android device or
emulator.

Usage: emulator_check.py APK OUT_DIR

It starts the app and requires its lines in logcat: one line for each part
of tests/android/jnibind/app.fern, with the values that the Java methods
give. It fails on a crash. OUT_DIR gets logcat.
"""

import re
import subprocess
import sys
import time

PACKAGE = "dev.fern.jnibindtest"
LINES = (
    re.compile(r"jnibind: activity android\.app\.NativeActivity finishing false package dev\.fern\.jnibindtest clock true"),
    re.compile(r"jnibind: intent android\.intent\.action\.MAIN flags 268435456 launchers true first true"),
    re.compile(r"jnibind: component dev\.fern\.a/\.Main point 13 16"),
    re.compile(r"jnibind: uptime true int -12345 max 2147483647 sqrt 1\.5 float 0\.5 out true"),
    re.compile(r"jnibind: thrown java: java\.lang\.NumberFormatException: .*fern.* then 7"),
    re.compile(r"jnibind: null x empty /x has true lost true then true"),
    re.compile(r"jnibind: done 6"),
)
CRASH = re.compile(r"Fatal signal|UnsatisfiedLinkError|AndroidRuntime|fern: ")


def adb(*args):
    return subprocess.run(["adb", *args], check=True, capture_output=True, text=True).stdout


def main(apk, out):
    subprocess.run(["mkdir", "-p", out], check=True)
    adb("install", "-r", "-g", apk)
    adb("logcat", "-c")
    adb("shell", "am", "start", "-W", "-n", PACKAGE + "/android.app.NativeActivity")
    log = ""
    deadline = time.time() + 40
    while time.time() < deadline:
        log = adb("logcat", "-d", "-v", "brief", "fern:I", "AndroidRuntime:E", "*:F")
        if all(p.search(log) for p in LINES) or "jnibind: failed:" in log:
            break
        time.sleep(0.5)
    with open(f"{out}/logcat.txt", "w") as f:
        f.write(log)
    print("\n".join(line for line in log.splitlines() if "jnibind" in line), flush=True)
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
