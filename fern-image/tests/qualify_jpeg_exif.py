"""Check finite Exif metadata, strict policy errors, cancellation, and worker cleanup."""

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


ERRORS = {"Signature", "Truncated", "ByteOrder", "Version", "Limit", "Directory",
          "Duplicate", "Type", "Value", "Cancelled"}


def sha(data):
    return hashlib.sha256(data).hexdigest()


def validate_case(case):
    data = bytes.fromhex(case["payload_hex"])
    if sha(data) != case["payload_sha256"]:
        raise ValueError("reference payload identity mismatch")
    for name in ["max_bytes", "max_entries", "cancel_at"]:
        value = case[name]
        if type(value) is not int or not -2147483648 <= value <= 2147483647:
            raise ValueError("reference worker configuration range")
    if case["cancel_at"] < 0:
        raise ValueError("negative cancellation point")
    expected = case["expected"]
    if type(expected["polls"]) is not int or not 0 <= expected["polls"] <= 4099:
        raise ValueError("reference poll range")
    if expected["status"] == "ok":
        if set(expected) != {"status", "orientation", "order", "directories", "entries", "polls"}:
            raise ValueError("reference metadata shape")
        orientation = expected["orientation"]
        if orientation is not None and (type(orientation) is not int or orientation not in range(1, 9)):
            raise ValueError("reference orientation range")
        if (expected["order"] not in ["II", "MM"] or type(expected["directories"]) is not int
                or expected["directories"] not in [1, 2]):
            raise ValueError("reference directory metadata")
        if type(expected["entries"]) is not int or not 0 <= expected["entries"] <= 4096:
            raise ValueError("reference entry count")
    elif expected["status"] == "error":
        if set(expected) != {"status", "error", "polls"} or expected["error"] not in ERRORS:
            raise ValueError("reference error metadata")
    else:
        raise ValueError("reference status")
    if type(case["pillow"]["applicable"]) is not bool:
        raise ValueError("reference applicability")
    construction = case["construction"]
    if construction is not None:
        if set(construction) != {"orientation", "order", "directories", "entries"}:
            raise ValueError("reference construction shape")
        orientation = construction["orientation"]
        if orientation is not None and (type(orientation) is not int or orientation not in range(1, 9)):
            raise ValueError("construction orientation")
        if (construction["order"] not in ["II", "MM"] or type(construction["directories"]) is not int
                or construction["directories"] not in [1, 2] or type(construction["entries"]) is not int
                or not 0 <= construction["entries"] <= 65535):
            raise ValueError("construction metadata")
    if case["pillow"]["applicable"] and construction is None:
        raise ValueError("Pillow observation has no construction reference")
    return data


def protocol_line(name, expected):
    if expected["status"] == "ok":
        return (f"exif {name} ok {expected['orientation'] or 0} {expected['order']} "
                f"{expected['directories']} {expected['entries']} {expected['polls']}")
    return f"exif {name} error {expected['error']} {expected['polls']}"


def check_output(stdout, stderr, cases, names, returncode=0):
    if returncode != 0:
        raise ValueError(f"worker exit {returncode}")
    census = re.findall(r"leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)", stderr)
    if len(census) != 1:
        raise ValueError("expected exactly one worker heap census")
    allocations, frees, live = map(int, census[0])
    if allocations == 0 or allocations != frees or live != 0:
        raise ValueError("unbalanced or empty worker heap census")
    lines = stdout.splitlines()
    if len(lines) != len(cases) or len(cases) != len(names):
        raise ValueError("missing or extra worker cases")
    if len(set(names)) != len(names) or len({case["name"] for case in cases}) != len(cases):
        raise ValueError("duplicate worker cases")
    samples = []
    for case, name, line in zip(cases, names, lines, strict=True):
        validate_case(case)
        if line != protocol_line(name, case["expected"]):
            raise ValueError("metadata or protocol mismatch in " + case["name"])
        samples.append({"name": case["name"], "payload_sha256": case["payload_sha256"],
                        "max_bytes": case["max_bytes"], "max_entries": case["max_entries"],
                        "cancel_at": case["cancel_at"], "observation": case["expected"],
                        "pillow": case["pillow"]})
    return samples, {"allocs": allocations, "frees": frees, "live_bytes": live}


