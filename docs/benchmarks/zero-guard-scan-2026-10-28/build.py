from pathlib import Path
import os, subprocess, hashlib, json, time
p=Path(__file__).parent
producer=p.parent/'record-pair-candidate/stage3'
sha=lambda x:hashlib.sha256(x.read_bytes()).hexdigest()
assert sha(producer)=='64f876ff80e4db91d1eb614605a7fe567bbad13ecb061dce204defb5c0b975a0'
records=[]
for kind, flags in [('stage2',[]),('stage2-g',['-g'])]:
    cmd=[str(producer),'-O']+flags+['-target','x86-64-linux','-o',str(p/kind),str(p/'compiler/fern.fern'),str(p.parent/'fixed-source/internal/stdlib')]
    env=dict(os.environ, FERN_RC_CERTIFY_MEMO=str(p/'memo-build'))
    start=time.perf_counter()
    with (p/(kind+'-build.log')).open('wb') as f:r=subprocess.run(cmd,env=env,stdout=f,stderr=subprocess.STDOUT)
    rec={'command':cmd,'producer_sha256':sha(producer),'exit':r.returncode,'elapsed_seconds':time.perf_counter()-start}
    if r.returncode==0:rec['output_sha256']=sha(p/kind)
    records.append(rec);(p/'builds.json').write_text(json.dumps(records,indent=2)+'\n');print(rec,flush=True)
    assert r.returncode==0
with (p/'stage2-g.nm').open('wb') as f:subprocess.run(['nm','-n',str(p/'stage2-g')],stdout=f,check=True)
subprocess.run(['python3',str(p/'profile.py')],check=True)
