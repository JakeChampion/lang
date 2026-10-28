from pathlib import Path
import datetime
import json
import os
import subprocess
import time

p = Path(__file__).parent
repo = Path('/workspace/lang-perf-4412')
state_file = p / 'printer-shards-state.json'
records = []
assert not state_file.exists()

def save():
    temporary = state_file.with_suffix('.json.tmp')
    temporary.write_text(json.dumps(dict(records=records, complete=len(records) == 4 and all(x.get('exit') == 0 and x.get('passes', 0) > 0 and not x.get('failures') and not x.get('skips') for x in records)), indent=2) + '\n')
    temporary.replace(state_file)

for shard in range(4):
    name = 'printer-shard-' + str(shard)
    env = dict(os.environ, FERN_PRINTER_CORPUS_SHARD=str(shard) + '/4')
    env.pop('STAGE0', None)
    command = ['go', 'test', './internal/syntax/printer', '-count=1', '-timeout', '20m', '-json']
    record = dict(shard=str(shard) + '/4', command=command, started_at=datetime.datetime.now(datetime.timezone.utc).isoformat())
    records.append(record)
    started = time.perf_counter()
    with (p / (name + '.jsonl')).open('wb') as output:
        process = subprocess.Popen(command, cwd=repo, env=env, stdout=output, stderr=subprocess.STDOUT)
        record['process_id'] = process.pid
        save()
        code = process.wait()
    events = []
    for line in (p / (name + '.jsonl')).read_text().splitlines():
        try:
            events.append(json.loads(line))
        except json.JSONDecodeError:
            pass
    record.update(exit=code, seconds=time.perf_counter() - started, passes=sum(x.get('Action') == 'pass' and bool(x.get('Test')) for x in events), failures=[x.get('Test', x.get('Package')) for x in events if x.get('Action') == 'fail'], skips=[x.get('Test', x.get('Package')) for x in events if x.get('Action') == 'skip'])
    save()
    print(json.dumps(record), flush=True)
    assert code == 0 and not record['failures'] and not record['skips']
