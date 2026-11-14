#!/usr/bin/env python3
"""Drives the music player APK on a running Android device or emulator.

Usage: emulator_check.py APK OUT_DIR

It installs the APK, which fern -apk wrote, and starts its NativeActivity.
The player makes its three tunes at the first start, and reads their
files into its library. The check taps a track in the list and lets it
play. Android has the media session of the player, the media keys stop
and start the track, and the player runs a foreground service.

The check goes to the home screen and back: the track plays on, and the
app draws its screen again. The back key then puts the app away, and the
track plays on. With no window, the buttons of the widget stop the track
and start it again.

After a stop, the back key closes the app, and the check requires the
line of the app in logcat: the library has the three tunes or more, a
track was open, and its time went on. Then the check starts the app a
second time. So the decoder gave its samples to the sound of the device
in time.

The check removes the task of the app, as a swipe in the recent apps
does. Android destroys the activity, and the track plays on. A new start
takes the loop over, and a tap on a shortcut while the app runs does not
start a second activity. A stop with no window ends the loop.

It fails on a crash in logcat. OUT_DIR gets the frames and logcat.
"""

from pathlib import Path
import re
import subprocess
import sys
import time

sys.path.insert(0, str(Path(__file__).resolve().parents[3] / "tests" / "android"))
from emulator import adb, count, crashes, fail, failures, finish, logcat, screen, wait_for  # noqa: E402

PACKAGE = "dev.fern.tune"
ACTIVITY = PACKAGE + "/.Main"


def has_widget_provider(dump):
    """Whether Android lists the exact widget component in full or short form."""
    package = re.escape(PACKAGE)
    return re.search(r"(?<![\w.$])" + package + r"/(?:" + package + r")?\.Widget(?![\w.$])", dump) is not None


def task_of(stacks):
    """The id of the task of the app in the output of `am stack list`, or
    None."""
    m = re.search(r"taskId=(\d+): " + re.escape(PACKAGE) + "/", stacks)
    return m.group(1) if m else None


def remove_task(out, name):
    """Removes the task of the app, so Android destroys its activity."""
    stacks = adb("shell", "am", "stack", "list")
    task = task_of(stacks)
    if task is None:
        Path(out, name + "-stacks.txt").write_text(stacks)
        fail("am stack list has no task of the app")
        return
    adb("shell", "am", "stack", "remove", task)


def session_text():
    """The part of `dumpsys media_session` for the session of the player."""
    text = adb("shell", "dumpsys", "media_session")
    at = text.find(PACKAGE)
    return text[at:at + 3000] if at >= 0 else ""


def session_state():
    """The PlaybackState of the session of the player: 3 while it plays and
    2 while it does not, or -1 with no session."""
    m = re.search(r"state=PlaybackState \{state=(?:[A-Z_]+\()?(\d+)", session_text())
    return int(m.group(1)) if m else -1


