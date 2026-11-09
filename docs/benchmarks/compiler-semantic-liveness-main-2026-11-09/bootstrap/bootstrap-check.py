from pathlib import Path
import datetime
import hashlib
import json
import os
import subprocess

repo = Path('/workspace/lang-semantic-liveness-20261109')
work = Path('/workspace/lang/build/semantic-liveness-main-20261109')
out = Path('/workspace/lang/build/semantic-liveness-main-bootstrap-20261109')
measurement = json.loads((work / 'measurements.json').read_text())
assert json.loads(Path('/workspace/lang/build/semantic-liveness-main-rusage-20261109/samples.json').read_text()).get('complete'), 'Finish timing samples before this extra build'
assert not out.exists(), 'Retain existing bootstrap evidence'
assert subprocess.check_output(['git', 'status', '--porcelain'], cwd=repo, text=True) == ''
assert subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=repo, text=True).strip() == measurement['candidate_source']
out.mkdir()
state = {'source': measurement['candidate_source'], 'pinned_producer_source': measurement['producer_source'], 'pinned_producer_sha256': measurement['producer_sha256'], 'target': 'x86-64-linux', 'debug': False, 'certification': True, 'proof_memo': False, 'stages': []}

def sha(p):
    return hashlib.sha256(Path(p).read_bytes()).hexdigest()

def save():
    (out / 'bootstrap.json').write_text(json.dumps(state, indent=2) + '\n')

producer = work / 'candidate-native'
assert sha(producer) == measurement['compiler_binaries']['candidate-native']['sha256']
state['stage1_sha256'] = sha(producer)
for stage in [2, 3]:
    binary = out / ('fern-stage' + str(stage))
    args = [str(producer), '-target', 'x86-64-linux', '-embed', 'internal/stdlib', '-o', str(binary), 'compiler/fern.fern', 'internal/stdlib']
    env = {key: value for key, value in os.environ.items() if not key.startswith('FERN_')}
    env.update(FERN_PROFILE='0', FERN_LEAKCHECK='0', FERN_RC_CERTIFY='1', FERN_RC_CERTIFY_MEMO='0')
    record = {'stage': stage, 'producer_sha256': sha(producer), 'args': args, 'cwd': str(repo), 'fern_env': {key:value for key,value in env.items() if key.startswith('FERN_')}, 'started': datetime.datetime.now(datetime.timezone.utc).isoformat()}
    state['stages'].append(record)
    save()
    with (out / ('stage' + str(stage) + '.stdout')).open('wb') as stdout, (out / ('stage' + str(stage) + '.log')).open('wb') as stderr:
        done = subprocess.run(args, cwd=repo, env=env, stdout=stdout, stderr=stderr)
    record.update(exit_code=done.returncode, finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
    save()
    assert done.returncode == 0, record
    record['sha256'] = sha(binary)
    save()
    print('Stage', stage, 'exit0 SHA', record['sha256'], flush=True)
    producer = binary
state['full_binaries_equal'] = (out / 'fern-stage2').read_bytes() == (out / 'fern-stage3').read_bytes()
save()
assert state['full_binaries_equal'], 'Retain stage mismatch for diagnosis'
assert subprocess.check_output(['git', 'status', '--porcelain'], cwd=repo, text=True) == ''
assert subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=repo, text=True).strip() == state['source']
state['complete'] = True
save()
print('Full stage2/stage3 binaries are byte-identical.', flush=True)
