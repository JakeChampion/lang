source /tmp/lang-performance-toolchain-20261109-1950.env
export STAGE0=/workspace/lang-interpreter-timeout-20261109/build/bootstrap/stage0/stage0-20261109-a19e872/fern-selfhost-x86-64-linux
export FERN_SELFHOST_BUILD_CACHE=/workspace/lang-interpreter-dispatch-20261109/build/interpreter-main-driver-cache-20261109-2320
cd /workspace/lang-interpreter-dispatch-20261109 || exit 2
started=$(date -u +%Y-%m-%dT%H:%M:%SZ)
go test ./internal/testing/e2ecompiler -run '^TestSelfHostStdTestInterp$/^opus_delimited$' -count=1 -v -timeout=33m > build/interpreter-main-qualification-20261109-2320/full-go.log 2>&1
result=$?
python3 - "$result" "$started" <<'PY'
from pathlib import Path
import sys,json,datetime
Path('build/interpreter-main-qualification-20261109-2320/full-go-result.json').write_text(json.dumps({'exit_code':int(sys.argv[1]),'started':sys.argv[2],'finished':datetime.datetime.now(datetime.timezone.utc).isoformat(),'command':"go test ./internal/testing/e2ecompiler -run '^TestSelfHostStdTestInterp$/^opus_delimited$' -count=1 -v -timeout=33m",'scope':'Same original oracle/native Opus subtest after merging currentmain4a. Other subtests filtered. Budget/exclusions/corpus unchanged.'},indent=2)+'\n')
PY
exit "$result"
