"""Compare only the subprocess core-memory filter on a native runner."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import resource
import subprocess
import time

from bench_native_test_workers import verify_events

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--binary',type=Path,required=True)
parser.add_argument('--output',type=Path,required=True)
parser.add_argument('--scale',choices=('pilot','full'),required=True)
args = parser.parse_args()
assert platform.system() == 'Linux' and platform.machine() == 'x86_64'
repo = Path(__file__).resolve().parents[1]
args.output.mkdir(parents=True,exist_ok=False)
pattern = '^TestCINativeStackProfilePilot$' if args.scale=='pilot' else '^TestX86_64Trmc(DeepStack|WidenedDeepStack)$'
names = subprocess.check_output([str(args.binary.resolve()),'-test.list',pattern],cwd=repo/'internal/e2e',text=True).splitlines()
wanted = {'TestCINativeStackProfilePilot'} if args.scale=='pilot' else {'TestX86_64TrmcDeepStack','TestX86_64TrmcWidenedDeepStack'}
assert len(names)==len(wanted) and set(names)==wanted
metadata = dict(revision=subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip(),
                binary_sha256=hashlib.sha256(args.binary.read_bytes()).hexdigest(),
                scale=args.scale,pattern=pattern,platform=platform.platform(),
                cpus=len(os.sched_getaffinity(0)),core_limit=resource.getrlimit(resource.RLIMIT_CORE),
                core_pattern=Path('/proc/sys/kernel/core_pattern').read_text().strip(),
                parent_core_filter=Path('/proc/self/coredump_filter').read_text().strip())
(args.output/'environment.json').write_text(json.dumps(metadata,indent=2)+'\n')
baseline = None
rows = []
experiment_start = time.monotonic()
for index,omit in enumerate((False,True,True,False)):
    path = args.output/f'trial-{index}-omit-{int(omit)}.jsonl'
    env = dict(os.environ,FERN_STACK_CORE_FILTER='omit' if omit else '')
    before = resource.getrusage(resource.RUSAGE_CHILDREN)
    start = time.monotonic()
    subprocess.run(['gotestsum','--format','standard-verbose','--jsonfile',str(path.resolve()),'--raw-command','--',
                    'go','tool','test2json','-t','-p','e2e','/usr/bin/env','--default-signal=INT',
                    str(args.binary.resolve()),'-test.v=test2json','-test.count=1','-test.run',pattern,'-test.timeout=15m'],
                   cwd=repo/'internal/e2e',env=env,check=True)
    wall = time.monotonic()-start
    after = resource.getrusage(resource.RUSAGE_CHILDREN)
    _,outcomes = verify_events(path,wanted)
    assert all(v=='pass' for v in outcomes.values())
    if baseline is None:
        baseline = outcomes
    assert outcomes == baseline
    logs = [e for l in path.read_text().splitlines() for e in [json.loads(l)] if 'stack-profile:' in e.get('Output','')]
    assert len(logs) == (1 if args.scale=='pilot' else 4)
    if args.scale=='pilot':
        expected_filter = '00000000' if omit else metadata['parent_core_filter']
        assert 'core-filter='+expected_filter in logs[0]['Output']
    else:
        for name in wanted:
            calls=[e['Output'] for e in logs if e['Test']==name]
            assert len(calls)==2 and 'status=exit status 0' in calls[0] and 'status=signal: segmentation fault' in calls[1]
            assert 'core-filter='+metadata['parent_core_filter'] in calls[0]
            expected_filter = '00000000' if omit else metadata['parent_core_filter']
            assert 'core-filter='+expected_filter in calls[1]
    assert Path('/proc/self/coredump_filter').read_text().strip()==metadata['parent_core_filter']
    assert Path('/proc/sys/kernel/core_pattern').read_text().strip()==metadata['core_pattern']
    row=dict(index=index,omit_memory=omit,wall_seconds=wall,
             child_cpu_seconds=after.ru_utime+after.ru_stime-before.ru_utime-before.ru_stime,
             outcomes=outcomes,programs=[e['Output'].strip() for e in logs])
    rows.append(row)
    (args.output/'summary.json').write_text(json.dumps(rows,indent=2)+'\n')
    print(json.dumps(row),flush=True)
if args.scale=='pilot':
    assert time.monotonic()-experiment_start<60
