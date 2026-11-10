from pathlib import Path
import subprocess,json,hashlib,datetime,os
root=Path('/workspace/lang-interpreter-dispatch-20261109');fixed=Path('/workspace/lang-interpreter-main-base-20261109-2320');out=root/'build/interpreter-ci-20261110/reproduce';path=out/'results.json';assert not path.exists()
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
def now():return datetime.datetime.now(datetime.timezone.utc).isoformat()
env={k:v for k,v in os.environ.items() if not k.startswith('FERN_')};env.update(FERN_PROFILE='0',FERN_LEAKCHECK='0',FERN_RC_CERTIFY='1',FERN_RC_CERTIFY_MEMO='0')
source=fixed/'examples/android/plex/app.fern'
d={'input_source':'4a04428264dd4641ca1032cf5edc84762a883dd5','entry_sha256':sha(source),'pr_head':'7bd045eb04c8ea0b5980f0526e778ae107571d05','code_source':'6f6bddfc2af38b6c7e85d23d9338a10c8fb9c307','producer_source':'a19e87208c5931d2600fdb75cc966fd179ae8c8f','note':'Paired correctness diagnosis for CI Android E051. No performance measurement. Fixed main4a entry and standard library, normal qualified binaries. The callback repair remains owned by PR15060.','runs':[]}
for label in ('baseline','candidate'):
 binary=root/f'build/interpreter-main-qualification-20261109-2320/{label}-normal'
 args=[str(binary),'-target','arm64-android','-stdlib',str(fixed/'internal/stdlib'),'-check',str(source)]
 row={'label':label,'args':args,'cwd':str(fixed),'binary_sha256':sha(binary),'fern_environment':{k:v for k,v in env.items() if k.startswith('FERN_')},'started':now()}
 with (out/f'{label}.stdout').open('wb') as stdout,(out/f'{label}.stderr').open('wb') as stderr:r=subprocess.run(args,cwd=fixed,env=env,stdout=stdout,stderr=stderr,timeout=120)
 row.update(exit_code=r.returncode,finished=now(),stdout_sha256=sha(out/f'{label}.stdout'),stderr_sha256=sha(out/f'{label}.stderr'));d['runs'].append(row);path.write_text(json.dumps(d,indent=2)+'\n')
 print(label,'exit',r.returncode,(out/f'{label}.stderr').read_text())
d['identical_diagnostics']=(out/'baseline.stderr').read_bytes()==(out/'candidate.stderr').read_bytes();d['finished']=now();path.write_text(json.dumps(d,indent=2)+'\n');assert d['identical_diagnostics'];assert all(r['exit_code']==1 for r in d['runs']);assert 'error[E051]' in (out/'baseline.stderr').read_text()
