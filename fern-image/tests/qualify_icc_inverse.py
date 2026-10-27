"""Check finite ICC curv values, exact error protocols, and worker cleanup."""

import argparse
from decimal import Decimal
from fractions import Fraction
import gzip
import hashlib
import importlib
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

from generate_icc_inverse import packet

ERRORS = {"Limit", "Input", "Shape", "Value", "Unsupported", "NonInvertible", "Function", "Domain", "Cancelled"}
TOLERANCE = Fraction(1, 10**12)


def sha(data):
    return hashlib.sha256(data).hexdigest()


def validate_case(case):
    for key in ["kind", "max_samples", "cancel_at", "parameter"]:
        if type(case[key]) is not int or not -(2**31) <= case[key] < 2**31:
            raise ValueError("worker configuration range")
    if type(case["input_bits"]) is not int or not -(2**63) <= case["input_bits"] < 2**63:
        raise ValueError("input IEEE range")
    if case["kind"] not in range(4) or case["cancel_at"] < 0 or len(case["values"]) > 65537:
        raise ValueError("worker mode or configuration")
    if any(type(v) is not int or not -(2**31) <= v < 2**31 for v in case["values"]):
        raise ValueError("public sample range")
    if sha(packet(case)) != case["payload_sha256"]:
        raise ValueError("reference payload identity")
    if set(case['expected']) != {'dispatch','direct'}:
        raise ValueError('paired reference metadata')
    for expected in case['expected'].values():
        validate_expected(expected)
    return packet(case)


def validate_expected(expected):
    if type(expected["polls"]) is not int or not 0 <= expected["polls"] <= 1026:
        raise ValueError("reference poll range")
    if expected["status"] == "error":
        if set(expected) != {"status", "error", "polls"} or expected["error"] not in ERRORS:
            raise ValueError("reference error metadata")
    elif expected["status"] == "ok":
        keys = set(expected) - {"exact_bits"}
        if keys == {"status", "polls", "fraction"}:
            if len(expected["fraction"]) != 2:
                raise ValueError("reference fraction shape")
            value = Fraction(*map(int, expected["fraction"]))
        elif keys == {"status", "polls", "decimal"}:
            decimal = Decimal(expected["decimal"])
            if not decimal.is_finite():
                raise ValueError("nonfinite reference")
            value = Fraction(decimal)
        else:
            raise ValueError("reference numeric shape")
        if not 0 <= value <= 1:
            raise ValueError("reference numeric range")
        if "exact_bits" in expected and (type(expected["exact_bits"]) is not int or not -(2**63) <= expected["exact_bits"] < 2**63):
            raise ValueError("reference exact bits")
    else:
        raise ValueError("reference status")



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
    if len(lines) != 2 * len(cases) or len(cases) != len(names):
        raise ValueError("missing or extra paired worker cases")
    if len(set(names)) != len(names) or len({case["name"] for case in cases}) != len(cases):
        raise ValueError("duplicate worker cases")
    samples = []
    for index, (case, name) in enumerate(zip(cases, names, strict=True)):
        validate_case(case)
        observation = {}
        for route, line in zip(['dispatch','direct'], lines[2*index:2*index+2], strict=True):
            expected = case['expected'][route]
            prefix = f"iid {name} {route} "
            if expected['status'] == 'error':
                if line != prefix + f"error {expected['error']} {expected['polls']}":
                    raise ValueError('error or poll mismatch in '+case['name'])
                observation[route] = dict(expected)
                continue
            fields = line.split(' ')
            if len(fields) != 6 or fields[:5] != ['iid', name, route, 'ok', str(expected['polls'])]:
                raise ValueError('numeric paired protocol or poll mismatch in '+case['name'])
            raw = int(fields[5])
            if str(raw) != fields[5] or not -(2**63) <= raw < 2**63:
                raise ValueError('result IEEE range')
            actual = struct.unpack('<d', struct.pack('<q', raw))[0]
            if not math.isfinite(actual) or not 0 <= actual <= 1:
                raise ValueError('result finite range')
            if 'exact_bits' in expected and raw != expected['exact_bits']:
                raise ValueError('exact declared bits in '+case['name'])
            reference = Fraction(*map(int, expected['fraction'])) if 'fraction' in expected else Fraction(Decimal(expected['decimal']))
            delta = abs(Fraction(actual)-reference)
            if delta > TOLERANCE:
                raise ValueError('finite numerical criterion in '+case['name']+': '+str(float(delta)))
            observation[route] = dict(status='ok',polls=expected['polls'],result_bits=raw,absolute_error=float(delta))
        if observation['dispatch']['status'] == observation['direct']['status'] == 'ok' and observation['dispatch']['result_bits'] != observation['direct']['result_bits']:
            raise ValueError('dispatcher differs from selected standalone result')
        samples.append(dict(name=case['name'],payload_sha256=case['payload_sha256'],input_bits=case['input_bits'],observation=observation))
    return samples, {'allocs':allocations,'frees':frees,'live_bytes':live}


