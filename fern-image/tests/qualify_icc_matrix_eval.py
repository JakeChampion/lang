"""Validate exact matrix protocols, rational references, and worker cleanup."""
import argparse
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
from generate_icc_matrix_eval import packet

ERRORS = {"Shape", "Input", "Numeric", "Cancelled"}
MAX_STEPS = 2

def sha(data): return hashlib.sha256(data).hexdigest()
def raw_value(raw): return struct.unpack('<d', struct.pack('<q', raw))[0]
def order(raw): return raw if raw >= 0 else -(raw & (2**63-1))
def raw_bits(value): return struct.unpack('<q', struct.pack('<d', value))[0]

def validate_case(case):
    if type(case['cancel_at']) is not int or not 0 <= case['cancel_at'] < 2**31 or len(case['coefficients']) > 10:
        raise ValueError('worker configuration range')
    if any(type(v) is not int or not -2**31 <= v < 2**31 for v in case['coefficients']):
        raise ValueError('coefficient range')
    if len(case['input_bits']) != 3 or any(type(v) is not int or not -2**63 <= v < 2**63 for v in case['input_bits']):
        raise ValueError('input IEEE shape or range')
    if sha(packet(case)) != case['payload_sha256']: raise ValueError('reference payload identity')
    e = case['expected']
    if type(e['polls']) is not int or not 0 <= e['polls'] <= 2: raise ValueError('reference polls')
    if e['status'] == 'error':
        if set(e) != {'status','error','polls'} or e['error'] not in ERRORS: raise ValueError('reference error')
    elif e['status'] == 'ok':
        if set(e) != {'status','polls','fractions','exact_bits'} or len(e['fractions']) != 3 or len(e['exact_bits']) != 3: raise ValueError('reference output shape')
        for f,b in zip(e['fractions'],e['exact_bits'],strict=True):
            if len(f) != 2: raise ValueError('reference fraction shape')
            v = Fraction(*map(int,f))
            if not -98304 <= v <= 98304: raise ValueError('reference range')
            if b is not None and (type(b) is not int or not -2**63 <= b < 2**63): raise ValueError('reference exact bits')
    else: raise ValueError('reference status')
    return packet(case)

def check_output(stdout, stderr, cases, names, returncode=0):
    if returncode != 0: raise ValueError(f'worker exit {returncode}')
    census=re.findall(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)',stderr)
    if 'fern-sanitizer:' in stderr or len(census)!=1: raise ValueError('expected exactly one worker heap census')
    allocations,frees,live=map(int,census[0])
    if allocations==0 or allocations!=frees or live!=0: raise ValueError('unbalanced or empty worker heap census')
    lines=stdout.splitlines()
    if len(lines)!=len(cases) or len(cases)!=len(names): raise ValueError('missing or extra worker cases')
    if len(set(names))!=len(names) or len({c['name'] for c in cases})!=len(cases): raise ValueError('duplicate worker cases')
    samples=[]
    for case,name,line in zip(cases,names,lines,strict=True):
        validate_case(case); e=case['expected']
        if e['status']=='error':
            if line!=f"matrix {name} error {e['error']} {e['polls']}": raise ValueError('error or poll mismatch in '+case['name'])
            observation=dict(e)
        else:
            fields=line.split(' ')
            if len(fields)!=7 or fields[:4]!=['matrix',name,'ok',str(e['polls'])]: raise ValueError('numeric protocol or poll mismatch in '+case['name'])
            raw=[];steps=[];errors=[]
            for token,f,exact in zip(fields[4:],e['fractions'],e['exact_bits'],strict=True):
                b=int(token)
                if str(b)!=token or not -2**63<=b<2**63: raise ValueError('result IEEE range')
                v=raw_value(b)
                if not math.isfinite(v): raise ValueError('nonfinite result')
                target=Fraction(*map(int,f));rounded=raw_bits(float(target))
                distance=abs(order(b)-order(rounded))
                if distance>MAX_STEPS: raise ValueError('finite numerical criterion in '+case['name']+': '+str(distance)+' steps')
                if exact is not None and b!=exact: raise ValueError('exact projection or dyadic bits in '+case['name'])
                raw.append(b);steps.append(distance);errors.append(float(abs(Fraction(v)-target)))
            observation=dict(status='ok',polls=e['polls'],result_bits=raw,representable_steps=steps,absolute_errors=errors)
        samples.append(dict(name=case['name'],payload_sha256=case['payload_sha256'],input_bits=case['input_bits'],observation=observation))
    return samples,dict(allocs=allocations,frees=frees,live_bytes=live)

def source_paths(root, options):
    paths = {"compiler": options.compiler.resolve(), "worker_binary": options.binary.resolve(),
             "Python": Path(sys.executable).resolve(), "reference_bundle": options.reference.resolve(),
             "frozen_profile": root / "tests/fixtures/icc-feasibility-srgb.icc"}
    for directory in [root / "packages", root.parent / "compiler", root.parent / "internal/stdlib"]:
        for path in sorted(directory.rglob("*")):
            if path.is_file() and (path.suffix == ".fern" or path.name.endswith(".fern.md")):
                paths[str(path.relative_to(root.parent))] = path
    for name in ["icc_matrix_eval.fern", "icc_matrix_eval_test.fern", "generate_icc_matrix_eval.py",
                 "qualify_icc_matrix_eval.py", "qualify_icc_matrix_eval_test.py"]:
        paths["fern-image/tests/" + name] = root / "tests" / name
    for name in ["test-icc-matrix-eval.sh", "test.sh", "test-wasm.sh"]:
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
    parser.add_argument("--reference", type=Path, default=Path(__file__).parent / "fixtures/icc-matrix-eval-reference.json.gz")
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
    if expected_hashes["generator"] != hashes["fern-image/tests/generate_icc_matrix_eval.py"]:
        raise ValueError("reference generator changed")
    if expected_hashes["profile"] != hashes["frozen_profile"]:
        raise ValueError("frozen profile changed")
    for case in document["cases"]:
        validate_case(case)
    if options.output.exists():
        raise ValueError("refuse an existing output record")
    options.output.parent.mkdir(parents=True, exist_ok=True)
    directory = Path(tempfile.mkdtemp(prefix="jpeg-icc_matrix_eval-", dir=options.output.parent.resolve()))
    record = {"status": "running", "source_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
              "compiler_version": subprocess.check_output([str(paths["compiler"]), "-version"], text=True).strip(),
              "worktree_dirty": bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=root, text=True)),
              "runtime": runtime, "host": platform.platform(), "host_machine": platform.machine(),
              "Python": platform.python_version(), "reference": document["reference"],
              "cases": len(document["cases"]), "sha256": hashes,
              "samples": [], "batches": [], "limits": {"cases_per_process": 8, "process_timeout_seconds": 20, "concurrency": 1},
              "scope": "Finite signed 3x3 matrix observations with a predeclared two-representable-step criterion, exact projections and specified dyadic controls; no universal error bound, profile model, adaptation, transform, encoded admission, compatibility, physical bound, or production claim."}
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
