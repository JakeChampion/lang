from pathlib import Path
import datetime,hashlib,json,os,subprocess
root=Path('/workspace/lang-free-wide-requalification-20261110-1015');out=Path(__file__).resolve().parent
normal=Path('/workspace/lang-interpreter-dispatch-20261109/build/compiler-main-validation-20261110-0950/normal')
state={'source':'93942727d19bcad77d7aef70da15b36c58056bf8','producer_sha256':hashlib.sha256(normal.read_bytes()).hexdigest(),'runs':[],'purpose':'Build the standard embedded CLI required by the unchanged qualification collector. The earlier no-embed compiler failed before worker execution.'}
def save():(out/'build.json').write_text(json.dumps(state,indent=2)+'\n')
try:
 assert state['producer_sha256']=='11407d36edde3ce430806655b5f7506e84f32962d671d22777fd14abe9013f61'
 env={k:v for k,v in os.environ.items()if not k.startswith('FERN_')};env.update(FERN_PROFILE='0',FERN_LEAKCHECK='0',FERN_RC_CERTIFY='1',FERN_RC_CERTIFY_MEMO='0')
 for label,args in [('assets',[str(root/'scripts/fern-embed-stage'),str(out/'embed')]),('shipped-cli',[str(normal),'-target','x86-64-linux','-embed',str(out/'embed'),'-o',str(out/'shipped-compiler'),'compiler/fern_main.fern','internal/stdlib'])]:
  row={'label':label,'args':args,'cwd':str(root),'started':datetime.datetime.now(datetime.timezone.utc).isoformat(),'environment':{k:v for k,v in env.items()if k.startswith('FERN_')}};state['runs'].append(row);save()
  with(out/(label+'.stdout')).open('wb')as stdout,(out/(label+'.stderr')).open('wb')as stderr:row['exit_code']=subprocess.run(args,cwd=root,env=env,stdout=stdout,stderr=stderr).returncode
  row['finished']=datetime.datetime.now(datetime.timezone.utc).isoformat();save();assert row['exit_code']==0
 state.update(exit_code=0,finished=datetime.datetime.now(datetime.timezone.utc).isoformat(),binary_sha256=hashlib.sha256((out/'shipped-compiler').read_bytes()).hexdigest())
except Exception as error:
 state.update(exit_code=1,error=repr(error),finished=datetime.datetime.now(datetime.timezone.utc).isoformat());raise
finally:save()
