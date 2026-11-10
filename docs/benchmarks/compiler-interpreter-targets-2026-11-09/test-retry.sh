#!/bin/bash
source /tmp/lang-performance-toolchain-20261109-1950.env
export STAGE0=/workspace/lang-interpreter-timeout-20261109/build/bootstrap/stage0/stage0-20261109-a19e872/fern-selfhost-x86-64-linux
export FERN_SELFHOST_BUILD_CACHE=/tmp/selfhost-bincache-p10611-1882023402
cd /workspace/lang-interpreter-dispatch-20261109 || exit 2
started=$(date -u +%Y-%m-%dT%H:%M:%SZ)
go test ./internal/testing/e2ecompiler -run '^TestSelfHostInterp(Driver(X86_64|Arm64)|CallableValues|StructOps|AssocConsts)$' -count=1 -v -timeout=20m > build/interpreter-dispatch-20261109/tests-retry.log 2>&1
result=$?
python3 - "$result" "$started" <<'PY'
from pathlib import Path
import sys,json,datetime
Path('build/interpreter-dispatch-20261109/tests-retry-result.json').write_text(json.dumps({'exit_code':int(sys.argv[1]),'started':sys.argv[2],'finished':datetime.datetime.now(datetime.timezone.utc).isoformat(),'command':"go test ./internal/testing/e2ecompiler -run '^TestSelfHostInterp(Driver(X86_64|Arm64)|CallableValues|StructOps|AssocConsts)$' -count=1 -v -timeout=20m",'note':'Detached retry after parent-session loss. Reuses the existing completed driver cache with the same source and producer. No test cache.'},indent=2)+'\n')
PY
exit "$result"
