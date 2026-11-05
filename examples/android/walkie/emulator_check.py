#!/usr/bin/env python3
"""Runs two copies of the walkie-talkie APK against each other on a running
Android device or emulator.

Usage: emulator_check.py APK SECOND_APK OUT_DIR

SECOND_APK is the same app built with the package dev.fern.walkie2. The check
turns off the freezer of cached apps, so that the copy that is
not in front keeps running as a second phone would. Then it:

1. starts both copies, and requires the start line of each: one on port
   47120 and one on 47121, that each one runs its foreground service, and
   that each one holds its wake lock;
2. waits until both made their keys, then taps MATCH in each copy;
3. holds the talk button of the first copy, then of the second, and requires
   that each copy transmits and the other hears it;
4. presses BACK in the second copy, waits 20 seconds, and requires that the
   second copy went to the background, that neither copy lost its peer, and
   that both still run with no crash in logcat;
5. stops both copies and starts them again, and requires that each copy
   kept the identity of the other and connects with no string to compare;
6. starts the first copy alone, opens SETTINGS, SERVER and PHONE KEYBOARD,
   types a server with key events and ENTER, and requires that the copy
   saves the server and loads it at the next start.

OUT_DIR gets logcat and a screenshot of each copy.
"""

import re
from pathlib import Path
import subprocess
import sys
import time

FIRST = "dev.fern.walkie"
SECOND = "dev.fern.walkie2"
ACTIVITY = "/android.app.NativeActivity"
START = re.compile(r"walkie: node \d+, port (4712[01]), socket (-?\d+), speaker rate ([1-9]\d*)")
CRASH = re.compile(r"Fatal signal|UnsatisfiedLinkError|java\.lang\.\w+Exception|fern: ")
IDENTITY = re.compile(r"walkie: identity [0-9a-f]{8}, (\d+) known phones")
SERVER = "rooms.example.com"
LOST = re.compile(r"walkie: (the peer stopped answering|the other phone did not confirm the code|the peer left|the connection was lost)")
LOGCAT = ["logcat", "-d", "-v", "brief", "fern:I", "AndroidRuntime:E", "DEBUG:F", "*:F"]
WAIT_SECONDS = 30


def adb(*args, check=True, timeout=WAIT_SECONDS, include_stderr=False):
    result = subprocess.run(["adb", *args], check=check, capture_output=True, text=True, timeout=timeout)
    return result.stdout + (result.stderr if include_stderr else "")


def pid(package):
    return adb("shell", "pidof", package, check=False).strip()


def logcat():
    return adb(*LOGCAT)


def foreground(package):
    """The service of package is a foreground service."""
    return "isForeground=true" in adb("shell", "dumpsys", "activity", "services", package, check=False)


def lines_of(log, p):
    """The fern lines of process p in a brief logcat."""
    return [line for line in log.splitlines() if re.search(r"\(\s*" + re.escape(p) + r"\)", line)]


def wait_for(what, test, seconds=WAIT_SECONDS):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        log = logcat()
        if test(log):
            return log
        time.sleep(0.5)
    raise Failure(f"timed out: {what}")


class Failure(Exception):
    pass


def disable_freezer():
    # Android observes this setting live. Check its effective state before
    # either copy starts, so the background copy remains active.
    adb("shell", "settings", "put", "global", "cached_apps_freezer", "disabled")
    deadline = time.monotonic() + WAIT_SECONDS
    while time.monotonic() < deadline:
        state = adb("shell", "dumpsys", "activity", "settings")
        if re.search(r"(?m)^\s*use_freezer=false\s*$", state):
            return
        time.sleep(0.5)
    raise Failure("Android did not disable the cached-app freezer")


def to_front(package):
    adb("shell", "am", "start", "-W", "-a", "android.intent.action.MAIN", "-c", "android.intent.category.LAUNCHER", "-f", "0x10200000", "--activity-single-top", "-n", package + ACTIVITY)
    time.sleep(1.5)


def check_node(log, p):
    starts = [START.search(line) for line in lines_of(log, p) if START.search(line)]
    if len(starts) != 1:
        raise Failure("expected one Walkie node in process " + p)
    if int(starts[0].group(2)) < 0:
        raise Failure("a copy did not open its UDP socket")


def known_phones(log, p):
    """The phones that the copy in process p knew when it started."""
    found = [IDENTITY.search(line) for line in lines_of(log, p) if IDENTITY.search(line)]
    if len(found) != 1:
        raise Failure("expected one identity line in process " + p)
    return int(found[0].group(1))


