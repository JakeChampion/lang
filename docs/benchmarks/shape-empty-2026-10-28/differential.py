from pathlib import Path
import hashlib
import json
import os
import re
import subprocess

p = Path(__file__).parent
root = p.parent
project = root / 'shape-plan-candidate/contract'
producer = root / 'successor-count-candidate/stage2-current'
env = dict(os.environ, FERN_STRICT_IR='1', FERN_SANITIZE='1', FERN_LEAKCHECK='1', FERN_RC_CERTIFY_MEMO=str(p / 'memo-contract'))
records = []
reference = None
for target in ['x86-64-linux', 'arm64-linux', 'wasm32-wasi']:
    name = 'contract-' + target
    binary = p / name
    command = [str(producer), '-O', '-sanitize', '-target', target, '-o', str(binary), str(project / 'shape_contract.fern'), str(root / 'fixed-source/internal/stdlib')]
    with (p / (name + '-build.log')).open('wb') as out:
        result = subprocess.run(command, env=env, stdout=out, stderr=subprocess.STDOUT)
    record = dict(target=target, build_command=command, build_exit=result.returncode)
    records.append(record)
    (p / 'differential.json').write_text(json.dumps(records, indent=2) + '\n')
    assert result.returncode == 0, name
    run = [str(binary)] if target == 'x86-64-linux' else ['qemu-aarch64', '-cpu', 'max', str(binary)] if target == 'arm64-linux' else ['wasmtime', 'run', '--env', 'FERN_LEAKCHECK=1', '--env', 'FERN_SANITIZE=1', str(binary)]
    result = subprocess.run(run, env=env, capture_output=True)
    (p / (name + '.stdout')).write_bytes(result.stdout)
    (p / (name + '.stderr')).write_bytes(result.stderr)
    census = re.fullmatch(rb'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)\s*', result.stderr)
    if reference is None:
        reference = result.stdout
    record.update(run_command=run, exit=result.returncode, output_bytes=len(result.stdout), output_lines=len(result.stdout.splitlines()), output_sha256=hashlib.sha256(result.stdout).hexdigest(), same_output=result.stdout == reference, binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(), census=list(map(int, census.groups())) if census else None)
    (p / 'differential.json').write_text(json.dumps(records, indent=2) + '\n')
    print(json.dumps(record), flush=True)
    assert result.returncode == 0 and census and record['same_output'], name
    assert result.stdout.endswith(b'cases=6048\n'), name
    a, freed, live = record['census']
    assert a > 0 and a == freed and live == 0, name
