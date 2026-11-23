#!/usr/bin/env python3
"""Drives the Fern Files APK on a running Android device or emulator.

Usage: emulator_check.py APK OUT_DIR

It installs the APK, gives it access to all files, and puts a text file
and a picture in the Download folder of the device. It then uses the
accessibility tree to operate the app as a person does:

- The home screen shows the storage and does not ask for access.
- The Download folder shows the two files.
- The text file shows its text.
- A copy of the picture arrives in the Pictures folder with the same bytes.
- A delete moves the text file to the trash, and Restore puts it back.
- The two content providers of the app answer.
- Open gives the file to another app.

It fails on a crash in logcat. OUT_DIR gets the frames, the trees and
logcat.
"""

import hashlib
from pathlib import Path
import re
import struct
import subprocess
import sys
import time
import xml.etree.ElementTree as ET
import zlib

sys.path.insert(0, str(Path(__file__).resolve().parents[3] / "tests" / "android"))
from emulator import adb, crashes, fail, failures, finish, logcat, root, save_log, screen  # noqa: E402

PACKAGE = "dev.fern.files"
ACTIVITY = PACKAGE + "/android.app.NativeActivity"
STORAGE = "/storage/emulated/0"
NOTE = "notes-check.txt"
NOTE_TEXT = "The emulator check of Fern Files wrote this line."
PICTURE = "dot-check.png"
TREE = "/sdcard/fern-files-ui.xml"


def png(width, height, rgb):
    """The bytes of a PNG of one colour."""
    def chunk(kind, data):
        body = kind + data
        return struct.pack(">I", len(data)) + body + struct.pack(">I", zlib.crc32(body))
    row = b"\x00" + bytes(rgb) * width
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(row * height)) + chunk(b"IEND", b""))


