"""Check exact finite YCbCr conversion observations and worker cleanup."""

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


def sha(data):
    return hashlib.sha256(data).hexdigest()


def check_output(stdout, stderr, cases, names, directory):
    census = re.findall(r"leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)", stderr)
    if len(census) != 1:
        raise ValueError("expected exactly one worker heap census")
    allocations, frees, live = map(int, census[0])
    if allocations == 0 or allocations != frees or live != 0:
        raise ValueError("unbalanced or empty worker heap census")
    lines = stdout.splitlines()
    if len(lines) != len(cases):
        raise ValueError("missing or extra worker cases")
    samples = []
    for case, name, line in zip(cases, names, lines, strict=True):
        if line != f"converted {name} {case['count']}":
            raise ValueError("worker protocol or row length mismatch")
        output = directory / (name + ".rgb.raw")
        if not output.is_file():
            raise ValueError("worker output missing")
        actual = output.read_bytes()
        expected = bytes.fromhex(case["rgb_hex"])
        if len(actual) != case["count"] * 3 or actual != expected:
            raise ValueError("exact rational reference mismatch in " + case["name"])
        samples.append({"name": case["name"], "count": case["count"], "input_sha256": case["input_sha256"],
                        "rgb_sha256": sha(actual), "reference_rgb_sha256": sha(expected)})
    return samples, {"allocs": allocations, "frees": frees, "live_bytes": live}


def source_paths(root, options):
    paths = {"compiler": options.compiler.resolve(), "worker_binary": options.binary.resolve(),
             "Python": Path(sys.executable).resolve()}
    for directory in [root / "packages", root.parent / "compiler", root.parent / "internal/stdlib"]:
        for path in sorted(directory.rglob("*")):
            if path.is_file() and (path.suffix == ".fern" or path.name.endswith(".fern.md")):
                paths[str(path.relative_to(root.parent))] = path
    for relative in ["tests/jpeg_ycbcr.fern", "tests/jpeg_ycbcr_test.fern", "tests/generate_jpeg_ycbcr.py",
                     "tests/qualify_jpeg_ycbcr.py", "tests/qualify_jpeg_ycbcr_test.py", "scripts/test-jpeg-ycbcr.sh"]:
        paths["fern-image/" + relative] = root / relative
    paths["reference_bundle"] = options.reference.resolve()
    paths[".github/workflows/fern-image.yml"] = root.parent / ".github/workflows/fern-image.yml"
    return paths


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("binary", type=Path)
    parser.add_argument("compiler", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--reference", type=Path, default=Path(__file__).parent / "fixtures/jpeg-ycbcr-reference.json.gz")
    runner = parser.add_mutually_exclusive_group()
    runner.add_argument("--wasmtime", type=Path)
    runner.add_argument("--qemu", type=Path)
    options = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    paths = source_paths(root, options)
    command = []
    runtime = "native x86-64"
    if options.wasmtime:
        paths["runner"] = options.wasmtime.resolve()
        command = [str(paths["runner"]), "run", "--dir=."]
        runtime = subprocess.check_output([str(paths["runner"]), "--version"], text=True).strip()
    elif options.qemu:
        paths["runner"] = options.qemu.resolve()
        command = [str(paths["runner"])]
        runtime = subprocess.check_output([str(paths["runner"]), "--version"], text=True).splitlines()[0]
    hashes = {name: sha(path.read_bytes()) for name, path in paths.items()}
    document = json.loads(gzip.decompress(options.reference.read_bytes()))
    if document["schema"] != 1 or not document["cases"]:
        raise ValueError("invalid reference bundle")
    if document["reference"]["sha256"]["generator"] != hashes["fern-image/tests/generate_jpeg_ycbcr.py"]:
        raise ValueError("reference generator changed")
    for case in document["cases"]:
        source, expected = bytes.fromhex(case["input_hex"]), bytes.fromhex(case["rgb_hex"])
        if case["count"] <= 0 or len(source) != case["count"] * 3 or len(expected) != len(source):
            raise ValueError("reference shape mismatch")
        if sha(source) != case["input_sha256"] or sha(expected) != case["rgb_sha256"]:
            raise ValueError("reference identity mismatch")
    if document["triples"] != sum(case["count"] for case in document["cases"]):
        raise ValueError("reference sample count mismatch")
    options.output.parent.mkdir(parents=True, exist_ok=True)
    directory = Path(tempfile.mkdtemp(prefix="jpeg-ycbcr-", dir=options.output.parent.resolve()))
    record = {"status": "running", "source_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
              "compiler_version": subprocess.check_output([str(paths["compiler"]), "-version"], text=True).strip(),
              "runtime": runtime, "host": platform.platform(), "host_machine": platform.machine(), "Python": platform.python_version(),
              "reference": document["reference"], "triples": document["triples"], "sha256": hashes, "samples": [], "batches": [],
              "limits": {"rows_per_process": 8, "process_timeout_seconds": 20, "concurrency": 1},
              "scope": "Finite explicit full-range sample conversion, without encoded JPEG, chroma reconstruction, profiles, codec parity, physical bounds, performance, or production claims."}
    try:
        cases = document["cases"]
        for first in range(0, len(cases), 8):
            batch = cases[first:first + 8]
            names = [f"row-{i}.ycbcr" for i in range(len(batch))]
            record["active_batch"] = {"first": first, "cases": [case["name"] for case in batch]}
            for case, name in zip(batch, names, strict=True):
                (directory / name).write_bytes(bytes.fromhex(case["input_hex"]))
            result = subprocess.run([*command, str(paths["worker_binary"]), *names], cwd=directory,
                                    capture_output=True, text=True, timeout=20)
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
            print(f"passed {first + len(batch)}/{len(cases)} rows", flush=True)
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
                    (directory / name).write_bytes(value.encode() if isinstance(value, str) else value)
        record["failure_directory"] = str(directory)
        record["failure"] = str(error)
        record["sources_unchanged"] = hashes == {name: sha(path.read_bytes()) for name, path in paths.items()}
        raise
    finally:
        options.output.write_text(json.dumps(record, indent=2) + "\n")


if __name__ == "__main__":
    main()
