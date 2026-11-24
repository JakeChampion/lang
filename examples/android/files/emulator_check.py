#!/usr/bin/env python3
"""Drives the Fern Files APK on a running Android device or emulator.

Usage: emulator_check.py APK OUT_DIR [SHARE_DIR PORT [DAV_DIR DAV_PORT]]

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

With SHARE_DIR and PORT, it also uses an SMB server on this computer,
which scripts/files-samba-serve starts. SHARE_DIR is the directory of its
share. The emulator reaches the computer at 10.0.2.2.

- The form of a server takes the address, the user and the password, and
  the server gives the name of its share.
- The folder of the server shows a file of the share, and its text.
- A copy of that file arrives in the Download folder with the same bytes.
- A copy of the picture arrives on the share with the same bytes.
- As root, the provider of documents gives the saved server as a root,
  the file of the share as a child of that root, and the text of the file.
  A write to that file through the provider reaches the share.

With DAV_DIR and DAV_PORT, it also adds the WebDAV server on this computer
that scripts/files-dav-serve starts, and does the same read and copies
with it. DAV_DIR is the directory of its folder.

It fails on a crash in logcat. OUT_DIR gets the frames, the trees and
logcat.
"""

import hashlib
import os
from pathlib import Path
import re
import struct
import subprocess
import sys
import time
import urllib.parse
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
# The address of the computer that runs the emulator, from the emulator.
HOST = "10.0.2.2"
SMB_USER = os.environ.get("FILES_SMB_USER", "fernfiles")
SMB_PASSWORD = os.environ.get("FILES_SMB_PASSWORD", "live-check-1")
SMB_NOTE = "smb-check.txt"
SMB_TEXT = "The share of the emulator check has this line."
CHANGED_TEXT = "Another app changed this file through the file picker.\n"
DAV_USER = os.environ.get("FILES_DAV_USER", "fernfiles")
DAV_PASSWORD = os.environ.get("FILES_DAV_PASSWORD", "live-check-1")
DAV_NOTE = "dav-check.txt"
DAV_TEXT = "The WebDAV folder of the emulator check has this line."


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


def field_point(xml, hint):
    """The middle of the text field with the hint `hint`, or None. The name
    of an empty field is its hint, and the name of a field with text starts
    with the hint and a comma. A label can have the same words, so only a
    node that takes a tap is a field."""
    try:
        tree = ET.fromstring(xml)
    except ET.ParseError:
        return None
    for node in tree.iter("node"):
        name = node.get("content-desc") or node.get("text") or ""
        if node.get("package") != PACKAGE or node.get("clickable") != "true":
            continue
        if name != hint and not name.startswith(hint + ", "):
            continue
        bounds = re.fullmatch(r"\[(-?\d+),(-?\d+)\]\[(-?\d+),(-?\d+)\]", node.get("bounds", ""))
        if bounds:
            x, y, right, bottom = map(int, bounds.groups())
            if right > x and bottom > y:
                return (x + right) // 2, (y + bottom) // 2
    return None


def crash_blocks(log):
    """Each crash in `log`: the line of the fatal exception and the lines of
    the same process after it, which name the process and give the stack."""
    lines = log.splitlines()
    blocks = []
    for at, line in enumerate(lines):
        if not re.match(r"E/AndroidRuntime\(\s*\d+\): FATAL EXCEPTION", line):
            continue
        tag = line[:line.index(")") + 1]
        block = [line]
        for after in lines[at + 1:at + 40]:
            if not after.startswith(tag):
                break
            block.append(after)
        blocks.append("\n".join(block))
    return blocks


def keyboard_shown(dumpsys):
    """True when `dumpsys input_method` shows the keyboard on the screen."""
    return "mInputShown=true" in dumpsys


def document_uri(document_id, children=False):
    """The URI of a document of the provider of documents, as
    DocumentsContract builds it: the ID is one encoded part of the path."""
    uri = f"content://{PACKAGE}.Documents/document/{urllib.parse.quote(document_id, safe='')}"
    return uri + "/children" if children else uri


