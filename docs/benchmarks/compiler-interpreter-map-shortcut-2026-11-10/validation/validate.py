from pathlib import Path
import datetime, hashlib, json, os, re, subprocess

root = Path('/workspace/lang-interpreter-map-qualified-20261110-0330')
out = Path('/workspace/lang-interpreter-dispatch-20261109/build/interpreter-map-integration-20261110-0330')
producer = os.environ['STAGE0']
state = {'source': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=root, text=True).strip(),
         'producer_sha256': hashlib.sha256(Path(producer).read_bytes()).hexdigest(), 'runs': []}
assert state['source'] == '70171cab7f9b7c0ed80ff26132b3e4d16425c406'

def save():
    (out / 'validation.json').write_text(json.dumps(state, indent=2) + '\n')

for label, args in [
    ('source-check', [producer, '-check', 'compiler/fern.fern', 'internal/stdlib']),
    ('format', [producer, '-fmt', '-d', 'compiler/interp.fern']),
    ('tests', ['go', 'test', './internal/testing/e2ecompiler', '-run',
               '^TestSelfHostInterp(X86_64|Arm64|Driver(X86_64|Arm64)|CallableValues|StructOps|AssocConsts|ScopeFrames|DynProvider|MapKeysCompareThroughEq|CallCostIgnoresFunctionCount)$',
               '-count=1', '-v', '-timeout=25m'])]:
    row = {'label': label, 'args': args, 'cwd': str(root), 'started': datetime.datetime.now(datetime.timezone.utc).isoformat()}
    state['runs'].append(row)
    save()
    with (out / (label + '.log')).open('wb') as log:
        result = subprocess.run(args, cwd=root, stdout=log, stderr=subprocess.STDOUT)
    row.update(exit_code=result.returncode, finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
    if label == 'tests':
        text = (out / (label + '.log')).read_text()
        row.update(passes=len(re.findall(r'^\s*--- PASS:', text, re.M)), skips=len(re.findall(r'^\s*--- SKIP:', text, re.M)))
    save()
    assert result.returncode == 0
    if label == 'format':
        assert not (out / (label + '.log')).read_bytes()
state['finished'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
save()
