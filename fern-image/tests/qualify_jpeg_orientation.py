"""Verify exact finite orientation observations and fresh-worker cleanup."""

import argparse
import gzip
import hashlib
import importlib
import json
from pathlib import Path
import platform
import re
import shutil
import struct
import subprocess
import sys
import tempfile

from generate_jpeg_orientation import observed


CONFIG = ("width", "height", "stride", "claimed_bytes", "channels", "orientation",
          "max_pixels", "max_bytes", "max_planned_bytes", "cancel_at")
ERRORS = {"Limit", "Cancelled", *{"Image." + s for s in
          ["Dimensions", "Overflow", "Stride", "Limit", "Storage", "Bounds", "Alpha"]}}


def sha(data):
    return hashlib.sha256(data).hexdigest()


def validate_case(case):
    data = bytes.fromhex(case["data_hex"])
    if sha(data) != case["data_sha256"] or set(case["config"]) != set(CONFIG):
        raise ValueError("reference input identity or configuration shape")
    c, e = case["config"], case["expected"]
    if any(type(c[k]) is not int or not -2147483648 <= c[k] <= 2147483647 for k in CONFIG):
        raise ValueError("reference signed worker configuration")
    if c["channels"] not in [3, 4] or c["orientation"] not in range(1, 9) or c["cancel_at"] < 0:
        raise ValueError("reference format, orientation, or cancellation")
    if type(e["polls"]) is not int or not 0 <= e["polls"] <= 2147483647 or type(case["pillow"]) is not bool:
        raise ValueError("reference polls or applicability")
    if e["status"] == "ok":
        if set(e) != {"status", "width", "height", "stride", "channels", "bytes", "input_bytes", "planned_bytes", "polls", "data_sha256"}:
            raise ValueError("reference success shape")
        if any(type(e[k]) is not int or e[k] <= 0 for k in ["width", "height", "stride", "channels", "bytes", "input_bytes", "planned_bytes"]):
            raise ValueError("reference output layout range")
        raw = bytes.fromhex(case["output_hex"])
        if (e["channels"] != c["channels"] or e["stride"] != e["width"] * e["channels"]
                or e["bytes"] != e["stride"] * e["height"] or len(raw) != e["bytes"]
                or e["input_bytes"] != len(data) or e["planned_bytes"] != len(data) + len(raw)
                or e["data_sha256"] != sha(raw) or not case["pillow"]):
            raise ValueError("reference exact output storage")
    elif e["status"] == "error":
        if set(e) != {"status", "error", "polls"} or e["error"] not in ERRORS:
            raise ValueError("reference error shape")
    else:
        raise ValueError("reference status")
    return data


def protocol_line(name, e):
    if e["status"] == "error":
        return f"orientation {name} error {e['error']} {e['polls']}"
    return "orientation " + name + " ok " + " ".join(str(e[k]) for k in
           ["width", "height", "stride", "channels", "bytes", "input_bytes", "planned_bytes", "polls"])


def check_output(stdout, stderr, cases, names, directory, returncode=0):
    if returncode != 0 or "fern-sanitizer:" in stderr:
        raise ValueError(f"worker exit or sanitizer failure: {returncode}")
    census = re.findall(r"leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)", stderr)
    if len(census) != 1:
        raise ValueError("expected exactly one worker heap census")
    allocations, frees, live = map(int, census[0])
    if allocations == 0 or allocations != frees or live != 0:
        raise ValueError("unbalanced or empty worker heap census")
    lines = stdout.splitlines()
    if (len(lines) != len(cases) or len(cases) != len(names) or len(set(names)) != len(names)
            or len({c["name"] for c in cases}) != len(cases)):
        raise ValueError("missing, extra, or duplicate worker cases")
    samples = []
    for case, name, line in zip(cases, names, lines, strict=True):
        validate_case(case)
        if line != protocol_line(name, case["expected"]):
            raise ValueError("layout, status, poll, or worker identity mismatch: " + case["name"])
        path = directory / (name + ".pixels.raw")
        if case["expected"]["status"] == "ok":
            if path.read_bytes() != bytes.fromhex(case["output_hex"]):
                raise ValueError("exact channel byte mismatch: " + case["name"])
        elif path.exists():
            raise ValueError("partial output escaped on error: " + case["name"])
        samples.append(dict(name=case["name"], input_sha256=case["data_sha256"], config=case["config"],
                            observation=case["expected"], Pillow=case["pillow"]))
    return samples, dict(allocs=allocations, frees=frees, live_bytes=live)


