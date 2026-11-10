from pathlib import Path
import json,subprocess,hashlib,os,re,time,datetime
q=Path(__file__).resolve().parent;out=q/'instructions-v2';out.mkdir(exist_ok=False);fixed=Path('/workspace/lang-compiler-main-20261110-1635');sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest();now=lambda:datetime.datetime.now(datetime.timezone.utc).isoformat()
state={'baseline_source':'515ddee8d0c06db2b94b4f3a86f3bf1c59cd2909','candidate_source':'3d51e1880abc5025731f23772f1a343fe9c7fcbf','fixed_input_source':'515ddee8d0c06db2b94b4f3a86f3bf1c59cd2909','started':now(),'runs':[],'note':'One normal guest-instruction pair per workload/cache mode after normal timings, allocation controls and all local correctness jobs finish. No instrumented time claim. Same retained equal-length compiler paths as timings. Child/cache/branch simulation off.'}
def save():(out/'results.json').write_text(json.dumps(state,indent=2)+'\n')
save()
try:
 deadline=time.monotonic()+10800
 while True:
  m=json.loads((q/'measurements-v3/results.json').read_text());assert m.get('exit_code')in[None,0]
  if m.get('finished'):assert m['exit_code']==0;break
  assert time.monotonic()<deadline;time.sleep(3)
 env={k:v for k,v in os.environ.items()if not k.startswith('FERN_')};env.update(FERN_PROFILE='0',FERN_LEAKCHECK='0',FERN_RC_CERTIFY='1',VALGRIND_LIB='/workspace/scratch/lang-valgrind-20261109/root/usr/libexec/valgrind');tool='/workspace/scratch/lang-valgrind-20261109/root/usr/bin/valgrind';state['tool_version']=subprocess.check_output([tool,'--version'],env=env,text=True).strip();state['producer_sha256']=m['producer_sha256'];state['binary_identities']=m['binary_identities'];state['expected_outputs']=m['outputs'];save()
 for w,source in [('opus','tests/stdlib/opus_delimited_test.fern'),('checker','compiler/checker.fern')]:
  for mode in ['cold','warm']:
   order=['candidate','baseline']if mode=='cold'else['baseline','candidate']
   for name in order:
    label=w+'-'+mode+'-'+name;binary=Path(m['binary_identities'][name]['path']);assert sha(binary)==m['binary_identities'][name]['sha256']
    env['FERN_RC_CERTIFY_MEMO']='0'if mode=='cold'else str(q/'measurements-v3'/(w+'-memo-'+name))
    product=out/(w+'-reference');args=[tool,'--tool=callgrind','--cache-sim=no','--branch-sim=no','--trace-children=no','--callgrind-out-file='+str(out/(label+'.callgrind')),'--log-file='+str(out/(label+'.valgrind.log')),str(binary),'-target','x86-64-linux','-o',str(product),str(fixed/source),str(fixed/'internal/stdlib')]
    row={'label':label,'workload':w,'mode':mode,'compiler':name,'args':args,'environment':{k:v for k,v in env.items()if k.startswith('FERN_')or k=='VALGRIND_LIB'},'started':now()};state['runs'].append(row);save()
    with(out/(label+'.stdout')).open('wb')as so,(out/(label+'.stderr')).open('wb')as se:r=subprocess.run(args,cwd=fixed,env=env,stdout=so,stderr=se,timeout=1980)
    row.update(exit_code=r.returncode,finished=now());save();assert r.returncode==0
    totals=re.findall(r'^summary:\s*(\d+)\s*$',(out/(label+'.callgrind')).read_text(),re.M);refs=re.findall(r'I\s+refs:\s*([\d,]+)',(out/(label+'.valgrind.log')).read_text());assert len(totals)==len(refs)==1 and int(totals[0])==int(refs[0].replace(',',''))
    row.update(guest_instructions=int(totals[0]),output_sha256=sha(product));save();assert row['output_sha256']==m['outputs'][w]
 state.update(exit_code=0,finished=now())
except Exception as e:state.update(exit_code=1,error=repr(e),finished=now());raise
finally:save()
