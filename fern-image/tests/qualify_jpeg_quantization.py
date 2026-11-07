"""Check quantized binary64 quotients against exact Decimal integer rounding."""

import argparse
import decimal
import hashlib
import json
import math
from pathlib import Path
import platform
import random
import re
import shutil
import struct
import subprocess
import sys
import tempfile


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def order():
    result = []
    for diagonal in range(15):
        low, high = max(0, diagonal - 7), min(7, diagonal)
        rows = range(high, low - 1, -1) if diagonal % 2 == 0 else range(low, high + 1)
        result.extend(y * 8 + diagonal - y for y in rows)
    assert sorted(result) == list(range(64))
    return result


def expected(coefficients, quantizers):
    result = [0] * 64
    for natural, step in zip(order(), quantizers, strict=True):
        if step == 0:
            return ["error", "Quantizer"]
        if not math.isfinite(coefficients[natural]):
            return ["error", "NonFinite"]
        quotient = coefficients[natural] / float(step)
        integer = int(decimal.Decimal.from_float(quotient).to_integral_value(rounding=decimal.ROUND_HALF_UP))
        if not -32768 <= integer <= 32767:
            return ["error", "Coefficient"]
        result[natural] = integer
    return ["ok", *map(str, result)]


def cases():
    for value in [0.0, -0.0, 0.5, -0.5, math.nextafter(0.5, 0.0),
                  -math.nextafter(0.5, 0.0), math.nextafter(0.5, 1.0), -math.nextafter(0.5, 1.0),
                  32767.0, -32768.0, 32767.499999999996, -32768.49999999999,
                  32767.5, -32768.5, math.ulp(0.0), -math.ulp(0.0), sys.float_info.max, -sys.float_info.max]:
        yield f"boundary-{value.hex()}", [value] * 64, [1] * 64
    for step in range(1, 256):
        coefficients = [0.0] * 64
        for wire, natural in enumerate(order()):
            value = (wire % 7 - 3 + 0.5) * step
            if wire % 3 == 1:
                value = math.nextafter(value, -math.inf)
            elif wire % 3 == 2:
                value = math.nextafter(value, math.inf)
            coefficients[natural] = value
        yield f"step-{step}", coefficients, [step] * 64
    for position in range(64):
        quantizers = [1] * 64
        quantizers[position] = 0
        yield f"zero-quantizer-{position}", [0.0] * 64, quantizers
        for name, value in [("nan", math.nan), ("inf", math.inf), ("negative-inf", -math.inf),
                            ("high", 32767.5), ("low", -32768.5)]:
            coefficients = [0.0] * 64
            coefficients[position] = value
            yield f"{name}-{position}", coefficients, [1] * 64
    rng = random.Random(20261107)
    for index in range(64):
        yield f"noise-{index}", [rng.uniform(-32768, 32768) for _ in range(64)], [rng.randrange(1, 256) for _ in range(64)]


def check_output(stdout, stderr, wanted):
    census = re.fullmatch(r"leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0\n?", stderr)
    if not census or int(census[1]) <= 0 or census[1] != census[2]:
        raise ValueError("empty or unbalanced worker heap")
    actual = [line.split() for line in stdout.splitlines()]
    if actual != wanted:
        raise ValueError("quantizer output differs from exact reference")
    return dict(allocs=int(census[1]), frees=int(census[2]), live_bytes=0)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=Path)
    parser.add_argument("compiler", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--wasmtime", type=Path)
    parser.add_argument("--max-cases", type=int)
    options = parser.parse_args()
    if options.max_cases is not None and options.max_cases <= 0:
        parser.error("--max-cases must be positive")
    root = Path(__file__).resolve().parents[2]
    paths = {"binary": options.binary.resolve(), "compiler": options.compiler.resolve(),
             "collector": Path(__file__).resolve(), "python": Path(sys.executable).resolve(),
             "decimal": Path(decimal.__file__).resolve()}
    for directory in ["compiler", "internal/stdlib", "fern-image/packages"]:
        for path in sorted((root / directory).rglob("*")):
            if path.is_file() and path.name.endswith((".fern", ".fern.md")):
                paths[str(path.relative_to(root))] = path
    for relative in ["fern-image/tests/jpeg_quantization.fern", "fern-image/tests/jpeg_quantization_test.fern",
                     "fern-image/scripts/test-jpeg-quantization.sh", ".github/workflows/fern-image.yml"]:
        paths[relative] = root / relative
    import _decimal
    if getattr(_decimal, "__file__", None):
        paths["decimal_extension"] = Path(_decimal.__file__).resolve()
    runner = []
    if options.wasmtime:
        paths["runner"] = options.wasmtime.resolve()
        runner = [str(paths["runner"]), "run", "--dir=."]
    hashes = {name: sha(path) for name, path in paths.items()}
    inputs = list(cases())
    if options.max_cases is not None:
        inputs = inputs[:options.max_cases]
    options.output.parent.mkdir(parents=True, exist_ok=True)
    directory = Path(tempfile.mkdtemp(prefix="jpeg-quantization-", dir=options.output.parent.resolve()))
    record = dict(status="running", target="wasm32-wasi" if runner else "native", host=platform.platform(),
                  sha256=hashes, max_cases=options.max_cases, samples=[], batches=[],
                  criterion="Exact nearest integer of each binary64 quotient, with ties away from zero.",
                  scope="Finite internal quantizer observations. No complete JPEG writer, precision certification, quality, performance or physical memory claim.")
    try:
        for first in range(0, len(inputs), 8):
            batch = inputs[first:first + 8]
            names, payloads = [], []
            for index, (_, coefficients, quantizers) in enumerate(batch):
                payload = struct.pack(">64d64B", *coefficients, *quantizers)
                name = f"block-{first + index}.raw"
                (directory / name).write_bytes(payload)
                names.append(name)
                payloads.append(payload)
            result = subprocess.run([*runner, str(paths["binary"]), *names], cwd=directory,
                                    capture_output=True, text=True, timeout=20, check=True)
            wanted = [[filename, *expected(coefficients, quantizers)]
                      for (_, coefficients, quantizers), filename in zip(batch, names, strict=True)]
            census = check_output(result.stdout, result.stderr, wanted)
            for (name, _, _), filename, payload, row in zip(batch, names, payloads, wanted, strict=True):
                if (directory / filename).read_bytes() != payload:
                    raise ValueError("caller input changed")
                record["samples"].append(dict(name=name, input_sha256=hashlib.sha256(payload).hexdigest(), result=row[1:]))
                (directory / filename).unlink()
            record["batches"].append(dict(first=first, count=len(batch), **census))
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
