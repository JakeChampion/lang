#!/usr/bin/env python3
"""Runs the i18n test APK on a running Android emulator.

Usage: emulator_check.py APK OUT_DIR

It starts the app, and requires its lines in logcat: android.languages
gives the language of the emulator, en-US, and std/i18n chooses the
English texts for it, and the layout is left to right. Each glyph of a
Hebrew, an Arabic and a Chinese word comes from a fallback font of the
system and has pixels. The fatha of an Arabic letter is above the middle
of the letter. The system has a font for an emoji, and the app logs the
tags of its tables. The emoji draws in color, with the yellow of its face.
It fails on a crash. OUT_DIR gets logcat.
"""

import re
import subprocess
import sys
import time

PACKAGE = "dev.fern.i18ntest"
LINES = (
    re.compile(r"i18ntest: languages en-US"),
    re.compile(r"i18ntest: chose en Hello"),
    re.compile(r"i18ntest: rtl false"),
    re.compile(r"i18ntest: fallback 4/4"),
    re.compile(r"i18ntest: fallback 5/5"),
    re.compile(r"i18ntest: fallback 2/2"),
    re.compile(r"i18ntest: mark true true"),
    re.compile(r"i18ntest: font of U\+1F600 /\S+ \w"),
    re.compile(r"i18ntest: emoji yellow true"),
)
CRASH = re.compile(r"Fatal signal|UnsatisfiedLinkError|java\.lang\.\w+Exception|fern: ")


def adb(*args):
    return subprocess.run(["adb", *args], check=True, capture_output=True, text=True).stdout


def main(apk, out):
    subprocess.run(["mkdir", "-p", out], check=True)
    adb("install", "-r", "-g", apk)
    adb("logcat", "-c")
    adb("shell", "am", "start", "-W", "-n", PACKAGE + "/android.app.NativeActivity")
    log = ""
    deadline = time.time() + 60
    while time.time() < deadline:
        log = adb("logcat", "-d", "-v", "brief", "fern:I", "AndroidRuntime:E", "*:F")
        if all(p.search(log) for p in LINES):
            break
        time.sleep(0.5)
    with open(f"{out}/logcat.txt", "w") as f:
        f.write(log)
    print("\n".join(line for line in log.splitlines() if "i18ntest" in line), flush=True)
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
