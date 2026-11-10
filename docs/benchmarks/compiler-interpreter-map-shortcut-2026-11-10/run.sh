#!/bin/bash
set -eu
source /tmp/lang-performance-toolchain-20261109-1950.env
export STAGE0=/workspace/lang-interpreter-timeout-20261109/build/bootstrap/stage0/stage0-20261109-a19e872/fern-selfhost-x86-64-linux
cd /workspace/lang-interpreter-dispatch-20261109/build/interpreter-map-qualification-20261110-0330
python3 - <<'CHECK'
from pathlib import Path
import json,time
validation=Path('../interpreter-map-integration-20261110-0330/validation.json')
for _ in range(900):
 p=json.loads(Path('prepare.json').read_text());v=json.loads(validation.read_text())
 assert all(r.get('exit_code',0)==0 for r in p['runs']+v['runs'])
 if p.get('finished') and v.get('finished'):break
 time.sleep(2)
else:raise RuntimeError('Preparation or validation did not finish')
assert not any(r.get('skips',0) for r in v['runs'])
CHECK
python3 measure.py subsets > subsets.log 2>&1
python3 compile-controls.py > compilation.log 2>&1
python3 measure.py full > full.log 2>&1
python3 - <<'CHECK'
from pathlib import Path
import json,datetime
Path('collector-result.json').write_text(json.dumps({'exit_code':0,'finished':datetime.datetime.now(datetime.timezone.utc).isoformat()},indent=2)+'\n')
CHECK
