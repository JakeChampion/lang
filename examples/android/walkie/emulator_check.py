#!/usr/bin/env python3
"""Runs two copies of the walkie-talkie APK against each other on a running
Android device or emulator.

Usage: emulator_check.py APK SECOND_APK OUT_DIR

SECOND_APK is the same app built with the package dev.fern.walkie2. The check
turns off the freezer of cached apps and reboots, so that the copy that is
not in front keeps running as a second phone would. Then it:

1. starts both copies, and requires the start line of each: one on port
   47120 and one on 47121;
2. waits until both made their keys, then taps SAME in each copy;
3. holds the talk button of the first copy, then of the second, and requires
   that each copy transmits and the other hears it;
4. waits 20 seconds, and requires that neither copy lost its peer and that
   both still run with no crash in logcat.

OUT_DIR gets logcat and a screenshot of each copy.
"""

import re
import subprocess
import sys
import time

FIRST = "dev.fern.walkie"
SECOND = "dev.fern.walkie2"
ACTIVITY = "/android.app.NativeActivity"
START = re.compile(r"walkie: node \d+, port (4712[01]), socket (\d+), speaker rate ([1-9]\d*)")
CRASH = re.compile(r"Fatal signal|UnsatisfiedLinkError|java\.lang\.\w+Exception|fern: ")
LOST = re.compile(r"walkie: (the peer stopped answering|the other phone did not confirm the code|the peer left|the connection was lost)")
LOGCAT = ["logcat", "-d", "-v", "brief", "fern:I", "AndroidRuntime:E", "DEBUG:F", "*:F"]


def adb(*args, check=True):
    return subprocess.run(["adb", *args], check=check, capture_output=True, text=True).stdout


def pid(package):
    return adb("shell", "pidof", package, check=False).strip()


def logcat():
    return adb(*LOGCAT)


def lines_of(log, p):
    """The fern lines of process p in a brief logcat."""
    return [line for line in log.splitlines() if re.search(r"\(\s*" + re.escape(p) + r"\)", line)]


def wait_for(what, test, seconds=30):
    deadline = time.time() + seconds
    while time.time() < deadline:
        log = logcat()
        if test(log):
            return log
        time.sleep(0.5)
    raise Failure(f"timed out: {what}")


class Failure(Exception):
    pass


def boot_without_freezer():
    if adb("shell", "settings", "get", "global", "cached_apps_freezer").strip() == "disabled":
        return
    adb("shell", "settings", "put", "global", "cached_apps_freezer", "disabled")
    adb("reboot")
    adb("wait-for-device")
    deadline = time.time() + 180
    while adb("shell", "getprop", "sys.boot_completed", check=False).strip() != "1":
        if time.time() > deadline:
            raise Failure("the emulator did not boot again")
        time.sleep(2)
    time.sleep(5)


def launch(package):
    """Starts a copy, or brings its task to the front.

    Every start uses the launcher intent, so the root intent of the task and
    the intent that brings it forward are the same. Android then moves the
    task to the front and keeps the running activity. A start with a different
    intent adds a second activity to the task, and its app_main opens a second
    node that finds both ports busy."""
    adb("shell", "am", "start", "-W", "-a", "android.intent.action.MAIN", "-c", "android.intent.category.LAUNCHER", "-n", package + ACTIVITY)


def to_front(package):
    launch(package)
    time.sleep(1.5)


def screen_size():
    m = re.search(r"(\d+)x(\d+)", adb("shell", "wm", "size").splitlines()[-1])
    return int(m.group(1)), int(m.group(2))


def has(log, p, text):
    return any(text in line for line in lines_of(log, p))


def last_state(log, p):
    states = [line.split("walkie: state ", 1)[1].strip() for line in lines_of(log, p) if "walkie: state " in line]
    return states[-1] if states else ""


