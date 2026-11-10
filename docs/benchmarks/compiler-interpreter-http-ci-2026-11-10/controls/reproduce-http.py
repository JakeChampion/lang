from pathlib import Path
import datetime
import hashlib
import json
import os
import re
import subprocess

repo = Path('/workspace/lang-interpreter-dispatch-20261109')
out = repo / 'build/interpreter-http-baselines-20261110-0520'
out.mkdir(exist_ok=False)
fixed = Path('/workspace/lang-interpreter-map-main-base-20261110-0330')
old = repo / 'build/interpreter-lazy-qualification-20261110-0345'
bins = {'base': old / 'baseline-normal', 'candidate': old / 'candidate-normal'}
expected_hashes = {'base': '1be868407c58e40bd6f0a56babf5021c708ea421b0fb6cb4f1ff2980f50a3a11',
                   'candidate': 'ed8d1f643ae1bd38f7d106d93a6b322dc3156f12fca98515b4813ac2dace7a64'}
sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
now = lambda: datetime.datetime.now(datetime.timezone.utc).isoformat()
assert {name: sha(path) for name, path in bins.items()} == expected_hashes
assert subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=fixed, text=True).strip() == 'da3282d0f3bc2614d45667b17edad8f844eb6a1b'
src = fixed / 'bench/http_hello.fern'
stdlib = fixed / 'internal/stdlib'
env = {k: v for k, v in os.environ.items() if not k.startswith('FERN_')}
env.update(FERN_PROFILE='0', FERN_LEAKCHECK='0', FERN_RC_CERTIFY='1', FERN_RC_CERTIFY_MEMO='0',
           VALGRIND_LIB='/workspace/scratch/lang-valgrind-20261109/root/usr/libexec/valgrind')
tool = '/workspace/scratch/lang-valgrind-20261109/root/usr/bin/valgrind'
state = {'baseline_source': 'da3282d0f3bc2614d45667b17edad8f844eb6a1b',
         'candidate_source': '597b6e782f5d2825baf3ed07dcf9eeba3dc57be2',
         'fixed_source': 'da3282d0f3bc2614d45667b17edad8f844eb6a1b',
         'producer_source': 'a19e87208c5931d2600fdb75cc966fd179ae8c8f',
         'compiler_sha256': expected_hashes, 'input_sha256': sha(src),
         'environment': {k: v for k, v in env.items() if k.startswith('FERN_') or k == 'VALGRIND_LIB'},
         'started': now(), 'runs': [], 'pairs': [], 'metrics': {}}
def save():
    (out / 'result.tmp').write_text(json.dumps(state, indent=2) + '\n')
    (out / 'result.tmp').replace(out / 'result.json')
def run(label, args, expected_exit=0):
    row = {'label': label, 'args': list(map(str, args)), 'cwd': str(fixed), 'started': now()}
    state['runs'].append(row)
    save()
    with (out / (label + '.stdout')).open('wb') as stdout, (out / (label + '.stderr')).open('wb') as stderr:
        row['exit_code'] = subprocess.run(args, cwd=fixed, env=env, stdout=stdout, stderr=stderr, timeout=300).returncode
    row['finished'] = now()
    save()
    if expected_exit is not None:
        assert row['exit_code'] == expected_exit, row
    return row['exit_code']
def compile_pair(target, profile='safe', native=True):
    tag = target + '-' + profile
    files = {}
    for name, binary in bins.items():
        asm = out / (tag + '-' + name + '.s')
        run(tag + '-' + name + '-asm', [binary, '-target', target, '-profile', profile,
                                      '-emit', 'asm', '-o', asm, src, stdlib])
        emit = sum(bool(re.match(rb'^\s', line)) for line in asm.read_bytes().splitlines())
        row = {'assembly_sha256': sha(asm), 'emit': emit, 'assembly': asm.name}
        if native:
            image = out / (tag + '-' + name + '.bin')
            run(tag + '-' + name + '-native', [binary, '-target', target, '-profile', profile,
                                              '-o', image, src, stdlib])
            row.update(native_sha256=sha(image), bytes=image.stat().st_size, native=image.name)
        files[name] = row
    assert files['base']['assembly_sha256'] == files['candidate']['assembly_sha256'], tag
    if native:
        assert files['base']['native_sha256'] == files['candidate']['native_sha256'], tag
    pair = {'target': target, 'profile': profile, 'files': files}
    if target == 'x86-64-linux' and native:
        image = out / files['base']['native']
        exit_code = run(tag + '-normal', [image], expected_exit=None)
        assert 0 <= exit_code < 97
        irs = []
        for name in bins:
            image = out / files[name]['native']
            raw = out / (tag + '-' + name + '.callgrind')
            log = out / (tag + '-' + name + '.valgrind.log')
            run(tag + '-' + name + '-count', [tool, '--tool=callgrind', '--cache-sim=no', '--branch-sim=no',
                                            '--trace-children=no', '--callgrind-out-file=' + str(raw),
                                            '--log-file=' + str(log), image], expected_exit=exit_code)
            counts = re.findall(r'^summary:\s*(\d+)\s*$', raw.read_text(), re.M)
            refs = re.findall(r'I\s+refs:\s*([\d,]+)', log.read_text())
            assert len(counts) == len(refs) == 1
            assert int(counts[0]) == int(refs[0].replace(',', ''))
            irs.append(int(counts[0]))
        assert irs[0] == irs[1]
        pair.update(guest_instructions=irs[0], expected_exit=exit_code)
        state['metrics'][f'profile/{target}/http_hello.{profile}.ir'] = irs[0]
        if profile == 'safe':
            state['metrics']['x86_64/http_hello.ir'] = irs[0]
    for suffix in ['emit', 'bytes']:
        if suffix in files['base']:
            state['metrics'][f'profile/{target}/http_hello.{profile}.{suffix}'] = files['base'][suffix]
    if profile == 'safe':
        state['metrics'][f'selfhost/{target}/http_hello.emit'] = files['base']['emit']
        if target in ['x86-64-linux', 'arm64-linux']:
            state['metrics'][('x86_64' if target == 'x86-64-linux' else 'aarch64') + '/http_hello.text'] = files['base']['emit']
    state['pairs'].append(pair)
    save()
for target in ['x86-64-linux', 'arm64-linux']:
    for profile in ['safe', 'hardened', 'diagnostic', 'minimal']:
        compile_pair(target, profile)
compile_pair('wasm32-wasi', native=False)
warnings = []
for filename in ['perf-x86-129627638421.log', 'perf-arm-129627638638.log']:
    text = (Path(__file__).resolve().parent / filename).read_text()
    for metric, measured, baseline in re.findall(r'##\[warning\]([^ ]+) measured (\d+) against a baseline of (\d+)', text):
        assert state['metrics'][metric] == int(measured), (metric, state['metrics'].get(metric), measured)
        row = {'metric': metric, 'old': int(baseline), 'measured': int(measured)}
        if row not in warnings:
            warnings.append(row)
assert len(warnings) == 24, len(warnings)
state.update(verified_ci_rows=warnings, finished=now())
save()
print(json.dumps({'finished': state['finished'], 'verified_ci_rows': len(warnings), 'pairs': len(state['pairs']), 'commands': len(state['runs'])}))
