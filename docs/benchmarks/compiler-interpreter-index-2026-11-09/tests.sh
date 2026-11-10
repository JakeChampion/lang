source /tmp/lang-performance-toolchain-20261109-1950.env
export STAGE0=/workspace/lang-interpreter-timeout-20261109/build/bootstrap/stage0/stage0-20261109-a19e872/fern-selfhost-x86-64-linux
export FERN_SELFHOST_BUILD_CACHE=/workspace/lang-interpreter-index-20261109/build/interpreter-index-driver-cache-20261109
cd /workspace/lang-interpreter-index-20261109 || exit 2
started=$(date -u +%Y-%m-%dT%H:%M:%SZ)
go test ./internal/testing/e2ecompiler -run '^TestSelfHostInterp(Driver(X86_64|Arm64)|CallableValues|StructOps|AssocConsts|ScopeFrames|DynProvider|MapKeysCompareThroughEq)$' -count=1 -v -timeout=25m > build/interpreter-index-trial-20261109/tests.log 2>&1
result=$?
python3 - "$result" "$started" <<'PY'
from pathlib import Path
import json,sys,datetime
Path('build/interpreter-index-trial-20261109/tests-result.json').write_text(json.dumps({'exit_code':int(sys.argv[1]),'started':sys.argv[2],'finished':datetime.datetime.now(datetime.timezone.utc).isoformat(),'command':"go test ./internal/testing/e2ecompiler -run '^TestSelfHostInterp(Driver(X86_64|Arm64)|CallableValues|StructOps|AssocConsts|ScopeFrames|DynProvider|MapKeysCompareThroughEq)$' -count=1 -v -timeout=25m"},indent=2)+'\n')
PY
exit "$result"
