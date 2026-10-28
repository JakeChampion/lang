from pathlib import Path
import hashlib
import json
import os
import re
import subprocess

p = Path(__file__).parent
repo = Path('/workspace/lang-perf-4412')
cli = p / 'fern-oracle'
env = dict(os.environ, FERN_STRICT_IR='1', FERN_RC_CERTIFY_MEMO=str(p / 'memo-fixtures'))
env.pop('STAGE0', None)
env.pop('FERN_SELFHOST', None)
records = []
for fixture in ['const_param_enums_impls', 'variant_payload_expected_type']:
    source = repo / 'conformance/cases' / fixture / 'main.fern'
    expected = (source.parent / 'expected.stdout').read_bytes()
    for target in ['x86-64-linux', 'arm64-linux', 'wasm32-wasi']:
        name = fixture + '-' + target
        binary = p / name
        command = [str(cli), '-O', '-sanitize', '-target', target, '-o', str(binary), str(source)]
        with (p / (name + '-build.log')).open('wb') as output:
            result = subprocess.run(command, cwd=repo, env=env, stdout=output, stderr=subprocess.STDOUT)
        record = dict(fixture=fixture, target=target, source_sha256=hashlib.sha256(source.read_bytes()).hexdigest(), expected_sha256=hashlib.sha256(expected).hexdigest(), launcher_sha256=hashlib.sha256(cli.read_bytes()).hexdigest(), build_command=command, build_exit=result.returncode)
        records.append(record)
        (p / 'fixtures.json').write_text(json.dumps(records, indent=2) + '\n')
        assert result.returncode == 0, name
        run = [str(binary)] if target == 'x86-64-linux' else ['qemu-aarch64', '-cpu', 'max', str(binary)] if target == 'arm64-linux' else ['wasmtime', 'run', '--env', 'FERN_LEAKCHECK=1', '--env', 'FERN_SANITIZE=1', str(binary)]
        result = subprocess.run(run, env=dict(env, FERN_LEAKCHECK='1', FERN_SANITIZE='1'), capture_output=True)
        (p / (name + '.stdout')).write_bytes(result.stdout)
        (p / (name + '.stderr')).write_bytes(result.stderr)
        census = re.fullmatch(rb'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)\s*', result.stderr)
        record.update(run_command=run, exit=result.returncode, exact_expected_output=result.stdout == expected, stdout_sha256=hashlib.sha256(result.stdout).hexdigest(), binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(), census=list(map(int, census.groups())) if census else None)
        (p / 'fixtures.json').write_text(json.dumps(records, indent=2) + '\n')
        print(json.dumps(record), flush=True)
        assert result.returncode == 0 and result.stdout == expected and census, name
        allocs, frees, live = record['census']
        assert allocs == frees and live == 0, name