def start_both():
    """Starts both copies, and gives their processes."""
    to_front(FIRST)
    wait_for("the first copy starts", lambda log: pid(FIRST) != "" and any(START.search(line) and START.search(line).group(1) == "47120" for line in lines_of(log, pid(FIRST))))
    to_front(SECOND)
    wait_for("the second copy starts", lambda log: pid(SECOND) != "" and any(START.search(line) and START.search(line).group(1) == "47121" for line in lines_of(log, pid(SECOND))))
    return pid(FIRST), pid(SECOND)


def meet_again():
    """Stops both copies and starts them again. Each copy loads the identity
    of the other from its files, so the two connect with no string."""
    for package in (FIRST, SECOND):
        adb("shell", "am", "force-stop", package)
    adb("logcat", "-c")
    a, b = start_both()
    log = logcat()
    for p in (a, b):
        if known_phones(log, p) != 1:
            raise Failure("a copy did not keep the identity of the other phone")
    wait_for("the copies know each other", lambda log: has(log, a, "walkie: the other phone is known: no string to compare") and has(log, b, "walkie: the other phone is known: no string to compare"))
    wait_for("each copy sees the other confirm again", lambda log: has(log, a, "walkie: the other phone confirmed") and has(log, b, "walkie: the other phone confirmed"), 10)


