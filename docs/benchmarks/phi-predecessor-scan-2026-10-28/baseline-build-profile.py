from pathlib import Path
import os, subprocess, hashlib, json, time, re, shutil

p = Path(__file__).parent
root = p.parent
producer = root/'successor-count-candidate/stage2-current'
sha = lambda f: hashlib.sha256(f.read_bytes()).hexdigest()
assert sha(producer) == '1e0ad6be807a01bbf50a05535d46808d611816016ce388e687d44589c5dc7e52'
stdlib = root/'fixed-source/internal/stdlib'
subject = root/'fixed-source/compiler/checker.fern'
assert sha(subject) == 'a0b69a0f97f3b45bf06eca8160200a936b56b06a279bb538795a387a5d76e172'
env = dict(os.environ,FERN_RC_CERTIFY_MEMO=str(p/'memo-build'),VALGRIND_LIB=str(root/'tools/usr/libexec/valgrind'))
records=[]
for name,flags in [('stage2',[]),('stage2-g',['-g'])]:
    cmd=[str(producer),'-O']+flags+['-target','x86-64-linux','-o',str(p/name),str(p/'compiler/fern.fern'),str(stdlib)]
    start=time.perf_counter()
    with (p/(name+'-build.log')).open('wb') as f:r=subprocess.run(cmd,env=env,stdout=f,stderr=subprocess.STDOUT)
    rec=dict(command=cmd,exit=r.returncode,elapsed_seconds=time.perf_counter()-start,producer_sha256=sha(producer))
    if r.returncode==0:rec['output_sha256']=sha(p/name)
    records.append(rec);(p/'builds.json').write_text(json.dumps(records,indent=2)+'\n');print(json.dumps(rec),flush=True)
    assert r.returncode==0
with (p/'stage2-g.nm').open('wb') as f:subprocess.run(['nm','-n',str(p/'stage2-g')],stdout=f,check=True)
shutil.copy2(root/'successor-count-candidate/resolve-profile.py',p/'resolve-profile.py')
env['FERN_RC_CERTIFY_MEMO']=str(p/'memo-profile')
outputs=[]
for target in ['x86-64-linux','arm64-linux','wasm32-wasi']:
    output=p/('checker-'+target+'.s');cmd=[str(p/'stage2'),'-O','-target',target,'-emit','asm','-o',str(output),str(subject),str(stdlib)]
    with (p/('checker-'+target+'.log')).open('wb') as f:r=subprocess.run(cmd,env=env,stdout=f,stderr=subprocess.STDOUT)
    rec=dict(command=cmd,exit=r.returncode)
    if r.returncode==0:rec.update(sha256=sha(output),bytes=output.stat().st_size)
    outputs.append(rec);(p/'outputs.json').write_text(json.dumps(outputs,indent=2)+'\n');assert r.returncode==0
cmd=[str(root/'tools/usr/bin/time'),'-v',str(root/'tools/usr/bin/valgrind'),'--tool=callgrind','--callgrind-out-file='+str(p/'stage2.callgrind'),str(p/'stage2'),'-O','-target','x86-64-linux','-emit','asm','-o','/dev/null',str(subject),str(stdlib)]
start=time.perf_counter()
with (p/'stage2-profile.log').open('wb') as f:r=subprocess.run(cmd,env=env,stdout=f,stderr=subprocess.STDOUT)
rec=dict(command=cmd,exit=r.returncode,valid_full_run=r.returncode==0,elapsed_seconds_under_callgrind=time.perf_counter()-start,binary_sha256=sha(p/'stage2'),common_producer_sha256=sha(producer),subject_sha256=sha(subject),subject_output_sha256=sha(p/'checker-x86-64-linux.s'),memo_warmed=True,comparison='baseline only, no optimization comparison')
if r.returncode==0:rec['instructions']=int(re.search(r'Collected\s*:\s*(\d+)',(p/'stage2-profile.log').read_text())[1])
(p/'stage2-profile.json').write_text(json.dumps(rec,indent=2)+'\n');print(json.dumps(rec),flush=True);assert r.returncode==0
subprocess.run(['python3',str(p/'resolve-profile.py'),'stage2'],check=True)
