from pathlib import Path
import json,hashlib,subprocess,os,re,datetime
root=Path('/workspace/lang-checker-union-index-20261110-1540');old=root/'build/qualification-20261110-1600/opus-cold-instructions';out=root/'build/qualification-20261110-1600/opus-cold-instructions-resume';out.mkdir(exist_ok=False)
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
state={'original_results_sha256':sha(old/'results.json'),'original_results':str(old/'results.json'),'reason':'Baseline copy mode0600 caused exit126 before baseline compilation. Candidate run is retained and reused without rerun. Correct only baseline execute bits, bytes unchanged.','runs':[]}
def save():(out/'results.json').write_text(json.dumps(state,indent=2)+'\n')
save()
try:
 original=json.loads((old/'results.json').read_text());c=next(x for x in original['runs']if x['label']=='candidate');assert c['exit_code']==0
 binary=old/'base/fern';before=sha(binary);assert before==original['binary_sha256']['baseline'];state['mode_before']=oct(binary.stat().st_mode&0o777);binary.chmod(0o755);assert sha(binary)==before;state['mode_after']=oct(binary.stat().st_mode&0o777);state['binary_sha256']=original['binary_sha256'];save()
 env={k:v for k,v in os.environ.items()if not k.startswith('FERN_')};env.update(FERN_PROFILE='0',FERN_LEAKCHECK='0',FERN_RC_CERTIFY='1',FERN_RC_CERTIFY_MEMO='0',VALGRIND_LIB='/workspace/scratch/lang-valgrind-20261109/root/usr/libexec/valgrind')
 args=next(x for x in original['runs']if x['label']=='baseline')['args'];args=[('--callgrind-out-file='+str(out/'baseline.callgrind'))if x.startswith('--callgrind-out-file=')else('--log-file='+str(out/'baseline.valgrind.log'))if x.startswith('--log-file=')else x for x in args]
 def run(label,args):
  row={'label':label,'args':args,'started':datetime.datetime.now(datetime.timezone.utc).isoformat(),'environment':{k:v for k,v in env.items()if k.startswith('FERN_')or k=='VALGRIND_LIB'}};state['runs'].append(row);save()
  with(out/(label+'.stdout')).open('wb')as so,(out/(label+'.stderr')).open('wb')as se:r=subprocess.run(args,cwd='/workspace/lang-compiler-main-20261110-1540',env=env,stdout=so,stderr=se,timeout=1980)
  row.update(exit_code=r.returncode,finished=datetime.datetime.now(datetime.timezone.utc).isoformat());save();assert r.returncode==0;return row
 row=run('baseline',args);totals=re.findall(r'^summary:\s*(\d+)\s*$',(out/'baseline.callgrind').read_text(),re.M);refs=re.findall(r'I\s+refs:\s*([\d,]+)',(out/'baseline.valgrind.log').read_text());assert len(totals)==len(refs)==1 and int(totals[0])==int(refs[0].replace(',',''));row['guest_instructions']=int(totals[0]);row['output_sha256']=sha(old/'base/opus');assert row['output_sha256']==c['output_sha256']==sha(old/'cand/opus');state['guest_instructions']={'baseline':row['guest_instructions'],'candidate':c['guest_instructions']};state['output_sha256']=row['output_sha256'];save()
 run('candidate-program',[str(old/'cand/opus')]);assert sha(out/'candidate-program.stdout')=='07c2de202ca09462f396f349cbf99df5b0f044194e9314840de8bf6331c29161';state.update(exit_code=0,finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
except Exception as e:state.update(exit_code=1,error=repr(e),finished=datetime.datetime.now(datetime.timezone.utc).isoformat());raise
finally:save()
