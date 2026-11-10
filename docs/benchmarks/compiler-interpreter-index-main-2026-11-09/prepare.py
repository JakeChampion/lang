from pathlib import Path
import os,subprocess,hashlib,json,datetime
root=Path('/workspace/lang-interpreter-dispatch-20261109');base=Path('/workspace/lang-interpreter-main-base-20261109-2320');out=root/'build/interpreter-main-qualification-20261109-2320';producer=Path('/workspace/lang-interpreter-timeout-20261109/build/bootstrap/stage0/stage0-20261109-a19e872/fern-selfhost-x86-64-linux');path=out/'prepare.json';data={'candidate_source':subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(),'baseline_source':subprocess.check_output(['git','rev-parse','HEAD'],cwd=base,text=True).strip(),'producer_sha256':hashlib.sha256(producer.read_bytes()).hexdigest(),'runs':[]};assert not path.exists()
def save():
 temp=path.with_suffix('.tmp');temp.write_text(json.dumps(data,indent=2)+'\n');temp.replace(path)
def run(label,args,cwd,env):
 d={'label':label,'args':list(map(str,args)),'cwd':str(cwd),'environment':{k:v for k,v in env.items() if k.startswith('FERN_')},'started':datetime.datetime.now(datetime.timezone.utc).isoformat()};data['runs'].append(d);save()
 with (out/(label+'.stdout')).open('wb') as stdout,(out/(label+'.stderr')).open('wb') as stderr:r=subprocess.run(d['args'],cwd=cwd,env=env,stdout=stdout,stderr=stderr)
 d.update(exit_code=r.returncode,finished=datetime.datetime.now(datetime.timezone.utc).isoformat());save();print(label,r.returncode,flush=True);assert r.returncode==0
for name,source in [('candidate',root),('baseline',base)]:
 env={k:v for k,v in os.environ.items() if not k.startswith('FERN_')};env.update(FERN_PROFILE='0',FERN_LEAKCHECK='0',FERN_RC_CERTIFY='1',FERN_RC_CERTIFY_MEMO='0');run(name+'-source-check',[producer,'-check','compiler/fern.fern','internal/stdlib'],source,env)
 for counted in (False,True):
  env['FERN_LEAKCHECK']='1' if counted else '0';label=name+('-count' if counted else '-normal');binary=out/label;run('build-'+label,[producer,'-target','x86-64-linux','-o',binary,'compiler/fern.fern','internal/stdlib'],source,env);data.setdefault('binaries',{})[label]={'sha256':hashlib.sha256(binary.read_bytes()).hexdigest()};save()
data['finished']=datetime.datetime.now(datetime.timezone.utc).isoformat();save();print('complete',flush=True)
