"""Compare MCU traversal with independently sorted component block grids."""

import argparse
import hashlib
import itertools
import json
from pathlib import Path
import re
import struct
import subprocess
import sys
import tempfile


def sha(data):
    return hashlib.sha256(data).hexdigest()


def cases():
    patterns = [((1, 1),), ((4, 4),), ((1, 1),) * 3,
                ((2, 2), (1, 1), (1, 1)), ((3, 1), (2, 2)),
                ((4, 1), (1, 2), (2, 2)), ((2, 2), (2, 1), (1, 2), (1, 1))]
    for width, height in [(1, 1), (7, 9), (17, 19), (25, 17), (33, 35)]:
        for factors in patterns:
            yield width, height, 2147483647, factors, 1
    for size in [-2147483648, -1, 0, 65536, 2147483647]:
        yield size, 1, -1, (), 1
        yield 1, size, -1, ((1, 1),), 1
    for factors in [(), ((1, 1),) * 5, ((0, 1),), ((1, 0),), ((5, 1),), ((1, 255),), ((4, 2), (3, 1))]:
        yield 1, 1, -1, factors, 1
    for limit in [-1, 0, 2, 3]:
        yield 1, 1, limit, ((1, 1),) * 3, 1
    for factors in patterns:
        yield 65535, 65535, 2147483647, factors, 0
    yield 65535, 65535, 83886079, patterns[5], 0
    yield 65535, 65535, 83886080, patterns[5], 0
    factors = list(itertools.product(range(1, 5), repeat=2))
    for count in range(1, 5):
        for selection in itertools.product(factors, repeat=count):
            yield 17, 19, 2147483647, selection, 1


def encoded(case):
    width, height, limit, factors, traverse = case
    payload = [value for item in factors[:4] for value in item]
    return struct.pack(">5i8B", width, height, limit, len(factors), traverse, *(payload + [0] * (8 - len(payload))))


def expected(case, index):
    width, height, limit, factors, traverse = case
    error = None
    if not (1 <= width <= 65535 and 1 <= height <= 65535):
        error = "Dimensions"
    elif not 1 <= len(factors) <= 4:
        error = "Components"
    elif any(not (1 <= h <= 4 and 1 <= v <= 4) for h, v in factors):
        error = "Sampling"
    elif len(factors) > 1 and sum(h * v for h, v in factors) > 10:
        error = "Sampling"
    if error:
        return [f"error {index} {error}"]
    hmax, vmax = map(max, zip(*factors))
    interleaved = len(factors) > 1
    span_x, span_y = (8 * hmax, 8 * vmax) if interleaved else (8, 8)
    columns, rows = -(-width // span_x), -(-height // span_y)
    units = sum(h * v for h, v in factors) if interleaved else 1
    count = columns * rows * units
    if limit < count:
        return [f"error {index} Limit"]
    output = [f"plan {index} {columns} {rows} {columns * rows} {units} {count} {hmax} {vmax}"]
    if not traverse:
        return output
    addresses, first = [], 0
    for component, (h, v) in enumerate(factors):
        actual_w, actual_h = -(-width * h // hmax), -(-height * v // vmax)
        if not interleaved:
            h = v = 1
        for by, bx in itertools.product(range(rows * v), range(columns * h)):
            mcu = by // v * columns + bx // h
            unit = first + by % v * h + bx % h
            dummy = int(bx * 8 >= actual_w or by * 8 >= actual_h)
            addresses.append((mcu, unit, component, bx, by, actual_w, actual_h, dummy))
        first += h * v
    output.extend(f"block {index} " + " ".join(map(str, row)) for row in sorted(addresses))
    return output


def check_output(stdout, stderr, inputs):
    census = re.fullmatch(r"leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0\n?", stderr)
    if census is None or int(census[1]) <= 0 or census[1] != census[2]:
        raise ValueError("invalid MCU worker heap census")
    wanted = [line for index, case in enumerate(inputs) for line in expected(case, index)]
    if stdout.splitlines() != wanted:
        raise ValueError("MCU order differs from component grids")
    return {"allocs": int(census[1]), "frees": int(census[2]), "live_bytes": 0,
            "observation_sha256": sha(stdout.encode()), "lines": len(wanted)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=Path)
    parser.add_argument("compiler", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--wasmtime", type=Path)
    parser.add_argument("--max-cases", type=int)
    args = parser.parse_args()
    if args.max_cases is not None and args.max_cases <= 0:
        parser.error("--max-cases must be positive")
    repo = Path(__file__).resolve().parents[2]
    paths = [Path(__file__), Path(__file__).with_name("jpeg_mcu_order.fern"),
             Path(__file__).with_name("jpeg_mcu_order_test.fern"), Path(__file__).with_name("qualify_jpeg_mcu_order_test.py"),
             repo / "fern-image/scripts/test-jpeg-mcu-order.sh", repo / ".github/workflows/fern-image.yml",
             args.binary.resolve(), args.compiler.resolve(), Path(sys.executable).resolve()]
    paths += [path for folder in ["fern-image/packages", "compiler", "internal/stdlib"]
              for path in sorted((repo / folder).rglob("*")) if path.is_file() and path.suffix in [".fern", ".md"]]
    if args.wasmtime:
        paths.append(args.wasmtime.resolve())
    frozen = {str(path): sha(path.read_bytes()) for path in paths}
    inputs = list(itertools.islice(cases(), args.max_cases))
    runner = [str(args.wasmtime.resolve()), "run", "--dir=."] if args.wasmtime else []
    args.output.parent.mkdir(parents=True, exist_ok=True)
    temporary = Path(tempfile.mkdtemp(prefix="mcu-order-", dir=args.output.parent.resolve()))
    record = {"complete": False, "cases": len(inputs), "max_cases": args.max_cases, "files_sha256": frozen,
              "batch_size": 256, "timeout_seconds": 20, "batches": [],
              "scope": "Complete single-scan DCT geometry only. No sample, entropy, performance or physical bounds claim."}
    try:
        for first in range(0, len(inputs), 256):
            batch = inputs[first:first + 256]
            source = temporary / "input.bin"
            payload = b"".join(map(encoded, batch))
            source.write_bytes(payload)
            record["active_batch"] = first
            result = subprocess.run([*runner, str(args.binary.resolve()), source.name], cwd=temporary,
                                    capture_output=True, text=True, timeout=20)
            (temporary / "stdout.txt").write_text(result.stdout)
            (temporary / "stderr.txt").write_text(result.stderr)
            result.check_returncode()
            if source.read_bytes() != payload:
                raise ValueError("MCU input changed")
            observation = check_output(result.stdout, result.stderr, batch)
            record["batches"].append({"first": first, "count": len(batch), "input_sha256": sha(payload), **observation})
            record.pop("active_batch")
        if frozen != {str(path): sha(path.read_bytes()) for path in paths}:
            raise ValueError("MCU source or tool changed")
        record["complete"] = True
        for path in temporary.iterdir():
            path.unlink()
        temporary.rmdir()
    except BaseException as error:
        record["failure"] = str(error)
        record["failure_directory"] = str(temporary)
        if isinstance(error, subprocess.TimeoutExpired):
            for name, value in [("stdout.txt", error.stdout), ("stderr.txt", error.stderr)]:
                if value is not None:
                    (temporary / name).write_bytes(value.encode() if isinstance(value, str) else value)
        raise
    finally:
        args.output.write_text(json.dumps(record, indent=2) + "\n")
    print(json.dumps({"complete": True, "cases": len(inputs), "batches": len(record["batches"])}))


if __name__ == "__main__":
    main()
