from pathlib import Path
import datetime
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
    ('primary', ['go', 'test', './internal/testing/e2ecompiler', '-run', '^(TestSelfHostNetAddr|TestSelfHostDnsPair|TestSelfHostDnsNat64|TestSelfHostForwardedProducerReclaim(IRX86_64|WasmIR))$', '-count=1', '-timeout', '20m', '-json']),
    ('source', ['go', 'test', './compiler', './internal/testing/sourcelint']),
    ('ratchets', ['go', 'test', './internal/testing/e2ecompiler', '-run', '^(TestSelfHostFeatureCensus|TestSelfHostRepoComplexityRatchet)$', '-count=1', '-timeout', '20m', '-json']),
]
for name, command in commands:
    started = datetime.datetime.now(datetime.timezone.utc).isoformat()
    start = time.perf_counter()
    log = p / (name + '.jsonl' if '-json' in command else name + '.log')
    with log.open('wb') as output:
        result = subprocess.run(command, cwd=repo, env=env, stdout=output, stderr=subprocess.STDOUT)
    record = dict(command=command, exit=result.returncode, seconds=time.perf_counter()-start, started_at=started, source_sha256=hashlib.sha256((repo/'compiler/asm_ir.fern').read_bytes()).hexdigest())
    if '-json' in command:
        events = []
        for line in log.read_text().splitlines():
            try:
                events.append(json.loads(line))
            except json.JSONDecodeError:
                pass
        record['passes'] = sum(x.get('Action') == 'pass' and bool(x.get('Test')) for x in events)
        record['failures'] = [x.get('Test', x.get('Package')) for x in events if x.get('Action') == 'fail']
        record['skips'] = [x.get('Test', x.get('Package')) for x in events if x.get('Action') == 'skip']
    (p/(name+'.json')).write_text(json.dumps(record, indent=2)+'\n')
    print(json.dumps(record), flush=True)
    assert result.returncode == 0, name
    assert not record.get('failures') and not record.get('skips'), name
