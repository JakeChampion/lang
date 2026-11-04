#!/usr/bin/env python3
"""Runs the DEX test APK on a running Android device or emulator.

Usage: emulator_check.py APK OUT_DIR

The app loads its own Ping class, constructs one and calls its native
method, which returns 7 * 6. Then it reads a Java string back with
jni.string_of, and calls a static method that returns an int and one that
returns a string. The check requires the five lines of the app in logcat,
and no crash. OUT_DIR gets logcat.
"""

import re
import subprocess
import sys
import time

PACKAGE = "dev.fern.dextest"
WANT = ("dextest: activity class bound", "dextest: ping 42", "dextest: string Fern ü", "dextest: parse 42", "dextest: value 7")
CRASH = re.compile(r"Fatal signal|UnsatisfiedLinkError|ClassNotFoundException|VerifyError|AbstractMethodError|fern: ")


def adb(*args):
    return subprocess.run(["adb", *args], check=True, capture_output=True, text=True).stdout


def main(apk, out):
    subprocess.run(["mkdir", "-p", out], check=True)
    adb("install", "-r", apk)
    adb("logcat", "-c")
    adb("shell", "am", "start", "-W", "-n", PACKAGE + "/.Main")
    log = ""
    deadline = time.time() + 30
    while time.time() < deadline:
        log = adb("logcat", "-d", "-v", "brief", "fern:I", "AndroidRuntime:E", "art:W", "*:F")
        if all(w in log for w in WANT):
            break
        time.sleep(0.5)
    with open(f"{out}/logcat.txt", "w") as f:
        f.write(log)
    failures = [f"no line {w!r} in logcat" for w in WANT if w not in log]
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
