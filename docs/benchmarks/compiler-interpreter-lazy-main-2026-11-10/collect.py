from pathlib import Path
import datetime
import json
import subprocess
import time

root = Path(__file__).resolve().parent
state = {'started': datetime.datetime.now(datetime.timezone.utc).isoformat(), 'phase': 'wait-for-prerequisites', 'runs': []}
def save():
    (root / 'collector-result.tmp').write_text(json.dumps(state, indent=2) + '\n')
    (root / 'collector-result.tmp').replace(root / 'collector-result.json')
def ready(path):
    if not path.exists():
        return False
    result = json.loads(path.read_text())
    assert all(row.get('exit_code') in [None, 0] for row in result['runs']), path
    return bool(result.get('finished'))
save()
try:
    deadline = time.monotonic() + 2100
    while not (ready(root / 'prepare.json') and ready(root / 'validation/validation.json')):
        assert time.monotonic() < deadline, 'Local prerequisites did not complete.'
        time.sleep(1)
    for label, args in [
        ('subsets', ['python3', str(root / 'measure.py'), 'subsets']),
        ('compilation', ['python3', str(root / 'compile-controls.py')]),
        ('full', ['python3', str(root / 'measure.py'), 'full']),
        ('instructions', ['python3', str(root / 'instructions.py')]),
    ]:
        state['phase'] = label
        row = {'label': label, 'args': args, 'started': datetime.datetime.now(datetime.timezone.utc).isoformat()}
        state['runs'].append(row)
        save()
        with (root / (label + '.log')).open('wb') as log:
            row['exit_code'] = subprocess.run(args, stdout=log, stderr=subprocess.STDOUT).returncode
        row['finished'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        save()
        assert row['exit_code'] == 0, label
    compiled = json.loads((root / 'compilation/results.json').read_text())
    full = json.loads((root / 'full/results.json').read_text())
    assert compiled['program']['stdout_sha256'] == full['runs'][0]['stdout_sha256']
    state.update(exit_code=0, phase='complete', finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
except Exception as error:
    state.update(exit_code=1, error=repr(error), finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
    raise
finally:
    save()
