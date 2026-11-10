#!/bin/bash
set -eu
source /tmp/lang-performance-toolchain-20261109-1950.env
export STAGE0=/workspace/lang-interpreter-timeout-20261109/build/bootstrap/stage0/stage0-20261109-a19e872/fern-selfhost-x86-64-linux
export FERN_SELFHOST_BUILD_CACHE=/workspace/lang-interpreter-dispatch-20261109/build/interpreter-lazy-driver-cache-20261110-0345
cd /workspace/lang-interpreter-dispatch-20261109/build/interpreter-lazy-qualification-20261110-0345
python3 compile-controls.py > compilation.log 2>&1
python3 validation/validate.py > validation/collector.log 2>&1 &
validation_pid=$!
python3 instructions.py > instructions-collector.log 2>&1 &
instructions_pid=$!
wait "$validation_pid"
wait "$instructions_pid"
python3 measure.py full > full.log 2>&1
python3 - <<'PY'
from pathlib import Path
import json,datetime
Path('collector-result.json').write_text(json.dumps({'exit_code':0,'finished':datetime.datetime.now(datetime.timezone.utc).isoformat()},indent=2)+'\n')
PY
