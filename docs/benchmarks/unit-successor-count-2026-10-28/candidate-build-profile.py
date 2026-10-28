from pathlib import Path
import os, subprocess, hashlib, json, time, re, shutil

p = Path(__file__).parent
root = p.parent
base = root/'current-c63-baseline'
producer = base/'stage1'
sha = lambda f: hashlib.sha256(f.read_bytes()).hexdigest()
assert sha(producer) == 'ce1074b697485ad511740e5c499ff771ceccf270c5afaf521fcc85e74e847b05'
assert sha(base/'stage2') == '4948b305ce2012f2aa47704843e883566a14ac79274aac3b50becb0d9f966251'
stdlib = root/'fixed-source/internal/stdlib'
subject = root/'fixed-source/compiler/checker.fern'
env = dict(os.environ,FERN_RC_CERTIFY_MEMO=str(p/'memo-build'),VALGRIND_LIB=str(root/'tools/usr/libexec/valgrind'))
records = []
for name,flags in [('stage2',[]),('stage2-g',['-g'])]:
    cmd=[str(producer),'-O']+flags+['-target','x86-64-linux','-o',str(p/name),str(p/'compiler/fern.fern'),str(stdlib)]
    start=time.perf_counter()
    with (p/(name+'-build.log')).open('wb') as f:r=subprocess.run(cmd,env=env,stdout=f,stderr=subprocess.STDOUT)
    rec=dict(command=cmd,exit=r.returncode,elapsed_seconds=time.perf_counter()-start,producer_sha256=sha(producer))
    if r.returncode==0:rec['output_sha256']=sha(p/name)
    records.append(rec);(p/'builds.json').write_text(json.dumps(records,indent=2)+'\n');print(rec,flush=True)
    assert r.returncode==0
with (p/'stage2-g.nm').open('wb') as f:subprocess.run(['nm','-n',str(p/'stage2-g')],stdout=f,check=True)
env['FERN_RC_CERTIFY_MEMO']=str(p/'memo-profile')
identities=[]
for target in ['x86-64-linux','arm64-linux','wasm32-wasi']:
    output=p/('checker-'+target+'.s');cmd=[str(p/'stage2'),'-O','-target',target,'-emit','asm','-o',str(output),str(subject),str(stdlib)]
    with (p/('checker-'+target+'.log')).open('wb') as f:r=subprocess.run(cmd,env=env,stdout=f,stderr=subprocess.STDOUT)
    identity=r.returncode==0 and output.read_bytes()==(base/output.name).read_bytes()
    identities.append(dict(command=cmd,exit=r.returncode,byte_identical=identity,sha256=sha(output) if r.returncode==0 else None))
    (p/'output-identities.json').write_text(json.dumps(identities,indent=2)+'\n');print(identities[-1],flush=True)
    assert identity,target
cmd=[str(root/'tools/usr/bin/time'),'-v',str(root/'tools/usr/bin/valgrind'),'--tool=callgrind','--callgrind-out-file='+str(p/'stage2.callgrind'),str(p/'stage2'),'-O','-target','x86-64-linux','-emit','asm','-o','/dev/null',str(subject),str(stdlib)]
start=time.perf_counter()
with (p/'stage2-profile.log').open('wb') as f:r=subprocess.run(cmd,env=env,stdout=f,stderr=subprocess.STDOUT)
rec=dict(command=cmd,exit=r.returncode,valid_full_run=r.returncode==0,elapsed_seconds_under_callgrind=time.perf_counter()-start,baseline_sha256=sha(base/'stage2'),candidate_sha256=sha(p/'stage2'),common_producer_sha256=sha(producer),subject_sha256=sha(subject),baseline_instructions=25020841026,memo_warmed=True)
if r.returncode==0:
    rec['instructions']=int(re.search(r'Collected\s*:\s*(\d+)',(p/'stage2-profile.log').read_text())[1])
    rec['instruction_change_percent']=(rec['instructions']/rec['baseline_instructions']-1)*100
(p/'stage2-profile.json').write_text(json.dumps(rec,indent=2)+'\n');print(rec,flush=True)
assert r.returncode==0