def audit_origins(document, root):
    required={'curv':1974,'power':640,'affine':1581,'quadratic':5780}
    if set(document['reference']['origins']) != set(required):
        raise ValueError('original reference set')
    originals = {}; seen = {name:set() for name in required}
    for name, count in required.items():
        metadata=document['reference']['origins'][name]
        bundle=root/'tests/fixtures'/f'icc-{name}-inverse-reference.json.gz'
        generator=root/'tests'/f'generate_icc_{name}_inverse.py'
        data=json.loads(gzip.decompress(bundle.read_bytes()))
        if metadata['cases'] != count or len(data['cases']) != count or metadata['bundle_sha256'] != sha(bundle.read_bytes()) or metadata['generator_sha256'] != sha(generator.read_bytes()):
            raise ValueError('original reference identity or count')
        originals[name]={case['name']:case for case in data['cases']}
    for case in document['cases']:
        if 'origin' not in case:
            continue
        origin=case['origin'];name=origin['bundle'];label=origin['name']
        if name not in required or label in seen[name] or label not in originals[name]:
            raise ValueError('duplicate or unknown original vector')
        seen[name].add(label);original=originals[name][label]
        digest=sha(json.dumps(original,sort_keys=True,separators=(',',':')).encode())
        if origin['canonical_sha256'] != digest or origin['original_expected'] != original['expected']:
            raise ValueError('original vector expectation identity')
        if name=='curv':
            if any(case[key] != original[key] for key in ['kind','parameter','max_samples','cancel_at','values','input_bits']):
                raise ValueError('original curv vector changed')
        elif case['kind'] != 3 or case['max_samples'] != 4096 or any(case[new] != original[old] for new,old in [('parameter','function_type'),('values','coefficients'),('input_bits','input_bits'),('cancel_at','cancel_at')]):
            raise ValueError('original parametric vector changed')
        if original['expected']['status']=='ok' and case['expected']['dispatch'] != original['expected']:
            raise ValueError('original mathematical success criterion changed')
    if any(len(seen[name]) != count for name,count in required.items()):
        raise ValueError('missing original reference vector')


def source_paths(root, options):
    paths = {"compiler": options.compiler.resolve(), "worker_binary": options.binary.resolve(),
             "Python": Path(sys.executable).resolve(), "reference_bundle": options.reference.resolve()}
    for directory in [root / "packages", root.parent / "compiler", root.parent / "internal/stdlib"]:
        for path in sorted(directory.rglob("*")):
            if path.is_file() and (path.suffix == ".fern" or path.name.endswith(".fern.md")):
                paths[str(path.relative_to(root.parent))] = path
    for name in ["icc_inverse.fern", "icc_inverse_test.fern", "generate_icc_inverse.py",
                 "qualify_icc_inverse.py", "qualify_icc_inverse_test.py"]:
        paths["fern-image/tests/" + name] = root / "tests" / name
    for origin in ['curv','power','affine','quadratic']:
        for relative in [f'tests/generate_icc_{origin}_inverse.py',f'tests/fixtures/icc-{origin}-inverse-reference.json.gz']:
            paths['fern-image/'+relative] = root / relative
    for name in ["test-icc-inverse.sh", "test.sh", "test-wasm.sh"]:
        paths["fern-image/scripts/" + name] = root / "scripts" / name
    paths[".github/workflows/fern-image.yml"] = root.parent / ".github/workflows/fern-image.yml"
    for name in ["json", "gzip", "struct", "_struct", "random", "decimal", "_decimal", "fractions", "math"]:
        module = importlib.import_module(name)
        if getattr(module, "__file__", None):
            paths[name] = Path(module.__file__).resolve()
    return paths


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("binary", type=Path)
    parser.add_argument("compiler", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--reference", type=Path, default=Path(__file__).parent / "fixtures/icc-inverse-reference.json.gz")
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
    if expected_hashes["generator"] != hashes["fern-image/tests/generate_icc_inverse.py"]:
        raise ValueError("reference generator changed")
    audit_origins(document, root)
    for case in document["cases"]:
        validate_case(case)
    if options.output.exists():
        raise ValueError("refuse an existing output record")
    options.output.parent.mkdir(parents=True, exist_ok=True)
    directory = Path(tempfile.mkdtemp(prefix="jpeg-icc_inverse-", dir=options.output.parent.resolve()))
    record = {"status": "running", "source_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
              "compiler_version": subprocess.check_output([str(paths["compiler"]), "-version"], text=True).strip(),
              "worktree_dirty": bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=root, text=True)),
              "runtime": runtime, "host": platform.platform(), "host_machine": platform.machine(),
              "Python": platform.python_version(), "reference": document["reference"],
              "cases": len(document["cases"]), "sha256": hashes,
              "samples": [], "batches": [], "limits": {"cases_per_process": 8, "process_timeout_seconds": 20, "concurrency": 1},
              "scope": "Finite standalone inverse routing with unchanged selected-kernel domains, a 1e-12 absolute criterion, exact declared bits, and exact paired standalone successes; no profile eligibility, transform, encoded admission, universal bound, or production claim."}
    def checkpoint():
        temporary = options.output.with_suffix(options.output.suffix + ".tmp")
        temporary.write_text(json.dumps(record, indent=2) + "\n")
        temporary.replace(options.output)
    checkpoint()
    try:
        for first in range(0, len(document["cases"]), 8):
            batch = document["cases"][first:first + 8]
            names = [f"case-{i}.icc-worker" for i in range(len(batch))]
            record["active_batch"] = {"first": first, "cases": [c["name"] for c in batch]}
            checkpoint()
            for case, name in zip(batch, names, strict=True):
                (directory / name).write_bytes(packet(case))
            result = subprocess.run([*command, str(paths["worker_binary"]), *names], cwd=directory,
                                    capture_output=True, text=True, timeout=20)
            (directory / "stdout.txt").write_text(result.stdout)
            (directory / "stderr.txt").write_text(result.stderr)
            samples, census = check_output(result.stdout, result.stderr, batch, names, result.returncode)
            record["samples"].extend(samples)
            record["batches"].append({"first": first, "count": len(batch), "exit_code": result.returncode, **census})
            record.pop("active_batch")
            checkpoint()
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
        checkpoint()


if __name__ == "__main__":
    main()