def main(apk, out):
    subprocess.run(["mkdir", "-p", out], check=True)
    adb("install", "-r", apk)
    adb("logcat", "-c")
    adb("shell", "am", "start", "-W", "-n", ACTIVITY)

    # The app makes its tunes and reads them, and Android can ask for the
    # permission to read the music of the phone.
    time.sleep(8)
    adb("shell", "input", "keyevent", "111")
    time.sleep(2)
    width, height, _ = screen(out, "library")
    # The first row of the list is below the title, the search field and
    # the tabs. The queue is then the three tunes, which play for 18 seconds.
    adb("shell", "input", "tap", str(width // 2), str(height * 38 // 100))
    time.sleep(4)
    screen(out, "playing")

    # Android has the media session of the player, and its media keys stop
    # the track and start it again. The player runs a foreground service.
    if session_state() != 3:
        fail("the media session does not play: " + session_text()[:400])
    adb("shell", "input", "keyevent", "127")
    time.sleep(2)
    if session_state() != 2:
        fail("the media key did not stop the track: " + session_text()[:400])
    adb("shell", "input", "keyevent", "126")
    time.sleep(2)
    if session_state() != 3:
        fail("the media key did not start the track: " + session_text()[:400])
    if "isForeground=true" not in adb("shell", "dumpsys", "activity", "services", PACKAGE):
        fail("the player has no foreground service")

    # The person goes to the home screen and comes back. The track plays on
    # while the app is away, and the app then draws its screen again.
    adb("shell", "input", "keyevent", "3")
    time.sleep(5)
    if session_state() != 3:
        fail("the track does not play while the app is away: " + session_text()[:400])
    adb("shell", "am", "start", "-W", "-n", ACTIVITY)
    time.sleep(4)
    width, height, pixels = screen(out, "returned")
    same = count(pixels, (pixels[0], pixels[1], pixels[2]), step=7)
    total = len(pixels) // 4 // 7
    if same * 100 > total * 99:
        fail(f"the screen is blank after the app comes back: {same} of {total} pixels have one colour")

    # The media key for the track before starts a track at its start, so
    # the queue does not end while the app is away.
    adb("shell", "input", "keyevent", "88")
    time.sleep(2.5)

    # The back key puts the app away while a track plays, and the track
    # plays on. With no window, the buttons of the widget stop the track and
    # start it again.
    adb("shell", "input", "keyevent", "4")
    time.sleep(3)
    if session_state() != 3:
        fail("the track does not play after the back key: " + session_text()[:400])
    if re.search(r"tune: closed with", logcat()):
        fail("the back key closed the app while a track played")
    for action, state in (("PAUSE", 2), ("PLAY", 3)):
        adb("shell", "am", "broadcast", "-a", "dev.fern.tune.widget." + action, "-n", PACKAGE + "/.Widget")
        time.sleep(2)
        if session_state() != state:
            fail(f"the widget button {action} did not reach the player with no window: " + session_text()[:400])

    # The back key closes the app when no track plays. The app tells where
    # the player was. The media key for the track before starts a track at
    # its start, and the stop comes 3 seconds later. The shortest tune
    # plays for 6 seconds, so the track does not end before the stop.
    adb("shell", "input", "keyevent", "88")
    time.sleep(3)
    adb("shell", "input", "keyevent", "127")
    time.sleep(2)
    adb("shell", "am", "start", "-W", "-n", ACTIVITY)
    time.sleep(4)
    adb("shell", "input", "keyevent", "4")
    m = wait_for(r"tune: closed with (\d+) tracks at track (-?\d+), (\d+):(\d\d) of (\d+):(\d\d), playing (\w+)", timeout=10)
    if not m:
        fail("the back key did not close the app")
    else:
        track, played = int(m.group(2)), int(m.group(3)) * 60 + int(m.group(4))
        print(f"closed: {m.group(0)}", flush=True)
        if int(m.group(1)) < 3:
            fail(f"the library does not have its 3 tunes: {m.group(0)!r}")
        elif track < 0 or m.group(7) != "false":
            fail(f"no track was open, or a track plays: {m.group(0)!r}")
        elif played < 1:
            fail(f"the time of the track did not go on: {m.group(0)!r}")

    # The person starts the app again after the back key closed it: the
    # process of the app can be the same one. The app draws its screen and
    # takes the back key again.
    first = logcat()
    adb("logcat", "-c")
    adb("shell", "am", "start", "-W", "-n", ACTIVITY)
    time.sleep(8)
    width, height, pixels = screen(out, "again")
    same = count(pixels, (pixels[0], pixels[1], pixels[2]), step=7)
    total = len(pixels) // 4 // 7
    if same * 100 > total * 99:
        fail(f"the screen is blank at the second start: {same} of {total} pixels have one colour")
    adb("shell", "input", "keyevent", "4")
    if not wait_for(r"tune: closed with (\d+) tracks", timeout=10):
        fail("the back key did not close the app at its second start")

    # The launcher has the two shortcuts of the app. A start with the
    # action of the first one plays the library.
    known = adb("shell", "dumpsys", "shortcut")
    for action in ("dev.fern.tune.SHUFFLE", "dev.fern.tune.FAVORITES"):
        if action not in known:
            fail(f"the launcher does not have the shortcut {action}")
    second = logcat()
    adb("logcat", "-c")
    adb("shell", "am", "start", "-W", "-n", ACTIVITY, "-a", "dev.fern.tune.SHUFFLE")
    time.sleep(8)
    screen(out, "shortcut")
    if session_state() != 3:
        fail(f"the shortcut did not play the library: the state of the session is {session_state()}")

    # The person removes the task of the app from the recent apps while a
    # track plays. Android destroys the activity, and the foreground service
    # keeps the process. The loop of the app lives on, and a new start takes
    # that loop over: the app does not start a second time. A tap on a
    # shortcut while the app runs gives its intent to the same activity. A
    # stop with no window ends the loop, and the app tells where the player
    # was.
    remove_task(out, "removed")
    time.sleep(5)
    if session_state() != 3:
        fail("the track does not play after Android destroyed the activity: " + session_text()[:400])
    if re.search(r"tune: closed with", logcat()):
        fail("the loop of the app ended with the activity while a track played")
    adb("shell", "am", "start", "-W", "-n", ACTIVITY)
    time.sleep(4)
    width, height, pixels = screen(out, "taken-over")
    same = count(pixels, (pixels[0], pixels[1], pixels[2]), step=7)
    total = len(pixels) // 4 // 7
    if same * 100 > total * 99:
        fail(f"the screen is blank after a new activity took the loop over: {same} of {total} pixels have one colour")
    adb("shell", "am", "start", "-W", "-n", ACTIVITY, "-a", "dev.fern.tune.SHUFFLE")
    time.sleep(3)
    if session_state() != 3:
        fail(f"the shortcut did not play the library in the app that runs: the state of the session is {session_state()}")
    starts = len(re.findall(r"tune: starts", logcat()))
    if starts != 1:
        fail(f"the app started {starts} times: a new activity must take the loop over, and a shortcut must reach the activity that runs")
    remove_task(out, "removed-again")
    time.sleep(3)
    adb("shell", "input", "keyevent", "127")
    m = wait_for(r"tune: closed with (\d+) tracks at track (-?\d+), (\d+):(\d\d) of (\d+):(\d\d), playing (\w+)", timeout=10)
    if not m:
        fail("the stop with no window did not end the loop of the app")
    elif m.group(7) != "false":
        fail(f"the loop ended while a track played: {m.group(0)!r}")

    # Android knows the widget of the app, which tells that it read the
    # provider file and the layout of the APK. The update broadcast starts
    # the provider class with no activity, and the process stays alive.
    providers = adb("shell", "dumpsys", "appwidget")
    Path(out, "appwidget.txt").write_text(providers)
    if not has_widget_provider(providers):
        fail("Android does not list the widget provider dev.fern.tune/.Widget")
    third = logcat()
    adb("logcat", "-c")
    adb("shell", "am", "broadcast", "-a", "android.appwidget.action.APPWIDGET_UPDATE", "-n", PACKAGE + "/.Widget")
    time.sleep(3)

    log = first + second + third + logcat()
    with open(f"{out}/logcat.txt", "w") as f:
        f.write(log)
    for line in crashes(log):
        fail("logcat: " + line)

    # A player that a failure left running must not show its notification
    # to the next check.
    adb("shell", "am", "force-stop", PACKAGE, check=False)
    if failures:
        print(f"{len(failures)} failures", flush=True)
    finish()


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    main(*sys.argv[1:])
