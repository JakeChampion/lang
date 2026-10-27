"""Compare every returned integer, exact error, callback count, and worker census."""
import argparse
import base64
import gzip
import hashlib
import importlib
import json
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys
import tempfile
from generate_icc_matrix_model import packet

ERRORS = {'Limit','Intent','MissingTag','UnsupportedTag','MetadataType','CurveDomain','UnsupportedCurve','Cancelled'}
ERRORS |= {'Envelope.'+v for v in ['Limit','Truncated','Signature','Version','DeviceClass','ColorSpace','Pcs','Header','Directory','Duplicate','Overlap','Layout']}
ERRORS |= {'Tag.'+v for v in ['Limit','Length','Signature','Reserved','Function']}

def sha(data): return hashlib.sha256(data).hexdigest()
def canonical(values): return json.dumps(values,separators=(',',':')).encode()

def validate_case(case):
    if type(case['requested']) is not int or case['requested'] not in range(4): raise ValueError('worker request')
    if len(case['limits'])!=4 or any(type(v) is not int or not -2**31<=v<2**31 for v in case['limits']): raise ValueError('worker limits')
    if type(case['cancel_at']) is not int or not 0<=case['cancel_at']<2**31: raise ValueError('worker cancellation')
    data=base64.b64decode(case['input_base64'],validate=True)
    if len(data)>16777216: raise ValueError('worker input cap')
    raw=packet(case)
    if sha(raw)!=case['payload_sha256']: raise ValueError('reference payload identity')
    expected=case['expected']
    if type(expected['polls']) is not int or not 0<=expected['polls']<2**31: raise ValueError('reference polls')
    if expected['status']=='error':
        if set(expected)!={'status','error','polls'} or expected['error']not in ERRORS: raise ValueError('reference error')
    elif expected['status']=='ok':
        if set(expected)!={'status','values','polls'} or len(expected['values'])<31 or any(type(v)is not int or not -2**63<=v<2**63 for v in expected['values']): raise ValueError('reference values')
    else: raise ValueError('reference status')
    return raw

def check_output(stdout,stderr,cases,names,returncode=0):
    if returncode!=0: raise ValueError(f'worker exit {returncode}')
    census=re.findall(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)',stderr)
    if 'fern-sanitizer:'in stderr or len(census)!=1: raise ValueError('expected exactly one worker heap census')
    allocations,frees,live=map(int,census[0])
    if allocations==0 or allocations!=frees or live!=0: raise ValueError('unbalanced or empty worker heap census')
    lines=stdout.splitlines()
    if len(lines)!=len(cases)or len(cases)!=len(names): raise ValueError('missing or extra worker cases')
    if len(set(names))!=len(names)or len({c['name']for c in cases})!=len(cases): raise ValueError('duplicate worker cases')
    samples=[]
    for case,name,line in zip(cases,names,lines,strict=True):
        validate_case(case);expected=case['expected']
        if expected['status']=='error':
            if line!=f"model {name} error {expected['error']} {expected['polls']}": raise ValueError('error or poll mismatch in '+case['name'])
            observation=dict(expected)
        else:
            fields=line.split(' ')
            if fields[:4]!=['model',name,'ok',str(expected['polls'])]or len(fields)!=4+len(expected['values']): raise ValueError('numeric protocol or poll mismatch in '+case['name'])
            actual=[]
            for index,(token,want)in enumerate(zip(fields[4:],expected['values'],strict=True)):
                got=int(token)
                if str(got)!=token or not -2**63<=got<2**63: raise ValueError('result integer range or spelling')
                if got!=want: raise ValueError('exact returned integer mismatch in '+case['name']+' at '+str(index))
                actual.append(got)
            observation=dict(status='ok',polls=expected['polls'],checked_integers=len(actual),observed_values_sha256=sha(canonical(actual)),prefix=actual[:64],suffix=actual[-16:])
        samples.append(dict(name=case['name'],payload_sha256=case['payload_sha256'],observation=observation))
    return samples,dict(allocs=allocations,frees=frees,live_bytes=live)

