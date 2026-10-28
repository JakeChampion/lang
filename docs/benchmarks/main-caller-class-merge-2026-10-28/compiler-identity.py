from pathlib import Path
import hashlib
import json
import os
import subprocess
import time

p=Path(__file__).parent
root=p.parent
repo=Path('/workspace/lang-perf-4412')
producer=root/'successor-count-candidate/stage2-current'
baseline=root/'phi-predecessor-candidate/stage2'
sha=lambda f: hashlib.sha256(f.read_bytes()).hexdigest()
assert sha(producer)=='1e0ad6be807a01bbf50a05535d46808d611816016ce388e687d44589c5dc7e52'
assert sha(baseline)=='fb78dd106a460a9732db18e3d45b3f837aec9b132f3fe49488681403df6eea80'
assert json.loads((p/'primary.json').read_text())['exit']==0
command=[str(producer),'-O','-target','x86-64-linux','-o',str(p/'stage2-current'),str(repo/'compiler/fern.fern'),str(root/'fixed-source/internal/stdlib')]
start=time.perf_counter()
env=dict(os.environ,FERN_RC_CERTIFY_MEMO=str(p/'memo-identity'))
with (p/'compiler-identity.log').open('wb') as out:
    result=subprocess.run(command,cwd=repo,env=env,stdout=out,stderr=subprocess.STDOUT)
record=dict(command=command,exit=result.returncode,seconds=time.perf_counter()-start,producer_sha256=sha(producer),baseline_sha256=sha(baseline),source_head=subprocess.check_output(['git','rev-parse','HEAD'],cwd=repo,text=True).strip())
if result.returncode==0:
    record['current_sha256']=sha(p/'stage2-current')
    record['byte_identical']=(p/'stage2-current').read_bytes()==baseline.read_bytes()
(p/'compiler-identity.json').write_text(json.dumps(record,indent=2)+'\n')
print(json.dumps(record),flush=True)
assert result.returncode==0 and record['byte_identical']
