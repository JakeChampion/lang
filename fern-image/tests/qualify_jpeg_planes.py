"""Compare finite raw component planes with independent offline observations."""

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
    allocations, frees, live = map(int, census[0])
    if allocations == 0 or allocations != frees or live != 0:
        raise ValueError("unbalanced or empty worker heap census")
    lines = iter(stdout.splitlines())
    samples = []
    for case, name in zip(cases, names, strict=True):
        reference = case["reference"]
        width, height = reference["width"], reference["height"]
        planes = reference["planes"]
        max_h = max(plane["horizontal"] for plane in planes)
        max_v = max(plane["vertical"] for plane in planes)
        total = sum(plane["width"] * plane["height"] for plane in planes)
        if next(lines, None) != f"begin {name} {width} {height} {max_h} {max_v} {len(planes)} {total}":
            raise ValueError("image metadata, component count, or total storage mismatch")
        observations = []
        for i, plane in enumerate(planes):
            component, horizontal, vertical = plane["id"], plane["horizontal"], plane["vertical"]
            w, h = plane["width"], plane["height"]
            if next(lines, None) != f"plane {i} {component} {horizontal} {vertical} {w} {h} {w} {w * h}":
                raise ValueError("component order, sampling, dimensions, or storage mismatch")
            path = directory / f"{name}.plane-{i}.raw"
            if not path.is_file():
                raise ValueError("component output missing")
            actual = path.read_bytes()
            expected = bytes(plane["pixels"])
            if len(actual) != w * h or len(expected) != len(actual):
                raise ValueError("component storage length mismatch")
            errors = [abs(a - b) for a, b in zip(actual, expected, strict=True)]
            if max(errors, default=0) > MAX_SAMPLE_ERROR:
                raise ValueError("raw component sample mismatch in " + case["name"])
            observations.append({"id": component, "horizontal": horizontal, "vertical": vertical, "width": w, "height": h,
                                 "stride": w, "bytes": len(actual), "pixels_sha256": sha(actual), "reference_pixels_sha256": sha(expected),
                                 "max_sample_error": max(errors, default=0), "different_samples": sum(error != 0 for error in errors)})
        if next(lines, None) != "end " + name:
            raise ValueError("missing component observation terminator")
        samples.append({"name": case["name"], "input_sha256": case["input_sha256"], "input_bytes": len(bytes.fromhex(case["encoded_hex"])),
                        "width": width, "height": height, "max_horizontal": max_h, "max_vertical": max_v, "bytes": total, "planes": observations})
    if next(lines, None) is not None:
        raise ValueError("extra worker output")
    return samples, {"allocs": allocations, "frees": frees, "live_bytes": live}


def source_paths(root, options):
    paths = {"compiler": options.compiler.resolve(), "worker_binary": options.binary.resolve(), "Python": Path(sys.executable).resolve(),
             "reference_bundle": options.reference.resolve(), ".github/workflows/fern-image.yml": root.parent / ".github/workflows/fern-image.yml"}
    for directory in [root / "packages", root.parent / "compiler", root.parent / "internal/stdlib"]:
        for path in sorted(directory.rglob("*")):
            if path.is_file() and (path.suffix == ".fern" or path.name.endswith(".fern.md")):
                paths[str(path.relative_to(root.parent))] = path
    for relative in ["tests/jpeg_planes.fern", "tests/jpeg_planes_test.fern", "tests/fixtures/jpeg_planes.fern", "tests/generate_jpeg_planes.py",
                     "tests/reference_jpeg_planes.c", "tests/fixtures/jpeg-grayscale-reference.json.gz", "tests/fixtures/jpeg_entropy.fern",
                     "tests/qualify_jpeg_planes.py", "tests/qualify_jpeg_planes_test.py", "scripts/test-jpeg-planes.sh"]:
        paths["fern-image/" + relative] = root / relative
    if options.oracle:
        paths["offline_oracle"] = options.oracle.resolve()
    return paths


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("binary", type=Path)
    parser.add_argument("compiler", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--reference", type=Path, default=Path(__file__).parent / "fixtures/jpeg-planes-reference.json.gz")
    parser.add_argument("--oracle", type=Path)
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
    reference = document["reference"]
    for key, source in [("generator", "fern-image/tests/generate_jpeg_planes.py"), ("oracle_source", "fern-image/tests/reference_jpeg_planes.c"),
                        ("source_bundle", "fern-image/tests/fixtures/jpeg-grayscale-reference.json.gz"), ("source_entropy_fixtures", "fern-image/tests/fixtures/jpeg_entropy.fern")]:
        if reference["sha256"][key] != hashes[source]:
            raise ValueError("reference source changed: " + key)
    if options.oracle and reference["sha256"]["oracle"] != hashes["offline_oracle"]:
        raise ValueError("offline oracle changed")
    for case in document["cases"]:
        if sha(bytes.fromhex(case["encoded_hex"])) != case["input_sha256"]:
            raise ValueError("reference input changed")
    options.output.parent.mkdir(parents=True, exist_ok=True)
    directory = Path(tempfile.mkdtemp(prefix="jpeg-planes-", dir=options.output.parent.resolve()))
    record = {"status": "running", "source_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
              "compiler_version": subprocess.check_output([str(paths["compiler"]), "-version"], text=True).strip(),
              "runtime": runtime, "host": platform.platform(), "host_machine": platform.machine(), "Python": platform.python_version(),
              "reference": reference, "sha256": hashes, "oracle_rerun": bool(options.oracle), "samples": [], "batches": [],
              "limits": {"cases_per_process": 8, "process_timeout_seconds": 20, "concurrency": 1, "max_reference_sample_error": MAX_SAMPLE_ERROR},
              "scope": "Finite raw component planes only. No chroma reconstruction, color interpretation, broad compatibility, photographs, security, performance, physical bounds, or production claim."}
    try:
        cases = document["cases"]
        for first in range(0, len(cases), 8):
            batch = cases[first:first + 8]
            names = [f"case-{i}.jpg" for i in range(len(batch))]
            record["active_batch"] = {"first": first, "cases": [case["name"] for case in batch]}
            for case, name in zip(batch, names, strict=True):
                path = directory / name
                path.write_bytes(bytes.fromhex(case["encoded_hex"]))
                if options.oracle:
                    result = subprocess.run([str(options.oracle.resolve()), str(path)], capture_output=True, timeout=20)
                    if result.returncode or result.stderr or sha(result.stdout) != case["oracle_stdout_sha256"] or json.loads(result.stdout) != case["reference"]:
                        (directory / "oracle-stdout.txt").write_bytes(result.stdout)
                        (directory / "oracle-stderr.txt").write_bytes(result.stderr)
                        raise ValueError("offline raw-component observation changed")
            result = subprocess.run([*command, str(paths["worker_binary"]), *names], cwd=directory, capture_output=True, text=True, timeout=20)
            (directory / "stdout.txt").write_text(result.stdout)
            (directory / "stderr.txt").write_text(result.stderr)
            if result.returncode:
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
                    (directory / name).write_bytes(value.encode() if isinstance(value, str) else value)
        record["failure_directory"] = str(directory)
        record["failure"] = str(error)
        record["sources_unchanged"] = hashes == {name: sha(path.read_bytes()) for name, path in paths.items()}
        raise
    finally:
        options.output.write_text(json.dumps(record, indent=2) + "\n")


if __name__ == "__main__":
    main()
