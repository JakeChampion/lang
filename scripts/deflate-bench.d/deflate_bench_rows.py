#!/usr/bin/env python3
"""Summarise a scripts/deflate-bench CSV.

Each CSV row is one implementation on one Silesia file: `fern` (std/deflate,
which has one compression setting) or `libdeflate` at a level from 1 to 12.
For each file the summary compares Fern with the libdeflate level whose
compressed size is nearest to Fern's, so the two did comparable work. A
decompression comparison also decodes the same libdeflate level-6 stream with
both decoders. Medians are over files, as in rtfeldman.com/fp-vs-c.
scripts/deflate-bench.d/test_deflate_bench_rows.py covers the arithmetic.
"""
import csv
import statistics
import sys


def load(path):
    with open(path, newline="") as f:
        return list(csv.DictReader(f))


def mb_per_s(nbytes, ns):
    return (nbytes / 1e6) / (ns / 1e9)


def summarise(rows):
    """One entry per file, sorted by name, and the corpus medians."""
    files = sorted({r["file"] for r in rows})
    per_file = []
    for name in files:
        fern = next(r for r in rows if r["file"] == name and r["impl"] == "fern")
        libs = [r for r in rows if r["file"] == name and r["impl"] == "libdeflate"]
        near = min(libs, key=lambda r: abs(int(r["out"]) - int(fern["out"])))
        l6 = next(r for r in libs if r["level"] == "6")
        size = int(fern["in"])
        per_file.append({
            "file": name,
            "bytes": size,
            "fern_ratio": size / int(fern["out"]),
            "near_level": int(near["level"]),
            "near_ratio": size / int(near["out"]),
            "fern_comp": mb_per_s(size, int(fern["comp_ns"])),
            "near_comp": mb_per_s(size, int(near["comp_ns"])),
            "comp_slowdown": int(fern["comp_ns"]) / int(near["comp_ns"]),
            "fern_decomp": mb_per_s(size, int(fern["decomp_ns"])),
            "near_decomp": mb_per_s(size, int(near["decomp_ns"])),
            "decomp_slowdown": int(fern["decomp_ns"]) / int(near["decomp_ns"]),
            "same_stream_slowdown": int(l6["cross_decomp_ns"]) / int(l6["decomp_ns"]),
        })
    medians = {
        key: statistics.median(f[key] for f in per_file)
        for key in ("comp_slowdown", "decomp_slowdown", "same_stream_slowdown")
    }
    return per_file, medians


def main(argv):
    per_file, medians = summarise(load(argv[1]))
    print(f"{'file':9} {'MB':>6} {'ratio':>6} {'level':>5} {'C ratio':>7} "
          f"{'comp MB/s':>9} {'C':>7} {'slower':>6} {'decomp MB/s':>11} {'C':>7} {'slower':>6}")
    for f in per_file:
        print(f"{f['file']:9} {f['bytes'] / 1e6:6.1f} {f['fern_ratio']:6.3f} {f['near_level']:5d} "
              f"{f['near_ratio']:7.3f} {f['fern_comp']:9.1f} {f['near_comp']:7.1f} "
              f"{f['comp_slowdown']:5.1f}x {f['fern_decomp']:11.1f} {f['near_decomp']:7.1f} "
              f"{f['decomp_slowdown']:5.1f}x")
    print(f"median compression slowdown: {medians['comp_slowdown']:.2f}x")
    print(f"median decompression slowdown: {medians['decomp_slowdown']:.2f}x")
    print(f"median decompression slowdown on one level-6 stream: {medians['same_stream_slowdown']:.2f}x")


if __name__ == "__main__":
    main(sys.argv)
