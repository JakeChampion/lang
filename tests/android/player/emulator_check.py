#!/usr/bin/env python3
"""Check AAC+AVC platform playback on an Android emulator or device.

Usage: emulator_check.py APK OUT_DIR
Requires adb, FFmpeg, and ffprobe. Set ANDROID_SERIAL for a physical device.
"""

from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import re
import subprocess
import sys
import threading
import time

PACKAGE = "dev.fern.playertest"


def adb(*args, binary=False):
    return subprocess.run(["adb", *args], check=True, capture_output=True,
                          text=not binary, timeout=45).stdout


def handler_for(path):
    data = path.read_bytes()

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass

        def do_HEAD(self):
            self.do_GET()

        def do_GET(self):
            if self.path != "/av.mp4":
                self.send_error(404)
                return
            start, end = 0, len(data) - 1
            requested = self.headers.get("Range")
            if requested:
                match = re.fullmatch(r"bytes=(\d+)-(\d*)", requested)
                if match is None:
                    self.send_error(416)
                    return
                start = int(match[1])
                if match[2]:
                    end = min(end, int(match[2]))
                if start > end or start >= len(data):
                    self.send_error(416)
                    return
            self.send_response(206 if requested else 200)
            self.send_header("Content-Type", "video/mp4")
            self.send_header("Accept-Ranges", "bytes")
            self.send_header("Content-Length", str(end - start + 1))
            if requested:
                self.send_header("Content-Range", f"bytes {start}-{end}/{len(data)}")
            self.end_headers()
            if self.command != "HEAD":
                try:
                    self.wfile.write(data[start:end + 1])
                except (BrokenPipeError, ConnectionResetError):
                    pass

    return Handler


def write_fixture(out):
    path = out / "av.mp4"
    subprocess.run([
        "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i",
        "testsrc=size=320x240:rate=25", "-f", "lavfi", "-i",
        "sine=frequency=440:sample_rate=48000", "-t", "4", "-c:v", "libx264",
        "-g", "25", "-pix_fmt", "yuv420p", "-c:a", "aac", "-ac", "2",
        "-movflags", "+faststart", str(path),
    ], check=True, timeout=45)
    result = subprocess.run([
        "ffprobe", "-v", "error", "-show_streams", "-show_format", "-of", "json",
        str(path),
    ], check=True, capture_output=True, text=True, timeout=15)
    metadata = json.loads(result.stdout)
    streams = metadata["streams"]
    assert len(streams) == 2, streams
    assert streams[0]["codec_name"] == "h264", streams
    assert streams[0]["width"] == 320 and streams[0]["height"] == 240, streams
    assert streams[1]["codec_name"] == "aac", streams
    assert streams[1]["sample_rate"] == "48000" and streams[1]["channels"] == 2, streams
    (out / "fixture.json").write_text(json.dumps(metadata, indent=2) + "\n")


def verify_screen(data):
    pixels = subprocess.run([
        "ffmpeg", "-v", "error", "-i", "pipe:0", "-vf",
        "crop=iw/2:ih/2:iw/4:ih/4,scale=32:32", "-pix_fmt", "rgb24", "-f",
        "rawvideo", "-",
    ], input=data, check=True, capture_output=True, timeout=15).stdout
    assert len(pixels) == 32 * 32 * 3, len(pixels)
    colors = {pixels[i:i + 3] for i in range(0, len(pixels), 3)}
    assert len(colors) > 8 and max(pixels) - min(pixels) > 100, "video surface is blank"


def main(apk, directory):
    out = Path(directory).resolve()
    out.mkdir(parents=True, exist_ok=True)
    write_fixture(out)
    server = ThreadingHTTPServer(("127.0.0.1", 0), handler_for(out / "av.mp4"))
    threading.Thread(target=server.serve_forever, daemon=True).start()
    port = str(server.server_address[1])
    log = ""
    installed = False
    mapped = False
    try:
        adb("install", "-r", apk)
        installed = True
        adb("shell", "am", "force-stop", PACKAGE)
        adb("reverse", "tcp:" + port, "tcp:" + port)
        mapped = True
        adb("logcat", "-c")
        adb("shell", "am", "start", "-W", "-n", PACKAGE + "/android.app.NativeActivity",
            "--es", "uri", "http://127.0.0.1:" + port + "/av.mp4")
        # Two playback rounds take about 12 seconds. The error round waits for
        # the retries of the platform's HTTP source, about 30 seconds more.
        deadline = time.monotonic() + 120
        screen = None
        audio_dump = False
        while time.monotonic() < deadline:
            log = adb("logcat", "-d", "-v", "time", "fern:I", "AndroidRuntime:E", "*:F")
            if "playertest: FAIL" in log or "playertest: passed" in log:
                break
            if "playertest: paused position_ms=" in log and screen is None:
                screen = adb("exec-out", "screencap", "-p", binary=True)
                (out / "screen.png").write_bytes(screen)
            if "playertest: seeked" in log and not audio_dump:
                (out / "audio-flinger.txt").write_text(adb("shell", "dumpsys", "media.audio_flinger"))
                (out / "media-player.txt").write_text(adb("shell", "dumpsys", "media.player"))
                audio_dump = True
            time.sleep(0.1)
        (out / "logcat.txt").write_text(log)
        print("\n".join(line for line in log.splitlines() if "playertest:" in line), flush=True)
        assert "playertest: FAIL" not in log, log
        assert not re.search(r"Fatal signal|FATAL EXCEPTION|UnsatisfiedLinkError|JNI DETECTED ERROR", log), log
        for marker in ("opened", "prepared round=0", "paused position_ms=", "pause stable",
                       "seeked position_ms=", "timestamp media_us=", "completed round=0",
                       "prepared round=1", "completed round=1", "expected error", "passed"):
            assert "playertest: " + marker in log, "missing " + marker
        assert screen is not None, "no screenshot during playback"
        verify_screen(screen)
        (out / "meminfo.txt").write_text(adb("shell", "dumpsys", "meminfo", PACKAGE))
        (out / "device.txt").write_text(adb("shell", "getprop", "ro.build.fingerprint"))
    finally:
        (out / "logcat.txt").write_text(log)
        if installed:
            adb("shell", "am", "force-stop", PACKAGE)
        if mapped:
            adb("reverse", "--remove", "tcp:" + port)
        server.shutdown()
        server.server_close()
    print("Android AAC+AVC playback, pause, seek, timeline, reuse, and errors passed", flush=True)


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    main(*sys.argv[1:])
