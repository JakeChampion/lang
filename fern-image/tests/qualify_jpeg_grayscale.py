"""Compare finite grayscale JPEG outputs with stored offline observations."""

import argparse
import gzip
import hashlib
import json
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys
import tempfile

MAX_SAMPLE_ERROR = 1


def sha(data):
    return hashlib.sha256(data).hexdigest()


def check_output(stdout, stderr, cases, names, directory):
    census = re.findall(r"leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)", stderr)
    if len(census) != 1:
        raise ValueError("expected exactly one worker heap census")
    allocs, frees, live = map(int, census[0])
    if allocs == 0 or allocs != frees or live != 0:
        raise ValueError("unbalanced or empty worker heap census")
    lines = stdout.splitlines()
    if len(lines) != len(cases):
        raise ValueError("missing or extra worker cases")
    samples = []
    for case, name, line in zip(cases, names, lines, strict=True):
        path = directory / (name + ".rgb.raw")
        sample = {"name": case["name"], "input_sha256": case["input_sha256"], "input_bytes": len(bytes.fromhex(case["encoded_hex"])),
                  "status": case["status"]}
        if case["status"] == "unsupported":
            if line != "unsupported " + name or path.exists():
                raise ValueError("unsupported profile produced an image")
            samples.append(sample)
            continue
        reference = case["reference"]
        width, height = reference["width"], reference["height"]
        expected_line = f"accepted {name} {width} {height} {width * 3} {width * height * 3}"
        if line != expected_line or not path.is_file():
            raise ValueError("image metadata or worker status mismatch")
        actual = path.read_bytes()
        if len(actual) != width * height * 3:
            raise ValueError("image storage length mismatch")
        if any(actual[i] != actual[i + 1] or actual[i] != actual[i + 2] for i in range(0, len(actual), 3)):
            raise ValueError("grayscale channels are not identical")
        expected = reference["pixels"]
        if len(expected) != width * height:
            raise ValueError("invalid reference image length")
        errors = [abs(value - wanted) for value, wanted in zip(actual[0::3], expected, strict=True)]
        if max(errors, default=0) > MAX_SAMPLE_ERROR:
            raise ValueError("reference sample mismatch in " + case["name"])
        rgb = bytes(value for pixel in expected for value in [pixel, pixel, pixel])
        sample.update(width=width, height=height, stride=width * 3, output_bytes=len(actual),
                      pixels_sha256=sha(actual), reference_rgb_sha256=sha(rgb),
                      max_sample_error=max(errors, default=0), different_samples=sum(error != 0 for error in errors))
        samples.append(sample)
    return samples, {"allocs": allocs, "frees": frees, "live_bytes": live}


