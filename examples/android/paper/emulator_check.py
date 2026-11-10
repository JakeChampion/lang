#!/usr/bin/env python3
"""Drives the Paper APK on a running Android device or emulator.

Usage: emulator_check.py APK OUT_DIR

It installs the APK, which fern -apk wrote, makes Paper the home app, and
presses the home key. The app draws its own pixels, so the check reads the
log lines of the app and the colours of the screen. It requires the home
screen in the paper colour above the wallpaper, a tap on the time that
opens the clock app, the clock as a button for an accessibility service, a
swipe up that opens the app list in the paper
colour with the keyboard for its text field, a tap on the search field that
shows the keyboard again after the back key hid it, a search for "pap" that
finds Paper and opens it, Back that hides the keyboard, then Back that goes
home, a frame section in the system trace while the list slides, and no
crash in logcat. OUT_DIR gets the frames, logcat and the trace.
"""

from pathlib import Path
import re
import subprocess
import sys
import time

sys.path.insert(0, str(Path(__file__).resolve().parents[3] / "tests" / "android"))
from emulator import adb, count, crashes, fail, finish, logcat, screen, wait_for  # noqa: E402

PACKAGE = "dev.fern.paper"
ACTIVITY = PACKAGE + "/.Home"
PAPER = (255, 244, 230)



def paper_part(out, name, top_half=False):
    """The share of the screen in the paper colour, from one pixel of each
    four. With `top_half`, only the top half counts, above the keyboard."""
    time.sleep(1.0)
    width, height, pixels = screen(out, name)
    rows = height // 2 if top_half else height
    part = count(pixels[: rows * width * 4], PAPER, 4) * 4 / (width * rows)
    print(f"{name}: {width}x{height}, paper {part:.3f}", flush=True)
    return width, height, part


def wait_for_keyboard(shown, timeout=5.0):
    """The keyboard reaches the requested state within `timeout` seconds."""
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        visible = bool(re.search(r"mInputShown=true", adb("shell", "dumpsys", "input_method", check=False)))
        if visible == shown:
            return True
        time.sleep(0.5)
    return False


def keyboard_shown(timeout=5.0):
    return wait_for_keyboard(True, timeout)


def editor_is_field():
    """The keyboard types into the text field of the app: an editor of text
    with the action Go, not the decor view."""
    editor = re.search(r"inputType=0x(\w+) imeOptions=0x(\w+)", adb("shell", "dumpsys", "input_method", check=False))
    if not editor or editor.group(1) != "1" or editor.group(2) != "10000002":
        fail("the keyboard does not type into the text field: " + (editor.group(0) if editor else "no editor"))
        return False
    return True


def hide_keyboard():
    """Back hides the keyboard before the app receives a Back event."""
    if not keyboard_shown():
        fail("the app list opened without the keyboard")
        return False
    if not editor_is_field():
        return False
    adb("shell", "input", "keyevent", "KEYCODE_BACK")
    if not wait_for_keyboard(False, 1.5):
        fail("the back key did not hide the keyboard")
        return False
    return True


