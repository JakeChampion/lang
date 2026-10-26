"""Compare finite JPEG coefficient observations with an offline reference bundle."""

import argparse
import gzip
import hashlib
import json
import platform
import re
import shutil
import subprocess
import sys
import tempfile
import zlib
from pathlib import Path


def sha(data):
    return hashlib.sha256(data).hexdigest()


def canonical(value):
    return json.dumps(value, separators=(",", ":"), sort_keys=True).encode()


def expected(case):
    reference = case["reference"]
    components = reference["components"]
    single = len(components) == 1
    max_h = 1 if single else max(c["horizontal"] for c in components)
    max_v = 1 if single else max(c["vertical"] for c in components)
    columns = (reference["width"] + 8 * max_h - 1) // (8 * max_h)
    rows = (reference["height"] + 8 * max_v - 1) // (8 * max_v)
    blocks = []
    decoded = 0
    for my in range(rows):
        for mx in range(columns):
            for component in components:
                horizontal = 1 if single else component["horizontal"]
                vertical = 1 if single else component["vertical"]
                for vy in range(vertical):
                    for hx in range(horizontal):
                        decoded += 1
                        x, y = mx * horizontal + hx, my * vertical + vy
                        if x < component["columns"] and y < component["rows"]:
                            values = component["blocks"][y * component["columns"] + x]
                            if len(values) != 64:
                                raise ValueError("invalid reference block length")
                            blocks.append([component["id"], x, y, *values])
    plan = [reference["width"], reference["height"], columns * rows, decoded, len(blocks)]
    return blocks, plan


def check_output(stdout, stderr, batch, names):
    census = re.findall(r"leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)", stderr)
    if len(census) != 1:
        raise ValueError("expected exactly one heap census")
    allocs, frees, live = map(int, census[0])
    if allocs == 0 or allocs != frees or live != 0:
        raise ValueError("unbalanced or empty worker heap census")
    lines = iter(stdout.splitlines())
    samples = []
    for case, name in zip(batch, names, strict=True):
        if next(lines, None) != "begin " + name:
            raise ValueError("missing or reordered worker case")
        actual = []
        while True:
            line = next(lines, None)
            if line is None:
                raise ValueError("incomplete worker case")
            fields = line.split()
            if fields and fields[0] == "block":
                if len(fields) != 68:
                    raise ValueError("invalid worker coefficient block length")
                actual.append(list(map(int, fields[1:])))
                continue
            if not fields or fields[0] != "ok":
                raise ValueError("worker rejected reference case " + case["name"])
            observed = list(map(int, fields[1:]))
            break
        wanted, plan = expected(case)
        encoded = bytes.fromhex(case["encoded_hex"])
        if actual != wanted:
            raise ValueError("coefficient, coordinate, or scheduling mismatch in " + case["name"])
        if observed != [*plan, len(encoded)]:
            raise ValueError("framing or plan mismatch in " + case["name"])
        samples.append({"name": case["name"], "input_sha256": sha(encoded), "input_bytes": len(encoded),
                        "width": plan[0], "height": plan[1], "mcus": plan[2], "decoded_blocks": plan[3],
                        "output_blocks": plan[4], "consumed": observed[-1],
                        "coefficient_sha256": sha(canonical(actual)),
                        "reference_coefficient_sha256": sha(canonical(wanted)),
                        "oracle_stdout_sha256": case["oracle_stdout_sha256"]})
    if next(lines, None) is not None:
        raise ValueError("unexpected worker output after the final case")
    return samples, {"allocs": allocs, "frees": frees, "live_bytes": live}


