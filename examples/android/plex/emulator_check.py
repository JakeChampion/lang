#!/usr/bin/env python3
"""Runs Fern for Plex on a running Android device or emulator against a fake
Plex server on the host.

Usage: emulator_check.py APK OUT_DIR [VIDEO.mp4]

The server answers the requests of the app with fixed JSON, a PNG poster, a
WAV track and the MP4 file, and records each request. The emulator reaches
the host at 10.0.2.2. The app gets the address and a token in the extras of
its intent, so it needs no sign-in at plex.tv. The check requires:

- the home screen shows the posters, which the app decoded with the image
  decoder of Android;
- a link plays the track to its end, reports the playback to the server
  and marks the track watched;
- the D-pad of a remote moves the focus to the first poster, and the
  centre key opens it;
- a track plays on to its end after the home key sends the app away, and
  the media session of Android shows the app;
- a link to an artist plays its two tracks, and the second one follows
  the first with no gap, from the second media player;
- the accessibility tree of the home screen names each poster, as
  TalkBack reads it;
- with VIDEO.mp4, a link shows the video on a texture that the app draws.
  The 4:3 pattern fills the width with black bars above and below it, and
  it is upright. A tap shows the controls over it with no pause, and a tap
  on the play button pauses it. Paused, the app draws the controls, and a
  tap on the play button shows the video again, upright, then the back key
  stops it. The home key during the video puts it in a picture-in-picture
  window;
- downloading Square Tone from its poster menu fetches the authenticated
  media file and changes the menu to Remove download;
- logcat shows no crash.

OUT_DIR gets logcat, the screens and the requests that the server saw.
"""

import http.server
import os
import re
import struct
import subprocess
import sys
import threading
import time
import wave
import xml.etree.ElementTree as ET
import zlib

PACKAGE = "dev.fern.plex"
POSTER = (30, 170, 160)
PLAY = (215, 25, 33)
CRASH = re.compile(r"Fatal signal|UnsatisfiedLinkError|java\.lang\.\w+(Exception|Error)|fern: |worker: |F DEBUG")

failures = []
requests = []


def fail(message):
    failures.append(message)
    print("FAIL " + message, flush=True)


def png(width, height, rgb):
    row = b"\x00" + bytes(rgb) * width
    raw = zlib.compress(row * height)

    def chunk(kind, data):
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data) & 0xFFFFFFFF)

    return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0)) + chunk(b"IDAT", raw) + chunk(b"IEND", b"")


