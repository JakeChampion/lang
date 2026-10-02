"""Compare native as_bytes conversion and scans with two Fern compilers.

Run a small pilot before increasing --scale. Run on the target host without
other compiler or test jobs. This measures native execution, never QEMU.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import statistics
import subprocess
import time


SOURCE = '''import "std/string";
@noinline function read(s: string, seed: i32): i32 {
  let v: [u8] = s.as_bytes();
  BODY
}
function main(): i32 {
  let n: i32 = args()[1].parse_int_or(1) * 1000;
  let s: string = "abcdefghijklmnop".repeat(256);
  let total: i64 = 0;
  let i: i32 = 0;
  while (i < n) { total = total + (read(s, i & 1) as i64); i = i + 1; }
  print(total);
  return 0;
}
'''

BODIES = {
    "length": "return v.len() + seed;",
    "scan": "let sum: i32 = seed; for byte in v { sum = sum + (byte as i32); } return sum;",
}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--before", type=Path, required=True)
    parser.add_argument("--after", type=Path, required=True)
    parser.add_argument("--stdlib", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--scale", type=int, default=1)
    parser.add_argument("--samples", type=int, default=5)
    args = parser.parse_args()
    targets = {
        ("Darwin", "arm64"): "arm64-darwin",
        ("Linux", "aarch64"): "arm64-linux",
        ("Linux", "x86_64"): "x86-64-linux",
    }
    target = targets.get((platform.system(), platform.machine()))
    if target is None:
        parser.error("run on a supported native host: ARM64 macOS/Linux or x86-64 Linux")
    if not 1 <= args.scale <= 2147483 or args.samples < 1:
        parser.error("scale must be 1..2147483 and samples must be positive")
    env = {key: value for key, value in os.environ.items() if not key.startswith("FERN_")}
    compilers = {"before": args.before.resolve(), "after": args.after.resolve()}
    work = args.output.resolve()
    work.mkdir(parents=True, exist_ok=True)
    results = {}
    for name, body in BODIES.items():
        source = work / (name + ".fern")
        source.write_text(SOURCE.replace("BODY", body))
        paths = {}
        payload = b"abcdefghijklmnop" * 256
        value = len(payload) if name == "length" else sum(payload)
        expected = value * args.scale * 1000 + args.scale * 500

        def run(binary):
            start = time.perf_counter()
            result = subprocess.run([str(binary), str(args.scale)], env=env, capture_output=True, timeout=60)
            elapsed = time.perf_counter() - start
            if result.returncode or result.stderr or result.stdout != f"{expected}\n".encode():
                raise RuntimeError(f"incorrect result from {binary}: {result}")
            return elapsed

        for side, compiler in compilers.items():
            binary = work / (name + "-" + side)
            command = [str(compiler), "-target", target, "-o", str(binary), str(source), str(args.stdlib.resolve())]
            subprocess.run(command, env=env, capture_output=True, check=True, timeout=60)
            paths[side] = binary
            run(binary)  # Exclude first-launch work from the timed samples.
        times = {side: [] for side in compilers}
        for sample in range(args.samples):
            order = list(compilers) if sample % 2 == 0 else list(reversed(compilers))
            for side in order:
                times[side].append(run(paths[side]))
        results[name] = {
            side: {"seconds": values, "median": statistics.median(values),
                   "bytes": paths[side].stat().st_size,
                   "sha256": hashlib.sha256(paths[side].read_bytes()).hexdigest()}
            for side, values in times.items()
        }
        print(name, json.dumps(results[name]), flush=True)
    report = {"target": target, "scale": args.scale, "samples": args.samples,
              "compilers": {side: {"path": str(path), "sha256": hashlib.sha256(path.read_bytes()).hexdigest()}
                            for side, path in compilers.items()}, "results": results}
    (work / "results.json").write_text(json.dumps(report, indent=2) + "\n")


if __name__ == "__main__":
    main()
