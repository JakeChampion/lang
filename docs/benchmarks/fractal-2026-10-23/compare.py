#!/usr/bin/env python3
"""Compare complete tile renders and PNG bytes with one native Fern compiler."""

import argparse
import hashlib
import json
import os
import platform
from pathlib import Path
import statistics
import subprocess
import tempfile


ROOT = Path(__file__).resolve().parents[3]
CASES = [
    ("whole", 0, 0, 0),
    ("seahorse14", 14, 7194, 7652),
    ("dive20", 20, 460418, 489730),
    ("dive30", 30, 471468254, 501484165),
    ("dive38", 38, 120695873044, 128379946314),
    ("dive42", 42, 1931133968718, 2054079141036),
    ("minibrot20", 20, 195329, 524288),
    ("spiral15", 15, 14241, 17078),
    ("exterior42", 42, 0, 0),
]
DRIVER = '''import "./fractal";
import "std/hash";
import "std/io";
function measure(name: string, z: i32, x: i64, y: i64, raw: boolean): void {
  let start: i64 = now_ns();
  let tile: fractal.Tile = fractal.render(z, x, y);
  let elapsed: i64 = now_ns() - start;
  if (raw) {
    match (stdout().write_bytes(tile.png)) {
      Ok(_) => {},
      Err(_) => { exit(1); },
    }
  } else {
    let crc: u32 = hash.crc32_new().update_array(tile.png).finish();
    print(name + "," + elapsed.to_string() + "," + tile.iterations.to_string() + "," + tile.png.len().to_string() + "," + crc.to_string());
  }
}
function main(): i32 {
  let raw: boolean = args().len() > 1;
CALLS
  return 0;
}
'''


def digest(data):
    return hashlib.sha256(data).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--fern", type=Path, default=ROOT / "bin/fern")
    parser.add_argument("--baseline", default="09706b670")
    parser.add_argument("--runs", type=int, default=5)
    parser.add_argument("--output", type=Path, required=True)
    options = parser.parse_args()
    if options.runs < 1:
        parser.error("--runs must be positive")
    compiler = options.fern.resolve()
    machine = platform.machine()
    if platform.system() == "Darwin" and machine == "arm64":
        target = "arm64-darwin"
    elif platform.system() == "Linux" and machine in ("aarch64", "arm64"):
        target = "arm64-linux"
    elif platform.system() == "Linux" and machine == "x86_64":
        target = "x86-64-linux"
    else:
        parser.error("unsupported native host")
    calls = "\n".join(
        f'  measure("{name}", {z}, {x}, {y}, raw);' for name, z, x, y in CASES
    )
    driver = DRIVER.replace("CALLS", calls)
    sources = {"baseline": {}, "candidate": {}}
    for name in ("fractal.fern", "binary.fern"):
        path = "examples/fractal/" + name
        sources["baseline"][name] = subprocess.check_output(
            ["git", "show", options.baseline + ":" + path], cwd=ROOT
        )
        sources["candidate"][name] = (ROOT / path).read_bytes()
    environment = dict(os.environ, FERN_PROFILE="off", FERN_SANITIZE="off", FERN_LEAKCHECK="off")
    runs = {"baseline": [], "candidate": []}
    with tempfile.TemporaryDirectory(prefix="fractal-bench-") as temporary:
        binaries = {}
        for variant, files in sources.items():
            directory = Path(temporary) / variant
            directory.mkdir()
            for name, data in files.items():
                (directory / name).write_bytes(data)
            entry = directory / "bench.fern"
            entry.write_text(driver)
            binary = directory / "bench"
            subprocess.run(
                [str(compiler), "-target", target, "-O", "-o", str(binary), str(entry), str(ROOT / "internal/stdlib")],
                env=environment, check=True,
            )
            binaries[variant] = binary
        baseline_pngs = subprocess.check_output([str(binaries["baseline"]), "--png"])
        candidate_pngs = subprocess.check_output([str(binaries["candidate"]), "--png"])
        if baseline_pngs != candidate_pngs:
            raise SystemExit("PNG bytes differ")
        for index in range(options.runs):
            order = ["baseline", "candidate"] if index % 2 == 0 else ["candidate", "baseline"]
            for variant in order:
                output = subprocess.check_output([str(binaries[variant])], text=True)
                run = {}
                for line in output.splitlines():
                    name, ns, iterations, length, crc = line.split(",")
                    run[name] = dict(ms=int(ns) / 1e6, iterations=int(iterations), bytes=int(length), crc32=int(crc))
                if set(run) != {case[0] for case in CASES}:
                    raise SystemExit("benchmark cases differ")
                runs[variant].append(run)
    summary = {}
    for name, _, _, _ in CASES:
        medians = {variant: statistics.median(run[name]["ms"] for run in samples) for variant, samples in runs.items()}
        summary[name] = dict(baseline_ms=medians["baseline"], candidate_ms=medians["candidate"], speedup=medians["baseline"] / medians["candidate"])
        print(f'{name}: {medians["baseline"]:.2f} -> {medians["candidate"]:.2f} ms ({summary[name]["speedup"]:.2f}x)')
    report = dict(
        baseline_revision=options.baseline,
        compiler_sha256=digest(compiler.read_bytes()),
        source_sha256={variant: {name: digest(data) for name, data in files.items()} for variant, files in sources.items()},
        target=target,
        cases=[dict(name=name, z=z, x=x, y=y) for name, z, x, y in CASES],
        png_bytes_match=True,
        png_stream_sha256=digest(baseline_pngs),
        runs=runs,
        summary=summary,
    )
    options.output.write_text(json.dumps(report, indent=2) + "\n")


if __name__ == "__main__":
    main()
