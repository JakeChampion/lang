#!/usr/bin/env python3
"""Runs the audio test APK on a running Android device or emulator.

Usage: emulator_check.py APK OUT_DIR

It grants RECORD_AUDIO, starts the app, and requires its six lines in
logcat: the permission seen through JNI from the loop thread, a capture
stream that read samples, a playback stream that wrote them, a stereo
stream of music that took whole frames before and after a flush, and a
timestamp diagnostic for each playback stream. It fails on a crash. OUT_DIR gets logcat.
"""

import re
import subprocess
import sys
import time

PACKAGE = "dev.fern.audiotest"
TIMESTAMP = re.compile(r"audiotest: timestamp (?:unavailable|frames \d+ monotonic_ns \d+)$", re.MULTILINE)
MUSIC_TIMESTAMP = re.compile(r"audiotest: music timestamp (?:unavailable|frames \d+ monotonic_ns \d+)$", re.MULTILINE)
LINES = (
    re.compile(r"audiotest: sdk (\d+), record permission true"),
    re.compile(r"audiotest: capture rate ([1-9]\d*), read ([1-9]\d*) samples"),
    re.compile(r"audiotest: playback rate ([1-9]\d*), wrote ([1-9]\d*) samples"),
    re.compile(r"audiotest: music rate ([1-9]\d*), channels 2, wrote ([1-9]\d*) samples, 0 odd, 4 after the flush"),
    TIMESTAMP,
    MUSIC_TIMESTAMP,
)
CRASH = re.compile(r"Fatal signal|UnsatisfiedLinkError|java\.lang\.\w+Exception|fern: ")


def adb(*args):
    return subprocess.run(["adb", *args], check=True, capture_output=True, text=True).stdout


def main(apk, out):
    subprocess.run(["mkdir", "-p", out], check=True)
    adb("install", "-r", "-g", apk)
    adb("shell", "pm", "grant", PACKAGE, "android.permission.RECORD_AUDIO")
    adb("logcat", "-c")
    adb("shell", "am", "start", "-W", "-n", PACKAGE + "/android.app.NativeActivity")
    log = ""
    deadline = time.time() + 40
    while time.time() < deadline:
        log = adb("logcat", "-d", "-v", "brief", "fern:I", "AndroidRuntime:E", "*:F")
        if all(p.search(log) for p in LINES):
            break
        time.sleep(0.5)
    with open(f"{out}/logcat.txt", "w") as f:
        f.write(log)
    print("\n".join(line for line in log.splitlines() if "audiotest" in line), flush=True)
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
