from pathlib import Path
import hashlib
import json
import os
import subprocess
import time

p = Path(__file__).parent
repo = Path('/workspace/lang-perf-4412')
env = dict(os.environ)
env.pop('STAGE0', None)
commands = [
    ('primary', ['go', 'test', './internal/testing/e2ecompiler', '-run', '^(TestImportFreeModulesDoNotDrift|TestSelfHostLargePushWideMachine|TestSelfHostLargePushWideMachineWorker)$', '-count=1', '-timeout', '20m', '-json']),
    ('source', ['go', 'test', './compiler', './internal/testing/sourcelint']),
    ('ratchets', ['go', 'test', './internal/testing/e2ecompiler', '-run', '^(TestSelfHostFeatureCensus|TestSelfHostRepoComplexityRatchet)$', '-count=1', '-timeout', '10m', '-json']),
    ('reference', ['python3', 'scripts/gen-large-push-wide-machine-reference.py', '--check']),
    ('protocol', ['python3', '-m', 'unittest', 'discover', '-s', 'scripts', '-p', 'test_large_push_wide_machine.py']),
]
for name, command in commands:
    log = p/(name+'.jsonl' if '-json' in command else name+'.log')
    start = time.perf_counter()
    with log.open('wb') as out:
        result = subprocess.run(command, cwd=repo, env=env, stdout=out, stderr=subprocess.STDOUT)
    record = dict(command=command, exit=result.returncode, seconds=time.perf_counter()-start, source_head=subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=repo, text=True).strip())
    if '-json' in command:
        events=[]
        for line in log.read_text().splitlines():
            try:
                events.append(json.loads(line))
            except ValueError:
                pass
        record.update(passes=sum(x.get('Action')=='pass' and bool(x.get('Test')) for x in events),failures=[x.get('Test',x.get('Package')) for x in events if x.get('Action')=='fail'],skips=[x.get('Test',x.get('Package')) for x in events if x.get('Action')=='skip'])
    (p/(name+'.json')).write_text(json.dumps(record, indent=2)+'\n')
    print(json.dumps(record), flush=True)
    assert result.returncode==0, name
    assert not record.get('failures') and not record.get('skips'), name

files=subprocess.check_output(['git','ls-files','*.fern','*.fern.md'],cwd=repo,text=True).splitlines()
files=[f for f in files if not f.startswith('conformance/') and '/testdata/' not in f]
start=time.perf_counter()
with (p/'formatter.log').open('wb') as out:
    result=subprocess.run([str(repo/'bin/fern'),'-fmt','-d',*files],cwd=repo,env=env,stdout=out,stderr=subprocess.STDOUT)
record=dict(exit=result.returncode,seconds=time.perf_counter()-start,inputs=len(files),formatter_sha256=hashlib.sha256((repo/'bin/fern').read_bytes()).hexdigest())
(p/'formatter.json').write_text(json.dumps(record,indent=2)+'\n')
print(json.dumps(record),flush=True)
assert result.returncode==0