def source_paths(root, options):
    paths = {"binary": options.binary.resolve(), "compiler": options.compiler.resolve(),
             "python": Path(sys.executable).resolve(), "collector": Path(__file__).resolve(),
             "reference_bundle": options.reference.resolve()}
    if getattr(zlib, "__file__", None):
        paths["python_zlib"] = Path(zlib.__file__).resolve()
    for relative in ["tests/jpeg_coefficients.fern", "tests/jpeg_entropy_test.fern",
                     "tests/fixtures/jpeg_entropy.fern", "tests/generate_jpeg_entropy.py",
                     "tests/reference_jpeg_coefficients.c", "tests/qualify_jpeg_coefficients_test.py",
                     "scripts/test-jpeg-coefficients.sh", "tests/requirements-reference.txt"]:
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
    parser.add_argument("--reference", type=Path, default=Path(__file__).parent / "fixtures/jpeg-coefficients-reference.json.gz")
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
    if document["schema"] != 1:
        raise ValueError("unknown reference bundle schema")
    cases = document["cases"]
    manifest = document["manifest"]
    if not cases or len(cases) != len(manifest["cases"]):
        raise ValueError("invalid reference case count")
    if manifest["generator_sha256"] != hashes["fern-image/tests/generate_jpeg_entropy.py"]:
        raise ValueError("reference generator does not match the bundle")
    if manifest["oracle_source_sha256"] != hashes["fern-image/tests/reference_jpeg_coefficients.c"]:
        raise ValueError("reference source does not match the bundle")
    if options.oracle and manifest["oracle_sha256"] != hashes["offline_oracle"]:
        raise ValueError("offline oracle does not match the bundle")
    for case, entry in zip(cases, manifest["cases"], strict=True):
        data = bytes.fromhex(case["encoded_hex"])
        if case["name"] != entry["name"] or sha(data) != entry["sha256"] or len(data) != entry["bytes"]:
            raise ValueError("reference input manifest mismatch")
    options.output.parent.mkdir(parents=True, exist_ok=True)
    record = {"status": "running", "source_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
              "compiler_version": subprocess.check_output([str(paths["compiler"]), "-version"], text=True).strip(),
              "runtime": runtime, "host": platform.platform(), "host_machine": platform.machine(),
              "Python": platform.python_version(), "Python_zlib": zlib.ZLIB_RUNTIME_VERSION,
              "Python_zlib_module": zlib.__spec__.origin,
              "reference": {k: v for k, v in manifest.items() if k != "cases"}, "sha256": hashes,
              "oracle_rerun": bool(options.oracle), "samples": [], "batches": [],
              "limits": {"cases_per_process": 8, "process_timeout_seconds": 20, "concurrency": 1},
              "scope": "Finite generated and synthetic coefficient streams. No IDCT, pixel, photograph, broad compatibility, security, performance, physical-memory, or production qualification."}
    directory = Path(tempfile.mkdtemp(prefix="jpeg-coefficients-", dir=options.output.parent.resolve()))
    try:
        for start in range(0, len(cases), 8):
            batch = cases[start:start + 8]
            names = [f"case-{i}.jpg" for i in range(len(batch))]
            record["active_batch"] = {"first": start, "cases": [case["name"] for case in batch]}
            for case, name in zip(batch, names, strict=True):
                (directory / name).write_bytes(bytes.fromhex(case["encoded_hex"]))
                if options.oracle:
                    reference = subprocess.run([str(paths["offline_oracle"]), str(directory / name)], capture_output=True, timeout=20, check=True)
                    if reference.stderr or sha(reference.stdout) != case["oracle_stdout_sha256"] or json.loads(reference.stdout) != case["reference"]:
                        raise ValueError("offline reference changed for " + case["name"])
            result = subprocess.run([*runner, str(paths["binary"]), *names], cwd=directory, capture_output=True, text=True, timeout=20)
            (directory / "stdout.txt").write_text(result.stdout)
            (directory / "stderr.txt").write_text(result.stderr)
            if result.returncode != 0:
                raise ValueError(f"worker exit {result.returncode}")
            samples, census = check_output(result.stdout, result.stderr, batch, names)
            record["samples"].extend(samples)
            record["batches"].append({"first": start, "count": len(batch), "exit_code": result.returncode, **census})
            record.pop("active_batch")
            for path in directory.iterdir():
                path.unlink()
            print(f"passed {start + len(batch)}/{len(cases)}", flush=True)
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