def source_paths(root, options):
    paths = {"compiler": options.compiler.resolve(), "worker_binary": options.binary.resolve(),
             "Python": Path(sys.executable).resolve(), "reference_bundle": options.reference.resolve()}
    for directory in [root / "packages", root.parent / "compiler", root.parent / "internal/stdlib"]:
        for path in sorted(directory.rglob("*")):
            if path.is_file() and (path.suffix == ".fern" or path.name.endswith(".fern.md")):
                paths[str(path.relative_to(root.parent))] = path
    for name in ["jpeg_exif.fern", "jpeg_exif_test.fern", "generate_jpeg_exif.py",
                 "qualify_jpeg_exif.py", "qualify_jpeg_exif_test.py"]:
        paths["fern-image/tests/" + name] = root / "tests" / name
    for name in ["test-jpeg-exif.sh", "test.sh", "test-wasm.sh"]:
        paths["fern-image/scripts/" + name] = root / "scripts" / name
    paths[".github/workflows/fern-image.yml"] = root.parent / ".github/workflows/fern-image.yml"
    for name in ["PIL.Image", "PIL.TiffImagePlugin", "PIL.TiffTags", "PIL.ExifTags", "PIL._imaging",
                 "json", "gzip", "struct", "_struct"]:
        module = importlib.import_module(name)
        if getattr(module, "__file__", None):
            paths[name] = Path(module.__file__).resolve()
    return paths


def verify_pillow(cases):
    from PIL import Image
    import warnings
    observations = 0
    for case in cases:
        if not case["pillow"]["applicable"]:
            continue
        with warnings.catch_warnings():
            warnings.simplefilter("error")
            decoded = Image.Exif()
            decoded.load(bytes.fromhex(case["payload_hex"]))
            actual = decoded.get(274)
        expected = case["construction"]["orientation"]
        if actual != expected or actual != case["pillow"]["orientation"]:
            raise ValueError("Pillow primary orientation mismatch in " + case["name"])
        observations += 1
    return observations


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("binary", type=Path)
    parser.add_argument("compiler", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--reference", type=Path, default=Path(__file__).parent / "fixtures/jpeg-exif-reference.json.gz")
    runners = parser.add_mutually_exclusive_group()
    runners.add_argument("--wasmtime", type=Path)
    runners.add_argument("--qemu", type=Path)
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
    if len({case["name"] for case in document["cases"]}) != len(document["cases"]):
        raise ValueError("duplicate reference case names")
    expected_hashes = document["reference"]["sha256"]
    if expected_hashes["generator"] != hashes["fern-image/tests/generate_jpeg_exif.py"]:
        raise ValueError("reference generator changed")
    for module in ["PIL.Image", "PIL.TiffImagePlugin", "PIL.TiffTags", "PIL.ExifTags"]:
        if expected_hashes[module] != hashes[module]:
            raise ValueError("Pillow reference source changed")
    import PIL
    if PIL.__version__ != document["reference"]["Pillow"]:
        raise ValueError("Pillow reference version changed")
    for case in document["cases"]:
        validate_case(case)
    pillow_count = verify_pillow(document["cases"])
    options.output.parent.mkdir(parents=True, exist_ok=True)
    directory = Path(tempfile.mkdtemp(prefix="jpeg-exif-", dir=options.output.parent.resolve()))
    record = {"status": "running", "source_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
              "compiler_version": subprocess.check_output([str(paths["compiler"]), "-version"], text=True).strip(),
              "worktree_dirty": bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=root, text=True)),
              "runtime": runtime, "host": platform.platform(), "host_machine": platform.machine(),
              "Python": platform.python_version(), "reference": document["reference"],
              "Pillow_observations": pillow_count, "cases": len(document["cases"]), "sha256": hashes,
              "samples": [], "batches": [], "limits": {"cases_per_process": 8, "process_timeout_seconds": 20, "concurrency": 1},
              "scope": "Finite APP1-payload metadata and strict policy observations, without complete Exif semantics, JPEG pixels, orientation application, codec parity, physical bounds, or production claims."}
    try:
        for first in range(0, len(document["cases"]), 8):
            batch = document["cases"][first:first + 8]
            names = [f"case-{i}.exif-worker" for i in range(len(batch))]
            record["active_batch"] = {"first": first, "cases": [c["name"] for c in batch]}
            for case, name in zip(batch, names, strict=True):
                header = b"FEX1" + struct.pack("<iii", case["max_bytes"], case["max_entries"], case["cancel_at"])
                (directory / name).write_bytes(header + bytes.fromhex(case["payload_hex"]))
            result = subprocess.run([*command, str(paths["worker_binary"]), *names], cwd=directory,
                                    capture_output=True, text=True, timeout=20)
            (directory / "stdout.txt").write_text(result.stdout)
            (directory / "stderr.txt").write_text(result.stderr)
            samples, census = check_output(result.stdout, result.stderr, batch, names, result.returncode)
            record["samples"].extend(samples)
            record["batches"].append({"first": first, "count": len(batch), "exit_code": result.returncode, **census})
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
