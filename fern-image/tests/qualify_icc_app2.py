"""Check every assembled byte against an independent file and require strict worker cleanup."""

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

from generate_icc_app2 import IDENTIFIER, materialize, packet, pillow_observation, profile_bytes

ERRORS = {"Limit", "Length", "Identifier", "Sequence", "Count", "Duplicate", "Missing", "Cancelled"}


def sha(data):
    return hashlib.sha256(data).hexdigest()


def validate_case(case):
    for key in ["max_profile_bytes", "max_chunks", "cancel_at"]:
        if type(case[key]) is not int or not -(2**31) <= case[key] < 2**31:
            raise ValueError("worker configuration range")
    if case["cancel_at"] < 0 or not isinstance(case["chunks"], list) or len(case["chunks"]) > 256:
        raise ValueError("worker configuration shape")
    recipe = case["profile"]
    if recipe["kind"] == "pattern":
        if type(recipe["length"]) is not int or not 0 <= recipe["length"] <= 16707345 or not 1 <= len(bytes.fromhex(recipe["pattern_hex"])) <= 256:
            raise ValueError("reference byte recipe")
    elif recipe != {"kind": "frozen"}:
        raise ValueError("reference profile kind")
    profile = profile_bytes(recipe)
    for part in case["chunks"]:
        for key in ["sequence", "total", "start", "length"]:
            if type(part[key]) is not int:
                raise ValueError("fragment integer shape")
        if (not 0 <= part["sequence"] <= 255 or not 0 <= part["total"] <= 255 or part["start"] < 0
                or not 0 <= part["length"] <= 65522 or part["start"] + part["length"] > len(profile)
                or len(bytes.fromhex(part.get("identifier_hex", IDENTIFIER.hex()))) != 12):
            raise ValueError("fragment construction range")
        if "truncate" in part and (type(part["truncate"]) is not int or not 0 <= part["truncate"] <= 65536):
            raise ValueError("fragment truncation range")
    _, payloads = materialize(case)
    if [sha(p) for p in payloads] != case["payload_sha256"] or sha(packet(case)) != case["packet_sha256"]:
        raise ValueError("reference byte identity")
    expected = case["expected"]
    if type(expected["polls"]) is not int or not 0 <= expected["polls"] <= 261056:
        raise ValueError("reference poll range")
    if expected["status"] == "error":
        if set(expected) != {"status", "error", "polls"} or expected["error"] not in ERRORS:
            raise ValueError("reference error shape")
    elif expected["status"] == "none":
        if set(expected) != {"status", "polls"} or case["chunks"] or profile:
            raise ValueError("reference absence shape")
    elif expected["status"] == "some":
        if (set(expected) != {"status", "polls", "length", "profile_sha256", "checked_bytes"}
                or type(expected["length"]) is not int or type(expected["checked_bytes"]) is not int
                or expected["length"] != len(profile) or expected["checked_bytes"] != len(profile)
                or expected["profile_sha256"] != sha(profile) or not case["chunks"]):
            raise ValueError("reference byte comparison shape")
    else:
        raise ValueError("reference status")
    return profile


def protocol_line(name, expected):
    if expected["status"] == "some":
        return f"app2 {name} some {expected['polls']} {expected['length']} {expected['checked_bytes']}"
    if expected["status"] == "none":
        return f"app2 {name} none {expected['polls']}"
    return f"app2 {name} error {expected['error']} {expected['polls']}"


def check_census(stderr):
    census = re.findall(r"leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)", stderr)
    if "fern-sanitizer:" in stderr or len(census) != 1:
        raise ValueError("expected exactly one worker heap census")
    allocations, frees, live = map(int, census[0])
    if allocations == 0 or allocations != frees or live != 0:
        raise ValueError("unbalanced or empty worker heap census")
    return {"allocs": allocations, "frees": frees, "live_bytes": live}


