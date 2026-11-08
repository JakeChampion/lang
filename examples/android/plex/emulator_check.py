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
- with VIDEO.mp4, a link shows the video on the window, a tap pauses it
  and draws the controls on the window, and a tap on the play button shows
  the video on the window again, then the back key stops it;
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
import zlib

PACKAGE = "dev.fern.plex"
POSTER = (30, 170, 160)
ACCENT = (229, 160, 13)
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
MOVIE = item("901", "movie", "Test Pattern", ',"year":2026,"duration":20000,"summary":"Bars and a clock.","Media":[{"container":"mp4","videoCodec":"h264","videoResolution":"240","Part":[{"key":"/library/parts/901/pattern.mp4","container":"mp4"}]}]')


def container(body):
    return '{"MediaContainer":{' + body + "}}"


ROUTES = {
    "/identity": container('"machineIdentifier":"fake"'),
    "/library/sections": container('"Directory":[{"key":"1","type":"movie","title":"Movies"},{"key":"3","type":"artist","title":"Music"}]'),
    "/hubs": container('"Hub":[{"title":"Recently Added","hubIdentifier":"home.recent","Metadata":[' + MOVIE + "," + TRACK + "]}]"),
    "/library/metadata/900": container('"Metadata":[' + TRACK + "]"),
    "/library/metadata/901": container('"Metadata":[' + MOVIE + "]"),
}


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
    return subprocess.run(["adb", *args], check=check, capture_output=True, text=True).stdout


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


def check_video(out, port):
    start(port, "901")
    if not wait_for(lambda: count_log(r"plex: video on the window") >= 1):
        return fail("the video did not go on the window: " + " | ".join(l for l in logcat().splitlines() if "plex" in l))
    time.sleep(2)
    width, height, px = screen(out, "video")
    colors = colors_in(width, height, px, (width // 4, height // 3, 3 * width // 4, 2 * height // 3))
    print(f"video: {colors} colours in the middle of the screen", flush=True)
    if colors < 12:
        fail(f"the video shows {colors} colours")
    adb("shell", "input", "tap", str(width // 2), str(height // 2))
    if not wait_for(lambda: "plex: paused at" in logcat()):
        return fail("a tap did not pause the video")
    time.sleep(1.5)
    _, _, px = screen(out, "paused")
    button = count_color(px, ACCENT)
    print(f"paused: {button} pixels of the play button", flush=True)
    if button < 500:
        fail(f"the paused screen shows {button} pixels of the play button")
    adb("shell", "input", "tap", str(width // 2), str(height // 2))
    if not wait_for(lambda: count_log(r"plex: video on the window") >= 2):
        return fail("the video did not go back on the window after the pause")
    time.sleep(1.5)
    screen(out, "resumed")
    adb("shell", "input", "keyevent", "4")
    if not wait_for(lambda: seen("ratingKey=901&key=%2Flibrary%2Fmetadata%2F901&state=stopped")):
        fail("no report that the video stopped")


def main(apk, out, video=None):
    os.makedirs(out, exist_ok=True)
    files = {"/library/parts/900/tone.wav": os.path.join(out, "tone.wav")}
    tone(files["/library/parts/900/tone.wav"], 3)
    if video:
        files["/library/parts/901/pattern.mp4"] = video
    httpd = server(files)
    port = httpd.server_address[1]
    adb("install", "-r", "-g", apk)
    adb("logcat", "-c")
    try:
        check_home(out, port)
        check_track(out, port)
        if video:
            check_video(out, port)
        else:
            print("no VIDEO.mp4: the video is not checked", flush=True)
    finally:
        log = logcat()
        with open(f"{out}/logcat.txt", "w") as f:
            f.write(log)
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
