source /tmp/lang-performance-toolchain-20261109-1950.env
export STAGE0=/workspace/lang-interpreter-timeout-20261109/build/bootstrap/stage0/stage0-20261109-a19e872/fern-selfhost-x86-64-linux
export FERN_SELFHOST_BUILD_CACHE=/workspace/lang-interpreter-dispatch-20261109/build/interpreter-main-driver-cache-20261109-2320
export FERN_STDTEST_SHARD=0/4
cd /workspace/lang-interpreter-dispatch-20261109 || exit 2
while ! test -f build/interpreter-shard3-20261110/result.json; do sleep 2; done
python3 - <<'CHECK'
import subprocess
assert subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip() == '7bd045eb04c8ea0b5980f0526e778ae107571d05'
assert subprocess.check_output(['git','status','--porcelain'],text=True) == ''
CHECK
if test $? -ne 0; then exit 2; fi
started=$(date -u +%Y-%m-%dT%H:%M:%SZ)
go test -json ./internal/testing/e2ecompiler -run '^TestSelfHostStdTestInterp$' -count=1 -timeout=33m > build/interpreter-shard0-20261110/tests.jsonl 2> build/interpreter-shard0-20261110/tests.stderr
result=$?
python3 - "$result" "$started" <<'PY'
from pathlib import Path
import sys,json,datetime,subprocess,collections,hashlib
p=Path('build/interpreter-shard0-20261110');events=[]
for line in (p/'tests.jsonl').read_text().splitlines():
 try:events.append(json.loads(line))
 except json.JSONDecodeError:pass
rows=[e for e in events if e.get('Action') in ('pass','fail','skip')]
d={'command':"FERN_STDTEST_SHARD=0/4 go test -json ./internal/testing/e2ecompiler -run '^TestSelfHostStdTestInterp$' -count=1 -timeout=33m",'source':subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip(),'measured_code_source':'6f6bddfc2af38b6c7e85d23d9338a10c8fb9c307','exit_code':int(sys.argv[1]),'started':sys.argv[2],'finished':datetime.datetime.now(datetime.timezone.utc).isoformat(),'terminal_counts':dict(collections.Counter(e['Action'] for e in rows)),'terminal_events':rows,'note':'Full unchanged interpreter shard0/4 (current Opus assignment), same selection and 33-minute test-package budget as CI. Original slow-test exclusions stay unchanged. This local control does not replace complete exact-head CI.'}
(p/'result.json').write_text(json.dumps(d,indent=2)+'\n')
manifest={f.name:hashlib.sha256(f.read_bytes()).hexdigest() for f in p.iterdir() if f.is_file() and f.name not in ('data-sha256.json','collector.pid','parent.log')}
(p/'data-sha256.json').write_text(json.dumps(manifest,indent=2)+'\n')
PY
exit "$result"
