from pathlib import Path
import datetime, hashlib, json, os, signal, subprocess, time

root = Path('/workspace/lang-interpreter-retained-main-20261110-0845')
main = Path('/workspace/lang-interpreter-main-base-20261110-0845')
out = Path(__file__).resolve().parent
fixed = Path('/workspace/lang-interpreter-timeout-20261109')
producer = Path(os.environ['STAGE0'])
sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
state = {'source': '1fb070786cd3bf799b78873d9dbaa6f2c9500ed3', 'main_source': 'e23f590d1e523a2a90c7b202c7631b80aa4635da',
         'producer_sha256': sha(producer), 'runs': [],
         'note': 'Fresh exact-binary and correctness controls after upstream compiler, HTTP, TLS, and runtime changes. Full interpretation overlaps targeted validation. No performance comparison or normal-run allocation-balance claim.'}
assert subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=root, text=True).strip() == state['source']
assert not subprocess.check_output(['git', 'diff', state['main_source'], state['source'], '--', 'compiler', 'internal/stdlib'], cwd=root)
def save():
    (out / 'build-full.json').write_text(json.dumps(state, indent=2) + '\n')
save()
try:
    env = {k: v for k, v in os.environ.items() if not k.startswith('FERN_')}
    env.update(FERN_PROFILE='0', FERN_LEAKCHECK='0', FERN_RC_CERTIFY='1', FERN_RC_CERTIFY_MEMO='0')
    for name, source in [('main', main), ('retained', root)]:
        args = [str(producer), '-target', 'x86-64-linux', '-o', str(out / (name + '-normal')), 'compiler/fern.fern', 'internal/stdlib']
        row = {'label': name + '-build', 'args': args, 'cwd': str(source), 'environment': {k: v for k, v in env.items() if k.startswith('FERN_')}, 'started': datetime.datetime.now(datetime.timezone.utc).isoformat()}
        state['runs'].append(row)
        save()
        with (out / (name + '-build.stdout')).open('wb') as stdout, (out / (name + '-build.stderr')).open('wb') as stderr:
            row['exit_code'] = subprocess.run(args, cwd=source, env=env, stdout=stdout, stderr=stderr).returncode
        row['finished'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        save()
        assert row['exit_code'] == 0
        row['binary_sha256'] = sha(out / (name + '-normal'))
        save()
    assert state['runs'][0]['binary_sha256'] == state['runs'][1]['binary_sha256']
    state['main_binary_byte_identical'] = True
    save()
    args = [str(out / 'retained-normal'), '-stdlib', str(fixed / 'internal/stdlib'), '-interp', str(fixed / 'tests/stdlib/opus_delimited_test.fern')]
    row = {'label': 'full', 'args': args, 'cwd': str(fixed), 'environment': {k: v for k, v in env.items() if k.startswith('FERN_')},
           'started': datetime.datetime.now(datetime.timezone.utc).isoformat(), 'diagnostic_limit_seconds': 1980}
    state['runs'].append(row)
    save()
    start = time.perf_counter_ns()
    with (out / 'full.stdout').open('wb') as stdout, (out / 'full.stderr').open('wb') as stderr:
        child = subprocess.Popen(args, cwd=fixed, env=env, stdout=stdout, stderr=stderr, start_new_session=True)
        try:
            row['exit_code'] = child.wait(timeout=1980)
        except subprocess.TimeoutExpired:
            os.killpg(child.pid, signal.SIGTERM)
            row.update(exit_code=child.wait(), timed_out=True)
    row.update(wall_ns=time.perf_counter_ns() - start, finished=datetime.datetime.now(datetime.timezone.utc).isoformat(), stdout_sha256=sha(out / 'full.stdout'))
    save()
    assert row['exit_code'] == 0
    assert row['stdout_sha256'] == '07c2de202ca09462f396f349cbf99df5b0f044194e9314840de8bf6331c29161'
    state.update(exit_code=0, finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
except Exception as error:
    state.update(exit_code=1, error=repr(error), finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
    raise
finally:
    save()
