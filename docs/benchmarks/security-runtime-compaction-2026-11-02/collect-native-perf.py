import hashlib
import json
import os
from pathlib import Path
import re
import subprocess

mode = os.environ['FERN_PERF_MODE']
limit = int(os.environ.get('FERN_PERF_LIMIT', '0'))
checkout = Path('/Users/jakechampion/codex/lang-pr-14010-validation')
root = Path('/tmp/fern-14010-guarded-perf-' + mode + ('-' + str(limit) if limit else '')).resolve()
root.mkdir(exist_ok=True)
compiler = Path('/tmp/fern-14010-guarded-fixed-point/stage1').resolve()
baseline = Path('/tmp/fern-14008-stage4').resolve()
fixed = json.loads(Path('/tmp/fern-14010-guarded-fixed-point/qualification.json').read_text())
assert hashlib.sha256(compiler.read_bytes()).hexdigest() == fixed['sha256']
for name, digest in fixed['source_hashes'].items():
    assert hashlib.sha256((checkout / name).read_bytes()).hexdigest() == digest, name
image = subprocess.check_output(['docker', 'image', 'inspect', 'fern-devbox:latest', '--format', '{{.Id}}'], text=True).strip()
record = {'scope': 'Frozen compiler output sizes on three targets and native ARM64 Linux instruction counts. No native latency or x86 dynamic claim.', 'mode': mode, 'compiler_sha256': hashlib.sha256(compiler.read_bytes()).hexdigest(), 'baseline_sha256': hashlib.sha256(baseline.read_bytes()).hexdigest(), 'image': image, 'rows': []}
metrics = []

def run(command, **kwargs):
    p = subprocess.run(command, capture_output=True, timeout=120, **kwargs)
    return p, {'command': command, 'exit_code': p.returncode, 'stdout_sha256': hashlib.sha256(p.stdout).hexdigest(), 'stderr': p.stderr.decode(errors='replace')}

def save():
    (root / 'qualification.json').write_text(json.dumps(record, indent=2) + '\n')
    (root / 'report.txt').write_text('\n'.join(metrics) + '\n')

def emit(source, name, target, profile=None):
    key = name + '-' + target + ('-' + profile if profile else '')
    path = root / (key + '.asm')
    command = [str(compiler), '-target', target] + (['-profile', profile] if profile else []) + ['-emit', 'asm', '-o', str(path), str(source), str(checkout / 'internal/stdlib')]
    p, step = run(command)
    assert p.returncode == 0 and path.stat().st_size > 0, step
    count = sum(bool(re.match(rb'^\s', line)) for line in path.read_bytes().splitlines())
    assert count > 0
    return key, count, {'name': name, 'target': target, 'profile': profile, 'source_sha256': hashlib.sha256(source.read_bytes()).hexdigest(), 'asm_sha256': hashlib.sha256(path.read_bytes()).hexdigest(), 'steps': [step]}

if mode == 'static-arm':
    benches = sorted((checkout / 'bench').glob('*.fern'))
    if limit:
        benches = benches[:limit]
    record['bench_count'] = len(benches)
    for i, source in enumerate(benches):
        for target in ('x86-64-linux', 'arm64-linux', 'wasm32-wasi'):
            key, count, row = emit(source, source.stem, target)
            metrics.append(f'selfhost/{target}/{source.stem}.emit\t{count}')
            if target == 'arm64-linux':
                metrics.append(f'aarch64/{source.stem}.text\t{count}')
                binary = root / key
                build, step = run([str(compiler), '-target', target, '-o', str(binary), str(source), str(checkout / 'internal/stdlib')])
                row['steps'].append(step)
                assert build.returncode == 0, step
                cg = root / (key + '.callgrind')
                command = ['docker', 'run', '--rm', '--pull=never', '-v', f'{root}:{root}', image, 'timeout', '--signal=KILL', '90s', 'valgrind', '--tool=callgrind', '--callgrind-out-file=' + str(cg), str(binary)]
                result, step = run(command)
                row['steps'].append(step)
                match = re.search(rb'I\s+refs:\s+([\d,]+)', result.stderr)
                assert match, step
                ir = int(match[1].replace(b',', b''))
                metrics.append(f'aarch64/{source.stem}.ir\t{ir}')
                row['ir'] = ir
                basebin = root / ('baseline-' + key)
                build, step = run([str(baseline), '-target', target, '-o', str(basebin), str(source), str(checkout / 'internal/stdlib')])
                row['steps'].append(step)
                assert build.returncode == 0, step
                base, step = run(['docker', 'run', '--rm', '--pull=never', '-v', f'{root}:{root}', image, 'timeout', '--signal=KILL', '90s', str(basebin)])
                row['steps'].append(step)
                assert base.returncode == result.returncode and base.stdout == result.stdout, (source.stem, row)
                row['observable_output_matches_baseline'] = True
                row['binary_sha256'] = hashlib.sha256(binary.read_bytes()).hexdigest()
                row['baseline_binary_sha256'] = hashlib.sha256(basebin.read_bytes()).hexdigest()
            row['emit'] = count
            record['rows'].append(row)
            save()
        print(i + 1, len(benches), source.stem, flush=True)
elif mode == 'profile':
    metrics.append('# no-valgrind: x86 dynamic profile rows require native x86 CI')
    startup = root / 'startup.fern'
    startup.write_text('function main(): i32 {\n  return 0;\n}\n')
    benches = [startup] + [checkout / 'bench' / (name + '.fern') for name in 'int_loop call_overhead closure_call array_append map_string enum_match http_hello deflate_decode'.split()]
    if limit:
        benches = benches[:limit]
    for target in ('x86-64-linux', 'arm64-linux'):
        for profile in ('safe', 'hardened', 'diagnostic', 'minimal'):
            for source in benches:
                key, count, row = emit(source, source.stem, target, profile)
                binary = root / key
                build, step = run([str(compiler), '-target', target, '-profile', profile, '-o', str(binary), str(source), str(checkout / 'internal/stdlib')])
                row['steps'].append(step)
                assert build.returncode == 0, step
                size = binary.stat().st_size
                row.update(emit=count, bytes=size, binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest())
                record['rows'].append(row)
                metrics += [f'profile/{target}/{source.stem}.{profile}.emit\t{count}', f'profile/{target}/{source.stem}.{profile}.bytes\t{size}']
                save()
            print(target, profile, 'passed', flush=True)
else:
    raise ValueError(mode)
