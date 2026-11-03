#!/usr/bin/env python3
"""Runs the walkie-talkie APK on a running Android device or emulator.

Usage: emulator_check.py APK OUT_DIR

It grants RECORD_AUDIO, starts the app, and requires its start line in
logcat: the node ID, an open socket and an open speaker stream. Then the app
must still run five seconds later, with no crash in logcat. OUT_DIR gets
logcat and a screenshot.
"""

import re
import subprocess
import sys
import time

PACKAGE = "dev.fern.walkie"
START = re.compile(r"walkie: node \d+, port 47120, socket (\d+), speaker rate ([1-9]\d*)")
CRASH = re.compile(r"Fatal signal|UnsatisfiedLinkError|java\.lang\.\w+Exception|fern: ")


def adb(*args):
    return subprocess.run(["adb", *args], check=True, capture_output=True, text=True).stdout


def alive():
    out = subprocess.run(["adb", "shell", "pidof", PACKAGE], capture_output=True, text=True).stdout
    return out.strip() != ""


def main(apk, out):
    subprocess.run(["mkdir", "-p", out], check=True)
    adb("install", "-r", "-g", apk)
    adb("shell", "pm", "grant", PACKAGE, "android.permission.RECORD_AUDIO")
    adb("logcat", "-c")
    adb("shell", "am", "start", "-W", "-n", PACKAGE + "/android.app.NativeActivity")
    log = ""
    deadline = time.time() + 30
    while time.time() < deadline:
        log = adb("logcat", "-d", "-v", "brief", "fern:I", "AndroidRuntime:E", "DEBUG:F", "*:F")
        if START.search(log):
            break
        time.sleep(0.5)
    time.sleep(5)
    running = alive()
    log = adb("logcat", "-d", "-v", "brief", "fern:I", "AndroidRuntime:E", "DEBUG:F", "*:F")
    with open(f"{out}/logcat.txt", "w") as f:
        f.write(log)
    with open(f"{out}/screen.png", "wb") as f:
        f.write(subprocess.run(["adb", "exec-out", "screencap", "-p"], capture_output=True).stdout)
    print("\n".join(line for line in log.splitlines() if "walkie" in line or "DEBUG" in line), flush=True)
    failures = []
    m = START.search(log)
    if not m:
        failures.append(f"no line matching {START.pattern!r}")
    elif int(m.group(1)) < 0:
        failures.append("the app did not open its UDP socket")
    if not running:
        failures.append("the app is not running five seconds after its start")
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
