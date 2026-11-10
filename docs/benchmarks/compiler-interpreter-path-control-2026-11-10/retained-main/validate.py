from pathlib import Path
import datetime, hashlib, json, os, re, subprocess

root = Path('/workspace/lang-interpreter-retained-main-20261110-0820')
out = Path(__file__).resolve().parent
producer = Path(os.environ['STAGE0'])
source = '9288f36037cd951751a4907c90879f6901483526'
state = {'source': source, 'producer_sha256': hashlib.sha256(producer.read_bytes()).hexdigest(),
         'purpose': 'Validate retained upstream eager lookup and the two regression files after rejection of the lazy shortcut.', 'runs': []}
assert subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=root, text=True).strip() == source
assert not subprocess.check_output(['git', 'diff', 'c9547605f513b81cb1c26d1cb4deaf37b20648bd', source, '--', 'compiler', 'internal/stdlib'], cwd=root)
def save():
    (out / 'validation.json').write_text(json.dumps(state, indent=2) + '\n')
save()
try:
    for label, args in [
        ('source-check', [str(producer), '-check', 'compiler/fern.fern', 'internal/stdlib']),
        ('format', [str(producer), '-fmt', '-d', 'compiler/interp.fern']),
        ('tests', ['go', 'test', './internal/testing/e2ecompiler', '-run',
                   '^(TestSelfHostInterp(X86_64|Arm64|Driver(X86_64|Arm64)|CallableValues|StructOps|AssocConsts|ScopeFrames|DynProvider|MapKeysCompareThroughEq|CallCostIgnoresFunctionCount)|TestSelfHostNestedOwnedFields)$',
                   '-count=1', '-v', '-timeout=25m']),
        ('go-vet', ['go', 'vet', './...']),
    ]:
        row = {'label': label, 'args': args, 'cwd': str(root), 'started': datetime.datetime.now(datetime.timezone.utc).isoformat()}
        state['runs'].append(row)
        save()
        with (out / (label + '.log')).open('wb') as log:
            row['exit_code'] = subprocess.run(args, cwd=root, stdout=log, stderr=subprocess.STDOUT).returncode
        row['finished'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        if label == 'tests':
            text = (out / 'tests.log').read_text()
            row.update(passes=len(re.findall(r'^\s*--- PASS:', text, re.M)), skips=len(re.findall(r'^\s*--- SKIP:', text, re.M)))
        save()
        assert row['exit_code'] == 0
        if label == 'tests':
            assert row['passes'] == 413 and row['skips'] == 0
        if label == 'format':
            assert not (out / 'format.log').read_bytes()
    state.update(exit_code=0, finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
except Exception as error:
    state.update(exit_code=1, error=repr(error), finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
    raise
finally:
    save()