def check_keyboard(width):
    """The app list opens with the keyboard. After Back hides it, a tap on
    the search field shows it again. The field is 36 dp below the status
    bar, so a tap 74 dp from the top is on it."""
    if not hide_keyboard():
        return
    density = re.findall(r"(\d+)", adb("shell", "wm", "density"))
    scale = int(density[-1]) / 160 if density else 1.0
    adb("shell", "input", "tap", str(width // 2), str(int(74 * scale)))
    if not keyboard_shown():
        fail("a tap on the search field did not show the keyboard")


def check_access(out):
    """An accessibility service reads the home screen: uiautomator dumps the
    window through the accessibility layer, and the dump has the clock of
    Paper as a button with the time as its text."""
    adb("shell", "uiautomator", "dump", "/sdcard/paper-ui.xml", check=False)
    dump = adb("shell", "cat", "/sdcard/paper-ui.xml", check=False)
    with open(f"{out}/ui.xml", "w") as f:
        f.write(dump)
    clock = re.search(r'<node [^>]*text="\d{1,2}:\d\d[^"]*"[^>]*class="android\.widget\.Button"[^>]*package="dev\.fern\.paper"[^>]*clickable="true"', dump)
    if not clock:
        fail("the accessibility layer has no clock button of Paper")


def open_clock():
    """Taps the time, which opens the clock app, and goes home again. The
    first frame logs the middle of the time."""
    place = re.search(r"paper: drawn .*clock at (\d+) (\d+)", logcat())
    if not place:
        fail("the home screen did not log the place of the clock")
        return
    adb("shell", "input", "tap", place.group(1), place.group(2))
    opened = wait_for(r"paper: open clock (\S+)")
    if not opened or opened.group(1) == "by":
        fail("a tap on the time did not find the clock app")
    else:
        time.sleep(2.0)
        resumed = adb("shell", "dumpsys", "activity", "activities", check=False)
        if not re.search(r"(mResumedActivity|topResumedActivity).*" + re.escape(opened.group(1)), resumed):
            fail(f"a tap on the time did not bring {opened.group(1)} to the front")
    adb("shell", "input", "keyevent", "KEYCODE_HOME")
    time.sleep(1.5)


def main(apk, out):
    subprocess.run(["mkdir", "-p", out], check=True)
    adb("install", "-r", "-g", apk)
    adb("logcat", "-c")
    adb("shell", "cmd", "package", "set-home-activity", ACTIVITY, check=False)
    adb("shell", "input", "keyevent", "KEYCODE_HOME")
    if not wait_for(r"paper: drawn \d+x\d+"):
        adb("shell", "am", "start", "-W", "-n", ACTIVITY)
        if not wait_for(r"paper: drawn \d+x\d+"):
            fail("the home screen did not draw")
    if not wait_for(r"paper: apps \d+, Paper listed true"):
        fail("the apps did not load with Paper among them")
    width, height, part = paper_part(out, "home")
    # The wallpaper is 947 by 2048 at the width of the screen, so on a
    # screen of 1 by 2 it covers the screen, and only its paper-coloured top
    # shows the paper colour.
    if part < 0.05 or part > 0.7:
        fail(f"the home screen has {part:.3f} of the paper colour, not paper above the wallpaper")
    check_access(out)
    open_clock()

    adb("shell", "atrace", "--async_start", "-c", "-b", "4096", "-a", PACKAGE, check=False)
    adb("shell", "input", "swipe", str(width // 2), str(height * 3 // 4), str(width // 2), str(height // 4), "150")
    if not wait_for(r"paper: layer 1"):
        fail("a swipe up did not open the app list")
    trace = adb("shell", "atrace", "--async_stop", check=False)
    with open(f"{out}/trace.txt", "w") as f:
        f.write(trace)
    if not re.search(r"B\|\d+\|frame", trace):
        fail("the system trace has no frame section of the app")
    _, _, part = paper_part(out, "drawer", top_half=True)
    if part < 0.6:
        fail(f"the app list has {part:.3f} of the paper colour")
    check_keyboard(width)

    adb("shell", "input", "text", "pap")
    if not wait_for(r"paper: launch dev\.fern\.paper/dev\.fern\.paper\.Home"):
        fail("a search for pap did not open Paper")
    adb("shell", "input", "keyevent", "KEYCODE_HOME")
    time.sleep(1.5)

    adb("shell", "input", "swipe", str(width // 2), str(height * 3 // 4), str(width // 2), str(height // 4), "150")
    time.sleep(1.0)
    keyboard_hidden = hide_keyboard()
    if keyboard_hidden:
        _, _, part = paper_part(out, "keyboard-back", top_half=True)
        if part < 0.6:
            fail("the back key hid the keyboard and closed the app list")
    early = logcat()
    adb("logcat", "-c")
    if keyboard_hidden:
        adb("shell", "input", "keyevent", "KEYCODE_BACK")
        if not wait_for(r"paper: layer 0"):
            fail("the back key did not go back to the home screen")
    _, _, part = paper_part(out, "back")
    if part < 0.05 or part > 0.7:
        fail(f"the home screen after Back has {part:.3f} of the paper colour, not paper above the wallpaper")

    log = early + logcat()
    with open(f"{out}/logcat.txt", "w") as f:
        f.write(log)
    for line in crashes(log):
        fail("logcat: " + line)
    adb("shell", "cmd", "package", "set-home-activity", "com.android.launcher3/.uioverrides.QuickstepLauncher", check=False)
    finish()


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    main(*sys.argv[1:])