def check_output(stdout, stderr, cases, names, returncode=0):
    if returncode != 0:
        raise ValueError(f"worker exit {returncode}")
    census = check_census(stderr)
    lines = stdout.splitlines()
    if len(lines) != len(cases) or len(cases) != len(names):
        raise ValueError("missing or extra worker cases")
    if len(set(names)) != len(names) or len({case["name"] for case in cases}) != len(cases):
        raise ValueError("duplicate worker cases")
    samples = []
    for case, name, line in zip(cases, names, lines, strict=True):
        validate_case(case)
        if line != protocol_line(name, case["expected"]):
            raise ValueError("byte comparison or protocol mismatch in " + case["name"])
        samples.append(dict(name=case["name"], packet_sha256=case["packet_sha256"], payload_sha256=case["payload_sha256"],
                            max_profile_bytes=case["max_profile_bytes"], max_chunks=case["max_chunks"], cancel_at=case["cancel_at"],
                            observation=case["expected"]))
    return samples, census


def check_reference_rejection(result, expected_exit, name, offset, actual, expected):
    census = check_census(result.stderr)
    line = f"app2 {name} mismatch byte {offset} {actual} {expected}\n"
    if result.returncode != expected_exit or result.stdout != line:
        raise ValueError("reference byte fault did not produce the exact required mismatch")
    return census


def save_record(path, record):
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(record, indent=2) + "\n")
    temporary.replace(path)


def reference_controls(command, binary, directory, case, expected_exit, record, checkpoint):
    profile = validate_case(case)
    if len(profile) != 130 or case["expected"]["status"] != "some":
        raise ValueError("reference control fixture")
    baseline = packet(case)
    prefix = baseline[:-len(profile)]
    for offset in [0, 64, 129]:
        changed = bytearray(profile)
        changed[offset] ^= 1
        mutant = packet(case, bytes(changed))
        if mutant[:-len(profile)] != prefix:
            raise ValueError("reference control changed API input")
        name = f"reference-byte-{offset}.icc-worker"
        (directory / name).write_bytes(mutant)
        record["active_reference_control"] = {"offset": offset, "packet_sha256": sha(mutant)}
        checkpoint()
        result = subprocess.run([*command, str(binary), name], cwd=directory, capture_output=True, text=True, timeout=120)
        (directory / "stdout.txt").write_text(result.stdout)
        (directory / "stderr.txt").write_text(result.stderr)
        census = check_reference_rejection(result, expected_exit, name, offset, profile[offset], changed[offset])
        record["reference_controls"].append(dict(offset=offset, api_input_sha256=sha(prefix), packet_sha256=sha(mutant),
                    expected_file_sha256=sha(changed), exit_code=result.returncode, stdout=result.stdout, **census))
        record.pop("active_reference_control")
        for path in directory.iterdir():
            path.unlink()
        checkpoint()