def source_paths(root, options):
    paths = {"binary": options.binary.resolve(), "compiler": options.compiler.resolve(), "python": Path(sys.executable).resolve(),
             "collector": Path(__file__).resolve(), "reference_bundle": options.reference.resolve()}
    for relative in ["tests/jpeg_grayscale.fern", "tests/jpeg_grayscale_test.fern", "tests/generate_jpeg_grayscale.py",
                     "tests/reference_jpeg_idct.c", "tests/qualify_jpeg_grayscale_test.py", "scripts/test-jpeg-grayscale.sh",
                     "tests/fixtures/jpeg-coefficients-reference.json.gz"]:
        paths["fern-image/" + relative] = root / relative
    paths["workflow"] = root.parent / ".github/workflows/fern-image.yml"
    for directory in [root / "packages", root.parent / "compiler", root.parent / "internal/stdlib"]:
        for path in sorted(directory.rglob("*")):
            if path.is_file() and (path.suffix == ".fern" or path.name.endswith(".fern.md")):
                paths[str(path.relative_to(root.parent))] = path
    if options.oracle:
        paths["offline_oracle"] = options.oracle.resolve()
    return paths


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("binary", type=Path)
    parser.add_argument("compiler", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--reference", type=Path, default=Path(__file__).parent / "fixtures/jpeg-grayscale-reference.json.gz")
    parser.add_argument("--oracle", type=Path)
    target = parser.add_mutually_exclusive_group()
    target.add_argument("--wasmtime", type=Path)
    target.add_argument("--qemu", type=Path)
    options = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    paths = source_paths(root, options)
    runner = []
    runtime = "native x86-64"
    if options.wasmtime:
        paths["wasmtime"] = options.wasmtime.resolve()
        runner = [str(paths["wasmtime"]), "run", "--dir=."]
        runtime = subprocess.check_output([str(paths["wasmtime"]), "--version"], text=True).strip()
    elif options.qemu:
        paths["qemu"] = options.qemu.resolve()
        runner = [str(paths["qemu"])]
        runtime = subprocess.check_output([str(paths["qemu"]), "--version"], text=True).splitlines()[0]
    hashes = {name: sha(path.read_bytes()) for name, path in paths.items()}
    document = json.loads(gzip.decompress(options.reference.read_bytes()))
    reference = document["reference"]
    if document["schema"] != 1 or not document["cases"]:
        raise ValueError("invalid reference bundle")
    for key, source in [("generator", "fern-image/tests/generate_jpeg_grayscale.py"),
                        ("oracle_source", "fern-image/tests/reference_jpeg_idct.c"),
                        ("coefficient_bundle", "fern-image/tests/fixtures/jpeg-coefficients-reference.json.gz")]:
        if reference["sha256"][key] != hashes[source]:
            raise ValueError("reference source changed: " + key)
    if options.oracle and hashes["offline_oracle"] != reference["sha256"]["oracle"]:
        raise ValueError("offline oracle changed")
    for case in document["cases"]:
        if sha(bytes.fromhex(case["encoded_hex"])) != case["input_sha256"] or case["status"] not in ("accepted", "unsupported"):
            raise ValueError("reference input mismatch")
    options.output.parent.mkdir(parents=True, exist_ok=True)
    directory = Path(tempfile.mkdtemp(prefix="jpeg-grayscale-", dir=options.output.parent.resolve()))
    record = {"status": "running", "source_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
              "compiler_version": subprocess.check_output([str(paths["compiler"]), "-version"], text=True).strip(),
              "runtime": runtime, "host": platform.platform(), "host_machine": platform.machine(), "Python": platform.python_version(),
              "reference": reference, "sha256": hashes, "oracle_rerun": bool(options.oracle), "samples": [], "batches": [],
              "limits": {"cases_per_process": 8, "process_timeout_seconds": 20, "concurrency": 1, "max_reference_sample_error": MAX_SAMPLE_ERROR},
              "scope": "Finite generated raw grayscale sample images and explicit unsupported RGB controls. No managed color, photographs, broad JPEG compatibility, performance, memory bounds, or production claim."}
    try:
        cases = document["cases"]
        for first in range(0, len(cases), 8):
            batch = cases[first:first + 8]
            names = [f"case-{i}.jpg" for i in range(len(batch))]
            record["active_batch"] = {"first": first, "cases": [case["name"] for case in batch]}
            for case, name in zip(batch, names, strict=True):
                path = directory / name
                path.write_bytes(bytes.fromhex(case["encoded_hex"]))
                if options.oracle and case["status"] == "accepted":
                    result = subprocess.run([str(options.oracle.resolve()), str(path)], capture_output=True, timeout=20)
                    if result.returncode != 0 or result.stderr or sha(result.stdout) != case["oracle_stdout_sha256"] or json.loads(result.stdout) != case["reference"]:
                        (directory / "oracle-stdout.txt").write_bytes(result.stdout)
                        (directory / "oracle-stderr.txt").write_bytes(result.stderr)
                        raise ValueError("offline grayscale observation changed")
            result = subprocess.run([*runner, str(paths["binary"]), *names], cwd=directory, capture_output=True, text=True, timeout=20)
            (directory / "stdout.txt").write_text(result.stdout)
            (directory / "stderr.txt").write_text(result.stderr)
            if result.returncode != 0:
                raise ValueError(f"worker exit {result.returncode}")
            samples, census = check_output(result.stdout, result.stderr, batch, names, directory)
            record["samples"].extend(samples)
            record["batches"].append({"first": first, "count": len(batch), "exit_code": result.returncode, **census})
            record.pop("active_batch")
            for path in directory.iterdir():
                path.unlink()
            print(f"passed {first + len(batch)}/{len(cases)}", flush=True)
        shutil.rmtree(directory)
        record["sources_unchanged"] = hashes == {name: sha(path.read_bytes()) for name, path in paths.items()}
        if not record["sources_unchanged"]:
            raise ValueError("source or tool changed during qualification")
        record["status"] = "complete"
    except BaseException as error:
        record["status"] = "failed"
        if isinstance(error, subprocess.TimeoutExpired):
            for name, value in [("stdout.txt", error.stdout), ("stderr.txt", error.stderr)]:
                if value is not None:
                    (directory / name).write_text(value.decode(errors="replace") if isinstance(value, bytes) else value)
        record["failure"] = {"type": type(error).__name__, "message": str(error), "fixtures": str(directory)}
        options.output.write_text(json.dumps(record, indent=2) + "\n")
        print("failure fixtures: " + str(directory), file=sys.stderr)
        raise
    options.output.write_text(json.dumps(record, indent=2) + "\n")


if __name__ == "__main__":
    main()
