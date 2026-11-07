"""Compare raw sample words with independent Python integer byte operations."""

import argparse
import hashlib
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
    for width in range(1, 9):
        maximum = 256 ** width - 1
        for order in range(2):
            for value in [0, 1, maximum // 2, maximum]:
                yield width, order, 2, width + 5, value
            if width < 8:
                yield width, order, 2, width + 5, maximum + 1
    for width in [0, 9, 255]:
        yield width, 0, -1, 0, 0
    for width in range(1, 9):
        for offset in [-2147483648, -1, 2147483647, 8 - width + 1]:
            yield width, 0, offset, 8, 0
        yield width, 0, 0, 0, 0
    for value in range(65536):
        for order in range(2):
            yield 2, order, 2, 7, value
    for width, values in [(2, [0x8000, 0x7C00, 0xFC00, 0x7E01, 1]),
                          (4, [0x80000000, 0x7F800000, 0xFF800000, 0x7FC00001, 1]),
                          (8, [0x8000000000000000, 0x7FF0000000000000, 0xFFF0000000000000, 0x7FF8000000000001, 1])]:
        for value in values:
            for order in range(2):
                yield width, order, 2, width + 5, value


def expected(case, index):
    width, order, offset, size, value = case
    if not 1 <= width <= 8:
        return f"error {index} Width", b""
    if offset < 0 or offset + width > size:
        return f"error {index} Bounds", b""
    try:
        word = value.to_bytes(width, "big" if order == 0 else "little")
    except OverflowError:
        return f"error {index} Value", b""
    output = b"\xa5" * offset + word + b"\xa5" * (size - offset - width)
    number = int.from_bytes(word, "big" if order == 0 else "little")
    return f"sample {index} {number}", output


def check_output(stdout, stderr, output, inputs):
    census = re.fullmatch(r"leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)\n?", stderr)
    if census is None or int(census[1]) <= 0 or census[1] != census[2] or census[3] != "0":
        raise ValueError("invalid sample worker heap census")
    wanted = [expected(case, index) for index, case in enumerate(inputs)]
    if stdout.splitlines() != [line for line, _ in wanted]:
        raise ValueError("sample word observation differs")
    if output != b"".join(data for _, data in wanted):
        raise ValueError("sample bytes differ")
    return {"allocs": int(census[1]), "frees": int(census[2]), "live_bytes": 0,
            "observation_sha256": sha(stdout.encode()), "output_sha256": sha(output)}


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
    paths = [Path(__file__), Path(__file__).with_name("sample_bytes.fern"), Path(__file__).with_name("sample_bytes_test.fern"),
             Path(__file__).with_name("qualify_sample_bytes_test.py"), args.binary.resolve(), args.compiler.resolve(), Path(sys.executable).resolve()]
    paths += [repo / "fern-image/scripts/test-sample-bytes.sh", repo / ".github/workflows/fern-image.yml"]
    paths += [path for folder in ["fern-image/packages", "compiler", "internal/stdlib"]
              for path in sorted((repo / folder).rglob("*")) if path.is_file() and path.suffix in [".fern", ".md"]]
    if args.wasmtime:
        paths.append(args.wasmtime.resolve())
    frozen = {str(path): sha(path.read_bytes()) for path in paths}
    inputs = list(cases())
    if args.max_cases is not None:
        inputs = inputs[:args.max_cases]
    runner = [str(args.wasmtime.resolve()), "run", "--dir=."] if args.wasmtime else []
    args.output.parent.mkdir(parents=True, exist_ok=True)
    temporary = Path(tempfile.mkdtemp(prefix="sample-words-", dir=args.output.parent.resolve()))
    record = {"complete": False, "max_cases": args.max_cases, "cases": len(inputs), "files_sha256": frozen,
              "batch_size": 4096, "timeout_seconds": 20, "batches": [], "scope": "Raw word access only; no image, numeric conversion, performance or physical bounds claim."}
    try:
        for first in range(0, len(inputs), 4096):
            batch = inputs[first:first + 4096]
            encoded = b"".join(struct.pack(">BBiHQ", *case) for case in batch)
            source = temporary / "input.bin"
            source.write_bytes(encoded)
            record["active_batch"] = first
            result = subprocess.run([*runner, str(args.binary.resolve()), source.name], cwd=temporary,
                                    capture_output=True, text=True, timeout=20)
            (temporary / "stdout.txt").write_text(result.stdout)
            (temporary / "stderr.txt").write_text(result.stderr)
            result.check_returncode()
            if source.read_bytes() != encoded:
                raise ValueError("sample input changed")
            observation = check_output(result.stdout, result.stderr, (temporary / "input.bin.raw").read_bytes(), batch)
            record["batches"].append({"first": first, "count": len(batch), "input_sha256": sha(encoded), **observation})
            record.pop("active_batch")
            print(f"passed {first + len(batch)}/{len(inputs)} sample words", flush=True)
        if frozen != {str(path): sha(path.read_bytes()) for path in paths}:
            raise ValueError("sample source or tool changed")
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
        record["sources_unchanged"] = frozen == {str(path): sha(path.read_bytes()) for path in paths}
        args.output.write_text(json.dumps(record, indent=2) + "\n")


if __name__ == "__main__":
    main()
