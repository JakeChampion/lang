cd /workspace/lang-interpreter-dispatch-20261109 || exit 2
started=$(date -u +%Y-%m-%dT%H:%M:%SZ)
FERN_PROFILE=0 FERN_LEAKCHECK=0 FERN_RC_CERTIFY=1 FERN_RC_CERTIFY_MEMO=0 /workspace/lang-interpreter-timeout-20261109/build/bootstrap/stage0/stage0-20261109-a19e872/fern-selfhost-x86-64-linux -check compiler/fern.fern internal/stdlib > build/interpreter-main-integration-20261110-0220/source-check.log 2>&1
result=$?
python3 - "$result" "$started" <<'PY'
from pathlib import Path
import json,sys,datetime
Path('build/interpreter-main-integration-20261110-0220/source-check-result.json').write_text(json.dumps({'exit_code':int(sys.argv[1]),'started':sys.argv[2],'finished':datetime.datetime.now(datetime.timezone.utc).isoformat(),'note':'Pinned a19 producer checks the main f990 merge source, before the merge commit. No performance sample.'},indent=2)+'\n')
PY
exit "$result"
