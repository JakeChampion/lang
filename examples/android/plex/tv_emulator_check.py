"""Check the home rows Plex publishes on a fresh Android TV emulator."""

import re
import sys
from pathlib import Path

from emulator_check import adb, logcat, requests, server, start, wait_for


def check(directory):
    if adb("shell", "pm", "has-feature", "android.software.leanback").strip() != "true":
        raise RuntimeError("the emulator needs the Android TV system image")
    for apk in ("fern-plex.apk", "rows.apk"):
        adb("install", str(directory / apk))
    adb("logcat", "-c")
    fixture = server({"/library/parts/901/pattern.mp4": directory / "pattern.mp4"})
    try:
        start(fixture.server_port)
        if not wait_for(lambda: "plex: published preview channel" in logcat()):
            raise RuntimeError("Plex did not publish its home channel")
        start(fixture.server_port, "901")
        if not wait_for(lambda: "plex: video on the window" in logcat()):
            raise RuntimeError("Plex did not start the movie")
        progress = re.compile(r"ratingKey=901&[^ ]*state=playing&time=[1-9][0-9]*&")
        if not wait_for(lambda: any(progress.search(r) for r in requests)):
            raise RuntimeError("the movie reported no playback progress")
        adb("shell", "input", "keyevent", "KEYCODE_BACK")
        if not wait_for(lambda: "plex: published Watch Next" in logcat()):
            raise RuntimeError("Plex did not publish the unfinished movie")
        result = adb("shell", "am", "instrument", "-w", "dev.fern.plex.rows/.Rows")
        lines = result.splitlines()
        if "INSTRUMENTATION_RESULT: rows=verified" not in lines or "INSTRUMENTATION_CODE: -1" not in lines:
            raise RuntimeError(result)
        print("Verified preview programs and unfinished movie in the TV provider.")
    except RuntimeError:
        print(logcat(), file=sys.stderr)
        raise
    finally:
        fixture.shutdown()
        fixture.server_close()


if __name__ == "__main__":
    check(Path(sys.argv[1]))
