#!/usr/bin/env python3
"""Runs the DNS test APK on a running Android device or emulator.

Usage: emulator_check.py APK OUT_DIR

It starts the app and requires its lines in logcat: a public name that
netd resolved, an IP literal, and a TLS connection that the system roots
verified. It fails on a crash. OUT_DIR gets logcat.
"""

import re
import subprocess
import sys
import time

PACKAGE = "dev.fern.dnstest"
LINES = (
    re.compile(r"dnstest: dns\.google has [1-9]\d* addresses: \S"),
    re.compile(r"dnstest: 192\.0\.2\.7 has 1 addresses: 192\.0\.2\.7"),
    re.compile(r"dnstest: SSL_CERT_FILE overrides system roots"),
    re.compile(r"dnstest: tls to dns\.google verified with [1-9]\d* system roots"),
)
CRASH = re.compile(r"Fatal signal|UnsatisfiedLinkError|java\.lang\.\w+Exception|fern: ")


def adb(*args):
    return subprocess.run(["adb", *args], check=True, capture_output=True, text=True).stdout


def main(apk, out):
    subprocess.run(["mkdir", "-p", out], check=True)
    adb("install", "-r", "-g", apk)
    adb("logcat", "-c")
    adb("shell", "am", "start", "-W", "-n", PACKAGE + "/android.app.NativeActivity")
    log = ""
    deadline = time.time() + 40
    while time.time() < deadline:
        log = adb("logcat", "-d", "-v", "brief", "fern:I", "AndroidRuntime:E", "*:F")
        if all(p.search(log) for p in LINES) or "failed:" in log or "no system roots" in log:
            break
        time.sleep(0.5)
    with open(f"{out}/logcat.txt", "w") as f:
        f.write(log)
    print("\n".join(line for line in log.splitlines() if "dnstest" in line), flush=True)
    failures = [f"no line matching {p.pattern!r}" for p in LINES if not p.search(log)]
    failures += ["logcat: " + line for line in log.splitlines() if CRASH.search(line)]
    adb("shell", "am", "force-stop", PACKAGE)
    for f in failures:
        print("FAIL " + f, flush=True)
    if failures:
        sys.exit(1)
    print("ok", flush=True)


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    main(*sys.argv[1:])
