source /tmp/lang-performance-toolchain-20261109-1950.env
export STAGE0=/workspace/lang-interpreter-timeout-20261109/build/bootstrap/stage0/stage0-20261109-a19e872/fern-selfhost-x86-64-linux
cd /workspace/lang-interpreter-dispatch-20261109 || exit 2
started=$(date -u +%Y-%m-%dT%H:%M:%SZ)
make check-sources > build/interpreter-source-gate-20261110/check-sources.log 2>&1
result=$?
python3 - "$result" "$started" <<'PY'
from pathlib import Path
import sys,json,datetime
Path('build/interpreter-source-gate-20261110/result.json').write_text(json.dumps({'command':'make check-sources','exit_code':int(sys.argv[1]),'started':sys.argv[2],'finished':datetime.datetime.now(datetime.timezone.utc).isoformat(),'note':'Full repository source gate, including runtime, fixture and driver checks. Exact published code matches measured6f6source. No gate changes.'},indent=2)+'\n')
PY
exit "$result"
