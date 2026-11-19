#!/usr/bin/env python3
"""Runs the DEX file of the dexer test on a running Android device or emulator.

Usage: emulator_check.py DEX EXPECTED OUT_DIR

DEX is the file that fern -dex writes from the classes of
tests/android/dexer. ART runs its class dev.fern.dexer.Main through
app_process. The output must be the lines of EXPECTED, which the JVM prints
for the same classes. OUT_DIR gets the output and logcat.
"""

import subprocess
import sys

REMOTE = "/data/local/tmp/fern-dexer.dex"


def adb(*args):
    return subprocess.run(["adb", *args], capture_output=True, text=True)


def main(dex, expected, out):
    subprocess.run(["mkdir", "-p", out], check=True)
    adb("push", dex, REMOTE).check_returncode()
    adb("shell", "chmod", "444", REMOTE).check_returncode()
    adb("logcat", "-c")
    run = adb("shell", "CLASSPATH=" + REMOTE, "app_process", "/", "dev.fern.dexer.Main")
    log = adb("logcat", "-d", "-v", "brief", "AndroidRuntime:E", "art:W", "*:F").stdout
    with open(f"{out}/output.txt", "w") as f:
        f.write(run.stdout)
    with open(f"{out}/stderr.txt", "w") as f:
        f.write(run.stderr)
    with open(f"{out}/logcat.txt", "w") as f:
        f.write(log)
    with open(expected) as f:
        want = f.read().splitlines()
    got = run.stdout.replace("\r\n", "\n").splitlines()
    failures = []
    for i, line in enumerate(want):
        have = got[i] if i < len(got) else None
        if have != line:
            failures.append(f"line {i + 1}: want {line!r}, got {have!r}")
    if len(got) > len(want):
        failures.append(f"{len(got) - len(want)} more lines than expected, from {got[len(want)]!r}")
    failures += ["stderr: " + line for line in run.stderr.splitlines()]
    failures += ["logcat: " + line for line in log.splitlines() if "verif" in line.lower() or "rejecting" in line]
    for f in failures:
        print("FAIL " + f, flush=True)
    if failures:
        sys.exit(1)
    print("ok", flush=True)


if __name__ == "__main__":
    if len(sys.argv) != 4:
        sys.exit(__doc__)
    main(*sys.argv[1:])
