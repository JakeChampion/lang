"""Validate linear traces, exact matrix dots, composition bounds, and offline CMM observations."""
import argparse
from decimal import Decimal, localcontext
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
from generate_icc_matrix_trc_eval import packet, frozen_model
from reference_icc_matrix_trc import observe, library_path

MAX_STEPS=2
ABS=Fraction(1,10**12)
CMM_ABS=Fraction(1,10**6)
ERRORS={'Limit','Intent','Shape','Input','Cancelled'}
ERRORS|={'Curve.'+x for x in ['Limit','Input','Shape','Value','Unsupported','Numeric']}
ERRORS|={'Parametric.'+x for x in ['Function','Shape','Input','Domain','Unsupported','Numeric']}
ERRORS|={'Matrix.'+x for x in ['Shape','Input','Numeric']}


def sha(data):return hashlib.sha256(data).hexdigest()
def raw_value(raw):return struct.unpack('<d',struct.pack('<q',raw))[0]
def raw_bits(value):return struct.unpack('<q',struct.pack('<d',value))[0]
def order(raw):return raw if raw>=0 else -(raw&(2**63-1))

def numeric_reference(reference):
    if set(reference)not in [{'fraction'},{'fraction','exact_bits'},{'decimal'},{'decimal','exact_bits'}]:raise ValueError('curve reference shape')
    if 'fraction'in reference:
        values=reference['fraction']
        if len(values)!=2 or any(type(x)is not str or str(int(x))!=x for x in values)or int(values[1])<=0:raise ValueError('curve reference fraction')
        value=Fraction(*map(int,values))
    else:
        if type(reference['decimal'])is not str:raise ValueError('curve reference decimal')
        d=Decimal(reference['decimal'])
        if not d.is_finite():raise ValueError('curve reference nonfinite')
        value=Fraction(d)
    if not 0<=value<=1:raise ValueError('curve reference range')
    if 'exact_bits'in reference:
        b=reference['exact_bits']
        if type(b)is not int or not -2**63<=b<2**63 or not math.isfinite(raw_value(b)):raise ValueError('curve reference exact bits')
    return value


def validate_case(case):
    if type(case['frozen'])is not bool or type(case['cmm_applicable'])is not bool:raise ValueError('worker mode')
    if type(case['requested'])is not int or case['requested']not in range(4):raise ValueError('worker intent')
    if type(case['max_samples'])is not int or not -2**31<=case['max_samples']<2**31:raise ValueError('worker limit')
    if type(case['cancel_at'])is not int or not 0<=case['cancel_at']<2**31:raise ValueError('worker cancellation')
    if len(case['input_bits'])!=3 or any(type(v)is not int or not -2**63<=v<2**63 for v in case['input_bits']):raise ValueError('worker input bits')
    if len(case['matrix'])>10 or any(type(v)is not int or not -2**31<=v<2**31 for v in case['matrix']):raise ValueError('worker matrix')
    if len(case['curves'])!=3:raise ValueError('worker curves')
    for curve in case['curves']:
        if set(curve)!={'kind','parameter','values'}or type(curve['kind'])is not int or curve['kind']not in range(4):raise ValueError('worker curve kind')
        if type(curve['parameter'])is not int or not -2**31<=curve['parameter']<2**31:raise ValueError('worker curve parameter')
        values=curve['values']
        if len(values)>65537 or any(type(v)is not int or not -2**31<=v<2**31 for v in values):raise ValueError('worker curve values')
        if curve['kind']<=1 and values or curve['kind']in (0,2)and curve['parameter']!=0:raise ValueError('worker curve encoding')
    if case['frozen']and (case['matrix'],case['curves'])!=frozen_model():raise ValueError('frozen model reference')
    data=packet(case)
    if len(data)>1048576 or sha(data)!=case['payload_sha256']:raise ValueError('reference payload identity')
    e=case['expected']
    if type(e['polls'])is not int or not 0<=e['polls']<=4096:raise ValueError('reference polls')
    if e['status']=='error':
        if set(e)!={'status','error','polls'}or e['error']not in ERRORS or case['cmm_applicable']:raise ValueError('reference error')
    elif e['status']=='ok':
        if case['requested']!=1 or case['max_samples']<=0 or any(not math.isfinite(raw_value(b))or not 0<=raw_value(b)<=1 for b in case['input_bits']):raise ValueError('reference positive admission')
        keys={'status','polls','linear','ideal_xyz','exact_xyz_bits'}
        if case['cmm_applicable']:keys.add('cmm_xyz_bits')
        if set(e)!=keys or len(e['linear'])!=3 or len(e['ideal_xyz'])!=3 or len(e['exact_xyz_bits'])!=3 or len(case['matrix'])!=9:raise ValueError('reference numeric shape')
        for r in e['linear']:numeric_reference(r)
        for x in e['ideal_xyz']:
            d=Decimal(x)
            if type(x)is not str or not d.is_finite()or abs(d)>98304:raise ValueError('reference ideal XYZ')
        for b in e['exact_xyz_bits']:
            if b is not None and (type(b)is not int or not -2**63<=b<2**63 or not math.isfinite(raw_value(b))):raise ValueError('reference exact XYZ')
        if case['cmm_applicable']:
            if not case['frozen']or case['requested']!=1 or case['cancel_at']!=0 or len(e['cmm_xyz_bits'])!=3:raise ValueError('CMM applicability')
            for b in e['cmm_xyz_bits']:
                if type(b)is not int or not -2**63<=b<2**63 or not math.isfinite(raw_value(b)):raise ValueError('CMM reference bits')
    else:raise ValueError('reference status')
    return data


