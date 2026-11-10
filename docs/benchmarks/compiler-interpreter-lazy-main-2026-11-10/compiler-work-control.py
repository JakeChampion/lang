from pathlib import Path
import datetime, hashlib, json, os, re, subprocess, time

parent = Path(__file__).resolve().parent
out = parent / 'compilation-instructions'
assert not out.exists()
out.mkdir()
integration = parent.parent / 'interpreter-main-integration-20261110-0710'
fixed = Path('/workspace/lang-interpreter-timeout-20261109')
tool = Path('/workspace/scratch/lang-valgrind-20261109/root/usr/bin/valgrind')
sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
state = {'started': datetime.datetime.now(datetime.timezone.utc).isoformat(), 'phase': 'wait-for-tls-control',
         'purpose': 'Check cold complete-compilation work after all three normal cold CPU pairs were slower. This is a new concern, not a repeated timing comparison.',
         'note': 'One guest instruction sample per compiler. No timing claim. No child tracing, cache simulation, or branch simulation.', 'runs': []}
def save():
    (out / 'results.tmp').write_text(json.dumps(state, indent=2) + '\n')
    (out / 'results.tmp').replace(out / 'results.json')
save()
try:
    deadline = time.monotonic() + 2700
    while True:
        result = json.loads((integration / 'tls-control-result.json').read_text())
        if result.get('finished'):
            assert result['exit_code'] == 0 and result['phase'] == 'complete'
            break
        assert time.monotonic() < deadline
        time.sleep(2)
    prep = json.loads((parent / 'prepare.json').read_text())
    compilation = json.loads((parent / 'compilation/results.json').read_text())
    expected = compilation['runs'][0]['output_sha256']
    state.update(phase='compilation-instructions', baseline_source=prep['baseline_source'], candidate_source=prep['candidate_source'],
                 tool_version=subprocess.check_output([str(tool), '--version'], text=True).strip(), tool_sha256=sha(tool), producer_sha256=prep['producer_sha256'])
    env = {k: v for k, v in os.environ.items() if not k.startswith('FERN_')}
    env.update(FERN_PROFILE='0', FERN_LEAKCHECK='0', FERN_RC_CERTIFY='1', FERN_RC_CERTIFY_MEMO='0',
               VALGRIND_LIB='/workspace/scratch/lang-valgrind-20261109/root/usr/libexec/valgrind')
    for name in ('candidate', 'baseline'):
        binary = parent / (name + '-normal')
        assert sha(binary) == prep['binaries'][name + '-normal']['sha256']
        args = [str(tool), '--tool=callgrind', '--cache-sim=no', '--branch-sim=no', '--trace-children=no',
                '--callgrind-out-file=' + str(out / (name + '.callgrind')), '--log-file=' + str(out / (name + '.valgrind.log')),
                str(binary), '-target', 'x86-64-linux', '-o', str(out / 'opus-reference'),
                str(fixed / 'tests/stdlib/opus_delimited_test.fern'), str(fixed / 'internal/stdlib')]
        row = {'compiler': name, 'binary_sha256': sha(binary), 'args': args, 'cwd': str(fixed),
               'environment': {k: v for k, v in env.items() if k.startswith('FERN_') or k == 'VALGRIND_LIB'},
               'started': datetime.datetime.now(datetime.timezone.utc).isoformat()}
        state['runs'].append(row)
        save()
        with (out / (name + '.stdout')).open('wb') as stdout, (out / (name + '.stderr')).open('wb') as stderr:
            row['exit_code'] = subprocess.run(args, cwd=fixed, env=env, stdout=stdout, stderr=stderr).returncode
        row['finished'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        save()
        assert row['exit_code'] == 0
        row['output_sha256'] = sha(out / 'opus-reference')
        assert row['output_sha256'] == expected
        totals = re.findall(r'^summary:\s*(\d+)\s*$', (out / (name + '.callgrind')).read_text(), re.M)
        refs = re.findall(r'I\s+refs:\s*([\d,]+)', (out / (name + '.valgrind.log')).read_text())
        assert len(totals) == len(refs) == 1 and int(totals[0]) == int(refs[0].replace(',', ''))
        row['guest_instructions'] = int(totals[0])
        save()
    state.update(exit_code=0, phase='complete', finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
except Exception as error:
    state.update(exit_code=1, error=repr(error), finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
    raise
finally:
    save()
