#!/usr/bin/env python3
"""Measure Fern and libpng encoder time and output size on identical RGB tiles."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import statistics
import struct
import subprocess
import tempfile
import zlib

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
spec = importlib.util.spec_from_file_location("tile_bench", HERE.parent / "fractal-2026-10-23/compare.py")
tile_bench = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tile_bench)
MODES = ["none-balanced", "none-fast", "sub-balanced", "sub-fast", "up-fast", "adaptive-balanced", "adaptive-fast"]


def decode_images(stream):
    images = []
    while stream:
        if stream[:8] != b"\x89PNG\r\n\x1a\n":
            raise ValueError("invalid PNG signature")
        offset, compressed = 8, b""
        while True:
            size = struct.unpack_from(">I", stream, offset)[0]
            kind, data = stream[offset + 4:offset + 8], stream[offset + 8:offset + 8 + size]
            if zlib.crc32(kind + data) != struct.unpack_from(">I", stream, offset + 8 + size)[0]:
                raise ValueError("PNG checksum mismatch")
            if kind == b"IHDR":
                width, height, depth, color, compression, filtering, interlace = struct.unpack(">IIBBBBB", data)
                if (depth, color, compression, filtering, interlace) != (8, 2, 0, 0, 0):
                    raise ValueError("unsupported PNG format")
            if kind == b"IDAT":
                compressed += data
            offset += size + 12
            if kind == b"IEND": break
        raw = zlib.decompress(compressed)
        stride = width * 3
        if len(raw) != (stride + 1) * height: raise ValueError("PNG row count differs")
        pixels = bytearray(stride * height)
        for row in range(height):
            filter_id = raw[row * (stride + 1)]
            if filter_id > 4: raise ValueError("invalid PNG filter")
            for x in range(stride):
                at = row * stride + x
                left = pixels[at - 3] if x >= 3 else 0
                up = pixels[at - stride] if row else 0
                corner = pixels[at - stride - 3] if row and x >= 3 else 0
                predictor = 0
                if filter_id == 1: predictor = left
                if filter_id == 2: predictor = up
                if filter_id == 3: predictor = (left + up) // 2
                if filter_id == 4:
                    estimate = left + up - corner
                    distances = [abs(estimate - value) for value in (left, up, corner)]
                    predictor = (left, up, corner)[distances.index(min(distances))]
                pixels[at] = (raw[row * (stride + 1) + 1 + x] + predictor) & 255
        images.append(bytes(pixels))
        stream = stream[offset:]
    return images


DRIVER = '''IMPORT
import "std/result";
import "std/i64";
import "std/i32";
import "std/string";
function main(): i32 {
  let filter: png.Filter = png.Filter.Unfiltered;
  let mode: string = args()[2];
  if (mode.starts_with("sub")) { filter = png.Filter.Sub; }
  if (mode.starts_with("up")) { filter = png.Filter.Up; }
  if (mode.starts_with("adaptive")) { filter = png.Filter.Adaptive; }
  let compression: png.Compression = png.Compression.Balanced;
  if (mode.ends_with("fast")) { compression = png.Compression.Fast; }
  let opts: png.Options = png.Options { filter: filter, compression: compression };
  for name in [NAMES] {
    match (read_file_bytes(args()[1] + "/" + name + ".rgb")) {
      Ok(pixels) => {
        let start: i64 = now_ns();
        match (ENCODE) {
          Ok(image) => {
            let ns: i64 = now_ns() - start;
            if (args().len() > 3) { match (stdout().write_bytes(image)) { Ok(_) => {}, Err(_) => { return 1; }, } }
            else { print(name + "," + ns.to_string() + "," + image.len().to_string()); }
          },
          Err(_) => { return 2; },
        }
      },
      Err(_) => { return 3; },
    }
  }
  return 0;
}
'''


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--fern", type=Path, required=True)
    parser.add_argument("--runs", type=int, default=7)
    parser.add_argument("--baseline", default="e20f0ae15")
    parser.add_argument("--png-include", type=Path)
    parser.add_argument("--png-library", default="-lpng")
    parser.add_argument("--output", type=Path, required=True)
    options = parser.parse_args()
    if options.runs < 1: parser.error("--runs must be positive")
    compiler = str(options.fern.resolve())
    environment = dict(os.environ, FERN_PROFILE="off", FERN_SANITIZE="off", FERN_LEAKCHECK="off")
    names = sorted(case[0] for case in tile_bench.CASES)
    with tempfile.TemporaryDirectory(prefix="fractal-png-") as temporary:
        directory = Path(temporary)
        for name in ["fractal", "kernel", "linear", "series"]:
            shutil.copyfile(ROOT / ("examples/fractal/" + name + ".fern"), directory / (name + ".fern"))
        fixture_driver = 'import "./fractal";\nfunction main(): i32 {\n'
        for _, z, x, y in sorted(tile_bench.CASES):
            fixture_driver += f'match (fractal.render_reference({z}, {x}, {y})) {{ Ok(tile) => {{ match (stdout().write_bytes(tile.png)) {{ Ok(_) => {{}}, Err(_) => {{ return 1; }}, }} }}, Err(_) => {{ return 2; }}, }}\n'
        fixture_driver += 'return 0; }\n'
        (directory / "fixtures.fern").write_text(fixture_driver)
        subprocess.run([compiler, "-target", "x86-64-linux", "-O", "-o", str(directory / "fixtures"), str(directory / "fixtures.fern"), str(ROOT / "internal/stdlib")], env=environment, check=True)
        fixture_bytes = decode_images(subprocess.check_output([str(directory / "fixtures")], env=environment))
        fixtures = dict(zip(names, fixture_bytes, strict=True))
        for name, pixels in fixtures.items(): (directory / (name + ".rgb")).write_bytes(pixels)
        baseline_source = subprocess.check_output(["git", "show", options.baseline + ":internal/stdlib/std/png.fern"], cwd=ROOT)
        (directory / "baseline_png.fern").write_bytes(baseline_source)
        for variant in ["baseline", "candidate"]:
            source = DRIVER.replace("NAMES", ",".join(json.dumps(name) for name in names))
            if variant == "baseline":
                a, b = source.index("  let filter:"), source.index("  for name")
                source = source[:a] + source[b:]
                source = source.replace("IMPORT", 'import "./baseline_png" as png;').replace("ENCODE", "png.encode_rgb8(256, 256, pixels)")
            else:
                source = source.replace("IMPORT", 'import "std/png";').replace("ENCODE", "png.encode_rgb8_with_options(256, 256, pixels, opts)")
            (directory / (variant + ".fern")).write_text(source)
            subprocess.run([compiler, "-target", "x86-64-linux", "-O", "-o", str(directory / variant), str(directory / (variant + ".fern")), str(ROOT / "internal/stdlib")], env=environment, check=True)
        flags = ["g++", "-O3", "-std=c++20", str(HERE / "png-reference.cpp")]
        if options.png_include: flags += ["-I", str(options.png_include)]
        subprocess.run(flags + [options.png_library, "-lz", "-o", str(directory / "libpng")], check=True)
        for mode in MODES:
            encoded = subprocess.check_output([str(directory / "candidate"), temporary, mode, "--raw"], env=environment)
            if decode_images(encoded) != fixture_bytes: raise ValueError("decoded pixels differ: " + mode)
        variants = ["baseline"] + ["fern-" + mode for mode in MODES] + ["libpng-" + mode for mode in ["none-fast", "sub-fast", "adaptive-fast", "adaptive-balanced"]]
        runs = {name: [] for name in variants}
        for run in range(options.runs):
            for variant in variants[run % len(variants):] + variants[:run % len(variants)]:
                mode = variant.split("-", 1)[-1]
                command = [str(directory / "libpng"), mode, temporary] if variant.startswith("libpng-") else [str(directory / ("baseline" if variant == "baseline" else "candidate")), temporary, mode]
                text = subprocess.check_output(command, env=environment, text=True)
                values = {}
                for line in text.splitlines():
                    case, ns, length = line.split(",")
                    values[case] = dict(ms=int(ns) / 1e6, bytes=int(length))
                if set(values) != set(names): raise ValueError("benchmark cases differ")
                runs[variant].append(values)
            print(f"Completed PNG benchmark round {run + 1}/{options.runs}", flush=True)
    summary = {name: {variant: dict(ms=statistics.median(run[name]["ms"] for run in samples), bytes=samples[0][name]["bytes"]) for variant, samples in runs.items()} for name in names}
    report = dict(baseline_revision=options.baseline, compiler_sha256=hashlib.sha256(Path(compiler).read_bytes()).hexdigest(),
        source_sha256={str(path.relative_to(ROOT)): hashlib.sha256(path.read_bytes()).hexdigest() for path in [ROOT / "internal/stdlib/std/png.fern", ROOT / "internal/stdlib/std/deflate.fern"]},
        fixture_sha256={name: hashlib.sha256(data).hexdigest() for name, data in fixtures.items()}, all_pixels_match=True, runs=runs, summary=summary)
    options.output.write_text(json.dumps(report, indent=2) + "\n")
    for name, row in summary.items():
        print(name, json.dumps({variant: row[variant] for variant in ["baseline", "fern-none-balanced", "fern-sub-fast", "fern-adaptive-fast", "libpng-adaptive-fast"]}))


if __name__ == "__main__": main()
