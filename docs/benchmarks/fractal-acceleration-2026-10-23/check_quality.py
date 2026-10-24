#!/usr/bin/env python3
"""Compare 2,601 stratified pixels with an independent 113-bit escape oracle."""
import argparse
import importlib.util
import json
from pathlib import Path
import shutil
import struct
import subprocess
import tempfile

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
spec = importlib.util.spec_from_file_location("compare", HERE.parent / "fractal-2026-10-23/compare.py")
compare = importlib.util.module_from_spec(spec)
spec.loader.exec_module(compare)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--fern", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    options = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="fractal-quality-") as temporary:
        directory = Path(temporary)
        for name in ["kernel", "linear", "series"]:
            shutil.copyfile(ROOT / ("examples/fractal/" + name + ".fern"), directory / (name + ".fern"))
        calls = "\n".join(f"  sample({z}, {x}, {y});" for _, z, x, y in compare.CASES)
        (directory / "samples.fern").write_text((HERE / "samples.fern.in").read_text().replace("CALLS", calls))
        subprocess.run([str(options.fern.resolve()), "-target", "x86-64-linux", "-O", "-o", str(directory / "samples"), str(directory / "samples.fern"), str(ROOT / "internal/stdlib")], check=True)
        subprocess.run(["g++", "-O2", "-ffp-contract=off", str(HERE / "quad.cpp"), "-o", str(directory / "quad")], check=True)
        data = subprocess.check_output([str(directory / "samples")])
        samples = list(struct.iter_unpack(">ddIii", data))
        if len(samples) != len(compare.CASES) * 289:
            raise ValueError("sample count differs")
        text = "".join(f"{r:.17g} {i:.17g} {limit}\n" for r, i, limit, _, _ in samples)
        quad = list(map(int, subprocess.check_output([str(directory / "quad")], input=text, text=True).splitlines()))
    report = {}
    for index, (name, _, _, _) in enumerate(compare.CASES):
        cases = samples[index * 289:(index + 1) * 289]
        ground = quad[index * 289:(index + 1) * 289]
        report[name] = {}
        for engine, position in [("accelerated", 3), ("direct", 4)]:
            values = [case[position] for case in cases]
            report[name][engine] = dict(samples=len(values),
                classification_mismatches=sum((a < 0) != (b < 0) for a, b in zip(values, ground)),
                escape_count_mismatches=sum(a != b for a, b in zip(values, ground)),
                max_escape_count_error=max((abs(a - b) for a, b in zip(values, ground) if a >= 0 and b >= 0), default=0))
    options.output.write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
