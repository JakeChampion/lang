from pathlib import Path
import datetime, hashlib, json, os, signal, subprocess, time

root = Path('/workspace/lang-interpreter-retained-main-20261110-0820')
out = Path(__file__).resolve().parent
fixed = Path('/workspace/lang-interpreter-timeout-20261109')
producer = Path(os.environ['STAGE0'])
sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
state = {'source': '9288f36037cd951751a4907c90879f6901483526', 'producer_sha256': sha(producer), 'runs': [],
         'note': 'Correctness and exact-binary control after rejection. The full normal corpus overlaps targeted validation. Its duration is not a performance comparison. Normal execution does not measure allocation balance.'}
def save():
    (out / 'build-full.json').write_text(json.dumps(state, indent=2) + '\n')
save()
try:
    env = {k: v for k, v in os.environ.items() if not k.startswith('FERN_')}
    env.update(FERN_PROFILE='0', FERN_LEAKCHECK='0', FERN_RC_CERTIFY='1', FERN_RC_CERTIFY_MEMO='0')
    args = [str(producer), '-target', 'x86-64-linux', '-o', str(out / 'retained-normal'), 'compiler/fern.fern', 'internal/stdlib']
    row = {'label': 'build', 'args': args, 'cwd': str(root), 'environment': {k: v for k, v in env.items() if k.startswith('FERN_')}, 'started': datetime.datetime.now(datetime.timezone.utc).isoformat()}
    state['runs'].append(row)
    save()
    with (out / 'build.stdout').open('wb') as stdout, (out / 'build.stderr').open('wb') as stderr:
        row['exit_code'] = subprocess.run(args, cwd=root, env=env, stdout=stdout, stderr=stderr).returncode
    row['finished'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
    save()
    assert row['exit_code'] == 0
    row['binary_sha256'] = sha(out / 'retained-normal')
    assert row['binary_sha256'] == 'bead6a81a9a0d0f31d5b84afc85e907e6cc28cfc9f07e488e0d94d9baf0d62c0'
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
