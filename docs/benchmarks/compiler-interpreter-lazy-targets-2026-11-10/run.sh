#!/bin/bash
set -eu
source /tmp/lang-performance-toolchain-20261109-1950.env
export STAGE0=/workspace/lang-interpreter-timeout-20261109/build/bootstrap/stage0/stage0-20261109-a19e872/fern-selfhost-x86-64-linux
cd /workspace/lang-interpreter-dispatch-20261109/build/interpreter-lazy-qualification-20261110-0345
python3 prepare.py > prepare.log 2>&1
python3 measure.py subsets > subsets.log 2>&1
python3 - <<'PY'
from pathlib import Path
import json,datetime
Path('initial-result.json').write_text(json.dumps({'exit_code':0,'finished':datetime.datetime.now(datetime.timezone.utc).isoformat()},indent=2)+'\n')
PY
