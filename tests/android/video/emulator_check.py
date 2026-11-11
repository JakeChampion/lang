#!/usr/bin/env python3
"""Verify codec export and playback on a running Android emulator or device.

Usage: emulator_check.py APK OUT_DIR [CONTENT_URI]
Requires adb, FFmpeg, and ffprobe. OUT_DIR receives clips, logs, and metrics.
Set ANDROID_SERIAL to select a physical device. The test requires API 29.
"""

import json
from pathlib import Path
import re
import subprocess
import sys
import time

PACKAGE = "dev.fern.videotest"


def adb(*args, binary=False):
    return subprocess.run(
        ["adb", *args], check=True, capture_output=True, text=not binary, timeout=45
    ).stdout


def verify_clip(path):
    result = subprocess.run(
        ["ffprobe", "-v", "error", "-select_streams", "v:0", "-count_frames",
         "-show_entries", "stream=codec_name,width,height,nb_read_frames",
         "-of", "json", str(path)],
        check=True, capture_output=True, text=True, timeout=30,
    )
    streams = json.loads(result.stdout)["streams"]
    assert len(streams) == 1, streams
    stream = streams[0]
    assert stream == {
        "codec_name": "h264", "width": 128, "height": 128, "nb_read_frames": "12"
    }, stream
    decoded = subprocess.run(
        ["ffmpeg", "-v", "error", "-i", str(path), "-f", "rawvideo", "-pix_fmt",
         "rgb24", "-"], check=True, capture_output=True, timeout=30,
    ).stdout
    assert len(decoded) == 12 * 128 * 128 * 3, len(decoded)
    # Both producers generate neutral 50-percent gray. Check every decoded sample.
    assert min(decoded) >= 110 and max(decoded) <= 145, (min(decoded), max(decoded))
    return stream


def verify_screen(data):
    # FFmpeg decodes the Android screenshot. No additional image package is needed.
    pixels = subprocess.run(
        ["ffmpeg", "-v", "error", "-i", "pipe:0", "-vf",
         "crop=32:32:iw/2-16:ih/2-16", "-f", "rawvideo", "-pix_fmt", "rgb24", "-"],
        input=data, check=True, capture_output=True, timeout=30,
    ).stdout
    assert len(pixels) == 32 * 32 * 3, len(pixels)
    assert min(pixels) >= 105 and max(pixels) <= 150, (min(pixels), max(pixels))


def main(apk, out_dir, content_uri=None):
    out = Path(out_dir)
    out.mkdir(parents=True, exist_ok=True)
    sdk = int(adb("shell", "getprop", "ro.build.version.sdk").strip())
    assert sdk >= 29
    adb("install", "-r", apk)
    adb("shell", "am", "force-stop", PACKAGE)
    adb("logcat", "-c")
    command = ["shell", "am", "start", "-W", "-n", PACKAGE + "/android.app.NativeActivity"]
    if content_uri is not None:
        assert content_uri.startswith("content://"), content_uri
        # The shell owns the MediaStore fixture and cannot grant its URI.
        permission = "READ_MEDIA_VIDEO" if sdk >= 33 else "READ_EXTERNAL_STORAGE"
        adb("shell", "pm", "grant", PACKAGE, "android.permission." + permission)
        command += ["--es", "uri", content_uri, "-d", content_uri]
    adb(*command)
    log = ""
    try:
        deadline = time.monotonic() + 75
        while time.monotonic() < deadline:
            log = adb("logcat", "-d", "-v", "brief", "fern:I", "AndroidRuntime:E", "*:F")
            if "videotest: roundtrip complete" in log or "videotest: FAIL" in log:
                break
            time.sleep(0.25)
        (out / "logcat.txt").write_text(log)
        print("\n".join(line for line in log.splitlines() if "videotest:" in line), flush=True)
        assert "videotest: FAIL" not in log, log
        assert not re.search(r"Fatal signal|UnsatisfiedLinkError|FATAL EXCEPTION|fern: ", log), log
        assert "videotest: roundtrip complete" in log, "codec test timeout"
        if content_uri is None:
            assert "export surface=false frames=12" in log, log
            assert "export surface=true frames=12" in log, log
        else:
            assert "videotest: source=content offset=" in log, log
        assert "decoder name=" in log, log
        assert "videotest: same surface accepted while paused" in log, log
        directory = re.search(r"videotest: sdk=\d+ directory=(\S+)", log).group(1)
        metrics = {"build": adb("shell", "getprop", "ro.build.fingerprint").strip(),
                   "abi": adb("shell", "getprop", "ro.product.cpu.abi").strip(),
                   "clips": {}}
        for name in (("byte", "surface") if content_uri is None else ()):
            path = out / (name + ".mp4")
            path.write_bytes(adb("exec-out", "run-as", PACKAGE, "cat", directory + "/" + name + ".mp4", binary=True))
            metrics["clips"][name] = verify_clip(path)
        # Keep the codec alive until the scheduled last frame reaches the display.
        time.sleep(0.3)
        screen = adb("exec-out", "screencap", "-p", binary=True)
        (out / "screen.png").write_bytes(screen)
        verify_screen(screen)
        (out / "meminfo.txt").write_text(adb("shell", "dumpsys", "meminfo", PACKAGE))
        (out / "metrics.json").write_text(json.dumps(metrics, indent=2) + "\n")
    finally:
        if log:
            (out / "logcat.txt").write_text(log)
        adb("shell", "am", "force-stop", PACKAGE)
    print("Android video export, pixels, surface playback, pause, and seek passed", flush=True)


if __name__ == "__main__":
    if len(sys.argv) not in (3, 4):
        sys.exit(__doc__)
    main(*sys.argv[1:])
