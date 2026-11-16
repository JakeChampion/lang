#!/usr/bin/env python3
"""Runs the notification test APK on a running Android device or emulator.

Usage: emulator_check.py APK OUT_DIR

It installs the app with its permissions granted and starts it. The app
posts a notification and then the same one with a progress bar. While it
shows, the notification manager must list it with its title and its
progress, at most 3 seconds after the app logs the progress. When the app cancels it, the list must not have it. It fails on
a crash. OUT_DIR gets logcat and the notification list.
"""

import re
import subprocess
import sys
import time

PACKAGE = "dev.fern.notifytest"
POSTED = (
    re.compile(r"notifytest: allowed true"),
    re.compile(r"notifytest: posted true"),
    re.compile(r"notifytest: progress true"),
)
CANCELLED = re.compile(r"notifytest: cancelled")
CRASH = re.compile(r"Fatal signal|UnsatisfiedLinkError|java\.lang\.\w+Exception|fern: ")


def adb(*args):
    return subprocess.run(["adb", *args], check=True, capture_output=True, text=True).stdout


def logcat():
    return adb("logcat", "-d", "-v", "brief", "fern:I", "AndroidRuntime:E", "*:F")


def wait_for(patterns, seconds):
    log = ""
    deadline = time.time() + seconds
    while time.time() < deadline:
        log = logcat()
        if all(p.search(log) for p in patterns):
            break
        time.sleep(0.5)
    return log


def records(listing):
    """The notification records of the package in a dumpsys listing."""
    return [r for r in listing.split("NotificationRecord(")[1:] if "pkg=" + PACKAGE in r]


def check_shown(listing):
    shown = records(listing)
    if not shown:
        return ["the notification manager lists no notification of " + PACKAGE]
    failures = []
    if not any("Fern notify test" in r for r in shown):
        failures.append("the notification has no title 'Fern notify test'")
    if not any(re.search(r"android\.progress=50\b", r) for r in shown):
        failures.append("the notification has no progress of 50")
    return failures


def wait_shown(read, seconds):
    """Reads the listing until it passes check_shown, or the time ends.

    The notification manager applies a post after notify() returns.
    """
    deadline = time.time() + seconds
    while True:
        listing = read()
        failures = check_shown(listing)
        if not failures or time.time() >= deadline:
            return listing, failures
        time.sleep(0.25)


def dumpsys():
    return adb("shell", "dumpsys", "notification", "--noredact")


def main(apk, out):
    subprocess.run(["mkdir", "-p", out], check=True)
    adb("install", "-r", "-g", apk)
    adb("logcat", "-c")
    adb("shell", "am", "start", "-W", "-n", PACKAGE + "/android.app.NativeActivity")
    log = wait_for(POSTED, 40)
    failures = [f"no line matching {p.pattern!r}" for p in POSTED if not p.search(log)]
    if failures:
        shown = dumpsys()
    else:
        # The app cancels 5 seconds after the progress post.
        shown, failures = wait_shown(dumpsys, 3)
    log = wait_for((CANCELLED,), 20)
    time.sleep(1)
    after = dumpsys()
    if not CANCELLED.search(log):
        failures.append("the app did not cancel the notification")
    elif records(after):
        failures.append("the notification is still listed after the cancel")
    with open(f"{out}/logcat.txt", "w") as f:
        f.write(log)
    with open(f"{out}/notifications.txt", "w") as f:
        f.write(shown)
    print("\n".join(line for line in log.splitlines() if "notifytest" in line), flush=True)
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
