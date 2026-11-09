from pathlib import Path
import os,subprocess,json,hashlib,datetime,time,re
root=Path('/workspace/lang-interpreter-index-20261109');fixed=Path('/workspace/lang-interpreter-timeout-20261109');out=root/'build/interpreter-index-count-20261109';out.mkdir();producer=fixed/'build/bootstrap/stage0/stage0-20261109-a19e872/fern-selfhost-x86-64-linux';path=out/'result.json';env={k:v for k,v in os.environ.items() if not k.startswith('FERN_')};env.update(FERN_PROFILE='0',FERN_LEAKCHECK='1',FERN_RC_CERTIFY='1',FERN_RC_CERTIFY_MEMO='0');data={'source':subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(),'producer_sha256':hashlib.sha256(producer.read_bytes()).hexdigest(),'note':'Separate allocation-counter build, not a production timing executable. Waits for the targeted Go test process to finish before the build.','runs':[]}
def save():
 temp=path.with_suffix('.tmp');temp.write_text(json.dumps(data,indent=2)+'\n');temp.replace(path)
save()
while not (root/'build/interpreter-index-trial-20261109/tests-result.json').exists():time.sleep(1)
binary=out/'fern-count';args=[str(producer),'-target','x86-64-linux','-o',str(binary),'compiler/fern.fern','internal/stdlib'];data['build']={'args':args,'cwd':str(root),'environment':{k:v for k,v in env.items() if k.startswith('FERN_')},'started':datetime.datetime.now(datetime.timezone.utc).isoformat()};save()
with (out/'build.stdout').open('wb') as stdout,(out/'build.stderr').open('wb') as stderr:r=subprocess.run(args,cwd=root,env=env,stdout=stdout,stderr=stderr)
data['build'].update(exit_code=r.returncode,finished=datetime.datetime.now(datetime.timezone.utc).isoformat());save();assert r.returncode==0;data['binary_sha256']=hashlib.sha256(binary.read_bytes()).hexdigest();save()
for group in ('first','second'):
 args=[str(binary),'-stdlib',str(fixed/'internal/stdlib'),'-interp',str(fixed/f'build/interpreter-profile-20261109/{group}-group.fern')];d={'group':group,'args':args,'cwd':str(fixed),'started':datetime.datetime.now(datetime.timezone.utc).isoformat()};data['runs'].append(d);save()
 with (out/(group+'.stdout')).open('wb') as stdout,(out/(group+'.stderr')).open('wb') as stderr:r=subprocess.run(args,cwd=fixed,env=env,stdout=stdout,stderr=stderr)
 d.update(exit_code=r.returncode,finished=datetime.datetime.now(datetime.timezone.utc).isoformat());save();assert r.returncode==0;m=re.findall(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)',(out/(group+'.stderr')).read_text());assert len(m)==1 and m[0][0]==m[0][1] and m[0][2]=='0';d.update(allocs=int(m[0][0]),frees=int(m[0][1]),live_bytes=0,stdout_sha256=hashlib.sha256((out/(group+'.stdout')).read_bytes()).hexdigest());save();print(group,d['allocs'],flush=True)
data['finished']=datetime.datetime.now(datetime.timezone.utc).isoformat();save()
