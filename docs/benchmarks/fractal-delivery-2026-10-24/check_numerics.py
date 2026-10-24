#!/usr/bin/env python3
"""Check seeded random pixels, tile edges, and reference-center changes at 113-bit precision."""
import argparse
import hashlib
import importlib.util
import json
import math
import os
from pathlib import Path
import shutil
import struct
import subprocess
import tempfile

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
spec = importlib.util.spec_from_file_location("tile_bench", HERE.parent / "fractal-2026-10-23/compare.py")
tile_bench = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tile_bench)
CASES = list(tile_bench.CASES)
for name, real, imaginary in [("cardioid-cusp42", 0.25, 0.0), ("period-two-tangent42", -1.25, 0.0), ("critical-orbit38", -0.75, 0.0)]:
    z = 38 if name.endswith("38") else 42
    CASES.append((name, z, int((real + 2.5) / 4 * 2**z), int((2 - imaginary) / 4 * 2**z)))
CASES += [("dive38-east", 38, 120695873045, 128379946314), ("dive42-east", 42, 1931133968719, 2054079141036), ("dive42-south", 42, 1931133968718, 2054079141037)]
PER_CASE = 801


def metrics(values, ground):
    errors = sorted(abs(a - b) for a, b in zip(values, ground) if a >= 0 and b >= 0)
    return dict(samples=len(values), classification_mismatches=sum((a < 0) != (b < 0) for a, b in zip(values, ground)),
        count_mismatches=sum(a != b for a, b in zip(values, ground)),
        count_error_p95=errors[math.ceil(len(errors) * .95) - 1] if errors else 0,
        max_count_error=max(errors, default=0))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--fern", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--check", action="store_true")
    options = parser.parse_args()
    environment = dict(os.environ, FERN_PROFILE="off", FERN_SANITIZE="off", FERN_LEAKCHECK="off")
    with tempfile.TemporaryDirectory(prefix="fractal-numerics-") as temporary:
        directory = Path(temporary)
        for name in ["kernel", "linear", "series"]:
            shutil.copyfile(ROOT / ("examples/fractal/" + name + ".fern"), directory / (name + ".fern"))
        calls = "\n".join(f"  sample({z}, {x}, {y});" for _, z, x, y in CASES)
        (directory / "samples.fern").write_text((HERE / "numeric-samples.fern.in").read_text().replace("CALLS", calls))
        subprocess.run([str(options.fern.resolve()), "-target", "x86-64-linux", "-O", "-o", str(directory / "samples"), str(directory / "samples.fern"), str(ROOT / "internal/stdlib")], env=environment, check=True)
        oracle = HERE.parent / "fractal-acceleration-2026-10-23/quad.cpp"
        subprocess.run(["g++", "-O2", "-ffp-contract=off", str(oracle), "-o", str(directory / "quad")], check=True)
        samples = list(struct.iter_unpack(">ddIiii", subprocess.check_output([str(directory / "samples")], env=environment)))
        if len(samples) != len(CASES) * PER_CASE: raise ValueError("sample count differs")
        text = "".join(f"{r:.17g} {i:.17g} {limit}\n" for r, i, limit, _, _, _ in samples)
        quad = list(map(int, subprocess.check_output([str(directory / "quad")], input=text, text=True).splitlines()))
    report = dict(samples=len(samples), distribution=dict(grid=289, random=256, edge=256),
        compiler_sha256=hashlib.sha256(options.fern.read_bytes()).hexdigest(),
        source_sha256={name: hashlib.sha256((ROOT / ("examples/fractal/" + name + ".fern")).read_bytes()).hexdigest() for name in ["kernel", "linear", "series"]}, cases={})
    failures = []
    for index, (name, z, x, y) in enumerate(CASES):
        rows = samples[index * PER_CASE:(index + 1) * PER_CASE]
        ground = quad[index * PER_CASE:(index + 1) * PER_CASE]
        a, b, c = ([row[position] for row in rows] for position in [3, 4, 5])
        result = dict(z=z, x=x, y=y, accelerated=metrics(a, ground), direct=metrics(b, ground), shifted_reference=metrics(c, ground), reference_agreement=metrics(a, c))
        result["edge_reference_agreement"] = metrics(a[545:], c[545:])
        result["classification_examples"] = [dict(real=row[0], imaginary=row[1], accelerated=row[3], direct=row[4], shifted=row[5], quad=want) for row, want in zip(rows, ground) if (row[3] < 0) != (want < 0)][:12]
        report["cases"][name] = result
        for label in ["accelerated", "reference_agreement"]:
            value = result[label]
            if value["classification_mismatches"] / value["samples"] > .005 or value["count_error_p95"] > 1:
                failures.append(name + ": " + label)
    report["budgets"] = dict(max_classification_fraction=.005, max_count_error_p95=1)
    report["failures"] = failures
    options.output.write_text(json.dumps(report, indent=2) + "\n")
    for name, result in report["cases"].items():
        print(name, json.dumps({label: result[label] for label in ["accelerated", "direct", "edge_reference_agreement"]}))
    if options.check and failures: raise SystemExit("Numerical error budget exceeded: " + ", ".join(failures))


if __name__ == "__main__": main()
