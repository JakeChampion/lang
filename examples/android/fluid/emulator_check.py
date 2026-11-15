#!/usr/bin/env python3
"""Drives the Fluid APK on a running Android device or emulator.

Usage: emulator_check.py APK OUT_DIR

It installs the APK, which fern -apk wrote, and starts its NativeActivity
with a background colour and no bloom as settings. The app draws its own
pixels, so the check reads the log lines of the app and the colours of the
screen. It requires that the GPU runs the simulation, the background
colour of the settings on a still screen, dye after a swipe, the places of
the touches with their times on the clock of the frames, an event of two
fingers, the dialog
after a tap on the button, a pause from a tap on its first row, a still
screen while the fluid is paused, the bloom from the B key, a picture in
the gallery from the S key, and the back key that closes the dialog and
then the app. It fails on a crash in logcat. OUT_DIR gets the frames and
logcat.

Kid mode pins the app, and Android asks the person before it does that, so
this check does not start kid mode. fluid_test.fern checks its rules.
"""

from pathlib import Path
import re
import subprocess
import sys
import time

sys.path.insert(0, str(Path(__file__).resolve().parents[3] / "tests" / "android"))
from emulator import adb, count, crashes, fail, finish, logcat, screen, wait_for  # noqa: E402

PACKAGE = "dev.fern.fluid"
# The background colour of the setting back_color.
BACKGROUND = (0, 0, 64)


def background(out, name):
    """The part of the screen in the background colour, in one screen frame,
    and the pixels of the frame."""
    width, height, pixels = screen(out, name)
    part = count(pixels, BACKGROUND, 4) * 4 / (width * height)
    print(f"{name}: {width}x{height}, background {part:.3f}", flush=True)
    return part, pixels


def tap(x, y):
    adb("shell", "input", "tap", str(x), str(y))


def key(text):
    adb("shell", "input", "text", text)


