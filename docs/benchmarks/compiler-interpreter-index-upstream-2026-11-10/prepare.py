from pathlib import Path
import os, subprocess, hashlib, json, datetime, sys, shutil
root=Path('/workspace/lang-interpreter-qualified-20261110-0230')
base=Path('/workspace/lang-interpreter-main-base-20261110-0220')
out=Path('/workspace/lang-interpreter-dispatch-20261109/build/interpreter-main-qualification-20261110-0230')
producer=Path('/workspace/lang-interpreter-timeout-20261109/build/bootstrap/stage0/stage0-20261109-a19e872/fern-selfhost-x86-64-linux')
path=out/'prepare.json'
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
def rev(p):return subprocess.check_output(['git','rev-parse','HEAD'],cwd=p,text=True).strip()
def now():return datetime.datetime.now(datetime.timezone.utc).isoformat()
assert not path.exists()
assert rev(root)=='206df7eeb094f5540ce1bd25cc2bd8090dae5df2'
assert rev(base)=='f9904974a77249949607f3b6196e489bf53c3602'
assert sha(producer)=='a66fcf94f418bea5f969bfaf2b07e6fe56235d2565ca405c950e6e263187a751'
data={'candidate_source':rev(root),'baseline_source':rev(base),'producer_source':'a19e87208c5931d2600fdb75cc966fd179ae8c8f','producer_sha256':sha(producer),'started':now(),'runs':[],'binaries':{}}
def save():
 p=path.with_suffix('.tmp');p.write_text(json.dumps(data,indent=2)+'\n');p.replace(path)
def error(kind,value,trace):
 data['collector_error']=repr(value);save();sys.__excepthook__(kind,value,trace)
sys.excepthook=error
save()
env={k:v for k,v in os.environ.items() if not k.startswith('FERN_')}
env.update(FERN_PROFILE='0',FERN_LEAKCHECK='0',FERN_RC_CERTIFY='1',FERN_RC_CERTIFY_MEMO='0')
probe=base/'build/interpreter-main-baseline-20261110-0220'
record=json.loads((probe/'result.json').read_text())
assert record['build']['exit_code']==0 and record['plex']['exit_code']==0
assert record['source']==rev(base) and record['producer_sha256']==sha(producer)
assert record['build']['environment']=={k:v for k,v in env.items() if k.startswith('FERN_')}
assert sha(probe/'fern-main')=='49abc86d4c39c18b64d6fa8411869ffdf06e225dd5bf828b2944c133eb2dcef8'
shutil.copy2(probe/'fern-main',out/'baseline-normal')
shutil.copy2(probe/'result.json',out/'baseline-build-record.json')
shutil.copy2(probe/'probe.py',out/'baseline-build-controller.py')
for name in ('build.stdout','build.stderr','plex.stdout','plex.stderr'):
 shutil.copy2(probe/name,out/('baseline-'+name))
data['binaries']['baseline-normal']={'sha256':sha(out/'baseline-normal'),'reused_build_record':'baseline-build-record.json','note':'Exact f990 normal production build. No timing sample uses the build interval.'}
save()
for name,source,counted in [('candidate',root,False),('candidate',root,True),('baseline',base,True)]:
 assert not subprocess.check_output(['git','status','--porcelain','--untracked-files=no'],cwd=source,text=True)
 env['FERN_LEAKCHECK']='1' if counted else '0'
 label=name+('-count' if counted else '-normal');binary=out/label
 args=[str(producer),'-target','x86-64-linux','-o',str(binary),'compiler/fern.fern','internal/stdlib']
 d={'label':label,'args':args,'cwd':str(source),'source':rev(source),'environment':{k:v for k,v in env.items() if k.startswith('FERN_')},'started':now()};data['runs'].append(d);save()
 with (out/(label+'-build.stdout')).open('wb') as stdout,(out/(label+'-build.stderr')).open('wb') as stderr:
  p=subprocess.run(args,cwd=source,env=env,stdout=stdout,stderr=stderr,timeout=600)
 d.update(exit_code=p.returncode,finished=now());save();assert p.returncode==0
 data['binaries'][label]={'sha256':sha(binary)};save();print(label,'build0',flush=True)
data['finished']=now();save();print('complete',flush=True)