def type_server(w, h):
    """Types a room server through the key events of the phone keyboard."""
    for package in (FIRST, SECOND):
        adb("shell", "am", "force-stop", package)
    adb("logcat", "-c")
    to_front(FIRST)
    wait_for("the first copy starts alone", lambda log: pid(FIRST) != "" and has(log, pid(FIRST), "walkie: node "))
    a = pid(FIRST)
    # The middles of screen.mode_button, screen.server_row and
    # screen.phone_keys_button.
    for x, y in [(w * 75 // 100, h * 45 // 1000), (w // 2, h * 795 // 1000), (w // 2, h * 32 // 100)]:
        adb("shell", "input", "tap", str(x), str(y))
        time.sleep(1)
    adb("shell", "input", "text", SERVER)
    adb("shell", "input", "keyevent", "KEYCODE_ENTER")
    wait_for("the copy saves the server", lambda log: has(log, a, "walkie: server " + SERVER), 10)
    adb("shell", "am", "force-stop", FIRST)
    adb("logcat", "-c")
    to_front(FIRST)
    wait_for("the copy loads the server", lambda log: pid(FIRST) != "" and has(log, pid(FIRST), ", server " + SERVER))


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
    check_node(logcat(), p)
    # The middle of screen.same_button.
    adb("shell", "input", "tap", str(w // 10 + w * 37 // 200), str(h * 33 // 100 + h * 3 // 100))
    wait_for(package + " confirms the code", lambda log: has(log, p, "walkie: the string is confirmed"), 10)


def talk(package, talker, listener, w, h):
    to_front(package)
    check_node(logcat(), talker)
    mark = len(lines_of(logcat(), listener))
    # A long press in the middle of screen.talk_button.
    adb("shell", "input", "swipe", str(w // 2), str(h * 68 // 100), str(w // 2), str(h * 68 // 100), "4000")
    log = logcat()
    if not has(log, talker, "walkie: state TRANSMITTING"):
        raise Failure(package + " did not transmit while its button was held")
    if not any("walkie: hearing the peer" in line for line in lines_of(log, listener)[mark:]):
        raise Failure("the other copy did not hear " + package)
    wait_for(package + " stops after the release", lambda log: last_state(log, talker) == "CONNECTED_LISTENING", 10)


def save(out):
    log = logcat()
    with open(f"{out}/logcat.txt", "w") as f:
        f.write(log)
    return log


def screenshot(path):
    image = subprocess.run(["adb", "exec-out", "screencap", "-p"], check=True, capture_output=True, timeout=WAIT_SECONDS).stdout
    if not image.startswith(b"\x89PNG\r\n\x1a\n"):
        raise Failure("the emulator did not return a PNG screenshot")
    with open(path, "wb") as f:
        f.write(image)


def diagnostics(out):
    """Save device state and native stacks before cleanup, within one deadline."""
    deadline = time.monotonic() + WAIT_SECONDS
    sections = []

    def collect(*command):
        remaining = deadline - time.monotonic()
        sections.append("adb " + " ".join(command))
        if remaining <= 0:
            sections.append("diagnostic deadline reached\n")
            return ""
        try:
            text = adb(*command, check=False, timeout=remaining, include_stderr=True)
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired, OSError) as error:
            text = str(error)
        sections.append(text + "\n")
        return text

    collect("logcat", "-d", "-v", "threadtime")
    collect("shell", "ip", "addr")
    collect("shell", "cat", "/proc/net/udp")
    collect("shell", "ps", "-A", "-T")
    collect("shell", "dumpsys", "activity", "processes")
    for package in (FIRST, SECOND):
        process = collect("shell", "pidof", package).strip()
        if process.isdecimal():
            collect("shell", "debuggerd", "-b", process)
        collect("shell", "dumpsys", "activity", "services", package)
    Path(out, "diagnostics.txt").write_text("\n".join(sections))


def run(apk, second_apk, out):
    print("walkie check: disable the cached-app freezer", flush=True)
    disable_freezer()
    for a, package in [(apk, FIRST), (second_apk, SECOND)]:
        print("walkie check: install " + package, flush=True)
        adb("install", "-r", "-g", a)
        adb("shell", "pm", "grant", package, "android.permission.RECORD_AUDIO")
    adb("logcat", "-c")
    a, b = start_both()
    for p in (a, b):
        check_node(logcat(), p)
        if known_phones(logcat(), p) != 0:
            raise Failure("a new copy already knows a phone")
    for package in (FIRST, SECOND):
        wait_for(package + " runs its foreground service", lambda log: foreground(package))
    wait_for("both copies stay awake", lambda log: has(log, a, "walkie: awake with the screen off") and has(log, b, "walkie: awake with the screen off"))
    if adb("shell", "dumpsys", "power").count("fern:walkie") < 2:
        raise Failure("Android does not list a wake lock of each copy")
    wait_for("both copies make keys", lambda log: has(log, a, "walkie: keys are ready") and has(log, b, "walkie: keys are ready"))
    w, h = screen_size()
    confirm(SECOND, b, w, h)
    confirm(FIRST, a, w, h)
    wait_for("each copy sees the other confirm", lambda log: has(log, a, "walkie: the other phone confirmed") and has(log, b, "walkie: the other phone confirmed"), 10)
    wait_for("each copy keeps the identity of the other", lambda log: has(log, a, "walkie: known phones 1") and has(log, b, "walkie: known phones 1"), 10)
    talk(FIRST, a, b, w, h)
    screenshot(f"{out}/first.png")
    talk(SECOND, b, a, w, h)
    screenshot(f"{out}/second.png")
    adb("shell", "input", "keyevent", "KEYCODE_BACK")
    wait_for("BACK moves the second copy to the background", lambda log: has(log, b, "walkie: to the background"), 10)
    time.sleep(20)
    log = logcat()
    if pid(FIRST) != a or pid(SECOND) != b:
        raise Failure("a copy stopped running")
    for p in (a, b):
        check_node(log, p)
    lost = [line for line in log.splitlines() if LOST.search(line)]
    if lost:
        raise Failure("a copy lost its peer: " + "; ".join(lost))
    crashes = [line for line in log.splitlines() if CRASH.search(line)]
    if crashes:
        raise Failure("logcat: " + "; ".join(crashes))
    meet_again()
    type_server(w, h)


def main(apk, second_apk, out):
    Path(out).mkdir(parents=True, exist_ok=True)
    failure = None
    errors = (Failure, subprocess.CalledProcessError, subprocess.TimeoutExpired)
    try:
        run(apk, second_apk, out)
    except errors as e:
        failure = e
    try:
        log = save(out)
        print("\n".join(line for line in log.splitlines() if "walkie" in line or "DEBUG" in line), flush=True)
    except errors as e:
        Path(out, "logcat.txt").write_text(str(e) + "\n")
        failure = failure or e
    if failure is not None:
        try:
            diagnostics(out)
        except OSError as e:
            Path(out, "diagnostics-error.txt").write_text(str(e) + "\n")
    for package in (FIRST, SECOND):
        try:
            adb("shell", "am", "force-stop", package, check=False)
        except errors as e:
            failure = failure or e
    if failure is not None:
        Path(out, "failure.txt").write_text(str(failure) + "\n")
        print(f"FAIL {failure}", flush=True)
        sys.exit(1)
    print("ok", flush=True)


if __name__ == "__main__":
    if len(sys.argv) != 4:
        sys.exit(__doc__)
    main(*sys.argv[1:])
