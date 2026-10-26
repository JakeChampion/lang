"""Compare finite scalar IDCT results with independent mathematical observations."""

import argparse
import decimal
import gzip
import hashlib
import json
import math
from pathlib import Path
import platform
import re
import shutil
import struct
import subprocess
import sys
import tempfile

ABSOLUTE_TOLERANCE = decimal.Decimal("0.000001")
CODEC_TOLERANCE = 1


def sha(data):
    return hashlib.sha256(data).hexdigest()


def rounded(value):
    shifted = value + 128.0
    if shifted <= 0.0:
        return 0
    if shifted >= 255.0:
        return 255
    return math.floor(shifted + 0.5)


def check_output(stdout, stderr, cases, names):
    census = re.findall(r"leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)", stderr)
    if len(census) != 1:
        raise ValueError("expected exactly one worker heap census")
    allocs, frees, live = map(int, census[0])
    if allocs == 0 or allocs != frees or live != 0:
        raise ValueError("unbalanced or empty worker heap census")
    lines = iter(stdout.splitlines())
    samples = []
    for case, name in zip(cases, names, strict=True):
        if next(lines, None) != "begin " + name:
            raise ValueError("missing or reordered worker case")
        numeric = (next(lines, "")).split()
        output = (next(lines, "")).split()
        if len(numeric) != 65 or numeric[0] != "samples" or len(output) != 65 or output[0] != "pixels":
            raise ValueError("incorrect worker output shape")
        values = list(map(float, numeric[1:]))
        pixels = list(map(int, output[1:]))
        if not all(math.isfinite(value) for value in values):
            raise ValueError("nonfinite transform output")
        with decimal.localcontext() as context:
            context.prec = 100
            errors = [abs(decimal.Decimal.from_float(a) - decimal.Decimal(b))
                      for a, b in zip(values, case["samples_decimal"], strict=True)]
            maximum = max(errors)
            if maximum > ABSOLUTE_TOLERANCE:
                raise ValueError("mathematical sample mismatch in " + case["name"])
        if pixels != [rounded(value) for value in values]:
            raise ValueError("sample rounding or clipping mismatch")
        ideal_errors = [abs(a - b) for a, b in zip(pixels, case["ideal_pixels"], strict=True)]
        codec_errors = [abs(a - b) for a, b in zip(pixels, case.get("codec_pixels", [None] * 64), strict=True) if b is not None]
        if codec_errors and max(codec_errors) > CODEC_TOLERANCE:
            raise ValueError("integer-codec sample mismatch in " + case["name"])
        samples.append({"name": case["name"], "input_sha256": case["input_sha256"],
                        "binary64_sha256": sha(struct.pack("<64d", *values)), "pixels_sha256": sha(bytes(pixels)),
                        "max_absolute_error": str(maximum), "ideal_pixel_differences": sum(x != 0 for x in ideal_errors),
                        "max_ideal_pixel_error": max(ideal_errors), "codec_visible_samples": len(codec_errors),
                        "codec_pixel_differences": sum(x != 0 for x in codec_errors),
                        "max_codec_pixel_error": max(codec_errors, default=0)})
    if next(lines, None) is not None:
        raise ValueError("unexpected worker output after final case")
    return samples, {"allocs": allocs, "frees": frees, "live_bytes": live}


