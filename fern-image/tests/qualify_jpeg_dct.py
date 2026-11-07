"""Compare forward coefficients with an independent high-precision direct sum."""

import argparse
import decimal
import hashlib
import json
from pathlib import Path
import platform
import random
import re
import shutil
import struct
import subprocess
import sys
import tempfile

from generate_jpeg_idct import basis


# Retain the established inverse-transform mathematical qualification threshold.
ABSOLUTE_TOLERANCE = decimal.Decimal("0.000001")


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def cases():
    for value in [0, 128, 255]:
        yield f"control-uniform-{value}", bytes([value] * 64)
    for index in range(64):
        for value in [0, 255]:
            data = bytearray([128] * 64)
            data[index] = value
            yield f"impulse-{index}-{value}", bytes(data)
    for name, data in [
        ("horizontal", [(i % 8) * 31 for i in range(64)]),
        ("vertical", [(i // 8) * 31 for i in range(64)]),
        ("checkerboard", [255 if (i // 8 + i % 8) % 2 else 0 for i in range(64)]),
        ("ramp", [i * 4 for i in range(64)]),
    ]:
        yield name, bytes(data)
    rng = random.Random(20261107)
    for index in range(64):
        yield f"noise-{index}", bytes(rng.randrange(256) for _ in range(64))
    for value in range(256):
        yield f"uniform-{value}", bytes([value] * 64)


def ideal(data, matrix):
    return [sum((decimal.Decimal(data[y * 8 + x] - 128) * matrix[x][u] * matrix[y][v]
                 for y in range(8) for x in range(8)), decimal.Decimal(0))
            for v in range(8) for u in range(8)]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("binary", type=Path)
    parser.add_argument("compiler", type=Path)
    parser.add_argument("output", type=Path)
    target = parser.add_mutually_exclusive_group()
    target.add_argument("--wasmtime", type=Path)
    target.add_argument("--qemu", type=Path)
    target.add_argument("--native-target", choices=["arm64-darwin", "x86-64-linux", "arm64-linux"], default="arm64-darwin")
    parser.add_argument("--max-cases", type=int)
    options = parser.parse_args()
    if options.max_cases is not None and options.max_cases <= 0:
        parser.error("--max-cases must be positive")
    root = Path(__file__).resolve().parents[2]
    binary = options.binary.resolve()
    paths = {"binary": binary, "compiler": options.compiler.resolve(), "collector": Path(__file__).resolve(),
             "reference_math": Path(__file__).with_name("generate_jpeg_idct.py"),
             "python": Path(sys.executable).resolve(), "decimal": Path(decimal.__file__).resolve()}
    import _decimal
    if getattr(_decimal, "__file__", None):
        paths["decimal_extension"] = Path(_decimal.__file__).resolve()
    for folder in ["fern-image/packages", "compiler", "internal/stdlib"]:
        for path in sorted((root / folder).rglob("*")):
            if path.is_file() and (path.suffix == ".fern" or path.name.endswith(".fern.md")):
                paths[str(path.relative_to(root))] = path
    for relative in ["fern-image/tests/jpeg_dct.fern", "fern-image/tests/jpeg_dct_test.fern",
                     "fern-image/scripts/test-jpeg-dct.sh", ".github/workflows/fern-image.yml"]:
        paths[relative] = root / relative
    runner = []
    target_name = options.native_target
    if options.wasmtime:
        paths["runner"] = options.wasmtime.resolve()
        runner = [str(paths["runner"]), "run", "--dir=."]
        target_name = "wasm32-wasi"
    elif options.qemu:
        paths["runner"] = options.qemu.resolve()
        runner = [str(paths["runner"])]
        target_name = "arm64-linux"
    hashes = {name: sha(path) for name, path in paths.items()}
    inputs = list(cases())
    if options.max_cases is not None:
        inputs = inputs[:options.max_cases]
    options.output.parent.mkdir(parents=True, exist_ok=True)
    directory = Path(tempfile.mkdtemp(prefix="jpeg-dct-", dir=options.output.parent.resolve()))
    record = dict(status="running", target=target_name, host=platform.platform(), Python=platform.python_version(),
                  source_commit=subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
                  sha256=hashes, absolute_tolerance=str(ABSOLUTE_TOLERANCE), decimal_precision=90,
                  max_cases=options.max_cases, samples=[], batches=[],
                  scope="Finite forward block-transform math checks. No complete JPEG writer, quality bound, precision certification, physical memory or performance claim.")
    try:
        with decimal.localcontext() as context:
            context.prec = 90
            matrix = basis()
            for first in range(0, len(inputs), 8):
                batch = inputs[first:first + 8]
                names = []
                for index, (_, data) in enumerate(batch):
                    name = f"block-{first + index}.raw"
                    (directory / name).write_bytes(data)
                    names.append(name)
                result = subprocess.run([*runner, str(binary), *names], cwd=directory, capture_output=True,
                                        text=True, timeout=20, check=True)
                census = re.fullmatch(r"leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0\n?", result.stderr)
                if not census or int(census[1]) <= 0 or census[1] != census[2]:
                    raise ValueError("empty or unbalanced worker heap")
                lines = result.stdout.splitlines()
                if len(lines) != len(batch):
                    raise ValueError("unexpected worker output count")
                for (name, data), filename, line in zip(batch, names, lines, strict=True):
                    fields = line.split()
                    if len(fields) != 65 or fields[0] != filename:
                        raise ValueError("unexpected coefficient shape or order")
                    binary_values = [float(value) for value in fields[1:]]
                    values = [decimal.Decimal.from_float(value) for value in binary_values]
                    if not all(value.is_finite() for value in values):
                        raise ValueError("nonfinite coefficient")
                    maximum = max(abs(actual - expected) for actual, expected in zip(values, ideal(data, matrix), strict=True))
                    if maximum > ABSOLUTE_TOLERANCE:
                        raise ValueError(f"forward coefficient mismatch in {name}: {maximum}")
                    if values[0] != decimal.Decimal(sum(value - 128 for value in data)) / 8:
                        raise ValueError("DC coefficient is not exact")
                    if len(set(data)) == 1 and any(value != 0 for value in values[1:]):
                        raise ValueError("uniform block has nonzero AC coefficients")
                    if (directory / filename).read_bytes() != data:
                        raise ValueError("caller input changed")
                    record["samples"].append(dict(name=name, input_sha256=hashlib.sha256(data).hexdigest(),
                                                  max_absolute_error=str(maximum),
                                                  binary64_sha256=hashlib.sha256(struct.pack("<64d", *binary_values)).hexdigest()))
                    (directory / filename).unlink()
                record["batches"].append(dict(first=first, count=len(batch), allocs=int(census[1]), frees=int(census[2]), live_bytes=0))
                print(f"passed {len(record['samples'])}/{len(inputs)}", flush=True)
        if hashes != {name: sha(path) for name, path in paths.items()}:
            raise ValueError("source or tool changed during qualification")
        record["status"] = "passed"
        shutil.rmtree(directory)
    except BaseException:
        record["status"] = "failed"
        record["failure_workspace"] = str(directory)
        raise
    finally:
        options.output.write_text(json.dumps(record, indent=2) + "\n")


if __name__ == "__main__":
    main()