def tone(path, seconds):
    with wave.open(path, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(44100)
        frames = bytearray()
        for i in range(int(44100 * seconds)):
            v = int(8000 * (1 if (i // 50) % 2 else -1))
            frames += struct.pack("<h", v)
        w.writeframes(bytes(frames))


def item(key, kind, title, extra):
    base = '"ratingKey":"%s","key":"/library/metadata/%s","type":"%s","title":"%s","thumb":"/library/metadata/%s/thumb"' % (key, key, kind, title, key)
    return "{" + base + extra + "}"


TRACK = item("900", "track", "Square Tone", ',"parentTitle":"Test Album","grandparentTitle":"Fern","duration":3000,"Media":[{"container":"wav","audioCodec":"pcm","Part":[{"key":"/library/parts/900/tone.wav","container":"wav"}]}]')
FIRST = item("911", "track", "First Tone", ',"parentTitle":"Test Album","grandparentTitle":"Fern","duration":2000,"Media":[{"container":"wav","audioCodec":"pcm","Part":[{"key":"/library/parts/911/tone.wav","container":"wav"}]}]')
SECOND = item("912", "track", "Second Tone", ',"parentTitle":"Test Album","grandparentTitle":"Fern","duration":2000,"Media":[{"container":"wav","audioCodec":"pcm","Part":[{"key":"/library/parts/912/tone.wav","container":"wav"}]}]')
ARTIST = item("920", "artist", "Fern", "")
MOVIE = item("901", "movie", "Test Pattern", ',"year":2026,"duration":20000,"summary":"Bars and a clock.","Media":[{"container":"mp4","videoCodec":"h264","videoResolution":"240","Part":[{"key":"/library/parts/901/pattern.mp4","container":"mp4"}]}]')


def container(body):
    return '{"MediaContainer":{' + body + "}}"


ROUTES = {
    "/identity": container('"machineIdentifier":"fake"'),
    "/library/sections": container('"Directory":[{"key":"1","type":"movie","title":"Movies"},{"key":"3","type":"artist","title":"Music"}]'),
    "/hubs": container('"Hub":[{"title":"Recently Added","hubIdentifier":"home.recent","Metadata":[' + MOVIE + "," + TRACK + "]}]"),
    "/library/metadata/900": container('"Metadata":[' + TRACK + "]"),
    "/library/metadata/901": container('"Metadata":[' + MOVIE + "]"),
    "/library/metadata/920": container('"Metadata":[' + ARTIST + "]"),
    "/library/metadata/920/children": container('"Metadata":[' + item("910", "album", "Test Album", "") + "]"),
    "/library/metadata/920/allLeaves": container('"Metadata":[' + FIRST + "," + SECOND + "]"),
}


DOWNLOAD_PATH = "/library/parts/900/tone.wav?download=1"


def server(files):
    poster = png(200, 300, POSTER)

    class Handler(http.server.BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, *args):
            pass

        def send(self, status, body, kind):
            start, end = 0, len(body)
            rng = self.headers.get("Range", "")
            m = re.match(r"bytes=(\d*)-(\d*)", rng)
            if m and status == 200 and len(body) > 0:
                if m.group(1):
                    start = int(m.group(1))
                if m.group(2):
                    end = min(end, int(m.group(2)) + 1)
                status = 206
            self.send_response(status)
            self.send_header("Content-Type", kind)
            self.send_header("Accept-Ranges", "bytes")
            if status == 206:
                self.send_header("Content-Range", "bytes %d-%d/%d" % (start, end - 1, len(body)))
            self.send_header("Content-Length", str(end - start))
            self.end_headers()
            if self.command != "HEAD":
                self.wfile.write(body[start:end])

        def do_HEAD(self):
            self.do_GET()

        def do_GET(self):
            requests.append(self.command + " " + self.path)
            path = self.path.split("?")[0]
            if self.path == DOWNLOAD_PATH and self.headers.get("X-Plex-Token") != "srv":
                return self.send(403, b"", "text/plain")
            if path in ROUTES:
                return self.send(200, ROUTES[path].encode(), "application/json")
            if path == "/photo/:/transcode":
                return self.send(200, poster, "image/png")
            if path in files:
                with open(files[path], "rb") as f:
                    data = f.read()
                return self.send(200, data, "video/mp4" if path.endswith(".mp4") else "audio/wav")
            if path in ("/:/timeline", "/:/scrobble"):
                return self.send(200, b"", "text/plain")
            self.send(404, b"", "text/plain")

    httpd = http.server.ThreadingHTTPServer(("0.0.0.0", 0), Handler)
    threading.Thread(target=httpd.serve_forever, daemon=True).start()
    return httpd


def adb(*args, check=True):
    return subprocess.run(["adb", *args], check=check, stdout=subprocess.PIPE, text=True).stdout


def logcat():
    return adb("logcat", "-d", "-v", "brief", "fern:I", "AndroidRuntime:E", "DEBUG:*", "libc:*", "*:F")


def wait_for(test, timeout=40):
    deadline = time.time() + timeout
    while time.time() < deadline:
        if test():
            return True
        time.sleep(0.5)
    return False


def seen(part):
    return any(part in r for r in requests)


def count_log(pattern):
    return len(re.findall(pattern, logcat()))


def screen(out, name):
    """The width, the height and the pixels of the screen, as RGB tuples."""
    raw = subprocess.run(["adb", "exec-out", "screencap"], check=True, capture_output=True).stdout
    subprocess.run(f"adb exec-out screencap -p > {out}/{name}.png", shell=True, check=False)
    width, height, _ = struct.unpack_from("<III", raw)
    head = len(raw) - width * height * 4
    return width, height, raw[head:]


def count_color(px, rgb):
    n = 0
    for at in range(0, len(px), 4):
        if abs(px[at] - rgb[0]) < 6 and abs(px[at + 1] - rgb[1]) < 6 and abs(px[at + 2] - rgb[2]) < 6:
            n += 1
    return n


def colors_in(width, height, px, box):
    x0, y0, x1, y1 = box
    out = set()
    for y in range(y0, y1, 4):
        for x in range(x0, x1, 4):
            at = (y * width + x) * 4
            out.add((px[at] >> 4, px[at + 1] >> 4, px[at + 2] >> 4))
    return len(out)


def video_colors(width, height, px):
    # The test pattern has flat bars in its center and a gradient near its bottom.
    # Include that gradient and exclude the outer edge of the window.
    return colors_in(width, height, px, (width // 20, height // 20,
                                        width * 19 // 20, height * 19 // 20))


def lit_rows(width, height, px):
    """The first and the last row with picture, or None for a black screen."""
    need = max(1, width // 4 // 10)
    rows = []
    for y in range(height):
        lit = 0
        for x in range(0, width, 4):
            at = (y * width + x) * 4
            if px[at] + px[at + 1] + px[at + 2] > 60:
                lit += 1
        if lit >= need:
            rows.append(y)
    if not rows:
        return None
    return rows[0], rows[-1] + 1


def frame_fault(width, height, px):
    """What is wrong with a screen of the 4:3 test pattern, or "".

    On a portrait screen the pattern fills the width, with black bars above
    and below it. Its rainbow band is in its lower half, so that half has
    more colours than the upper half when the video is upright.
    """
    rows = lit_rows(width, height, px)
    if rows is None:
        return "the screen is black"
    top, bottom = rows
    shape = (bottom - top) / width
    if not 0.65 <= shape <= 0.85 or top < height // 20:
        return f"the video is rows {top} to {bottom} of {height} on {width} columns, not 4:3 with bars"
    mid = (top + bottom) // 2
    upper = colors_in(width, height, px, (0, top, width, mid))
    lower = colors_in(width, height, px, (0, mid, width, bottom))
    if lower <= upper:
        return f"the rainbow of the pattern is not in its lower half ({upper} colours above, {lower} below), so the video is upside down"
    return ""


def start(port, play=""):
    adb("shell", "am", "force-stop", PACKAGE)
    args = ["shell", "am", "start", "-W", "-n", PACKAGE + "/android.app.NativeActivity",
            "--es", "server", f"http://10.0.2.2:{port}", "--es", "token", "srv"]
    if play:
        args += ["--es", "play", play]
    adb(*args)


def check_home(out, port):
    start(port)
    if not wait_for(lambda: seen("/hubs") and seen("/photo/:/transcode")):
        return fail("the home screen asked for no hubs or no posters")
    time.sleep(3)
    width, height, px = screen(out, "home")
    posters = count_color(px, POSTER)
    print(f"home: {width}x{height}, {posters} poster pixels", flush=True)
    if posters < 2000:
        fail(f"the home screen shows {posters} pixels of the posters")


def check_track(out, port):
    start(port, "900")
    if not wait_for(lambda: "plex: prepared 900" in logcat()):
        return fail("the track did not prepare")
    if not wait_for(lambda: seen("ratingKey=900&key=%2Flibrary%2Fmetadata%2F900&state=playing")):
        fail("no report that the track plays")
    if not wait_for(lambda: seen("/:/scrobble?identifier=com.plexapp.plugins.library&key=900"), timeout=20):
        fail("the track did not play to its end and become watched")
    if not seen("ratingKey=900&key=%2Flibrary%2Fmetadata%2F900&state=stopped"):
        fail("no report that the track stopped")


def requests_for(part):
    return sum(1 for r in requests if part in r)


def check_remote(out, port):
    start(port)
    if not wait_for(lambda: seen("/hubs") and seen("/photo/:/transcode")):
        return fail("the home screen asked for no hubs or no posters")
    time.sleep(2)
    before = requests_for("/library/metadata/901")
    adb("shell", "input", "keyevent", "20")
    time.sleep(1)
    screen(out, "focus")
    adb("shell", "input", "keyevent", "23")
    if not wait_for(lambda: requests_for("/library/metadata/901") > before):
        fail("down and the centre key did not open the first poster")


def check_background(out, port):
    start(port, "900")
    if not wait_for(lambda: "plex: prepared 900" in logcat()):
        return fail("the track did not prepare")
    adb("shell", "input", "keyevent", "3")
    session = adb("shell", "dumpsys", "media_session", check=False)
    if PACKAGE not in session:
        fail("the media session of Android does not show the app")
    if not wait_for(lambda: requests_for("/:/scrobble?identifier=com.plexapp.plugins.library&key=900") >= 2, timeout=20):
        fail("the track did not play on to its end after the home key")


def check_video(out, port):
    start(port, "901")
    if not wait_for(lambda: count_log(r"plex: video on the window") >= 1):
        return fail("the video did not go on the window: " + " | ".join(l for l in logcat().splitlines() if "plex" in l))
    if "plex: video on a texture" not in logcat():
        return fail("the video is not on a texture, so the app does not draw it: " + " | ".join(l for l in logcat().splitlines() if "plex" in l))
    time.sleep(2)
    width, height, px = screen(out, "video")
    colors = video_colors(width, height, px)
    print(f"video: {colors} colours inside the screen edges", flush=True)
    if colors < 12:
        fail(f"the video shows {colors} colours")
    fault = frame_fault(width, height, px)
    if fault:
        fail("video: " + fault)
    # A tap shows the controls over the video, and the video plays on. The
    # play button is in the middle.
    adb("shell", "input", "tap", str(width // 2), str(height // 2))
    if not wait_for(lambda: "plex: controls over the video" in logcat()):
        return fail("a tap did not show the controls over the video")
    time.sleep(1)
    _, _, px = screen(out, "controls")
    button = count_color(px, PLAY)
    print(f"controls: {button} pixels of the play button over the video", flush=True)
    if button < 500:
        fail(f"the controls over the video show {button} pixels of the play button")
    if "plex: paused at" in logcat():
        return fail("a tap on the video paused it")
    adb("shell", "input", "tap", str(width // 2), str(height // 2))
    if not wait_for(lambda: "plex: paused at" in logcat()):
        return fail("the play button did not pause the video")
    time.sleep(1.5)
    _, _, px = screen(out, "paused")
    button = count_color(px, PLAY)
    print(f"paused: {button} pixels of the play button", flush=True)
    if button < 500:
        fail(f"the paused screen shows {button} pixels of the play button")
    adb("shell", "input", "tap", str(width // 2), str(height // 2))
    if not wait_for(lambda: count_log(r"plex: video on the window") >= 2):
        return fail("the video did not go back on the window after the pause")
    # A new texture after the pause must not show an old frame turned over.
    time.sleep(2)
    _, _, px = screen(out, "resumed")
    fault = frame_fault(width, height, px)
    if fault:
        fail("resumed: " + fault)
    adb("shell", "input", "keyevent", "4")
    if not wait_for(lambda: seen("ratingKey=901&key=%2Flibrary%2Fmetadata%2F901&state=stopped")):
        fail("no report that the video stopped")


def check_gapless(out, port):
    start(port, "920")
    if not wait_for(lambda: "plex: prepared 911" in logcat()):
        return fail("Play on the artist did not play its first track")
    if not wait_for(lambda: "plex: play 912 with no gap" in logcat(), timeout=30):
        return fail("the second track did not follow with no gap: " + " | ".join(l for l in logcat().splitlines() if "plex" in l))
    # The scrobble goes out after the "stopped" timeline in the report queue.
    if not wait_for(lambda: seen("/:/scrobble?identifier=com.plexapp.plugins.library&key=911"), timeout=15):
        fail("the first track did not become watched at its end")


# The names of the views and of the virtual views of a uiautomator dump.
def accessible_names(xml):
    return set(re.findall(r'(?:text|content-desc)="([^"]+)"', xml))


def check_access(out, port):
    start(port)
    if not wait_for(lambda: seen("/hubs") and seen("/photo/:/transcode")):
        return fail("the home screen asked for no hubs or no posters")
    time.sleep(3)
    adb("shell", "uiautomator", "dump", "/sdcard/plex-ui.xml", check=False)
    xml = adb("shell", "cat", "/sdcard/plex-ui.xml", check=False)
    with open(f"{out}/ui.xml", "w") as f:
        f.write(xml)
    names = accessible_names(xml)
    for title in ("Test Pattern", "Square Tone"):
        if not any(title in n for n in names):
            fail(f"the accessibility tree of the home screen does not name {title}")


# True when a dump of `dumpsys activity activities` shows a task of the app
# in the pinned mode of picture-in-picture.
def pinned(dump):
    return any(PACKAGE in line and "mode=pinned" in line for line in dump.splitlines())


def check_pip(out, port):
    start(port, "901")
    if not wait_for(lambda: count_log(r"plex: video on the window") >= 1):
        return fail("the video for picture-in-picture did not show")
    time.sleep(2)
    adb("shell", "input", "keyevent", "3")
    if not wait_for(lambda: pinned(adb("shell", "dumpsys", "activity", "activities", check=False)), timeout=15):
        fail("the home key during a video did not open a picture-in-picture window")
    screen(out, "pip")
    adb("shell", "am", "force-stop", PACKAGE)


def node_center(xml, label, long_click=False):
    try:
        root = ET.fromstring(xml)
    except ET.ParseError:
        return None
    for node in root.iter("node"):
        if node.get("package") != PACKAGE or label not in (node.get("text"), node.get("content-desc")):
            continue
        if long_click and node.get("long-clickable") != "true":
            continue
        bounds = re.fullmatch(r"\[(\d+),(\d+)\]\[(\d+),(\d+)\]", node.get("bounds", ""))
        if bounds:
            x, y, right, bottom = map(int, bounds.groups())
            if right > x and bottom > y:
                return (str((x + right) // 2), str((y + bottom) // 2))
    return None


def download_node(out, label, long_click=False):
    path = "/sdcard/plex-download.xml"
    deadline = time.monotonic() + 25
    while time.monotonic() < deadline:
        # An unsuccessful dump must not reuse the preceding screen.
        adb("shell", "rm", "-f", path)
        adb("shell", "uiautomator", "dump", path, check=False)
        xml = adb("shell", "cat", path, check=False)
        with open(f"{out}/download-ui.xml", "w") as f:
            f.write(xml)
        point = node_center(xml, label, long_click)
        if point:
            return point
        time.sleep(0.5)
    return None


def check_download(out, port):
    start(port)

    def hold_track():
        point = download_node(out, "Square Tone", long_click=True)
        if point:
            adb("shell", "input", "swipe", *point, *point, "800")
        return point

    if not hold_track():
        return fail("the home screen has no Square Tone poster to hold")
    button = download_node(out, "Download")
    if not button:
        return fail("holding the track did not offer Download")
    before = len(requests)
    adb("shell", "input", "tap", *button)
    if not wait_for(lambda: "GET " + DOWNLOAD_PATH in requests[before:]):
        return fail("Android did not request the track from the download URL")
    if not hold_track():
        return fail("the downloaded track disappeared from the home screen")
    remove = download_node(out, "Remove download")
    if not remove:
        return fail("Android did not complete the track download")
    adb("shell", "input", "tap", *remove)


def main(apk, out, video=None):
    os.makedirs(out, exist_ok=True)
    files = {"/library/parts/900/tone.wav": os.path.join(out, "tone.wav")}
    tone(files["/library/parts/900/tone.wav"], 3)
    for key in ("911", "912"):
        files[f"/library/parts/{key}/tone.wav"] = os.path.join(out, f"tone-{key}.wav")
        tone(files[f"/library/parts/{key}/tone.wav"], 2)
    if video:
        files["/library/parts/901/pattern.mp4"] = video
    httpd = server(files)
    port = httpd.server_address[1]
    adb("install", "-r", "-g", apk)
    adb("logcat", "-c")
    try:
        check_home(out, port)
        check_remote(out, port)
        check_track(out, port)
        check_background(out, port)
        check_gapless(out, port)
        check_access(out, port)
        if video:
            check_video(out, port)
            check_pip(out, port)
        else:
            print("no VIDEO.mp4: the video is not checked", flush=True)
        check_download(out, port)
    finally:
        log = logcat()
        with open(f"{out}/logcat.txt", "w") as f:
            f.write(log)
        if failures:
            # Each tag, so a failure also shows the errors of the media
            # framework and of the codecs.
            with open(f"{out}/logcat-all.txt", "w") as f:
                f.write(adb("logcat", "-d", "-v", "threadtime", check=False))
        with open(f"{out}/requests.txt", "w") as f:
            f.write("\n".join(requests) + "\n")
        print("\n".join(line for line in log.splitlines() if "plex" in line), flush=True)
        for line in log.splitlines():
            if CRASH.search(line):
                fail("logcat: " + line)
        adb("shell", "am", "force-stop", PACKAGE, check=False)
        httpd.shutdown()
    if failures:
        sys.exit(1)
    print("ok", flush=True)


if __name__ == "__main__":
    if len(sys.argv) not in (3, 4):
        sys.exit(__doc__)
    main(*sys.argv[1:])