def nodes(xml):
    """Each node of the app in a uiautomator dump: its name and the middle
    of its bounds. The name is its text, or else its content description."""
    try:
        tree = ET.fromstring(xml)
    except ET.ParseError:
        return []
    out = []
    for node in tree.iter("node"):
        if node.get("package") != PACKAGE:
            continue
        bounds = re.fullmatch(r"\[(-?\d+),(-?\d+)\]\[(-?\d+),(-?\d+)\]", node.get("bounds", ""))
        if not bounds:
            continue
        x, y, right, bottom = map(int, bounds.groups())
        if right <= x or bottom <= y:
            continue
        for name in (node.get("text"), node.get("content-desc")):
            if name:
                out.append((name, (x + right) // 2, (y + bottom) // 2))
    return out


def point_of(xml, label):
    """The middle of the first node with the name `label`, or None."""
    for name, x, y in nodes(xml):
        if name == label:
            return x, y
    return None


def trash_entry(listing, name):
    """The entry of the trash that holds the file `name`, or None. An entry
    has a time, the folder of the file in base64, and the name."""
    for line in listing.splitlines():
        parts = line.strip().split("~", 2)
        if len(parts) == 3 and parts[0].isdigit() and parts[2] == name:
            return line.strip()
    return None


def digest(text):
    """The first word of the output of sha256sum, or "" for no output."""
    words = text.split()
    return words[0] if words else ""


def dump(out, step):
    # A dump that fails must not give the tree of the screen before it.
    adb("shell", "rm", "-f", TREE, check=False)
    adb("shell", "uiautomator", "dump", TREE, check=False)
    xml = adb("shell", "cat", TREE, check=False)
    with open(f"{out}/{step}.xml", "w") as f:
        f.write(xml)
    return xml


def screen_size(text):
    """The width and the height in the output of `wm size`, or None. An
    override of the size comes after the physical size."""
    sizes = re.findall(r"(\d+)x(\d+)", text)
    return tuple(map(int, sizes[-1])) if sizes else None


def scroll():
    """Moves a list up by a third of the screen."""
    width, height = screen_size(adb("shell", "wm", "size", check=False)) or (1080, 1920)
    x = str(width // 2)
    adb("shell", "input", "swipe", x, str(height * 2 // 3), x, str(height // 3), "400")
    time.sleep(0.7)


def find(out, step, label, timeout=30):
    """The middle of the node `label`, when it shows in `timeout` seconds.
    The tree has only the rows on the screen, so each third look without
    the node moves the list up."""
    deadline = time.monotonic() + timeout
    looks = 0
    while time.monotonic() < deadline:
        point = point_of(dump(out, step), label)
        if point:
            return point
        looks += 1
        if looks % 3 == 0:
            scroll()
        time.sleep(0.5)
    return None


def tap(out, step, label, hold=False):
    """Taps the node `label`, or holds it. False when it does not show."""
    point = find(out, step, label)
    if not point:
        screen(out, step)
        shown = sorted({name for name, _, _ in nodes(dump(out, step))})
        fail(f"{step}: the screen does not show {label}. It shows {shown}")
        return False
    x, y = map(str, point)
    if hold:
        adb("shell", "input", "swipe", x, y, x, y, "900")
    else:
        adb("shell", "input", "tap", x, y)
    time.sleep(0.5)
    return True


def taps(out, step, labels):
    return all(tap(out, f"{step}-{at}", label) for at, label in enumerate(labels))


def device_has(path):
    return subprocess.run(["adb", "shell", "test", "-e", path]).returncode == 0


def until(test, timeout=20):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if test():
            return True
        time.sleep(0.5)
    return False


def sum_of(path):
    return digest(adb("shell", "sha256sum", path, check=False))


def start():
    adb("shell", "am", "force-stop", PACKAGE)
    adb("shell", "am", "start", "-W", "-n", ACTIVITY)


def check_home(out):
    start()
    if not find(out, "home", "Phone storage"):
        screen(out, "home")
        return fail("the home screen does not show the storage of the phone")
    if point_of(dump(out, "home"), "Allow access"):
        fail("the app asks for access that it has")


def check_text(out):
    if not taps(out, "text", ["Downloads", NOTE, "Preview as text"]):
        return
    if not until(lambda: any(NOTE_TEXT in name for name, _, _ in nodes(dump(out, "text-shown")))):
        screen(out, "text-shown")
        fail("the text file does not show its text")
    screen(out, "text-shown")


def check_copy(out, picture):
    start()
    # The clipboard is in the memory of the app, so the Home button of the
    # app goes to Pictures, and not a new start.
    if not taps(out, "copy", ["Downloads", PICTURE, "Copy", "Home", "Pictures", "Paste here"]):
        return
    want = hashlib.sha256(picture).hexdigest()
    target = f"{STORAGE}/Pictures/{PICTURE}"
    if not until(lambda: sum_of(target) == want):
        screen(out, "paste-done")
        fail(f"the copy in Pictures has the digest {sum_of(target) or 'of no file'}, not {want}")


def check_trash(out):
    start()
    source = f"{STORAGE}/Download/{NOTE}"
    if not taps(out, "delete", ["Downloads", NOTE, "Delete", "Confirm delete"]):
        return
    listing = lambda: adb("shell", "ls", "-a", STORAGE + "/.FernTrash", check=False)
    if not until(lambda: trash_entry(listing(), NOTE) and not device_has(source)):
        screen(out, "delete-done")
        return fail("the delete did not move the text file to the trash")
    start()
    if not tap(out, "trash", "Trash") or not tap(out, "trash-hold", NOTE, hold=True) or not tap(out, "restore", "Restore"):
        return
    if not until(lambda: NOTE_TEXT in adb("shell", "cat", source, check=False)):
        screen(out, "restore-done")
        fail("Restore did not put the text file back in Download")


def check_providers(out):
    if not root():
        return print("the device refuses root: the providers are not read", flush=True)
    shared = subprocess.run(["adb", "shell", "content", "read", "--uri", f"content://{PACKAGE}.Shared{STORAGE}/Download/{NOTE}"],
                            capture_output=True, text=True)
    if NOTE_TEXT not in shared.stdout:
        fail(f"the provider of shared files gave {shared.stdout!r} {shared.stderr!r}")
    row = adb("shell", "content", "query", "--uri", f"content://{PACKAGE}.Shared{STORAGE}/Download/{NOTE}", check=False)
    if NOTE not in row or str(len(NOTE_TEXT) + 1) not in row:
        fail(f"the provider of shared files describes the file as {row!r}")
    servers = subprocess.run(["adb", "shell", "content", "query", "--uri", f"content://{PACKAGE}.Documents/root"],
                             capture_output=True, text=True)
    if "No result found" not in servers.stdout:
        fail(f"the provider of documents gave {servers.stdout!r} {servers.stderr!r} for no saved server")
    with open(f"{out}/providers.txt", "w") as f:
        f.write(shared.stdout + row + servers.stdout + servers.stderr)


def check_open(out):
    start()
    if not taps(out, "open", ["Downloads", NOTE, "Open"]):
        return
    # Another activity comes to the front: a viewer, or the list of them.
    front = lambda: adb("shell", "dumpsys", "activity", "activities", check=False)
    if not until(lambda: any("ResumedActivity" in line and PACKAGE + "/" not in line for line in front().splitlines())):
        screen(out, "open-done")
        if not point_of(dump(out, "open-done"), "No app on this phone opens this file."):
            fail("Open did not give the file to another app and did not say so")
    screen(out, "open-done")
    adb("shell", "input", "keyevent", "4")


def main(apk, out):
    subprocess.run(["mkdir", "-p", out], check=True)
    picture = png(48, 48, (215, 25, 33))
    Path(out, NOTE).write_text(NOTE_TEXT + "\n")
    Path(out, PICTURE).write_bytes(picture)
    adb("install", "-r", apk)
    adb("shell", "appops", "set", PACKAGE, "MANAGE_EXTERNAL_STORAGE", "allow")
    adb("shell", "rm", "-rf", f"{STORAGE}/.FernTrash", f"{STORAGE}/Pictures/{PICTURE}", check=False)
    adb("shell", "mkdir", "-p", f"{STORAGE}/Download", f"{STORAGE}/Pictures")
    adb("push", f"{out}/{NOTE}", f"{STORAGE}/Download/{NOTE}")
    adb("push", f"{out}/{PICTURE}", f"{STORAGE}/Download/{PICTURE}")
    adb("logcat", "-c")

    check_home(out)
    if not failures:
        check_text(out)
        check_copy(out, picture)
        check_trash(out)
        check_providers(out)
        check_open(out)

    log = logcat()
    with open(f"{out}/logcat.txt", "w") as f:
        f.write(log)
    for line in crashes(log):
        fail("logcat: " + line)
    save_log(out, "logcat-all.txt")
    adb("shell", "am", "force-stop", PACKAGE, check=False)
    if failures:
        print(f"{len(failures)} failures", flush=True)
    finish()


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    main(*sys.argv[1:])