def offline_metadata(root,reference):
    from PIL import ImageCms,features,__version__
    if __version__!=reference['Pillow_version']or features.version_module('littlecms2')!=reference['LittleCMS_version']: raise ValueError('offline reference version')
    p=ImageCms.ImageCmsProfile(str(root/'tests/fixtures/icc-feasibility-srgb.icc')).profile
    def integers(values):
        scaled=[v*65536 for v in values]
        if any(int(v)!=v for v in scaled): raise ValueError('offline metadata exact fixed point')
        return list(map(int,scaled))
    observed=dict(device_class=p.device_class,color_space=p.xcolor_space,PCS=p.connection_space,preferred_intent=p.rendering_intent,columns=[integers(getattr(p,n)[0])for n in ['red_colorant','green_colorant','blue_colorant']],media_white=integers(p.media_white_point[0]))
    if observed!=reference['offline_metadata']: raise ValueError('offline frozen profile metadata')
    return dict(Pillow=__version__,LittleCMS=features.version_module('littlecms2'),observation=observed,scope='Host-side metadata only, not a transform or negative-policy oracle.')

def source_paths(root, options):
    paths = {"compiler": options.compiler.resolve(), "worker_binary": options.binary.resolve(),
             "Python": Path(sys.executable).resolve(), "reference_bundle": options.reference.resolve(),
             "frozen_profile": root / "tests/fixtures/icc-feasibility-srgb.icc"}
    for directory in [root / "packages", root.parent / "compiler", root.parent / "internal/stdlib"]:
        for path in sorted(directory.rglob("*")):
            if path.is_file() and (path.suffix == ".fern" or path.name.endswith(".fern.md")):
                paths[str(path.relative_to(root.parent))] = path
    for name in ["icc_matrix_model.fern", "icc_matrix_model_test.fern", "generate_icc_matrix_model.py",
                 "qualify_icc_matrix_model.py", "qualify_icc_matrix_model_test.py"]:
        paths["fern-image/tests/" + name] = root / "tests" / name
    for name in ["test-icc-matrix-model.sh", "test.sh", "test-wasm.sh"]:
        paths["fern-image/scripts/" + name] = root / "scripts" / name
    paths[".github/workflows/fern-image.yml"] = root.parent / ".github/workflows/fern-image.yml"
    for name in ["PIL", "PIL.Image", "PIL.ImageCms", "PIL._imaging", "PIL._imagingcms"]:
        module = importlib.import_module(name)
        if getattr(module, "__file__", None):
            paths[name] = Path(module.__file__).resolve()
    import PIL
    for library in sorted((Path(PIL.__file__).resolve().parent.parent / "pillow.libs").glob("*.so*")):
        paths["Pillow-library/" + library.name] = library
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
    parser.add_argument("--reference", type=Path, default=Path(__file__).parent / "fixtures/icc-matrix-model-reference.json.gz")
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
    if expected_hashes["generator"] != hashes["fern-image/tests/generate_icc_matrix_model.py"]:
        raise ValueError("reference generator changed")
    if expected_hashes["profile"] != hashes["frozen_profile"]:
        raise ValueError("frozen profile changed")
    for case in document["cases"]:
        validate_case(case)
    if options.output.exists():
        raise ValueError("refuse an existing output record")
    offline = offline_metadata(root, document["reference"])
    options.output.parent.mkdir(parents=True, exist_ok=True)
    directory = Path(tempfile.mkdtemp(prefix="jpeg-icc_matrix_model-", dir=options.output.parent.resolve()))
    record = {"status": "running", "source_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
              "compiler_version": subprocess.check_output([str(paths["compiler"]), "-version"], text=True).strip(),
              "worktree_dirty": bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=root, text=True)),
              "runtime": runtime, "host": platform.platform(), "host_machine": platform.machine(),
              "Python": platform.python_version(), "reference": document["reference"],
              "cases": len(document["cases"]), "sha256": hashes,
              "offline_metadata": offline, "samples": [], "batches": [], "limits": {"cases_per_process": 8, "process_timeout_seconds": 60, "concurrency": 1},
              "scope": "Exact finite owned model data selection and error protocols, with opaque optional metadata and explicit restricted curve admission; no complete profile conformance, adaptation, transform eligibility, encoded admission, compatibility, physical bound, or production claim."}
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
                                    capture_output=True, text=True, timeout=60)
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