def source_paths(root, options):
    paths = {"binary": options.binary.resolve(), "compiler": options.compiler.resolve(),
             "python": Path(sys.executable).resolve(), "collector": Path(__file__).resolve(),
             "reference_bundle": options.reference.resolve()}
    for module, name in [(decimal, "decimal")]:
        if getattr(module, "__file__", None):
            paths[name] = Path(module.__file__).resolve()
    import _decimal
    if getattr(_decimal, "__file__", None):
        paths["decimal_extension"] = Path(_decimal.__file__).resolve()
    for relative in ["tests/jpeg_idct.fern", "tests/jpeg_idct_test.fern", "tests/generate_jpeg_idct.py",
                     "tests/reference_jpeg_idct.c", "tests/qualify_jpeg_idct_test.py", "scripts/test-jpeg-idct.sh",
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


def repeat_oracle(reference, bundle, oracle, directory):
    old = {case["name"]: case for case in bundle["cases"]}
    results = {}
    for entry in reference["jpeg_observations"]:
        encoded = bytes.fromhex(old[entry["name"]]["encoded_hex"])
        if sha(encoded) != entry["input_sha256"]:
            raise ValueError("offline JPEG input changed")
        path = directory / "reference.jpg"
        path.write_bytes(encoded)
        result = subprocess.run([str(oracle.resolve()), str(path)], capture_output=True, timeout=20)
        if result.returncode != 0 or result.stderr or sha(result.stdout) != entry["oracle_stdout_sha256"]:
            (directory / "oracle-stdout.txt").write_bytes(result.stdout)
            (directory / "oracle-stderr.txt").write_bytes(result.stderr)
            raise ValueError(f"offline grayscale observation changed, exit {result.returncode}")
        results[entry["name"]] = json.loads(result.stdout)
        path.unlink()
    return results


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("binary", type=Path)
    parser.add_argument("compiler", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--reference", type=Path, default=Path(__file__).parent / "fixtures/jpeg-idct-reference.json.gz")
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
    for key, source in [("generator", "fern-image/tests/generate_jpeg_idct.py"),
                        ("oracle_source", "fern-image/tests/reference_jpeg_idct.c"),
                        ("coefficient_bundle", "fern-image/tests/fixtures/jpeg-coefficients-reference.json.gz")]:
        if reference["sha256"][key] != hashes[source]:
            raise ValueError("reference source changed: " + key)
    if options.oracle and hashes["offline_oracle"] != reference["sha256"]["oracle"]:
        raise ValueError("offline oracle changed")
    for case in document["cases"]:
        if sha(bytes.fromhex(case["encoded_hex"])) != case["input_sha256"]:
            raise ValueError("reference input hash mismatch")
    options.output.parent.mkdir(parents=True, exist_ok=True)
    directory = Path(tempfile.mkdtemp(prefix="jpeg-idct-", dir=options.output.parent.resolve()))
    record = {"status": "running", "source_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
              "compiler_version": subprocess.check_output([str(paths["compiler"]), "-version"], text=True).strip(),
              "runtime": runtime, "host": platform.platform(), "host_machine": platform.machine(),
              "Python": platform.python_version(), "reference": reference, "sha256": hashes,
              "criteria": {"absolute_sample_error": str(ABSOLUTE_TOLERANCE), "codec_sample_error": CODEC_TOLERANCE,
                           "cases_per_process": 8, "process_timeout_seconds": 20, "concurrency": 1},
              "oracle_rerun": bool(options.oracle), "samples": [], "batches": [],
              "scope": "Finite component-block numeric observations. No complete JPEG pixel decoder, universal error bound, standards precision certification, performance, memory bound, or production claim."}
    try:
        if options.oracle:
            original = json.loads(gzip.decompress(paths["fern-image/tests/fixtures/jpeg-coefficients-reference.json.gz"].read_bytes()))
            observations = repeat_oracle(reference, original, options.oracle, directory)
            coefficient_cases = {entry["name"]: entry for entry in original["cases"]}
            for case in document["cases"]:
                if "jpeg_origin" not in case:
                    continue
                origin = case["jpeg_origin"]
                decoded = observations[origin["name"]]
                x, y, width, height = origin["block_x"], origin["block_y"], decoded["width"], decoded["height"]
                expected = [decoded["pixels"][(y * 8 + vy) * width + x * 8 + vx]
                            if y * 8 + vy < height and x * 8 + vx < width else None
                            for vy in range(8) for vx in range(8)]
                component = coefficient_cases[origin["name"]]["reference"]["components"][0]
                unpacked = struct.unpack("<64h64B", bytes.fromhex(case["encoded_hex"]))
                if list(unpacked[:64]) != component["blocks"][y * component["columns"] + x]:
                    raise ValueError("stored JPEG coefficients disagree")
                order = []
                for diagonal in range(15):
                    pairs = [(row, diagonal - row) for row in range(8) if 0 <= diagonal - row < 8]
                    if diagonal % 2 == 0:
                        pairs.reverse()
                    order.extend(row * 8 + column for row, column in pairs)
                if list(unpacked[64:]) != [decoded["quantizers_natural"][n] for n in order]:
                    raise ValueError("stored JPEG quantizers disagree")
                if case["codec_pixels"] != expected:
                    raise ValueError("stored grayscale pixels disagree")
        cases = document["cases"]
        for first in range(0, len(cases), 8):
            batch = cases[first:first + 8]
            names = [f"case-{i}.bin" for i in range(len(batch))]
            record["active_batch"] = {"first": first, "cases": [case["name"] for case in batch]}
            for case, name in zip(batch, names, strict=True):
                (directory / name).write_bytes(bytes.fromhex(case["encoded_hex"]))
            result = subprocess.run([*runner, str(paths["binary"]), *names], cwd=directory, capture_output=True, text=True, timeout=20)
            (directory / "stdout.txt").write_text(result.stdout)
            (directory / "stderr.txt").write_text(result.stderr)
            if result.returncode != 0:
                raise ValueError(f"worker exit {result.returncode}")
            samples, census = check_output(result.stdout, result.stderr, batch, names)
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
