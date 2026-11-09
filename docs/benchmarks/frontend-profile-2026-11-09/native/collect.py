from pathlib import Path
import datetime
import hashlib
import json
import os
import re
import subprocess

repo = Path('/workspace/lang-semantic-liveness-20261109')
fixed = Path('/workspace/lang-semantic-base-20261109')
out = Path('/workspace/lang/build/parser-production-profile-20261109')
entry = repo / 'build/parser-production-profile-20261109/frontend.fern'
producer = Path('/workspace/lang/build/bootstrap/stage0/stage0-20261107-38f241c/fern-selfhost-x86-64-linux')
files = [fixed / 'compiler' / (name + '.fern') for name in ['lexer', 'parser', 'checker', 'ssarc', 'fern']]
record_file = out / 'manifest.json'
assert not record_file.exists(), 'Retain diagnostic evidence'


def sha(p):
    return hashlib.sha256(Path(p).read_bytes()).hexdigest()


def git(root, *args):
    return subprocess.check_output(['git', '-C', str(root), *args], text=True).strip()


assert git(repo, 'status', '--porcelain') == ''
assert git(fixed, 'status', '--porcelain') == ''
state = {'library_source': git(repo, 'rev-parse', 'HEAD'), 'input_source': git(fixed, 'rev-parse', 'HEAD'),
         'producer_source': '38f241ca26366e5ef39a898c780bbfbc3a4694f5', 'producer_sha256': sha(producer),
         'corpus': [{'path': str(f), 'bytes': f.stat().st_size, 'sha256': sha(f)} for f in files],
         'harness_sha256': sha(entry), 'loops': 100, 'target': 'x86-64-linux', 'debug': False,
         'certification': True, 'purpose': 'Counter-free diagnostic frontend CPU profiles. No speed comparison or external rank claim.',
         'concurrency': 'A separate Callgrind compiler comparison was active.', 'runs': []}


def save():
    record_file.write_text(json.dumps(state, indent=2) + '\n')


def run(name, args, profile=False):
    env = {key: value for key, value in os.environ.items() if not key.startswith('FERN_')}
    env.update(FERN_PROFILE='cpu' if profile else '0', FERN_LEAKCHECK='0', FERN_RC_CERTIFY='1', FERN_RC_CERTIFY_MEMO='0')
    rec = {'name': name, 'args': list(map(str,args)), 'cwd': str(repo),
           'fern_env': {k:v for k,v in env.items() if k.startswith('FERN_')},
           'started': datetime.datetime.now(datetime.timezone.utc).isoformat()}
    state['runs'].append(rec)
    save()
    with (out / (name + '.stdout')).open('wb') as stdout, (out / (name + '.log')).open('wb') as stderr:
        done = subprocess.run(rec['args'], cwd=repo, env=env, stdout=stdout, stderr=stderr)
    rec.update(exit_code=done.returncode, finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
    save()
    return rec


save()
rec = run('build', [producer, '-target', 'x86-64-linux', '-o', out / 'frontend-cpu', entry, repo / 'internal/stdlib'], profile=True)
assert rec['exit_code'] == 0, rec
state['cpu_binary_sha256'] = sha(out / 'frontend-cpu')
save()
for mode in ['lex', 'parse']:
    rec = run(mode, [out / 'frontend-cpu', mode, *files])
    lines = (out / (mode + '.stdout')).read_text().splitlines()
    assert len(lines) == 1 and all(line.isdigit() for line in lines), (rec, lines)
    checksum = int(lines[-1])
    assert rec['exit_code'] == checksum % 97, rec
    profile = (out / (mode + '.log')).read_text()
    match = re.search(r'# fern CPU profile samples=(\d+) dropped=(\d+)', profile)
    assert match, profile[:200]
    rec.update(checksum=checksum, profile_samples=int(match[1]), profile_dropped=int(match[2]))
    save()
    print(mode, 'checksum', checksum, 'exit', rec['exit_code'], 'samples', match[1], 'dropped', match[2], flush=True)
assert git(repo, 'status', '--porcelain') == ''
assert git(fixed, 'status', '--porcelain') == ''
state['complete'] = True
state['files_sha256'] = {f.name: sha(f) for f in sorted(out.iterdir()) if f.is_file() and f != record_file}
save()
print('Diagnostic profiles complete.', flush=True)