def check_cmm(actual,reference):
    if len(reference)!=3:raise ValueError('CMM observation shape')
    errors=[]
    for x,b in zip(actual,reference,strict=True):
        if type(b)is not int or not -2**63<=b<2**63 or not math.isfinite(raw_value(b)):raise ValueError('CMM observation bits')
        difference=abs(Fraction(x)-Fraction(raw_value(b)))
        if difference>CMM_ABS:raise ValueError('finite CMM criterion')
        errors.append(float(difference))
    return errors


def check_output(stdout,stderr,cases,names,returncode=0,cmm=None):
    if returncode!=0:raise ValueError(f'worker exit {returncode}')
    census=re.findall(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)',stderr)
    if 'fern-sanitizer:'in stderr or len(census)!=1:raise ValueError('expected exactly one worker heap census')
    allocations,frees,live=map(int,census[0])
    if allocations==0 or allocations!=frees or live!=0:raise ValueError('unbalanced or empty worker heap census')
    lines=stdout.splitlines()
    if len(lines)!=len(cases)or len(cases)!=len(names):raise ValueError('missing or extra worker cases')
    if len(set(names))!=len(names)or len({c['name']for c in cases})!=len(cases):raise ValueError('duplicate worker cases')
    samples=[]
    for case,name,line in zip(cases,names,lines,strict=True):
        validate_case(case);e=case['expected']
        if e['status']=='error':
            if line!=f"composition {name} error {e['error']} {e['polls']}":raise ValueError('error or poll mismatch in '+case['name'])
            observation=dict(e)
        else:
            fields=line.split(' ')
            if len(fields)!=10 or fields[:4]!=['composition',name,'ok',str(e['polls'])]:raise ValueError('numeric protocol or poll mismatch in '+case['name'])
            raw=[];numbers=[]
            for token in fields[4:]:
                b=int(token)
                if str(b)!=token or not -2**63<=b<2**63:raise ValueError('result IEEE range or spelling')
                v=raw_value(b)
                if not math.isfinite(v):raise ValueError('nonfinite result')
                raw.append(b);numbers.append(v)
            linear=numbers[:3];xyz=numbers[3:];linear_errors=[]
            for b,x,reference in zip(raw[:3],linear,e['linear'],strict=True):
                if not 0<=x<=1:raise ValueError('linear output range')
                delta=abs(Fraction(x)-numeric_reference(reference))
                if delta>ABS:raise ValueError('finite curve criterion in '+case['name'])
                if 'exact_bits'in reference and b!=reference['exact_bits']:raise ValueError('exact curve identity bits')
                linear_errors.append(float(delta))
            steps=[];matrix_errors=[];ideal_errors=[];bounds=[]
            for i,(x,b)in enumerate(zip(xyz,raw[3:],strict=True)):
                row=case['matrix'][3*i:3*i+3]
                dot=sum((Fraction(c,65536)*Fraction(v)for c,v in zip(row,linear)),Fraction())
                rounded=float(dot);distance=abs(order(b)-order(raw_bits(rounded)))
                if distance>MAX_STEPS:raise ValueError('finite matrix criterion in '+case['name'])
                projection=[j for j in range(3)if row[j]==65536 and all(row[k]==0 for k in range(3)if k!=j)]
                if projection and b!=raw[projection[0]]:raise ValueError('exact projection bits')
                exact=e['exact_xyz_bits'][i]
                if exact is not None and b!=exact:raise ValueError('declared exact dyadic XYZ bits')
                upper=math.nextafter(math.nextafter(rounded,math.inf),math.inf)
                lower=math.nextafter(math.nextafter(rounded,-math.inf),-math.inf)
                matrix_bound=max(abs(Fraction(upper)-dot),abs(Fraction(lower)-dot))
                bound=sum((Fraction(abs(c),65536)for c in row),Fraction())*ABS+matrix_bound
                ideal=Fraction(Decimal(e['ideal_xyz'][i]));difference=abs(Fraction(x)-ideal)
                if difference>bound:raise ValueError('finite composed criterion in '+case['name'])
                steps.append(distance);matrix_errors.append(float(abs(Fraction(x)-dot)));ideal_errors.append(float(difference));bounds.append(float(bound))
            observation=dict(status='ok',polls=e['polls'],linear_bits=raw[:3],XYZ_bits=raw[3:],linear_absolute_errors=linear_errors,
                             matrix_representable_steps=steps,matrix_absolute_errors=matrix_errors,ideal_XYZ_absolute_errors=ideal_errors,composed_limits=bounds)
            if case['cmm_applicable']:
                if cmm is None or case['name']not in cmm:raise ValueError('missing live CMM observation')
                observation['CMM_absolute_errors']=check_cmm(xyz,cmm[case['name']])
                observation['frozen_CMM_absolute_errors']=check_cmm(xyz,e['cmm_xyz_bits'])
                observation['live_CMM_bits']=cmm[case['name']]
            else:observation['CMM_applicability']='Outside the frozen-profile reference set. Full primary mathematics still checked.'
        samples.append(dict(name=case['name'],payload_sha256=case['payload_sha256'],input_bits=case['input_bits'],observation=observation))
    return samples,dict(allocs=allocations,frees=frees,live_bytes=live)


