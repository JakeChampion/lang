"""Check finite ICC tag data metadata, strict policy errors, cancellation, and worker cleanup."""

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


ERRORS = {"Limit", "Length", "Signature", "Reserved", "Function", "Cancelled"}


def sha(data):
    return hashlib.sha256(data).hexdigest()


def validate_case(case):
    data = bytes.fromhex(case["payload_hex"])
    if sha(data) != case["payload_sha256"]:
        raise ValueError("reference payload identity")
    for key in ["mode", "max_bytes", "max_samples", "cancel_at"]:
        if type(case[key]) is not int or not -(2**31) <= case[key] < 2**31:
            raise ValueError("worker configuration range")
    if case["mode"] not in (0, 1) or case["cancel_at"] < 0:
        raise ValueError("worker mode or cancellation point")
    expected = case["expected"]
    if type(expected["polls"]) is not int or not 0 <= expected["polls"] <= 2048:
        raise ValueError("reference poll range")
    if expected["status"] == "ok":
        if set(expected) != {"status", "fields", "polls"}:
            raise ValueError("reference metadata shape")
        fields = expected["fields"]
        if (not isinstance(fields, list) or len(fields) < 3 or any(type(v) is not int or not -(2**31) <= v < 2**31 for v in fields)
                or fields[0] not in range(5) or not 0 <= fields[2] <= 65536 or len(fields) != 3 + fields[2]):
            raise ValueError("reference wire fields")
        kind, function, count = fields[:3]
        if (kind == 4 and (function not in range(5) or count != [1, 3, 4, 5, 7][function])
                or kind != 4 and function != -1 or kind == 0 and count != 3 or kind == 1 and count != 0
                or kind == 2 and count != 1 or kind == 3 and count < 2
                or kind in (2, 3) and any(not 0 <= v <= 65535 for v in fields[3:])):
            raise ValueError("reference curve shape")
    elif expected["status"] == "error":
        if set(expected) != {"status", "error", "polls"} or expected["error"] not in ERRORS:
            raise ValueError("reference error metadata")
    else:
        raise ValueError("reference status")
    return data


def protocol_line(name, expected):
    if expected["status"] == "ok":
        return f"tag {name} ok {expected['polls']} " + " ".join(map(str, expected["fields"]))
    return f"tag {name} error {expected['error']} {expected['polls']}"


def check_output(stdout, stderr, cases, names, returncode=0):
    if returncode != 0:
        raise ValueError(f"worker exit {returncode}")
    census = re.findall(r"leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)", stderr)
    if "fern-sanitizer:" in stderr or len(census) != 1:
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
                        "max_bytes": case["max_bytes"], "max_samples": case["max_samples"],
                        "cancel_at": case["cancel_at"], "observation": case["expected"],
                        "mode": case["mode"]})
    return samples, {"allocs": allocations, "frees": frees, "live_bytes": live}


def source_paths(root, options):
    paths = {"compiler": options.compiler.resolve(), "worker_binary": options.binary.resolve(),
             "Python": Path(sys.executable).resolve(), "reference_bundle": options.reference.resolve()}
    for directory in [root / "packages", root.parent / "compiler", root.parent / "internal/stdlib"]:
        for path in sorted(directory.rglob("*")):
            if path.is_file() and (path.suffix == ".fern" or path.name.endswith(".fern.md")):
                paths[str(path.relative_to(root.parent))] = path
    for name in ["icc_tag_data.fern", "icc_tag_data_test.fern", "generate_icc_tag_data.py",
                 "qualify_icc_tag_data.py", "qualify_icc_tag_data_test.py"]:
        paths["fern-image/tests/" + name] = root / "tests" / name
    for name in ["test-icc-tag-data.sh", "test.sh", "test-wasm.sh"]:
        paths["fern-image/scripts/" + name] = root / "scripts" / name
    paths[".github/workflows/fern-image.yml"] = root.parent / ".github/workflows/fern-image.yml"
    for name in ["json", "gzip", "struct", "_struct", "random"]:
        module = importlib.import_module(name)
        if getattr(module, "__file__", None):
            paths[name] = Path(module.__file__).resolve()
    paths["frozen_profile"] = root / "tests/fixtures/icc-feasibility-srgb.icc"
    paths["frozen_profile_manifest"] = root / "tests/fixtures/icc-feasibility-reference.json"
    return paths


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("binary", type=Path)
    parser.add_argument("compiler", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--reference", type=Path, default=Path(__file__).parent / "fixtures/icc-tag-data-reference.json.gz")
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
    if (expected_hashes["generator"] != hashes["fern-image/tests/generate_icc_tag_data.py"]
            or expected_hashes["frozen_profile"] != hashes["frozen_profile"]):
        raise ValueError("reference generator or frozen profile changed")
    for case in document["cases"]:
        validate_case(case)
    options.output.parent.mkdir(parents=True, exist_ok=True)
    directory = Path(tempfile.mkdtemp(prefix="jpeg-icc_tag_data-", dir=options.output.parent.resolve()))
    record = {"status": "running", "source_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
              "compiler_version": subprocess.check_output([str(paths["compiler"]), "-version"], text=True).strip(),
              "worktree_dirty": bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=root, text=True)),
              "runtime": runtime, "host": platform.platform(), "host_machine": platform.machine(),
              "Python": platform.python_version(), "reference": document["reference"],
              "cases": len(document["cases"]), "sha256": hashes,
              "samples": [], "batches": [], "limits": {"cases_per_process": 8, "process_timeout_seconds": 20, "concurrency": 1},
              "scope": "Finite standalone ICC XYZ/curve wire-data observations, without numerical evaluation, domain qualification, profile models, transforms, JPEG admission, compatibility, physical bounds, or production claims."}
    try:
        for first in range(0, len(document["cases"]), 8):
            batch = document["cases"][first:first + 8]
            names = [f"case-{i}.icc-worker" for i in range(len(batch))]
            record["active_batch"] = {"first": first, "cases": [c["name"] for c in batch]}
            for case, name in zip(batch, names, strict=True):
                header = b"FIT1" + struct.pack("<iiii", case["mode"], case["max_bytes"], case["max_samples"], case["cancel_at"])
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
