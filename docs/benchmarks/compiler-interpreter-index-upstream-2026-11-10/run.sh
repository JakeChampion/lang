#!/bin/bash
set -eu
source /tmp/lang-performance-toolchain-20261109-1950.env
export STAGE0=/workspace/lang-interpreter-timeout-20261109/build/bootstrap/stage0/stage0-20261109-a19e872/fern-selfhost-x86-64-linux
cd /workspace/lang-interpreter-dispatch-20261109/build/interpreter-main-qualification-20261110-0230
python3 prepare.py > prepare.log 2>&1
python3 measure.py subsets > subsets.log 2>&1
python3 compile-controls.py > compilation.log 2>&1
python3 measure.py full > full.log 2>&1
python3 - <<'PY'
import json,datetime
from pathlib import Path
Path('collector-result.json').write_text(json.dumps({'exit_code':0,'finished':datetime.datetime.now(datetime.timezone.utc).isoformat()},indent=2)+'\n')
PY
