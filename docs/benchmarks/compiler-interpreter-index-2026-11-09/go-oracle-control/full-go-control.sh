source /tmp/lang-performance-toolchain-20261109-1950.env
export STAGE0=/workspace/lang-interpreter-timeout-20261109/build/bootstrap/stage0/stage0-20261109-a19e872/fern-selfhost-x86-64-linux
export FERN_SELFHOST_BUILD_CACHE=/workspace/lang-interpreter-index-20261109/build/interpreter-index-driver-cache-20261109
cd /workspace/lang-interpreter-index-20261109 || exit 2
started=$(date -u +%Y-%m-%dT%H:%M:%SZ)
go test ./internal/testing/e2ecompiler -run '^TestSelfHostStdTestInterp$/^opus_delimited$' -count=1 -v -timeout=33m > build/interpreter-index-trial-20261109/full-go-control.log 2>&1
result=$?
python3 - "$result" "$started" <<'PY'
from pathlib import Path
import sys,json,datetime
Path('build/interpreter-index-trial-20261109/full-go-control-result.json').write_text(json.dumps({'exit_code':int(sys.argv[1]),'started':sys.argv[2],'finished':datetime.datetime.now(datetime.timezone.utc).isoformat(),'command':"go test ./internal/testing/e2ecompiler -run '^TestSelfHostStdTestInterp$/^opus_delimited$' -count=1 -v -timeout=33m",'scope':'Exact failing Opus subtest through the Go oracle/native self-host comparison. Other subtests are filtered, not reported as passed. Existing33minutepackagebudget, no exclusion changes.'},indent=2)+'\n')
PY
exit "$result"
