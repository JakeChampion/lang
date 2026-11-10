from pathlib import Path
import datetime, hashlib, json, os, subprocess

out = Path('/workspace/lang-interpreter-dispatch-20261109/build/interpreter-map-qualification-20261110-0330')
roots = {'baseline': Path('/workspace/lang-interpreter-map-main-base-20261110-0330'),
         'candidate': Path('/workspace/lang-interpreter-map-qualified-20261110-0330')}
expected = {'baseline': 'da3282d0f3bc2614d45667b17edad8f844eb6a1b', 'candidate': '70171cab7f9b7c0ed80ff26132b3e4d16425c406'}
producer = Path(os.environ['STAGE0'])
sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
state = {'baseline_source': expected['baseline'], 'candidate_source': expected['candidate'],
         'producer_source': 'a19e87208c5931d2600fdb75cc966fd179ae8c8f', 'producer_sha256': sha(producer),
         'started': datetime.datetime.now(datetime.timezone.utc).isoformat(), 'runs': [], 'binaries': {}}
assert state['producer_sha256'] == 'a66fcf94f418bea5f969bfaf2b07e6fe56235d2565ca405c950e6e263187a751'

def save():
    (out / 'prepare.json').write_text(json.dumps(state, indent=2) + '\n')

for name, root in roots.items():
    assert subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=root, text=True).strip() == expected[name]
    assert not subprocess.check_output(['git', 'status', '--porcelain', '--untracked-files=no'], cwd=root, text=True)
    for count in (False, True):
        label = name + ('-count' if count else '-normal')
        env = {k: v for k, v in os.environ.items() if not k.startswith('FERN_')}
        env.update(FERN_PROFILE='0', FERN_LEAKCHECK='1' if count else '0', FERN_RC_CERTIFY='1', FERN_RC_CERTIFY_MEMO='0')
        args = [str(producer), '-target', 'x86-64-linux', '-o', str(out / label), 'compiler/fern.fern', 'internal/stdlib']
        row = {'label': label, 'args': args, 'cwd': str(root), 'source': expected[name],
               'environment': {k: v for k, v in env.items() if k.startswith('FERN_')},
               'started': datetime.datetime.now(datetime.timezone.utc).isoformat()}
        state['runs'].append(row)
        save()
        with (out / (label + '.stdout')).open('wb') as stdout, (out / (label + '.stderr')).open('wb') as stderr:
            result = subprocess.run(args, cwd=root, env=env, stdout=stdout, stderr=stderr)
        row.update(exit_code=result.returncode, finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
        save()
        assert result.returncode == 0
        state['binaries'][label] = {'sha256': sha(out / label)}
        save()
state['finished'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
save()