def offline_cmm(root,document):
    cases=[c for c in document['cases']if c['cmm_applicable']]
    inputs=[[raw_value(b)for b in c['input_bits']]for c in cases]
    outputs,info=observe((root/'tests/fixtures/icc-feasibility-srgb.icc').read_bytes(),inputs)
    expected=document['reference']['CMM']
    for key in ['Pillow','LittleCMS','encoded_version','profile_sha256','input_format','output_format','intent','flags','black_point_compensation']:
        if info[key]!=expected[key]:raise ValueError('offline CMM configuration changed: '+key)
    if len(cases)!=document['reference']['CMM_cases']:raise ValueError('offline CMM applicability count')
    return {c['name']:list(map(raw_bits,row))for c,row in zip(cases,outputs,strict=True)},dict(info,observations=len(cases),phase='Host-side public CMM observation before Fern workers. No CMM code runs in the application.')

def source_paths(root, options):
    paths = {"compiler": options.compiler.resolve(), "worker_binary": options.binary.resolve(),
             "Python": Path(sys.executable).resolve(), "reference_bundle": options.reference.resolve(),
             "frozen_profile": root / "tests/fixtures/icc-feasibility-srgb.icc"}
    for directory in [root / "packages", root.parent / "compiler", root.parent / "internal/stdlib"]:
        for path in sorted(directory.rglob("*")):
            if path.is_file() and (path.suffix == ".fern" or path.name.endswith(".fern.md")):
                paths[str(path.relative_to(root.parent))] = path
    for name in ["icc_matrix_trc_eval.fern", "icc_matrix_trc_eval_test.fern", "generate_icc_matrix_trc_eval.py",
                 "qualify_icc_matrix_trc_eval.py", "qualify_icc_matrix_trc_eval_test.py", "reference_icc_matrix_trc.py", "generate_icc_parametric_eval.py"]:
        paths["fern-image/tests/" + name] = root / "tests" / name
    for name in ["test-icc-matrix-trc.sh", "test.sh", "test-wasm.sh"]:
        paths["fern-image/scripts/" + name] = root / "scripts" / name
    paths[".github/workflows/fern-image.yml"] = root.parent / ".github/workflows/fern-image.yml"
    for name in ["json", "gzip", "struct", "_struct", "random", "decimal", "_decimal", "fractions", "math"]:
        module = importlib.import_module(name)
        if getattr(module, "__file__", None):
            paths[name] = Path(module.__file__).resolve()
    paths["profile_manifest"] = root / "tests/fixtures/icc-feasibility-reference.json"
    for name in ["PIL", "PIL.Image", "PIL.ImageCms", "PIL._imaging", "PIL._imagingcms", "ctypes", "_ctypes"]:
        module=importlib.import_module(name)
        if getattr(module,"__file__",None): paths[name]=Path(module.__file__).resolve()
    import PIL
    for library in sorted((Path(PIL.__file__).resolve().parent.parent/"pillow.libs").glob("*.so*")):
        paths["Pillow-library/"+library.name]=library
    return paths


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("binary", type=Path)
    parser.add_argument("compiler", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--reference", type=Path, default=Path(__file__).parent / "fixtures/icc-matrix-trc-reference.json.gz")
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
    if expected_hashes["generator"] != hashes["fern-image/tests/generate_icc_matrix_trc_eval.py"]:
        raise ValueError("reference generator changed")
    if expected_hashes["profile"] != hashes["frozen_profile"]:
        raise ValueError("frozen profile changed")
    links={"parametric_reference":"fern-image/tests/generate_icc_parametric_eval.py", "CMM_reference":"fern-image/tests/reference_icc_matrix_trc.py", "profile_manifest":"profile_manifest"}
    for key,name in links.items():
        if expected_hashes[key]!=hashes[name]: raise ValueError("reference source changed: "+key)
    if document["reference"]["criterion"]!={"linear_absolute":"1e-12","matrix_steps":2,"composed":"row_L1*1e-12+B(A)","cmm_absolute":"1e-6"}:
        raise ValueError("declared numerical criteria changed")
    for case in document["cases"]:
        validate_case(case)
    live_cmm,cmm_info=offline_cmm(root,document)
    if options.output.exists():
        raise ValueError("refuse an existing output record")
    options.output.parent.mkdir(parents=True, exist_ok=True)
    directory = Path(tempfile.mkdtemp(prefix="jpeg-icc_matrix_trc_eval-", dir=options.output.parent.resolve()))
    record = {"status": "running", "source_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
              "compiler_version": subprocess.check_output([str(paths["compiler"]), "-version"], text=True).strip(),
              "worktree_dirty": bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=root, text=True)),
              "runtime": runtime, "host": platform.platform(), "host_machine": platform.machine(),
              "Python": platform.python_version(), "reference": document["reference"], "offline_CMM":cmm_info,
              "cases": len(document["cases"]), "sha256": hashes,
              "samples": [], "batches": [], "limits": {"cases_per_process": 8, "process_timeout_seconds": 60, "concurrency": 1},
              "scope": "Finite matrix/TRC numerical composition to explicit unclipped relative D50 XYZ. Predeclared curve, matrix, propagated-error and frozen-profile offline CMM criteria. Model metadata does not certify conformance or transform eligibility. No additional adaptation, destination RGB encoding, encoded admission, runtime CMM, compatibility, physical bound, or production claim."}
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
            (directory / "reference-profile.icc").write_bytes(paths["frozen_profile"].read_bytes())
            result = subprocess.run([*command, str(paths["worker_binary"]), *names], cwd=directory,
                                    capture_output=True, text=True, timeout=60)
            (directory / "stdout.txt").write_text(result.stdout)
            (directory / "stderr.txt").write_text(result.stderr)
            samples, census = check_output(result.stdout, result.stderr, batch, names, result.returncode,live_cmm)
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
