from pathlib import Path
import hashlib
import json
import os
import re
import subprocess

p = Path(__file__).parent
repo = Path('/workspace/lang-perf-4412')
env = dict(os.environ, FERN_STRICT_IR='1', FERN_RC_CERTIFY_MEMO=str(p / 'memo-contract'))
env.pop('STAGE0', None)
env.pop('FERN_SELFHOST', None)
expected = 'cd5c9feafa6f7026b5e97a48c6d6b791df0b27a9f3e43e523a5dccc0472c9397'
records = []
for target in ['x86-64-linux', 'arm64-linux', 'wasm32-wasi']:
    name = 'bound-contract-' + target
    binary = p / name
    command = [str(p / 'fern-oracle'), '-O', '-sanitize', '-target', target, '-o', str(binary), str(p / 'contract-current/bound_contract.fern')]
    with (p / (name + '-build.log')).open('wb') as output:
        result = subprocess.run(command, cwd=repo, env=env, stdout=output, stderr=subprocess.STDOUT)
    record = dict(target=target, build_command=command, build_exit=result.returncode, expected_stdout_sha256=expected)
    records.append(record)
    (p / 'contract.json').write_text(json.dumps(records, indent=2) + '\n')
    assert result.returncode == 0, name
    run = [str(binary)] if target == 'x86-64-linux' else ['qemu-aarch64', '-cpu', 'max', str(binary)] if target == 'arm64-linux' else ['wasmtime', 'run', '--env', 'FERN_LEAKCHECK=1', '--env', 'FERN_SANITIZE=1', str(binary)]
    result = subprocess.run(run, env=dict(env, FERN_LEAKCHECK='1', FERN_SANITIZE='1'), capture_output=True)
    (p / (name + '.stdout')).write_bytes(result.stdout)
    (p / (name + '.stderr')).write_bytes(result.stderr)
    census = re.fullmatch(rb'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)\s*', result.stderr)
    record.update(run_command=run, exit=result.returncode, stdout_sha256=hashlib.sha256(result.stdout).hexdigest(), output_bytes=len(result.stdout), output_lines=len(result.stdout.splitlines()), binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(), census=list(map(int, census.groups())) if census else None)
    (p / 'contract.json').write_text(json.dumps(records, indent=2) + '\n')
    print(json.dumps(record), flush=True)
    assert result.returncode == 0 and record['stdout_sha256'] == expected and census, name
    allocs, frees, live = record['census']
    assert allocs > 0 and allocs == frees and live == 0, name
