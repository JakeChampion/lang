from pathlib import Path
import datetime, hashlib, json, os, shutil, statistics, subprocess

out = Path(__file__).resolve().parent
qualification = out.parent / 'interpreter-lazy-main-qualification-20261110-0710'
fixed = Path('/workspace/lang-interpreter-timeout-20261109')
sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
prep = json.loads((qualification / 'prepare.json').read_text())
original = json.loads((qualification / 'compilation/results.json').read_text())
expected = original['runs'][0]['output_sha256']
state = {'started': datetime.datetime.now(datetime.timezone.utc).isoformat(),
         'baseline_source': prep['baseline_source'], 'candidate_source': prep['candidate_source'],
         'producer_sha256': prep['producer_sha256'], 'fixed_input_source': 'ebb50c78c0ca714180704352881da7e39efb570f',
         'purpose': 'Resolve the existing complete-compilation timing concern: every original cold CPU pair is slower despite a later guest instruction difference of only 164.',
         'design': 'Eight fixed pairs per proof-cache mode. Four pairs use each execution order. Binary paths have equal lengths. Both binaries receive separate cache primes and zero-miss probes before timing. No timing value changes the sample count or stopping rule.',
         'limits': 'Shared host, no exclusive CPU or frequency control. This is a separate follow-up, not a replacement for earlier samples. No cache simulator or allocation counters. Small read-only CI observations can overlap.',
         'runs': [], 'binary_identities': {}}
def save():
    (out / 'results.tmp').write_text(json.dumps(state, indent=2) + '\n')
    (out / 'results.tmp').replace(out / 'results.json')
save()
try:
    for compiler, directory in [('baseline', 'base'), ('candidate', 'cand')]:
        target = out / directory / 'fern'
        target.parent.mkdir()
        shutil.copy2(qualification / (compiler + '-normal'), target)
        assert sha(target) == prep['binaries'][compiler + '-normal']['sha256']
        state['binary_identities'][compiler] = {'path': str(target), 'sha256': sha(target), 'source_binary': str(qualification / (compiler + '-normal'))}
    assert len(state['binary_identities']['baseline']['path']) == len(state['binary_identities']['candidate']['path'])
    state['host'] = {'uname': subprocess.check_output(['uname', '-a'], text=True).strip(),
                     'cpu': Path('/proc/cpuinfo').read_text().split('model name', 1)[1].split('\n', 1)[0].strip(),
                     'processes_before': subprocess.check_output(['ps', '-eo', 'pid,ppid,pcpu,comm'], text=True)}
    def run(label, compiler, memo, report=False):
        binary = Path(state['binary_identities'][compiler]['path'])
        assert sha(binary) == state['binary_identities'][compiler]['sha256']
        env = {k: v for k, v in os.environ.items() if not k.startswith('FERN_')}
        env.update(FERN_PROFILE='0', FERN_LEAKCHECK='0', FERN_RC_CERTIFY='1', FERN_RC_CERTIFY_MEMO=str(memo))
        if report:
            env['FERN_IR_VERIFY'] = '1'
        args = [str(binary), '-target', 'x86-64-linux', '-o', str(out / 'opus-reference'),
                str(fixed / 'tests/stdlib/opus_delimited_test.fern'), str(fixed / 'internal/stdlib')]
        row = {'label': label, 'compiler': compiler, 'args': args, 'cwd': str(fixed),
               'environment': {k: v for k, v in env.items() if k.startswith('FERN_')},
               'started': datetime.datetime.now(datetime.timezone.utc).isoformat()}
        state['runs'].append(row)
        save()
        import time
        start = time.perf_counter_ns()
        with (out / (label + '.stdout')).open('wb') as stdout, (out / (label + '.stderr')).open('wb') as stderr:
            child = subprocess.Popen(args, env=env, cwd=fixed, stdout=stdout, stderr=stderr)
            pid, status, usage = os.wait4(child.pid, 0)
            child.returncode = os.waitstatus_to_exitcode(status)
        row.update(exit_code=child.returncode, wall_ns=time.perf_counter_ns() - start, cpu_user=usage.ru_utime,
                   cpu_system=usage.ru_stime, peak_rss_kib=usage.ru_maxrss, finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
        save()
        assert child.returncode == 0
        row['output_sha256'] = sha(out / 'opus-reference')
        assert row['output_sha256'] == expected
        if report:
            import re
            values = re.findall(r'FERN_RC_CERTIFY: memo hits=(\d+) misses=(\d+)', (out / (label + '.stderr')).read_text())
            assert values and all(int(m) == 0 for h, m in values)
            row.update(cache_hits=sum(int(h) for h, m in values), cache_misses=0)
            assert row['cache_hits'] > 0
        save()
    for compiler in ('candidate', 'baseline'):
        memo = out / ('memo-cand' if compiler == 'candidate' else 'memo-base')
        run(compiler + '-prime', compiler, memo)
        run(compiler + '-probe', compiler, memo, True)
    for mode in ('cold', 'warm'):
        for pair in range(8):
            order = ('candidate', 'baseline') if (pair + (mode == 'warm')) % 2 == 0 else ('baseline', 'candidate')
            for compiler in order:
                memo = '0' if mode == 'cold' else out / ('memo-cand' if compiler == 'candidate' else 'memo-base')
                run(f'{mode}-{pair}-{compiler}', compiler, memo)
    assert len(state['runs']) == 36 and all(r['exit_code'] == 0 for r in state['runs'])
    state['summary'] = {}
    for mode in ('cold', 'warm'):
        data = {}
        for compiler in ('baseline', 'candidate'):
            rows = [r for r in state['runs'] if r['label'].startswith(mode + '-') and r['compiler'] == compiler]
            data[compiler] = {'samples': len(rows), 'wall_median_s': statistics.median(r['wall_ns'] / 1e9 for r in rows),
                              'wall_range_s': [min(r['wall_ns'] / 1e9 for r in rows), max(r['wall_ns'] / 1e9 for r in rows)],
                              'cpu_median_s': statistics.median(r['cpu_user'] + r['cpu_system'] for r in rows),
                              'peak_rss_median_kib': statistics.median(r['peak_rss_kib'] for r in rows)}
        changes = []
        for pair in range(8):
            rows = {r['compiler']: r for r in state['runs'] if r['label'].startswith(f'{mode}-{pair}-')}
            changes.append(((rows['candidate']['cpu_user'] + rows['candidate']['cpu_system']) /
                            (rows['baseline']['cpu_user'] + rows['baseline']['cpu_system']) - 1) * 100)
        data['paired_cpu_change_percent'] = changes
        state['summary'][mode] = data
    state.update(exit_code=0, finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
except Exception as error:
    state.update(exit_code=1, error=repr(error), finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
    raise
finally:
    save()
