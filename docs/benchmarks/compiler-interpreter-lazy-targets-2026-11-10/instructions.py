from pathlib import Path
import datetime, hashlib, json, os, re, subprocess

parent = Path('/workspace/lang-interpreter-dispatch-20261109/build/interpreter-lazy-qualification-20261110-0345')
out = parent / 'instructions'
out.mkdir()
fixed = Path('/workspace/lang-interpreter-timeout-20261109')
tool = Path('/workspace/scratch/lang-valgrind-20261109/root/usr/bin/valgrind')
prep = json.loads((parent / 'prepare.json').read_text())
sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
env = {k: v for k, v in os.environ.items() if not k.startswith('FERN_')}
env.update(FERN_PROFILE='0', FERN_LEAKCHECK='0', FERN_RC_CERTIFY='1', FERN_RC_CERTIFY_MEMO='0',
           VALGRIND_LIB='/workspace/scratch/lang-valgrind-20261109/root/usr/libexec/valgrind')
state = {'baseline_source': prep['baseline_source'], 'candidate_source': prep['candidate_source'],
         'tool_sha256': sha(tool), 'tool_version': subprocess.check_output([str(tool), '--version'], env=env, text=True).strip(),
         'note': 'One guest instruction sample per normal binary on the first fixed 32-record group. Compiler children are not traced. Cache and branch simulation are off. Targeted validation can overlap these instruction collectors. No timing claim derives from these runs.', 'runs': []}

def save():
    (out / 'results.json').write_text(json.dumps(state, indent=2) + '\n')

for name in ('baseline', 'candidate'):
    binary = parent / (name + '-normal')
    assert sha(binary) == prep['binaries'][name + '-normal']['sha256']
    args = [str(tool), '--tool=callgrind', '--cache-sim=no', '--branch-sim=no', '--trace-children=no',
            '--callgrind-out-file=' + str(out / (name + '.callgrind')), '--log-file=' + str(out / (name + '.valgrind.log')),
            str(binary), '-stdlib', str(fixed / 'internal/stdlib'), '-interp',
            str(fixed / 'build/interpreter-profile-20261109/first-group.fern')]
    row = {'compiler': name, 'args': args, 'binary_sha256': sha(binary), 'cwd': str(fixed),
           'environment': {k: v for k, v in env.items() if k.startswith('FERN_') or k == 'VALGRIND_LIB'},
           'started': datetime.datetime.now(datetime.timezone.utc).isoformat()}
    state['runs'].append(row)
    save()
    with (out / (name + '.stdout')).open('wb') as stdout, (out / (name + '.stderr')).open('wb') as stderr:
        result = subprocess.run(args, env=env, cwd=fixed, stdout=stdout, stderr=stderr)
    row.update(exit_code=result.returncode, finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
    save()
    assert result.returncode == 0
    totals = re.findall(r'^summary:\s*(\d+)\s*$', (out / (name + '.callgrind')).read_text(), re.M)
    assert len(totals) == 1
    refs = re.findall(r'I\s+refs:\s*([\d,]+)', (out / (name + '.valgrind.log')).read_text())
    assert len(refs) == 1 and int(refs[0].replace(',', '')) == int(totals[0])
    row.update(guest_instructions=int(totals[0]), stdout_sha256=sha(out / (name + '.stdout')))
    save()
assert state['runs'][0]['stdout_sha256'] == state['runs'][1]['stdout_sha256']
state['finished'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
save()
