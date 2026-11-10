from pathlib import Path
import datetime, hashlib, json, os, shutil, subprocess

out = Path('/workspace/lang-interpreter-dispatch-20261109/build/interpreter-lazy-qualification-20261110-0345')
root = Path('/workspace/lang-interpreter-lazy-target-trial-20261110-0345')
old = Path('/workspace/lang-interpreter-dispatch-20261109/build/interpreter-map-qualification-20261110-0330')
producer = Path(os.environ['STAGE0'])
source = json.loads((out / 'trial-source.json').read_text())['source']
sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
assert subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=root, text=True).strip() == source
assert not subprocess.check_output(['git', 'status', '--porcelain', '--untracked-files=no'], cwd=root, text=True)
record = json.loads((old / 'prepare.json').read_text())
assert record['finished'] and record['baseline_source'] == 'da3282d0f3bc2614d45667b17edad8f844eb6a1b'
assert record['producer_sha256'] == sha(producer) == 'a66fcf94f418bea5f969bfaf2b07e6fe56235d2565ca405c950e6e263187a751'
state = {'baseline_source': record['baseline_source'], 'candidate_source': source,
         'producer_source': record['producer_source'], 'producer_sha256': sha(producer),
         'started': datetime.datetime.now(datetime.timezone.utc).isoformat(), 'runs': [], 'binaries': {}}

def save():
    (out / 'prepare.json').write_text(json.dumps(state, indent=2) + '\n')

for name in ('baseline-normal', 'baseline-count'):
    assert sha(old / name) == record['binaries'][name]['sha256']
    shutil.copy2(old / name, out / name)
    state['binaries'][name] = {'sha256': sha(out / name), 'reused_build_record': 'reused-baseline-build.json'}
(out / 'reused-baseline-build.json').write_text(json.dumps(record, indent=2) + '\n')
env = {k: v for k, v in os.environ.items() if not k.startswith('FERN_')}
env.update(FERN_PROFILE='0', FERN_LEAKCHECK='0', FERN_RC_CERTIFY='1', FERN_RC_CERTIFY_MEMO='0')
commands = [('source-check', [str(producer), '-check', 'compiler/fern.fern', 'internal/stdlib'], False),
            ('format', [str(producer), '-fmt', '-d', 'compiler/interp.fern'], False)]
for count in (False, True):
    label = 'candidate-' + ('count' if count else 'normal')
    commands.append((label, [str(producer), '-target', 'x86-64-linux', '-o', str(out / label), 'compiler/fern.fern', 'internal/stdlib'], count))
for label, args, count in commands:
    env['FERN_LEAKCHECK'] = '1' if count else '0'
    row = {'label': label, 'args': args, 'cwd': str(root), 'source': source,
           'environment': {k: v for k, v in env.items() if k.startswith('FERN_')},
           'started': datetime.datetime.now(datetime.timezone.utc).isoformat()}
    state['runs'].append(row)
    save()
    with (out / (label + '.stdout')).open('wb') as stdout, (out / (label + '.stderr')).open('wb') as stderr:
        result = subprocess.run(args, cwd=root, env=env, stdout=stdout, stderr=stderr)
    row.update(exit_code=result.returncode, finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
    save()
    assert result.returncode == 0
    if label == 'format':
        assert not (out / (label + '.stdout')).read_bytes()
    if label.startswith('candidate-'):
        state['binaries'][label] = {'sha256': sha(out / label)}
        save()
state['finished'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
save()
