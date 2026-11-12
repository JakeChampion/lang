#!/usr/bin/env python3
"""Verify codec export and playback on a running Android emulator or device.

Usage: emulator_check.py APK OUT_DIR [CONTENT_URI]
Requires adb, FFmpeg, and ffprobe. OUT_DIR receives clips, logs, and metrics.
Set ANDROID_SERIAL to select a physical device. The test requires API 29.
"""

import json
import hashlib
import importlib.util
from pathlib import Path
import re
import subprocess
import sys
import time

PACKAGE = "dev.fern.videotest"

fixture_spec = importlib.util.spec_from_file_location("native_av_fixture", Path(__file__).with_name("native_av_fixture.py"))
fixture = importlib.util.module_from_spec(fixture_spec)
fixture_spec.loader.exec_module(fixture)


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


def native_av_metrics(log):
    assert "videotest: native AAC frames=19200 rate=48000 channels=2" in log
    submissions = re.findall(r"videotest: native AV submitted_frames=(\d+) origin_us=(\d+)", log)
    assert submissions == [('19200', '0'), ('14400', '100000')], submissions
    timestamps = []
    for line in log.splitlines():
        if "videotest: native AV timestamp" not in line:
            continue
        if line.endswith("videotest: native AV timestamp unavailable"):
            timestamps.append(None)
            continue
        value = re.search(r"videotest: native AV timestamp frames=(\d+) monotonic_ns=(\d+)$", line)
        assert value is not None, line
        position, time_ns = map(int, value.groups())
        assert max(position, time_ns) <= (1 << 63) - 1
        timestamps.append({'frame_position': position, 'monotonic_ns': time_ns})
    assert len(timestamps) == 2, timestamps
    return {'rate': 48000, 'channels': 2, 'frames': 19200, 'timestamps': timestamps,
            'submissions': [{'frames': int(n), 'origin_us': int(at)} for n, at in submissions]}


def native_content_metrics(log, expected):
    assert 0 < len(expected) <= 2097152, len(expected)
    values = re.findall(r"videotest: native content bytes=([0-9]+) sha256=([0-9a-f]{64})$", log, re.MULTILINE)
    assert len(values) == 1, values
    count, digest = values[0]
    assert int(count) == len(expected), values
    assert digest == hashlib.sha256(expected).hexdigest(), values
    return {'bytes': int(count), 'sha256': digest}


def main(apk, out_dir, content_uri=None, av_path=None):
    out = Path(out_dir)
    out.mkdir(parents=True, exist_ok=True)
    sdk = int(adb("shell", "getprop", "ro.build.version.sdk").strip())
    assert sdk >= 29
    adb("install", "-r", apk)
    adb("shell", "am", "force-stop", PACKAGE)
    adb("logcat", "-c")
    command = ["shell", "am", "start", "-W", "-n", PACKAGE + "/android.app.NativeActivity"]
    if av_path is not None:
        assert content_uri is None
        command += ['--es', 'av_path', av_path]
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
        if content_uri is None and av_path is None:
            assert "export surface=false frames=12" in log, log
            assert "export surface=true frames=12" in log, log
        elif content_uri is not None:
            assert "videotest: source=content offset=" in log, log
        assert "decoder name=" in log, log
        assert "videotest: same surface accepted while paused" in log, log
        directory = re.search(r"videotest: sdk=\d+ directory=(\S+)", log).group(1)
        metrics = {"build": adb("shell", "getprop", "ro.build.fingerprint").strip(),
                   "abi": adb("shell", "getprop", "ro.product.cpu.abi").strip(),
                   "clips": {}}
        if av_path is not None:
            metrics['native_av'] = native_av_metrics(log)
        if content_uri is not None:
            expected = adb('exec-out', 'content', 'read', '--uri', content_uri, binary=True)
            metrics['native_content'] = native_content_metrics(log, expected)
        for name in (("byte", "surface") if content_uri is None and av_path is None else ()):
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
    return directory


def native_av(apk, directory, private_directory):
    out = Path(directory)
    path = out / 'native-av.mp4'
    metadata = fixture.create(out / 'byte.mp4', path)
    (out / 'native-av-fixture.json').write_text(json.dumps(metadata, indent=2) + '\n')
    assert re.fullmatch(r'/data/(?:user/\d+|data)/dev\.fern\.videotest/files', private_directory), private_directory
    destination = private_directory + '/native-av.mp4'
    data = path.read_bytes()
    subprocess.run(['adb', 'shell', '-T', 'run-as', PACKAGE, 'tee', destination], input=data,
                   stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, check=True, timeout=45)
    assert adb('exec-out', 'run-as', PACKAGE, 'cat', destination, binary=True) == data
    main(apk, out / 'native-av', av_path=destination)


if __name__ == "__main__":
    if len(sys.argv) not in (3, 4):
        sys.exit(__doc__)
    private = main(*sys.argv[1:])
    if len(sys.argv) == 3:
        native_av(sys.argv[1], sys.argv[2], private)
