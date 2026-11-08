"""Compare complete padded blocks with independent source-coordinate rules."""

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


def encoded(case):
    return struct.pack(">7iBB", *case[:9]) + case[9]


def expected(case):
    width, height, stride, columns, rows, bx, by, edge, fill, data = case
    if width <= 0 or height <= 0 or stride < width:
        return "error Layout"
    if (height - 1) * stride + width > len(data):
        return "error Storage"
    if columns < -(-width // 8) or rows < -(-height // 8):
        return "error Layout"
    if not (0 <= bx < columns and 0 <= by < rows):
        return "error Bounds"
    output = []
    for y, x in itertools.product(range(by * 8, by * 8 + 8), range(bx * 8, bx * 8 + 8)):
        outside = x >= width or y >= height
        output.append(fill if outside and edge else data[min(y, height - 1) * stride + min(x, width - 1)])
    return "ok " + bytes(output).hex()


def cases():
    for width, height, extra in itertools.product([1, 7, 8, 9, 17], [1, 7, 8, 9, 17], [0, 3]):
        stride = width + extra
        data = bytes((x * 37 + y * 17 + 11) % 256 if x < width else 221 for y in range(height) for x in range(stride))
        actual_c, actual_r = -(-width // 8), -(-height // 8)
        for padx, pady in [(0, 0), (1, 0), (0, 1), (1, 1), (3, 3)]:
            columns, rows = actual_c + padx, actual_r + pady
            positions = {(0, 0), (columns - 1, 0), (0, rows - 1), (columns - 1, rows - 1), (actual_c - 1, actual_r - 1)}
            for minimal in [False, True]:
                payload = data[:(height - 1) * stride + width] if minimal else data
                for (bx, by), (edge, fill) in itertools.product(sorted(positions), [(0, 0), (1, 0), (1, 128), (1, 255)]):
                    yield width, height, stride, columns, rows, bx, by, edge, fill, payload
    for value in range(256):
        yield 1, 1, 1, 2, 2, 1, 1, 1, value, b"\x11"
    for bx, by in [(2147483646, 0), (0, 2147483646), (2147483646, 2147483646)]:
        for edge, fill in [(0, 0), (1, 0), (1, 128), (1, 255)]:
            yield 1, 1, 2147483647, 2147483647, 2147483647, bx, by, edge, fill, b"\x11"
    for layout in [(0, 1, 1), (1, 0, 1), (-1, 1, 1), (1, -1, 1), (2, 1, 1), (1, 1, -1),
                   (2147483647, 2147483647, 2147483647)]:
        yield *layout, 0, 0, -1, -1, 0, 0, b""
    for columns, rows in [(0, 1), (1, 0), (-2147483648, 1), (1, -1)]:
        yield 1, 1, 1, columns, rows, -1, -1, 0, 0, b"\x11"
    for bx, by in [(-1, 0), (0, -1), (2, 0), (0, 2), (2147483647, 2147483647)]:
        yield 1, 1, 1, 2, 2, bx, by, 0, 0, b"\x11"
    yield 1, 1, 1, 2, 2, 1, 1, 0, 0, b""


def check_output(stdout, stderr, inputs, names):
    census = re.fullmatch(r"leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0\n?", stderr)
    if census is None or int(census[1]) <= 0 or census[1] != census[2]:
        raise ValueError("invalid padded block heap census")
    wanted = [name + " " + expected(case) for name, case in zip(names, inputs)]
    if len(names) != len(inputs) or stdout.splitlines() != wanted:
        raise ValueError("padded sample observation differs")
    return {"allocs": int(census[1]), "frees": int(census[2]), "live_bytes": 0, "observation_sha256": sha(stdout.encode())}


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
    paths = [Path(__file__), Path(__file__).with_name("jpeg_padded_blocks.fern"), Path(__file__).with_name("jpeg_padded_blocks_test.fern"),
             Path(__file__).with_name("qualify_jpeg_padded_blocks_test.py"), repo / "fern-image/scripts/test-jpeg-padded-blocks.sh",
             repo / ".github/workflows/fern-image.yml", args.binary.resolve(), args.compiler.resolve(), Path(sys.executable).resolve()]
    paths += [path for folder in ["fern-image/packages", "compiler", "internal/stdlib"]
              for path in sorted((repo / folder).rglob("*")) if path.is_file() and path.suffix in [".fern", ".md"]]
    if args.wasmtime:
        paths.append(args.wasmtime.resolve())
    frozen = {str(path): sha(path.read_bytes()) for path in paths}
    inputs = list(itertools.islice(cases(), args.max_cases))
    runner = [str(args.wasmtime.resolve()), "run", "--dir=."] if args.wasmtime else []
    args.output.parent.mkdir(parents=True, exist_ok=True)
    temporary = Path(tempfile.mkdtemp(prefix="padded-blocks-", dir=args.output.parent.resolve()))
    record = {"complete": False, "cases": len(inputs), "max_cases": args.max_cases, "files_sha256": frozen,
              "batch_size": 16, "timeout_seconds": 20, "batches": [], "samples": [],
              "scope": "Finite complete padded component blocks only. No image writer, performance or physical bounds claim."}
    try:
        for first in range(0, len(inputs), 16):
            batch = inputs[first:first + 16]
            names = [f"block-{first + i}.raw" for i in range(len(batch))]
            for name, case in zip(names, batch):
                (temporary / name).write_bytes(encoded(case))
            record["active_batch"] = first
            result = subprocess.run([*runner, str(args.binary.resolve()), *names], cwd=temporary,
                                    capture_output=True, text=True, timeout=20)
            (temporary / "stdout.txt").write_text(result.stdout)
            (temporary / "stderr.txt").write_text(result.stderr)
            result.check_returncode()
            for name, case in zip(names, batch):
                if (temporary / name).read_bytes() != encoded(case):
                    raise ValueError("padded block input changed")
            observation = check_output(result.stdout, result.stderr, batch, names)
            record["batches"].append({"first": first, "count": len(batch), "input_sha256": sha(b"".join(map(encoded, batch))), **observation})
            record["samples"].extend({"input_sha256": sha(encoded(case)), "observation": line[len(name) + 1:]}
                                     for name, case, line in zip(names, batch, result.stdout.splitlines()))
            for name in names:
                (temporary / name).unlink()
            record.pop("active_batch")
        if frozen != {str(path): sha(path.read_bytes()) for path in paths}:
            raise ValueError("padded source or tool changed")
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
