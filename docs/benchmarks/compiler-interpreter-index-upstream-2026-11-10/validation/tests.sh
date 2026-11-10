source /tmp/lang-performance-toolchain-20261109-1950.env
export STAGE0=/workspace/lang-interpreter-timeout-20261109/build/bootstrap/stage0/stage0-20261109-a19e872/fern-selfhost-x86-64-linux
export FERN_SELFHOST_BUILD_CACHE=/workspace/lang-interpreter-dispatch-20261109/build/interpreter-main-driver-cache-20261110-0220
cd /workspace/lang-interpreter-dispatch-20261109 || exit 2
started=$(date -u +%Y-%m-%dT%H:%M:%SZ)
go test ./internal/testing/e2ecompiler -run '^TestSelfHostInterp(X86_64|Arm64|Driver(X86_64|Arm64)|CallableValues|StructOps|AssocConsts|ScopeFrames|DynProvider|MapKeysCompareThroughEq)$' -count=1 -v -timeout=25m > build/interpreter-main-integration-20261110-0220/tests.log 2>&1
result=$?
python3 - "$result" "$started" <<'PY'
from pathlib import Path
import json,sys,datetime
Path('build/interpreter-main-integration-20261110-0220/tests-result.json').write_text(json.dumps({'exit_code':int(sys.argv[1]),'started':sys.argv[2],'finished':datetime.datetime.now(datetime.timezone.utc).isoformat(),'command':"go test ./internal/testing/e2ecompiler -run '^TestSelfHostInterp(X86_64|Arm64|Driver(X86_64|Arm64)|CallableValues|StructOps|AssocConsts|ScopeFrames|DynProvider|MapKeysCompareThroughEq)$' -count=1 -v -timeout=25m",'note':'Fresh main f990 integration before the merge commit. Includes both upstream extracted interpreter-check drivers, the eight earlier suites and same-size REPL replacement on three targets. CI33minutepackagebudget unchanged.'},indent=2)+'\n')
PY
exit "$result"
