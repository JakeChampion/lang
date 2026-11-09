from pathlib import Path
import datetime
import hashlib
import json
import os
import re
import subprocess
import time

work = Path('/workspace/lang/build/semantic-liveness-20261109')
out = Path('/workspace/lang/build/semantic-liveness-rusage-20261109')
base = Path('/workspace/lang-semantic-base-20261109')
repo = Path('/workspace/lang-semantic-liveness-20261109')
measured = json.loads((work / 'measurements.json').read_text())
assert all(name + '-native' in measured['compiler_binaries'] for name in ['base', 'candidate'])
assert not out.exists(), 'Retain existing timing samples'
out.mkdir()

def sha(p):
    return hashlib.sha256(Path(p).read_bytes()).hexdigest()

def git(root, *args):
    return subprocess.check_output(['git', '-C', str(root), *args], text=True).strip()

for root in [repo, base]:
    assert git(root, 'status', '--porcelain') == ''
assert git(repo, 'rev-parse', 'HEAD') == measured['candidate_source']
assert git(base, 'rev-parse', 'HEAD') == measured['baseline_source']
state = {key: measured[key] for key in ['baseline_source', 'candidate_source', 'fixed_input_source', 'producer_source', 'producer_sha256', 'target', 'debug', 'certification']}
state.update(samples=[], controls=[], compiler_binaries={}, concurrency='A separate Callgrind work-count comparison was active. Shared-host samples do not establish a timing rank.', samples_per_compiler_per_mode=5)
for label in ['base', 'candidate']:
    digest = sha(work / (label + '-native'))
    assert digest == measured['compiler_binaries'][label + '-native']['sha256']
    state['compiler_binaries'][label] = digest
state['measurement_method'] = 'Python perf_counter_ns and Linux wait4 rusage for each completed compiler process. RSS units are KiB.'
state['cpu_affinity'] = sorted(os.sched_getaffinity(0))
state['cgroup_cpu_max'] = Path('/sys/fs/cgroup/cpu.max').read_text().strip()
state['cgroup_memory_max'] = Path('/sys/fs/cgroup/memory.max').read_text().strip()
expected = None

def save():
    (out / 'samples.json').write_text(json.dumps(state, indent=2) + '\n')

def run(label, mode, name, sample=False, probe=False):
    global expected
    memo = '0' if mode == 'cold' else str(out / ('memo-base' if label == 'base' else 'memo-cand'))
    env = {key: value for key, value in os.environ.items() if not key.startswith('FERN_')}
    env.update(FERN_PROFILE='0', FERN_LEAKCHECK='0', FERN_RC_CERTIFY='1', FERN_RC_CERTIFY_MEMO=memo)
    if probe:
        env['FERN_IR_VERIFY'] = '1'
    args = [str(work / (label + '-native')), '-target', 'x86-64-linux', '-o', str(out / 'checker'), str(base / 'compiler/checker.fern'), str(base / 'internal/stdlib')]
    cmd = args
    record = {'name': name, 'compiler': label, 'mode': mode, 'args': cmd, 'cwd': str(repo), 'fern_env': {key: value for key, value in env.items() if key.startswith('FERN_')}, 'started': datetime.datetime.now(datetime.timezone.utc).isoformat()}
    state['samples' if sample else 'controls'].append(record)
    save()
    start = time.perf_counter_ns()
    with (out / (name + '.stdout')).open('wb') as stdout, (out / (name + '.log')).open('wb') as stderr:
        child = subprocess.Popen(cmd, cwd=repo, env=env, stdout=stdout, stderr=stderr)
        _, status, usage = os.wait4(child.pid, 0)
        child.returncode = os.waitstatus_to_exitcode(status)
    record.update(exit_code=child.returncode, elapsed_ns=time.perf_counter_ns()-start)
    save()
    assert child.returncode == 0, record
    digest = sha(out / 'checker')
    if expected is None:
        expected = digest
    assert digest == expected, 'Native output differs'
    record['native_sha256'] = digest
    if sample:
        record.update(wall_seconds=record['elapsed_ns']/1e9, user_seconds=usage.ru_utime, system_seconds=usage.ru_stime, peak_rss_kib=usage.ru_maxrss)
        (out / (name + '.time')).write_text(json.dumps({key: record[key] for key in ['wall_seconds', 'user_seconds', 'system_seconds', 'peak_rss_kib']}, indent=2) + '\n')
    if probe:
        rows = re.findall(r'FERN_RC_CERTIFY: memo hits=(\d+) misses=(\d+)', (out / (name + '.log')).read_text())
        assert rows and sum(int(h) for h,m in rows)>0 and all(int(m)==0 for h,m in rows), rows
        record.update(cache_hits=sum(int(h) for h,m in rows), cache_misses=0)
    save()
    print(name + ': exit0, output matches', flush=True)

save()
for label in ['base', 'candidate']:
    run(label, 'warm', label + '-prime')
    run(label, 'warm', label + '-probe', probe=True)
for mode in ['cold', 'warm']:
    for pair in range(5):
        labels = ['base', 'candidate'] if (pair + (mode == 'warm')) % 2 == 0 else ['candidate', 'base']
        for label in labels:
            run(label, mode, mode + '-' + str(pair+1) + '-' + label, sample=True)
for root in [repo, base]:
    assert git(root, 'status', '--porcelain') == ''
assert git(repo, 'rev-parse', 'HEAD') == state['candidate_source']
assert git(base, 'rev-parse', 'HEAD') == state['baseline_source']
state['complete'] = True
save()
print('All timing and peak-RSS samples complete.', flush=True)
