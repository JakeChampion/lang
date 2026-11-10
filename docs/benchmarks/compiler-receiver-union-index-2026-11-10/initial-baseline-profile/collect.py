from pathlib import Path
import datetime, hashlib, json, os, re, shutil, subprocess, time

out = Path(__file__).resolve().parent
repo = Path('/workspace/lang-interpreter-dispatch-20261109')
source = Path('/workspace/lang-compiler-main-20261110-1540')
normal = out / 'normal'
producer = Path('/workspace/lang-interpreter-timeout-20261109/build/bootstrap/stage0/stage0-20261109-a19e872/fern-selfhost-x86-64-linux')
helper_root = repo / 'build/compiler-normal-profile-20261110-0435'
helper = helper_root / 'reconstruct'
tool = Path('/workspace/scratch/lang-valgrind-20261109/root/usr/bin/valgrind')
sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
state = {'source': '0e715dffcb9220479b3b6b5dc2055c81cd990497', 'phase': 'baseline-build', 'runs': [],
         'started': datetime.datetime.now(datetime.timezone.utc).isoformat(),
         'note': 'One cold compilation diagnosis of the original stock 0e715dff production compiler and fixed 0e715dff Opus input/stdlib. Not a source-pair timing or speedup comparison. The reconstructed symbol image never executes.'}
def save():
    (out / 'result.tmp').write_text(json.dumps(state, indent=2) + '\n')
    (out / 'result.tmp').replace(out / 'result.json')
save()
try:
    assert subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=source, text=True).strip() == state['source']
    assert not subprocess.check_output(['git', 'status', '--porcelain', '--untracked-files=no'], cwd=source, text=True)
    assert sha(producer) == 'a66fcf94f418bea5f969bfaf2b07e6fe56235d2565ca405c950e6e263187a751'
    assert 'x86-64-linux ' + sha(producer) in (source / 'bootstrap/stage0.lock').read_text()
    assert sha(helper) == json.loads((helper_root / 'manifest.json').read_text())['binary_sha256']['reconstruct']
    state.update(phase='baseline-build', producer_sha256=sha(producer), helper_sha256=sha(helper))
    env = {k: v for k, v in os.environ.items() if not k.startswith('FERN_')}
    env.update(FERN_PROFILE='0', FERN_LEAKCHECK='0', FERN_RC_CERTIFY='1', FERN_RC_CERTIFY_MEMO='0',
               VALGRIND_LIB='/workspace/scratch/lang-valgrind-20261109/root/usr/libexec/valgrind')
    state['tool_version'] = subprocess.check_output([str(tool), '--version'], env=env, text=True).strip()
    state['tool_sha256'] = sha(tool)
    state['input_sha256'] = sha(source / 'tests/stdlib/opus_delimited_test.fern')
    state['stdlib_tree'] = subprocess.check_output(['git', 'rev-parse', state['source'] + ':internal/stdlib'], cwd=source, text=True).strip()
    commands = [
        ('normal-build', [str(producer), '-target', 'x86-64-linux', '-o', str(normal), 'compiler/fern.fern', 'internal/stdlib']),
        ('normal-check', [str(normal), '-check', 'compiler/fern.fern', 'internal/stdlib']),
        ('cold', [str(tool), '--tool=callgrind', '--cache-sim=no', '--branch-sim=no', '--trace-children=no',
                  '--callgrind-out-file=' + str(out / 'cold.callgrind'), '--log-file=' + str(out / 'cold.valgrind.log'),
                  str(normal), '-target', 'x86-64-linux', '-o', str(out / 'opus-native'),
                  str(source / 'tests/stdlib/opus_delimited_test.fern'), str(source / 'internal/stdlib')]),
        ('program', [str(out / 'opus-native')]),
        ('assembly', [str(producer), '-target', 'x86-64-linux', '-emit', 'asm', '-o', str(out / 'normal.s'), 'compiler/fern.fern', 'internal/stdlib']),
        ('symbol-map', [str(helper), str(out / 'normal.s'), str(out / 'normal-reconstructed')]),
    ]
    for label, args in commands:
        row = {'label': label, 'args': args, 'cwd': str(source), 'environment': {k: v for k, v in env.items() if k.startswith('FERN_') or k == 'VALGRIND_LIB'},
               'started': datetime.datetime.now(datetime.timezone.utc).isoformat()}
        state['runs'].append(row)
        save()
        with (out / (label + '.stdout')).open('wb') as stdout, (out / (label + '.stderr')).open('wb') as stderr:
            row['exit_code'] = subprocess.run(args, cwd=source, env=env, stdout=stdout, stderr=stderr, timeout=1980).returncode
        row['finished'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        save()
        assert row['exit_code'] == 0
        if label == 'normal-build':
            state.update(normal_sha256=sha(normal), phase='cold-count')
        if label == 'cold':
            totals = re.findall(r'^summary:\s*(\d+)\s*$', (out / 'cold.callgrind').read_text(), re.M)
            refs = re.findall(r'I\s+refs:\s*([\d,]+)', (out / 'cold.valgrind.log').read_text())
            assert len(totals) == len(refs) == 1 and int(totals[0]) == int(refs[0].replace(',', ''))
            state.update(guest_instructions=int(totals[0]), output_sha256=sha(out / 'opus-native'))
            (out / 'cold-result.json').write_text(json.dumps({'finished': row['finished'], 'exit_code': 0, 'normal_sha256': sha(normal),
                                                          'guest_instructions': state['guest_instructions'], 'source': state['source'],
                                                          'raw_callgrind_sha256': sha(out / 'cold.callgrind')}, indent=2) + '\n')
        if label == 'program':
            row['stdout_sha256'] = sha(out / 'program.stdout')
            assert row['stdout_sha256'] == '07c2de202ca09462f396f349cbf99df5b0f044194e9314840de8bf6331c29161'
        save()
    assert sha(out / 'normal-reconstructed') == sha(normal)
    assert not subprocess.check_output(['git', 'status', '--porcelain', '--untracked-files=no'], cwd=source, text=True)
    shutil.copyfile(helper_root / 'reconstruct-final.fern.txt', out / 'reconstruct-source.fern.txt')
    (out / 'qualified-symbols.json').write_text(json.dumps({'source': state['source'], 'normal_sha256': sha(normal),
                                                         'reconstruction_sha256': sha(out / 'normal-reconstructed'),
                                                         'symbol_map_sha256': sha(out / 'symbol-map.stdout'), 'normal_assembly_sha256': sha(out / 'normal.s'),
                                                         'helper_binary_sha256': sha(helper), 'helper_source_sha256': sha(out / 'reconstruct-source.fern.txt'),
                                                         'note': 'Full byte identity qualifies the offline map. The reconstructed image never executes.'}, indent=2) + '\n')
    with (out / 'analysis.log').open('wb') as log:
        state['analysis_exit'] = subprocess.run(['python3', str(out / 'analyze-v2.py')], stdout=log, stderr=subprocess.STDOUT).returncode
    assert state['analysis_exit'] == 0
    a = json.loads((out / 'cold-analysis.json').read_text())
    assert not a['unresolved_addresses'] and a['self_cost_sum_matches_summary']
    state.update(exit_code=0, phase='complete', finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
except Exception as error:
    state.update(exit_code=1, error=repr(error), finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
    raise
finally:
    save()