def source_paths(root, options):
    paths = {"compiler": options.compiler.resolve(), "worker_binary": options.binary.resolve(),
             "Python": Path(sys.executable).resolve(), "reference_bundle": options.reference.resolve()}
    for directory in [root / "packages", root.parent / "compiler", root.parent / "internal/stdlib"]:
        for path in sorted(directory.rglob("*")):
            if path.is_file() and (path.suffix == ".fern" or path.name.endswith(".fern.md")):
                paths[str(path.relative_to(root.parent))] = path
    for name in ["icc_app2.fern", "icc_app2_test.fern", "generate_icc_app2.py",
                 "qualify_icc_app2.py", "qualify_icc_app2_test.py"]:
        paths["fern-image/tests/" + name] = root / "tests" / name
    for name in ["test-icc-app2.sh", "test.sh", "test-wasm.sh"]:
        paths["fern-image/scripts/" + name] = root / "scripts" / name
    paths[".github/workflows/fern-image.yml"] = root.parent / ".github/workflows/fern-image.yml"
    for name in ["PIL.Image", "PIL.JpegImagePlugin", "PIL._imaging", "PIL.features", "json", "gzip", "struct", "_struct", "random", "io"]:
        module = importlib.import_module(name)
        if getattr(module, "__file__", None):
            paths[name] = Path(module.__file__).resolve()
    paths["frozen_profile"] = root / "tests/fixtures/icc-feasibility-srgb.icc"
    paths["frozen_profile_manifest"] = root / "tests/fixtures/icc-feasibility-reference.json"
    import PIL
    for path in sorted((Path(PIL.__file__).resolve().parent.parent / "pillow.libs").glob("*jpeg*")):
        paths["reference_library/" + path.name] = path
    return paths


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("binary", type=Path)
    parser.add_argument("compiler", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--reference", type=Path, default=Path(__file__).parent / "fixtures/icc-app2-reference.json.gz")
    runners = parser.add_mutually_exclusive_group()
    runners.add_argument("--wasmtime", type=Path)
    runners.add_argument("--qemu", type=Path)
    options = parser.parse_args()
    if options.output.exists():
        raise ValueError("refuse to replace an existing qualification record")
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
    if (expected_hashes["generator"] != hashes["fern-image/tests/generate_icc_app2.py"]
            or expected_hashes["frozen_profile"] != hashes["frozen_profile"]):
        raise ValueError("reference generator or frozen profile changed")
    import PIL
    from PIL import features
    if (PIL.__version__ != document["reference"]["Pillow"] or features.version_feature("libjpeg_turbo") != document["reference"]["JPEG_encoder"]
            or features.version_codec("jpg") != document["reference"]["JPEG_API"]):
        raise ValueError("offline JPEG reference version mismatch")
    pillow_count = 0
    for case in document["cases"]:
        profile = validate_case(case)
        if "Pillow_observation" in case:
            supplied = materialize(case)[1] if case.get("Pillow_constructed") else None
            if pillow_observation(profile, supplied) != case["Pillow_observation"]:
                raise ValueError("offline JPEG metadata observation mismatch")
            pillow_count += 1
    options.output.parent.mkdir(parents=True, exist_ok=True)
    directory = Path(tempfile.mkdtemp(prefix="jpeg-icc_app2-", dir=options.output.parent.resolve()))
    record = {"status": "running", "source_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
              "compiler_version": subprocess.check_output([str(paths["compiler"]), "-version"], text=True).strip(),
              "worktree_dirty": bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=root, text=True)),
              "runtime": runtime, "host": platform.platform(), "host_machine": platform.machine(),
              "Python": platform.python_version(), "reference": document["reference"],
              "cases": len(document["cases"]), "sha256": hashes,
              "samples": [], "batches": [], "reference_controls": [], "Pillow_metadata_observations": pillow_count,
              "limits": {"cases_per_process": 8, "process_timeout_seconds": 120, "concurrency": 1},
              "scope": "Finite caller-supplied ICC APP2 payload assembly with every output byte compared against an independent expected file. No marker traversal, profile validity, transform eligibility, encoded JPEG admission, compatibility, physical bounds, or production claim."}
    try:
        save_record(options.output, record)
        control = next(c for c in document["cases"] if c["name"] == "sequence-130-3-forward")
        reference_controls(command, paths["worker_binary"], directory, control, 1 if options.wasmtime else 3, record, lambda: save_record(options.output, record))
        for first in range(0, len(document["cases"]), 8):
            batch = document["cases"][first:first + 8]
            names = [f"case-{i}.icc-worker" for i in range(len(batch))]
            record["active_batch"] = {"first": first, "cases": [c["name"] for c in batch]}
            save_record(options.output, record)
            for case, name in zip(batch, names, strict=True):
                (directory / name).write_bytes(packet(case))
            result = subprocess.run([*command, str(paths["worker_binary"]), *names], cwd=directory,
                                    capture_output=True, text=True, timeout=120)
            (directory / "stdout.txt").write_text(result.stdout)
            (directory / "stderr.txt").write_text(result.stderr)
            samples, census = check_output(result.stdout, result.stderr, batch, names, result.returncode)
            record["samples"].extend(samples)
            record["batches"].append({"first": first, "count": len(batch), "exit_code": result.returncode, **census})
            record.pop("active_batch")
            for path in directory.iterdir():
                path.unlink()
            save_record(options.output, record)
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
        save_record(options.output, record)


if __name__ == "__main__":
    main()