def trash_entry(listing, name):
    """The entry of the trash that holds the file `name`, or None. An entry
    has a time, the folder of the file in base64, and the name."""
    for line in listing.splitlines():
        parts = line.strip().split("~", 2)
        if len(parts) == 3 and parts[0].isdigit() and parts[2] == name:
            return line.strip()
    return None


def swipe_up(xml):
    """The two points of a swipe that moves the screen of a uiautomator dump
    up by two fifths of its height, or None when the dump has no screen. The
    swipe starts above the middle, because the bars of a task and of the
    clipboard can cover the bottom of the screen, and they do not scroll."""
    try:
        top = ET.fromstring(xml).find("node")
    except ET.ParseError:
        return None
    bounds = re.fullmatch(r"\[(-?\d+),(-?\d+)\]\[(-?\d+),(-?\d+)\]", top.get("bounds", "") if top is not None else "")
    if not bounds:
        return None
    x, y, right, bottom = map(int, bounds.groups())
    if right <= x or bottom <= y:
        return None
    middle, height = (x + right) // 2, bottom - y
    return (middle, y + height * 11 // 20), (middle, y + height * 3 // 20)


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


def find(out, step, label, timeout=30, scroll=False):
    """The middle of the node `label`, when it shows in `timeout` seconds.
    With `scroll`, each dump that does not show it scrolls the screen down,
    and the time is 60 seconds at least. When two scrolls in a row do not
    change the names on the screen, the list is at its end, and the next
    scrolls go the other way."""
    deadline = time.monotonic() + (max(timeout, 60) if scroll else timeout)
    down = True
    before = None
    still = 0
    while time.monotonic() < deadline:
        xml = dump(out, step)
        point = point_of(xml, label)
        if point:
            return point
        swipe = swipe_up(xml) if scroll else None
        if swipe:
            names = sorted(name for name, _, _ in nodes(xml))
            still = still + 1 if names == before else 0
            if still >= 2:
                down = not down
                still = 0
            before = names
            print(f"{step}: no {label}, scroll {'down' if down else 'up'} from {names[:2]} … {names[-2:]}", flush=True)
            (x, y), (x2, y2) = swipe if down else reversed(swipe)
            # A slow drag scrolls the list and does not fling it.
            adb("shell", "input", "swipe", str(x), str(y), str(x2), str(y2), "800")
            time.sleep(1)
        else:
            time.sleep(0.5)
    return None


def focused_window(dumpsys):
    """The window that has the focus in a dump of `dumpsys window`: its
    component, or the word that the dump gives, or "" for no such line."""
    m = re.search(r"mCurrentFocus=Window\{\S+ \S+ ([^}]+)\}", dumpsys) or re.search(r"mCurrentFocus=(\S+)", dumpsys)
    return m.group(1) if m else ""


def front_window():
    return focused_window(adb("shell", "dumpsys", "window", "windows", check=False))


def tap(out, step, label, hold=False, scroll=False):
    """Taps the node `label`, or holds it. False when it does not show."""
    point = find(out, step, label, scroll=scroll)
    if not point:
        screen(out, step)
        shown = sorted({name for name, _, _ in nodes(dump(out, step))})
        fail(f"{step}: the screen does not show {label}. It shows {shown}, and the window in front is {front_window()!r}")
        return False
    x, y = map(str, point)
    if hold:
        adb("shell", "input", "swipe", x, y, x, y, "900")
    else:
        adb("shell", "input", "tap", x, y)
    time.sleep(0.5)
    return True


def taps(out, step, labels):
    return all(tap(out, f"{step}-{at}", label, scroll=True) for at, label in enumerate(labels))


def type_in(out, step, hint, text):
    """Types `text` in the field with the hint `hint`. False when the field
    does not show or does not take the text."""
    xml = dump(out, step)
    point = field_point(xml, hint)
    if not point:
        screen(out, step)
        fail(f"{step}: the screen has no field {hint}. It shows {sorted({name for name, _, _ in nodes(xml)})}")
        return False
    adb("shell", "input", "tap", str(point[0]), str(point[1]))
    time.sleep(1.5)
    adb("shell", "input", "text", text)
    time.sleep(1)
    # The keyboard covers the fields below, and the back key closes it.
    if keyboard_shown(adb("shell", "dumpsys", "input_method", check=False)):
        adb("shell", "input", "keyevent", "4")
        time.sleep(1)
    names = [name for name, _, _ in nodes(dump(out, step + "-typed"))]
    if not any(name.startswith(hint + ", ") for name in names):
        screen(out, step + "-typed")
        fail(f"{step}: the field {hint} did not take the text. The screen shows {sorted(set(names))}")
        return False
    return True


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
    # The Trash row is below the fold of a small screen.
    if not tap(out, "trash", "Trash", scroll=True) or not tap(out, "trash-hold", NOTE, hold=True) or not tap(out, "restore", "Restore"):
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


def check_server(out, share_dir, port):
    server = f"share on {HOST}"
    Path(share_dir, SMB_NOTE).write_text(SMB_TEXT + "\n")
    start()
    if not tap(out, "server-add", "Add a server", scroll=True):
        return
    if not (type_in(out, "server-host", "192.168.1.10 or nas.local:4450", f"{HOST}:{port}")
            and type_in(out, "server-user", "Leave empty for a guest", SMB_USER)
            and type_in(out, "server-password", "Password", SMB_PASSWORD)):
        return
    # The server gives the names of its shares over the srvsvc pipe.
    if not (tap(out, "server-shares", "Find shares", scroll=True) and tap(out, "server-share", "Share share", scroll=True)
            and tap(out, "server-save", "Save", scroll=True)):
        return
    round_trip(out, "smb", server, SMB_NOTE, SMB_TEXT, share_dir)


def round_trip(out, kind, server, note, text, share_dir):
    """Opens the saved server `server` and shows the text of its file
    `note`, copies that file to the Download folder, and copies the picture
    to the server. `share_dir` is the directory of the server on this
    computer. The names of the steps start with `kind`."""
    if not tap(out, f"{kind}-open", server, scroll=True) or not taps(out, f"{kind}-text", [note, "Preview as text"]):
        return
    if not until(lambda: any(text in name for name, _, _ in nodes(dump(out, f"{kind}-text-shown")))):
        screen(out, f"{kind}-text-shown")
        fail(f"{kind}: the file of the server does not show its text")
    screen(out, f"{kind}-text-shown")

    # From the server to the phone. A new start reads the saved server again.
    start()
    if not tap(out, f"{kind}-download-open", server, scroll=True) or not taps(out, f"{kind}-download", [note, "Copy", "Home", "Downloads", "Paste here"]):
        return
    want = hashlib.sha256((text + "\n").encode()).hexdigest()
    target = f"{STORAGE}/Download/{note}"
    if not until(lambda: sum_of(target) == want):
        screen(out, f"{kind}-download-done")
        fail(f"{kind}: the copy from the server has the digest {sum_of(target) or 'of no file'}, not {want}")

    # From the phone to the server.
    if not taps(out, f"{kind}-upload", [PICTURE, "Copy", "Home"]) or not tap(out, f"{kind}-upload-open", server, scroll=True) or not tap(out, f"{kind}-upload-paste", "Paste here"):
        return
    sent = Path(share_dir, PICTURE)
    picture = Path(out, PICTURE).read_bytes()
    if not until(lambda: sent.exists() and sent.read_bytes() == picture):
        screen(out, f"{kind}-upload-done")
        fail(f"{kind}: the copy of the picture did not arrive on the server with the same bytes")


def check_dav(out, dav_dir, port):
    """Adds the WebDAV server on this computer in the form, and does the
    round trip with it."""
    server = f"files on {HOST}"
    Path(dav_dir, DAV_NOTE).write_text(DAV_TEXT + "\n")
    start()
    if not tap(out, "dav-add", "Add a server", scroll=True) or not tap(out, "dav-kind", "WebDAV server"):
        return
    if not (type_in(out, "dav-address", "https://nas.local:5006/home", f"http://{HOST}:{port}/files")
            and type_in(out, "dav-user", "Leave empty for no user", DAV_USER)
            and type_in(out, "dav-password", "Password", DAV_PASSWORD)
            and tap(out, "dav-save", "Save", scroll=True)):
        return
    round_trip(out, "dav", server, DAV_NOTE, DAV_TEXT, dav_dir)


def check_documents(out, share_dir):
    """Reads the saved server through the provider of documents, which the
    file picker of the system uses, and writes a file of it. The first
    server has the ID 0."""
    if not root():
        return print("the device refuses root: the provider of documents is not read", flush=True)
    queries = {
        "roots": ["query", "--uri", f"content://{PACKAGE}.Documents/root"],
        "children": ["query", "--uri", document_uri("0:", children=True)],
        "text": ["read", "--uri", document_uri("0:" + SMB_NOTE)],
    }
    got = {name: subprocess.run(["adb", "shell", "content", *args], capture_output=True, text=True) for name, args in queries.items()}
    with open(f"{out}/documents.txt", "w") as f:
        for name, done in got.items():
            f.write(f"== {name}\n{done.stdout}{done.stderr}")
    if f"smb://{HOST}/share" not in got["roots"].stdout:
        fail(f"the provider of documents does not give the saved server as a root: {got['roots'].stdout!r} {got['roots'].stderr!r}")
    if f"_display_name={SMB_NOTE}" not in got["children"].stdout:
        fail(f"the provider of documents does not give the file of the share: {got['children'].stdout!r} {got['children'].stderr!r}")
    if SMB_TEXT not in got["text"].stdout:
        fail(f"the provider of documents does not give the text of the file: {got['text'].stdout!r} {got['text'].stderr!r}")
    # A write goes to the cache of the app, and to the server when the
    # writer closes the file.
    wrote = subprocess.run(["adb", "shell", "content", "write", "--uri", document_uri("0:" + SMB_NOTE)],
                           input=CHANGED_TEXT, capture_output=True, text=True)
    target = Path(share_dir, SMB_NOTE)
    if not until(lambda: target.exists() and target.read_text() == CHANGED_TEXT):
        now = target.read_text() if target.exists() else "no file"
        fail(f"a write through the provider of documents did not reach the share: it holds {now!r}, and the write gave {wrote.stdout!r} {wrote.stderr!r}")


def main(apk, out, share_dir=None, port=None, dav_dir=None, dav_port=None):
    subprocess.run(["mkdir", "-p", out], check=True)
    picture = png(48, 48, (215, 25, 33))
    Path(out, NOTE).write_text(NOTE_TEXT + "\n")
    Path(out, PICTURE).write_bytes(picture)
    adb("install", "-r", apk)
    adb("shell", "appops", "set", PACKAGE, "MANAGE_EXTERNAL_STORAGE", "allow")
    # A copy shows a notification. Without this permission the first copy
    # asks for it, and the dialog of Android covers the app.
    adb("shell", "pm", "grant", PACKAGE, "android.permission.POST_NOTIFICATIONS", check=False)
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
        if share_dir:
            check_server(out, share_dir, port)
            check_documents(out, share_dir)
        if dav_dir:
            check_dav(out, dav_dir, dav_port)

    log = logcat()
    with open(f"{out}/logcat.txt", "w") as f:
        f.write(log)
    for line in crashes(log):
        fail("logcat: " + line)
    for block in crash_blocks(log):
        print(block, flush=True)
    save_log(out, "logcat-all.txt")
    adb("shell", "am", "force-stop", PACKAGE, check=False)
    if failures:
        print(f"{len(failures)} failures", flush=True)
    finish()


if __name__ == "__main__":
    if len(sys.argv) not in (3, 5, 7):
        sys.exit(__doc__)
    main(*sys.argv[1:])