def main(apk, out):
    subprocess.run(["mkdir", "-p", out], check=True)
    adb("install", "-r", apk)
    adb("shell", "pm", "clear", PACKAGE)
    adb("shell", "rm", "-rf", "/sdcard/Pictures/Fluid")
    adb("logcat", "-c")
    adb("shell", "am", "start", "-W", "-n", PACKAGE + "/android.app.NativeActivity", "--es", "back_color", "000040", "--es", "bloom", "false", "--es", "curl", "fast")

    m = wait_for(r"fluid: window (\d+)x(\d+)")
    if not m:
        fail("no window line from the app in logcat")
        finish()
    width, height = int(m.group(1)), int(m.group(2))
    gpu = wait_for(r"fluid: gpu (true|false)")
    if not gpu or gpu.group(1) != "true":
        fail("the GPU of the device does not run the simulation")
    still = wait_for(r"fluid: still (true|false)")
    place = wait_for(r"fluid: button (\d+),(\d+) row (\d+),(\d+)")
    if not still or not place:
        fail("no still line or no button line from the app in logcat")
        finish()
    time.sleep(2)

    part, _ = background(out, "1-start")
    # With the animations of the system off, the app starts still, with no
    # burst: the screen has the background colour and the button.
    if still.group(1) == "true" and part < 0.9:
        fail(f"the background colour covers {part:.3f} of the still screen, not 0.9 or more")

    adb("shell", "input", "swipe", str(width // 4), str(height // 2), str(3 * width // 4), str(height * 2 // 3), "400")
    time.sleep(1)
    dyed, _ = background(out, "2-dye")
    if dyed > part - 0.02:
        fail(f"a swipe put no dye on the screen: the background covers {dyed:.3f} after {part:.3f}")
    if still.group(1) == "true" and not wait_for(r"fluid: paused false"):
        fail("a swipe did not start the fluid, which was still")

    # Three slow swipes give the app the places of a finger for 5 seconds.
    # The app then writes what the touches were: the count of the events and
    # of their places, and how old a place was when the app read it.
    for _ in range(3):
        adb("shell", "input", "swipe", str(width // 4), str(height // 3), str(3 * width // 4), str(2 * height // 3), "1500")
    touch = wait_for(r"fluid: touch (\d+) events, (\d+) places, read (-?\d+) to (-?\d+) us after their time, (\d+) frames with an old place", 20)
    if not touch:
        fail("the app wrote no line about its touches")
    else:
        print("touches: " + touch.group(0), flush=True)
        events, places, soonest, latest = (int(touch.group(k)) for k in (1, 2, 3, 4))
        if places < events or events < 10:
            fail(f"the app read {events} touch events with {places} places")
        # The time of a place is on the clock of the frames: the app reads a
        # place after its time, and less than half a second after it.
        if soonest < 0 or latest > 500000:
            fail(f"the app read a place {soonest} to {latest} us after its time")
    pace = re.findall(r"fluid: (\d+ fps, longest \d+ ms.*)", logcat())
    if pace:
        print("pace: " + pace[-1], flush=True)

    # Two fingers that move at the same time. `input` has one finger only,
    # so a script of monkey gives the two. The app then writes the largest
    # count of the fingers of one event.
    script = f"{out}/two-fingers.txt"
    with open(script, "w") as f:
        f.write("type= raw events\ncount= 1\nspeed= 1.0\nstart data >>\n")
        for _ in range(8):
            f.write(
                f"PinchZoom({width // 4},{height // 3},{width // 4},{2 * height // 3},"
                f"{3 * width // 4},{2 * height // 3},{3 * width // 4},{height // 3},40)\n"
            )
            f.write("UserWait(700)\n")
    adb("push", script, "/data/local/tmp/two-fingers.txt")
    adb("shell", "monkey", "-f", "/data/local/tmp/two-fingers.txt", "1", check=False)
    both = wait_for(r"fluid: touch \d+ events, .* ([2-9]) fingers at most", 20)
    if not both:
        fail("the app read no event of two fingers")
    else:
        print("two fingers: " + both.group(0), flush=True)
    # The script ends with one finger down. A tap starts a new touch.
    adb("shell", "input", "tap", str(width // 2), str(height // 2))

    # The places of the button and of the first row come again with the
    # insets of the system bars, so the last line has them.
    bx, by, rx, ry = map(int, re.findall(r"fluid: button (\d+),(\d+) row (\d+),(\d+)", logcat())[-1])
    tap(bx, by)
    if not wait_for(r"fluid: dialog true"):
        fail("a tap on the button did not open the dialog")
    time.sleep(1)
    background(out, "3-dialog")
    tap(rx, ry)
    if not wait_for(r"fluid: paused true"):
        fail("a tap on the first row of the dialog did not pause the fluid")
    adb("shell", "input", "keyevent", "4")
    if not wait_for(r"fluid: dialog false"):
        fail("the back key did not close the dialog")
    time.sleep(1)
    _, first = background(out, "4-paused")
    time.sleep(1)
    _, second = background(out, "5-paused")
    if first != second:
        fail("the paused fluid moved")

    key("b")
    if not wait_for(r"fluid: bloom true"):
        fail("the B key did not turn the bloom on")
    key("s")
    saved = wait_for(r"fluid: saved (true|false)")
    if not saved or saved.group(1) != "true":
        fail("the S key did not save a picture")
    elif not re.search(r"fluid-\d+\.png", adb("shell", "ls", "/sdcard/Pictures/Fluid", check=False)):
        fail("the gallery has no picture of the fluid")
    if not re.search(r'fluid: Could not read "curl=fast" as number; ignored\.', logcat()):
        fail("the app did not report the setting of its Intent that it cannot read")

    adb("shell", "input", "keyevent", "4")
    if not wait_for(r"fluid: destroyed"):
        fail("the back key did not close the app")

    log = logcat()
    with open(f"{out}/logcat.txt", "w") as f:
        f.write(log)
    for line in crashes(log):
        fail("logcat: " + line)
    finish()


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    main(*sys.argv[1:3])