def confirm(package, p, w, h):
    to_front(package)
    # The middle of screen.same_button.
    adb("shell", "input", "tap", str(w // 10 + w * 7 // 40), str(h * 3 // 10 + h // 16))
    wait_for(package + " confirms the code", lambda log: has(log, p, "walkie: the string is confirmed"), 10)


def talk(package, talker, listener, w, h):
    to_front(package)
    mark = len(lines_of(logcat(), listener))
    # A long press in the middle of screen.talk_button.
    adb("shell", "input", "swipe", str(w // 2), str(h * 7 // 10), str(w // 2), str(h * 7 // 10), "4000")
    log = logcat()
    if not has(log, talker, "walkie: state TRANSMITTING"):
        raise Failure(package + " did not transmit while its button was held")
    if not any("walkie: hearing the peer" in line for line in lines_of(log, listener)[mark:]):
        raise Failure("the other copy did not hear " + package)
    wait_for(package + " stops after the release", lambda log: last_state(log, talker) == "CONNECTED_LISTENING", 10)


def save(out):
    with open(f"{out}/logcat.txt", "w") as f:
        f.write(logcat())


def screenshot(path):
    with open(path, "wb") as f:
        f.write(subprocess.run(["adb", "exec-out", "screencap", "-p"], capture_output=True).stdout)


def run(apk, second_apk, out):
    boot_without_freezer()
    for a, package in [(apk, FIRST), (second_apk, SECOND)]:
        adb("install", "-r", "-g", a)
        adb("shell", "pm", "grant", package, "android.permission.RECORD_AUDIO")
    adb("logcat", "-c")
    launch(FIRST)
    wait_for("the first copy starts", lambda log: pid(FIRST) != "" and any(START.search(line) and START.search(line).group(1) == "47120" for line in lines_of(log, pid(FIRST))))
    launch(SECOND)
    wait_for("the second copy starts", lambda log: pid(SECOND) != "" and any(START.search(line) and START.search(line).group(1) == "47121" for line in lines_of(log, pid(SECOND))))
    a, b = pid(FIRST), pid(SECOND)
    for p in (a, b):
        m = next(START.search(line) for line in lines_of(logcat(), p) if START.search(line))
        if int(m.group(2)) < 0:
            raise Failure("a copy did not open its UDP socket")
    wait_for("both copies make keys", lambda log: has(log, a, "walkie: keys are ready") and has(log, b, "walkie: keys are ready"))
    w, h = screen_size()
    confirm(SECOND, b, w, h)
    confirm(FIRST, a, w, h)
    wait_for("each copy sees the other confirm", lambda log: has(log, a, "walkie: the other phone confirmed") and has(log, b, "walkie: the other phone confirmed"), 10)
    talk(FIRST, a, b, w, h)
    screenshot(f"{out}/first.png")
    talk(SECOND, b, a, w, h)
    screenshot(f"{out}/second.png")
    time.sleep(20)
    log = logcat()
    if pid(FIRST) != a or pid(SECOND) != b:
        raise Failure("a copy stopped running")
    lost = [line for line in log.splitlines() if LOST.search(line)]
    if lost:
        raise Failure("a copy lost its peer: " + "; ".join(lost))
    crashes = [line for line in log.splitlines() if CRASH.search(line)]
    if crashes:
        raise Failure("logcat: " + "; ".join(crashes))


def main(apk, second_apk, out):
    subprocess.run(["mkdir", "-p", out], check=True)
    failure = None
    try:
        run(apk, second_apk, out)
    except (Failure, subprocess.CalledProcessError) as e:
        failure = e
    save(out)
    print("\n".join(line for line in logcat().splitlines() if "walkie" in line or "DEBUG" in line), flush=True)
    for package in (FIRST, SECOND):
        adb("shell", "am", "force-stop", package, check=False)
    if failure is not None:
        print(f"FAIL {failure}", flush=True)
        sys.exit(1)
    print("ok", flush=True)


if __name__ == "__main__":
    if len(sys.argv) != 4:
        sys.exit(__doc__)
    main(*sys.argv[1:])
