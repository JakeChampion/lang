"""The shared parts of the emulator checks of the Android apps.

Each check drives one APK on a running device or emulator with adb. It
reads the log lines of the app, counts colours on the screen, and keeps
a list of failures. A check imports this module from tests/android.
"""
import re
import struct
import subprocess
import sys
import time

# The log lines that show a crash, a refused library, or a Fern abort.
CRASH = re.compile(r"Fatal signal|SIGSYS|UnsatisfiedLinkError|FATAL EXCEPTION|ClassNotFoundException|VerifyError|fern: |Unable to load native library")

failures = []


def adb(*args, check=True):
    return subprocess.run(["adb", *args], check=check, capture_output=True, text=True).stdout


def fail(message):
    failures.append(message)
    print("FAIL " + message, flush=True)


def logcat():
    return adb("logcat", "-d", "-v", "brief", "fern:I", "AndroidRuntime:E", "DEBUG:*", "libc:*", "*:F")


def wait_for(pattern, timeout=30):
    """The first match of `pattern` in logcat within `timeout` seconds, or
    None."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        m = re.search(pattern, logcat())
        if m:
            return m
        time.sleep(0.5)
    return None


def crashes(log):
    """The lines of `log` that show a crash."""
    return [line for line in log.splitlines() if CRASH.search(line)]


def screen(out, name):
    """One frame of the screen: its width, its height and its pixels, four
    bytes of RGBA for each one. The raw frame and a PNG go to OUT."""
    raw = subprocess.run(["adb", "exec-out", "screencap"], check=True, capture_output=True).stdout
    with open(f"{out}/{name}.raw", "wb") as f:
        f.write(raw)
    subprocess.run(f"adb exec-out screencap -p > {out}/{name}.png", shell=True, check=False)
    return parse_screencap(raw)


def parse_screencap(raw):
    """The width, the height and the RGBA pixels of a raw screencap frame,
    which has a header before its pixels."""
    width, height, _ = struct.unpack_from("<III", raw)
    return width, height, raw[len(raw) - width * height * 4:]


def count(pixels, rgb, step=1):
    """The pixels of `pixels` with the colour `rgb`, when one pixel in each
    `step` is read."""
    n = 0
    for at in range(0, len(pixels), 4 * step):
        if (pixels[at], pixels[at + 1], pixels[at + 2]) == rgb:
            n += 1
    return n


def finish():
    """Ends the check: exit status 1 after a failure, else "ok"."""
    if failures:
        sys.exit(1)
    print("ok", flush=True)