def source_paths(root, options):
    paths = dict(compiler=options.compiler.resolve(), worker_binary=options.binary.resolve(),
                 Python=Path(sys.executable).resolve(), reference_bundle=options.reference.resolve())
    for directory in [root / "packages", root.parent / "compiler", root.parent / "internal/stdlib"]:
        for path in sorted(directory.rglob("*")):
            if path.is_file() and (path.suffix == ".fern" or path.name.endswith(".fern.md")):
                paths[str(path.relative_to(root.parent))] = path
    for name in ["jpeg_orientation.fern", "jpeg_orientation_test.fern", "generate_jpeg_orientation.py",
                 "qualify_jpeg_orientation.py", "qualify_jpeg_orientation_test.py"]:
        paths["fern-image/tests/" + name] = root / "tests" / name
    for name in ["test-jpeg-orientation.sh", "test.sh", "test-wasm.sh"]:
        paths["fern-image/scripts/" + name] = root / "scripts" / name
    paths[".github/workflows/fern-image.yml"] = root.parent / ".github/workflows/fern-image.yml"
    for name in ["PIL.Image", "PIL.ImageOps", "PIL.ExifTags", "PIL.TiffImagePlugin", "PIL._imaging",
                 "random", "json", "gzip", "struct", "_struct", "zlib"]:
        module = importlib.import_module(name)
        if getattr(module, "__file__", None):
            paths[name] = Path(module.__file__).resolve()
    return paths


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("binary", type=Path)
    parser.add_argument("compiler", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--reference", type=Path, default=Path(__file__).parent / "fixtures/jpeg-orientation-reference.json.gz")
    runners = parser.add_mutually_exclusive_group()
    runners.add_argument("--wasmtime", type=Path)
    runners.add_argument("--qemu", type=Path)
    options = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    paths = source_paths(root, options)
    command, runtime = [], "native x86-64"
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
    if document["schema"] != 1 or not document["cases"] or len({c["name"] for c in document["cases"]}) != len(document["cases"]):
        raise ValueError("reference schema or cases")
    import PIL
    if document["reference"]["Pillow"] != PIL.__version__:
        raise ValueError("Pillow version changed")
    for key, expected in document["reference"]["sha256"].items():
        actual = hashes["fern-image/tests/generate_jpeg_orientation.py" if key == "generator" else key]
        if actual != expected:
            raise ValueError("reference source changed: " + key)
    observations = 0
    for case in document["cases"]:
        validate_case(case)
        if case["pillow"]:
            w, h, raw = observed(case)
            if raw.hex() != case["output_hex"]:
                raise ValueError("independent Pillow bytes changed: " + case["name"])
            if case["expected"]["status"] == "ok" and (w, h) != (case["expected"]["width"], case["expected"]["height"]):
                raise ValueError("independent Pillow dimensions changed")
            observations += 1
    options.output.parent.mkdir(parents=True, exist_ok=True)
    directory = Path(tempfile.mkdtemp(prefix="jpeg-orientation-", dir=options.output.parent.resolve()))
    record = dict(status="running", source_commit=subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
                  worktree_dirty=bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=root, text=True)),
                  compiler_version=subprocess.check_output([str(paths["compiler"]), "-version"], text=True).strip(),
                  runtime=runtime, host=platform.platform(), host_machine=platform.machine(), Python=platform.python_version(),
                  reference=document["reference"], Pillow_observations=observations, cases=len(document["cases"]), sha256=hashes,
                  samples=[], batches=[], limits=dict(cases_per_process=8, process_timeout_seconds=20, concurrency=1),
                  scope="Finite explicit raw-image orientation only. No encoded JPEG admission, color inference, resampling, codec parity, physical bounds, or production claim.")
    try:
        for first in range(0, len(document["cases"]), 8):
            batch = document["cases"][first:first + 8]
            names = [f"case-{i}.orientation-worker" for i in range(len(batch))]
            record["active_batch"] = dict(first=first, cases=[c["name"] for c in batch])
            for case, name in zip(batch, names, strict=True):
                header = b"FOR1" + struct.pack("<10i", *(case["config"][k] for k in CONFIG))
                (directory / name).write_bytes(header + bytes.fromhex(case["data_hex"]))
            result = subprocess.run([*command, str(paths["worker_binary"]), *names], cwd=directory,
                                    capture_output=True, text=True, timeout=20)
            (directory / "stdout.txt").write_text(result.stdout)
            (directory / "stderr.txt").write_text(result.stderr)
            samples, census = check_output(result.stdout, result.stderr, batch, names, directory, result.returncode)
            record["samples"].extend(samples)
            record["batches"].append(dict(first=first, count=len(batch), exit_code=result.returncode, **census))
            record.pop("active_batch")
            for path in directory.iterdir():
                path.unlink()
            print(f"passed {first + len(batch)}/{len(document['cases'])} cases", flush=True)
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
