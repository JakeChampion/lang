from pathlib import Path
import os,subprocess,json,hashlib,datetime,sys
root=Path('/workspace/lang-interpreter-main-base-20261110-0220');out=root/'build/interpreter-main-baseline-20261110-0220';path=out/'result.json';assert not path.exists()
producer=Path('/workspace/lang-interpreter-timeout-20261109/build/bootstrap/stage0/stage0-20261109-a19e872/fern-selfhost-x86-64-linux')
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
def now():return datetime.datetime.now(datetime.timezone.utc).isoformat()
assert sha(producer)=='a66fcf94f418bea5f969bfaf2b07e6fe56235d2565ca405c950e6e263187a751'
d={'source':subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(),'producer_source':'a19e87208c5931d2600fdb75cc966fd179ae8c8f','producer_sha256':sha(producer),'started':now(),'note':'Normal main f990 baseline and Plex correctness probe after upstream callback repair. No index change or paired performance measurement. Concurrent integration tests may run during this build.'}
def save():
 p=path.with_suffix('.tmp');p.write_text(json.dumps(d,indent=2)+'\n');p.replace(path)
def error(kind,value,trace):
 d['collector_error']=repr(value);d['finished']=now();save();sys.__excepthook__(kind,value,trace)
sys.excepthook=error
save();env={k:v for k,v in os.environ.items() if not k.startswith('FERN_')};env.update(FERN_PROFILE='0',FERN_LEAKCHECK='0',FERN_RC_CERTIFY='1',FERN_RC_CERTIFY_MEMO='0');binary=out/'fern-main'
args=[str(producer),'-target','x86-64-linux','-o',str(binary),'compiler/fern.fern','internal/stdlib'];d['build']={'args':args,'cwd':str(root),'environment':{k:v for k,v in env.items() if k.startswith('FERN_')},'started':now()};save()
with (out/'build.stdout').open('wb') as stdout,(out/'build.stderr').open('wb') as stderr:r=subprocess.run(args,cwd=root,env=env,stdout=stdout,stderr=stderr,timeout=600)
d['build'].update(exit_code=r.returncode,finished=now());save();assert r.returncode==0;d['binary_sha256']=sha(binary)
source=root/'examples/android/plex/app.fern';args=[str(binary),'-target','arm64-android','-stdlib',str(root/'internal/stdlib'),'-check',str(source)];d['plex']={'args':args,'cwd':str(root),'entry_sha256':sha(source),'started':now()};save()
with (out/'plex.stdout').open('wb') as stdout,(out/'plex.stderr').open('wb') as stderr:r=subprocess.run(args,cwd=root,env=env,stdout=stdout,stderr=stderr,timeout=120)
d['plex'].update(exit_code=r.returncode,finished=now(),stdout_sha256=sha(out/'plex.stdout'),stderr_sha256=sha(out/'plex.stderr'),has_e051='error[E051]' in (out/'plex.stderr').read_text());d['finished']=now();save()
manifest={p.name:sha(p) for p in out.iterdir() if p.is_file() and p.name not in ('fern-main','manifest.json','parent.log','collector.pid')};(out/'manifest.json').write_text(json.dumps({'sha256':manifest},indent=2)+'\n');print(json.dumps(d,indent=2))
