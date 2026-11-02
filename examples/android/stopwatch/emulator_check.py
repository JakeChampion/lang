#!/usr/bin/env python3
"""Drives the stopwatch APK on a running Android device or emulator.

Usage: emulator_check.py APK OUT_DIR

It installs the APK, starts the app and reads the view tree with
uiautomator. It requires the clock and the stopwatch at the centre of the
screen, a tap to start the stopwatch, a long tap to reset it, and a
settings screen with the rows of the Fern settings table. It fails on a
crash in logcat. OUT_DIR gets the screenshots, the view trees and logcat.
"""

import re
import subprocess
import sys
import time
import xml.etree.ElementTree as ET

PACKAGE = "dev.fern.stopwatch"
# With the default settings the stopwatch shows its seconds, and the clock
# does not, so the two patterns do not overlap.
STOPWATCH = re.compile(r"^\d+:\d\d:\d\d$")
CLOCK = re.compile(r"^\d\d:\d\d$")
CRASH = re.compile(r"FATAL EXCEPTION|UnsatisfiedLinkError|Fatal signal|SIGSYS|fern: ")
BOUNDS = re.compile(r"\[(-?\d+),(-?\d+)\]\[(-?\d+),(-?\d+)\]")
# The content may sit a few pixels off the exact centre: integer halves and
# the text padding.
CENTRE_SLACK = 8

failures = []


def adb(*args, check=True):
    return subprocess.run(["adb", *args], check=check, capture_output=True, text=True).stdout


def fail(message):
    failures.append(message)
    print("FAIL " + message, flush=True)


def save(out, name, text):
    with open(f"{out}/{name}", "w") as f:
        f.write(text)


def screenshot(out, name):
    png = subprocess.run(["adb", "exec-out", "screencap", "-p"], check=True, capture_output=True).stdout
    with open(f"{out}/{name}.png", "wb") as f:
        f.write(png)


def tree(out, name):
    adb("shell", "uiautomator", "dump", "/sdcard/ui.xml")
    text = adb("shell", "cat", "/sdcard/ui.xml")
    save(out, name + ".xml", text)
    return ET.fromstring(text[text.index("<"):])


def bounds(node):
    x1, y1, x2, y2 = map(int, BOUNDS.match(node.get("bounds")).groups())
    return x1, y1, x2, y2


def find(root, pattern=None, desc=None):
    for node in root.iter("node"):
        if node.get("package") != PACKAGE:
            continue
        if pattern is not None and pattern.match(node.get("text", "")):
            return node
        if desc is not None and node.get("content-desc") == desc:
            return node
    return None


def texts(root):
    return [n.get("text") for n in root.iter("node") if n.get("package") == PACKAGE and n.get("text")]


def centre(node):
    x1, y1, x2, y2 = bounds(node)
    return (x1 + x2) // 2, (y1 + y2) // 2


def tap(node):
    x, y = centre(node)
    adb("shell", "input", "tap", str(x), str(y))


def long_tap(node):
    x, y = centre(node)
    adb("shell", "input", "swipe", str(x), str(y), str(x), str(y), "1500")


def check_centred(root):
    clock = find(root, CLOCK)
    stopwatch = find(root, STOPWATCH)
    if clock is None or stopwatch is None:
        fail(f"no clock and stopwatch text; the app shows {texts(root)}")
        return None
    sx1, sy1, sx2, sy2 = bounds(root.find("node"))
    cx1, cy1, cx2, cy2 = bounds(clock)
    wx1, wy1, wx2, wy2 = bounds(stopwatch)
    content_x = (min(cx1, wx1) + max(cx2, wx2)) // 2
    content_y = (cy1 + wy2) // 2
    screen_x, screen_y = (sx1 + sx2) // 2, (sy1 + sy2) // 2
    print(f"screen centre {screen_x},{screen_y}; content centre {content_x},{content_y}", flush=True)
    if abs(content_x - screen_x) > CENTRE_SLACK or abs(content_y - screen_y) > CENTRE_SLACK:
        fail(f"the content centre {content_x},{content_y} is not the screen centre {screen_x},{screen_y}")
    return stopwatch


def main(apk, out):
    subprocess.run(["mkdir", "-p", out], check=True)
    adb("install", "-r", apk)
    adb("logcat", "-c")
    adb("shell", "am", "start", "-W", "-n", f"{PACKAGE}/.MainActivity")
    time.sleep(3)
    screenshot(out, "1-start")

    root = tree(out, "1-start")
    stopwatch = check_centred(root)
    if stopwatch is not None:
        if stopwatch.get("text") != "0:00:00":
            fail(f"the stopwatch shows {stopwatch.get('text')!r} at start, not '0:00:00'")
        tap(stopwatch)
        time.sleep(3.5)
        root = tree(out, "2-running")
        screenshot(out, "2-running")
        running = find(root, STOPWATCH)
        if running is None or running.get("text") == "0:00:00":
            fail(f"a tap did not start the stopwatch; the app shows {texts(root)}")
        else:
            long_tap(running)
            time.sleep(1.5)
            root = tree(out, "3-reset")
            reset = find(root, STOPWATCH)
            if reset is None or reset.get("text") != "0:00:00":
                fail(f"a long tap did not reset the stopwatch; the app shows {texts(root)}")

    # Any tap shows the menu button. Open the settings screen from the menu.
    root = tree(out, "4-menu")
    menu = find(root, desc="Open the menu")
    if menu is None:
        x1, y1, x2, y2 = bounds(root.find("node"))
        adb("shell", "input", "tap", str(x1 + 20), str(y1 + 20))
        time.sleep(1)
        root = tree(out, "4-menu")
        menu = find(root, desc="Open the menu")
    if menu is None:
        fail("no menu button after a tap")
    else:
        tap(menu)
        time.sleep(1)
        root = tree(out, "5-menu-open")
        item = find(root, re.compile(r"^Settings$"))
        if item is None:
            fail(f"the menu has no Settings item; it shows {texts(root)}")
        else:
            tap(item)
            time.sleep(2)
            root = tree(out, "6-settings")
            screenshot(out, "6-settings")
            for want in ("Show the clock", "Show seconds", "Beep interval"):
                if want not in texts(root):
                    fail(f"the settings screen has no {want!r} row; it shows {texts(root)}")
            adb("shell", "input", "keyevent", "KEYCODE_BACK")
            time.sleep(1)

    if not adb("shell", "pidof", PACKAGE, check=False).strip():
        fail("the app process is not running")
    log = adb("logcat", "-d")
    save(out, "logcat.txt", log)
    for line in log.splitlines():
        if CRASH.search(line):
            fail("logcat: " + line)

    if failures:
        print(f"{len(failures)} check(s) failed", flush=True)
        return 1
    print("ok", flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1], sys.argv[2]))
