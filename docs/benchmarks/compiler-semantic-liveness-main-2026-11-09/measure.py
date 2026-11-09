from pathlib import Path
import datetime
import hashlib
import json
import os
import re
import subprocess

repo = Path('/workspace/lang-semantic-liveness-20261109')
base = Path('/workspace/lang-semantic-main-base-20261109')
fixed = Path('/workspace/lang-semantic-base-20261109')
out = Path('/workspace/lang/build/semantic-liveness-main-20261109')
producer = Path('/workspace/lang/build/bootstrap/stage0/stage0-20261107-38f241c/fern-selfhost-x86-64-linux')
state_file = out / 'measurements.json'
assert not state_file.exists(), 'Retain the existing measurement record'

def git(root, *args):
    return subprocess.check_output(['git', '-C', str(root), *args], text=True).strip()

def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()

def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()

assert git(repo, 'status', '--porcelain') == ''
assert git(base, 'status', '--porcelain') == ''
state = {
    'baseline_source': git(base, 'rev-parse', 'HEAD'),
    'candidate_source': git(repo, 'rev-parse', 'HEAD'),
    'fixed_input_source': git(fixed, 'rev-parse', 'HEAD'),
    'producer_sha256': sha(producer),
    'producer_source': '38f241ca26366e5ef39a898c780bbfbc3a4694f5',
    'target': 'x86-64-linux', 'debug': False, 'certification': True,
    'compiler_binaries': {}, 'runs': [], 'allocations': {}, 'instructions': {},
    'cache_probes': {}, 'claim': 'Work counts on one fixed checker snapshot. No wall-time or external rank claim.'
}

def save():
    state_file.write_text(json.dumps(state, indent=2) + '\n')

def env(memo='0', count=False):
    result = {key: value for key, value in os.environ.items() if not key.startswith('FERN_')}
    result.update(FERN_PROFILE='0', FERN_RC_CERTIFY='1', FERN_RC_CERTIFY_MEMO=str(memo), FERN_LEAKCHECK='1' if count else '0')
    result.pop('FERN_IR_VERIFY', None)
    return result

def run(name, args, root=repo, environment=None):
    item = {'name': name, 'args': list(map(str, args)), 'cwd': str(root), 'started': now(), 'fern_env': {key: value for key, value in (environment or env()).items() if key.startswith('FERN_')}}
    state['runs'].append(item)
    save()
    with (out / (name + '.stdout')).open('wb') as stdout, (out / (name + '.log')).open('wb') as stderr:
        done = subprocess.run(item['args'], cwd=root, env=environment or env(), stdout=stdout, stderr=stderr)
    item.update(finished=now(), exit_code=done.returncode)
    save()
    assert done.returncode == 0, item
    print(name + ': exit 0', flush=True)

save()
for label, root in [('base', base), ('candidate', repo)]:
    for counted in [False, True]:
        name = label + ('-count' if counted else '-native')
        binary = out / name
        run('build-' + name, [producer, '-target', 'x86-64-linux', '-embed', 'internal/stdlib', '-o', binary, 'compiler/fern.fern', 'internal/stdlib'], root, env(count=counted))
        state['compiler_binaries'][name] = {'sha256': sha(binary)}
        save()

input_args = [fixed / 'compiler/checker.fern', fixed / 'internal/stdlib']
assembly_sha = None
for label in ['base', 'candidate']:
    name = label + '-alloc'
    assembly = out / 'checker.s'
    run(name, [out / (label + '-count'), '-target', 'x86-64-linux', '-emit', 'asm', '-o', assembly, *input_args])
    matches = re.findall(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)', (out / (name + '.log')).read_text())
    assert len(matches) == 1, matches
    allocs, frees, live = map(int, matches[0])
    assert allocs > 0 and allocs == frees and live == 0, matches
    digest = sha(assembly)
    if assembly_sha is None:
        assembly_sha = digest
    assert digest == assembly_sha, 'Assembly differs'
    state['allocations'][label] = {'allocs': allocs, 'frees': frees, 'live_bytes': live, 'assembly_sha256': digest}
    save()
    print(name + ': ' + str(allocs) + ' balanced allocations', flush=True)

native_sha = None
for label in ['base', 'candidate']:
    memo = out / ('memo-' + ('base' if label == 'base' else 'cand'))
    native = out / 'checker'
    args = [out / (label + '-native'), '-target', 'x86-64-linux', '-o', native, *input_args]
    run(label + '-prime', args, environment=env(memo))
    digest = sha(native)
    if native_sha is None:
        native_sha = digest
    assert digest == native_sha, 'Native output differs'
    probe_env = env(memo)
    probe_env['FERN_IR_VERIFY'] = '1'
    run(label + '-cache-probe', args, environment=probe_env)
    hits = re.findall(r'FERN_RC_CERTIFY: memo hits=(\d+) misses=(\d+)', (out / (label + '-cache-probe.log')).read_text())
    assert hits and sum(int(h) for h,m in hits)>0 and all(int(m)==0 for h,m in hits), hits
    assert sha(native) == native_sha
    state['cache_probes'][label] = {'hits': sum(int(h) for h,m in hits), 'misses': 0}
    save()

assert len(str(out / 'memo-base')) == len(str(out / 'memo-cand'))
for mode, labels in [('cold', ['base', 'candidate']), ('warm', ['candidate', 'base'])]:
    state['instructions'][mode] = {}
    for label in labels:
        name = mode + '-' + label + '-callgrind'
        memo = '0' if mode == 'cold' else out / ('memo-' + ('base' if label == 'base' else 'cand'))
        run_env = env(memo)
        run_env['VALGRIND_LIB'] = '/workspace/scratch/valgrind/usr/libexec/valgrind'
        run(name, ['/workspace/scratch/valgrind/usr/bin/valgrind', '--tool=callgrind', '--trace-children=no', '--cache-sim=no', '--branch-sim=no', '--callgrind-out-file=/dev/null', out / (label + '-native'), '-target', 'x86-64-linux', '-o', out / 'checker', *input_args], environment=run_env)
        matches = re.findall(r'I\s+refs:\s*([\d,]+)', (out / (name + '.log')).read_text())
        assert len(matches) == 1, matches
        assert sha(out / 'checker') == native_sha, 'Native output differs'
        instructions = int(matches[0].replace(',', ''))
        state['instructions'][mode][label] = {'instructions': instructions, 'native_sha256': native_sha}
        save()
        print(name + ': ' + str(instructions) + ' instructions; output matches', flush=True)

assert git(repo, 'rev-parse', 'HEAD') == state['candidate_source']
assert git(base, 'rev-parse', 'HEAD') == state['baseline_source']
assert git(repo, 'status', '--porcelain') == ''
assert git(base, 'status', '--porcelain') == ''
state['complete'] = True
save()
print('All fixed-input comparisons complete.', flush=True)
