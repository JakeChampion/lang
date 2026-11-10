from pathlib import Path
import datetime, hashlib, json, os, re, subprocess, time

root = Path('/workspace/lang-interpreter-qualified-20261110-0710')
out = Path(__file__).resolve().parent
qualification = out.parent / 'interpreter-lazy-main-qualification-20261110-0710'
state = {'source': 'c05b5ce874fc4a6ec6b0be45ee71d3ed1bd5f8f1',
         'purpose': 'Verify both TLS selectors that failed the superseded head. Main supplies the expectation repair.',
         'started': datetime.datetime.now(datetime.timezone.utc).isoformat(), 'phase': 'wait-for-qualification'}
def save():
    (out / 'tls-control-result.tmp').write_text(json.dumps(state, indent=2) + '\n')
    (out / 'tls-control-result.tmp').replace(out / 'tls-control-result.json')
save()
try:
    deadline = time.monotonic() + 2400
    while True:
        result = json.loads((qualification / 'collector-result.json').read_text())
        if result.get('finished'):
            assert result['exit_code'] == 0, result
            break
        assert time.monotonic() < deadline
        time.sleep(2)
    assert subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=root, text=True).strip() == state['source']
    assert not subprocess.check_output(['git', 'status', '--porcelain', '--untracked-files=no'], cwd=root, text=True)
    args = ['go', 'test', './internal/testing/e2e', '-run', '^TestTLSServerAgainstGo(Interp)?$', '-count=1', '-v', '-timeout=20m']
    state.update(phase='tests', args=args, cwd=str(root),
                 producer_sha256=hashlib.sha256(Path(os.environ['STAGE0']).read_bytes()).hexdigest(),
                 tests_started=datetime.datetime.now(datetime.timezone.utc).isoformat())
    save()
    with (out / 'tls-control-tests.log').open('wb') as log:
        state['exit_code'] = subprocess.run(args, cwd=root, stdout=log, stderr=subprocess.STDOUT).returncode
    text = (out / 'tls-control-tests.log').read_text()
    state.update(passes=len(re.findall(r'^\s*--- PASS:', text, re.M)), skips=len(re.findall(r'^\s*--- SKIP:', text, re.M)),
                 finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
    save()
    assert state['exit_code'] == 0 and state['passes'] > 0 and state['skips'] == 0
    state['phase'] = 'complete'
except Exception as error:
    state.update(exit_code=1, error=repr(error), finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
    raise
finally:
    save()
