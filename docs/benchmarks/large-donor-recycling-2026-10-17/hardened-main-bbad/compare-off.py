from pathlib import Path
import hashlib
import json
import os
import subprocess
import sys

baseline, candidate, stdlib, output = map(lambda s: Path(s).resolve(), sys.argv[1:])
output.mkdir(parents=True, exist_ok=True)
source = output / 'probe.fern'
source.write_text('function main(): i32 { let p: usize = __raw_arr_box(1); __fern_rc_dec(p); return 0; }\n')
env = {k: v for k, v in os.environ.items() if not k.startswith('FERN_')}
result = {'source_sha256': hashlib.sha256(source.read_bytes()).hexdigest(), 'targets': {}}
for target in ['x86-64-linux', 'arm64-linux']:
    rows = {}
    for name, compiler in [('baseline', baseline), ('candidate', candidate)]:
        dest = output / (target + '-' + name)
        command = [str(compiler), '-target', target, '-o', str(dest), str(source), str(stdlib)]
        run = subprocess.run(command, env=env, capture_output=True, check=True)
        assert not run.stdout and not run.stderr, (run.stdout, run.stderr)
        rows[name] = {'compiler_sha256': hashlib.sha256(compiler.read_bytes()).hexdigest(), 'command': command, 'bytes': dest.stat().st_size, 'sha256': hashlib.sha256(dest.read_bytes()).hexdigest()}
    assert rows['baseline']['sha256'] == rows['candidate']['sha256'], (target, rows)
    result['targets'][target] = rows
    print(target, rows, flush=True)
(output / 'results.json').write_text(json.dumps(result, indent=2) + '\n')
