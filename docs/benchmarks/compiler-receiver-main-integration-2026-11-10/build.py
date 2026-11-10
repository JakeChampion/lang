from pathlib import Path
import os,subprocess,json,datetime,hashlib
out=Path(__file__).resolve().parent;producer=Path(os.environ['STAGE0']);sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest();state={'baseline':'bcd223bae6ab2d8dc14b3b152027fa7be90e6779','retained':'63d33384bb4261faa38f4436b4abf4d52a9b9542','purpose':'Fresh main and retained-source normal images must match byte for byte after rejection. No performance comparison.','producer_sha256':sha(producer),'runs':[]}
def save():(out/'build.json').write_text(json.dumps(state,indent=2)+'\n')
save()
try:
 assert state['producer_sha256']=='a66fcf94f418bea5f969bfaf2b07e6fe56235d2565ca405c950e6e263187a751'
 env={k:v for k,v in os.environ.items()if not k.startswith('FERN_')};env.update(FERN_PROFILE='0',FERN_LEAKCHECK='0',FERN_RC_CERTIFY='1',FERN_RC_CERTIFY_MEMO='0')
 for label,root in [('main','/workspace/lang-compiler-main-20261110-1845'),('retained','/workspace/lang-checker-union-retained-20261110-1845')]:
  expected=state['baseline'if label=='main'else'retained'];assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip()==expected;assert not subprocess.check_output(['git','status','--porcelain'],cwd=root,text=True)
  args=[str(producer),'-target','x86-64-linux','-o',str(out/(label+'-normal')),'compiler/fern.fern','internal/stdlib'];row={'label':label,'args':args,'cwd':root,'environment':{k:v for k,v in env.items()if k.startswith('FERN_')},'started':datetime.datetime.now(datetime.timezone.utc).isoformat()};state['runs'].append(row);save()
  with(out/(label+'.stdout')).open('wb')as so,(out/(label+'.stderr')).open('wb')as se:r=subprocess.run(args,cwd=root,env=env,stdout=so,stderr=se,timeout=1980)
  row.update(exit_code=r.returncode,finished=datetime.datetime.now(datetime.timezone.utc).isoformat());save();assert r.returncode==0;row['sha256']=sha(out/(label+'-normal'));save()
 assert state['runs'][0]['sha256']==state['runs'][1]['sha256'];state.update(exit_code=0,identical_normal_sha256=state['runs'][0]['sha256'],finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
except Exception as e:state.update(exit_code=1,error=repr(e),finished=datetime.datetime.now(datetime.timezone.utc).isoformat());raise
finally:save()
