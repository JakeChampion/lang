from pathlib import Path
import os, subprocess, json, hashlib, datetime, time, re, collections
root = Path('/workspace/lang-interpreter-dispatch-20261109')
fixed = Path('/workspace/lang-interpreter-timeout-20261109')
out = root / 'build/interpreter-index-profile-20261110'
out.mkdir()
producer = fixed / 'build/bootstrap/stage0/stage0-20261109-a19e872/fern-selfhost-x86-64-linux'
source = fixed / 'build/interpreter-profile-20261109/second-group.fern'
def sha(p): return hashlib.sha256(p.read_bytes()).hexdigest()
def now(): return datetime.datetime.now(datetime.timezone.utc).isoformat()
d = {'source': subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(), 'measured_code_source':'6f6bddfc2af38b6c7e85d23d9338a10c8fb9c307','fixed_input_source':'ebb50c78c0ca714180704352881da7e39efb570f','input_sha256':sha(source),'producer_source':'a19e87208c5931d2600fdb75cc966fd179ae8c8f','producer_sha256':sha(producer),'interp_source_sha256':sha(root/'compiler/interp.fern'),'started':now(),'note':'Independent residual CPU diagnosis after the index. Not a speedup comparison. Counter-free diagnostic build, certification enabled, no debug or embedded sources. Build starts only after the full source gate succeeds.'}
path = out/'result.json'
def save():
 tmp=path.with_suffix('.tmp');tmp.write_text(json.dumps(d,indent=2)+'\n');tmp.replace(path)
save()
gate = root / 'build/interpreter-source-gate-20261110/result.json'
end = time.monotonic()+600
while not gate.exists():
 if time.monotonic()>end:
  d.update(aborted='Source gate not complete within controller preflight',finished=now());save();raise SystemExit(2)
 time.sleep(2)
d['source_gate'] = json.loads(gate.read_text())
assert d['source_gate']['exit_code']==0
assert subprocess.check_output(['git','status','--porcelain'],cwd=root,text=True)==''
assert subprocess.check_output(['git','diff','6f6bddfc2af38b6c7e85d23d9338a10c8fb9c307','HEAD','--','compiler','internal/stdlib','internal/testing/e2ecompiler'],cwd=root,text=True)==''
env={k:v for k,v in os.environ.items() if not k.startswith('FERN_')}
env.update(FERN_PROFILE='cpu',FERN_LEAKCHECK='0',FERN_RC_CERTIFY='1',FERN_RC_CERTIFY_MEMO='0')
binary=out/'fern-cpu'
args=[str(producer),'-target','x86-64-linux','-o',str(binary),'compiler/fern.fern','internal/stdlib']
d['build']={'args':args,'cwd':str(root),'environment':{k:v for k,v in env.items() if k.startswith('FERN_')},'started':now()};save()
with (out/'build.stdout').open('wb') as stdout,(out/'build.stderr').open('wb') as stderr:
 r=subprocess.run(args,cwd=root,env=env,stdout=stdout,stderr=stderr,timeout=600)
d['build'].update(exit_code=r.returncode,finished=now());save();assert r.returncode==0
d['binary_sha256']=sha(binary)
env['FERN_PROFILE']='0'
args=[str(binary),'-stdlib',str(fixed/'internal/stdlib'),'-interp',str(source)]
d['run']={'args':args,'cwd':str(fixed),'environment':{k:v for k,v in env.items() if k.startswith('FERN_')},'started':now(),'budget_seconds':1980};save()
with (out/'run.stdout').open('wb') as stdout,(out/'run.stderr').open('wb') as stderr:
 r=subprocess.run(args,cwd=fixed,env=env,stdout=stdout,stderr=stderr,timeout=1980)
d['run'].update(exit_code=r.returncode,finished=now(),stdout_sha256=sha(out/'run.stdout'));save();assert r.returncode==0
exclusive=collections.Counter();inclusive=collections.Counter();deep=0;total=0
raw=(out/'run.stderr').read_text()
header=re.search(r'# fern CPU profile samples=(\d+) dropped=(\d+)',raw);assert header
for line in raw.splitlines():
 if not line.startswith('_start;'):continue
 stack,n=line.rsplit(' ',1);frames=stack.split(';');n=int(n)
 total+=n;exclusive[frames[-1]]+=n
 for frame in set(frames):inclusive[frame]+=n
 if len(frames)>=64:deep+=n
assert total==int(header[1])
d['profile']={'samples':total,'dropped':int(header[2]),'at_least_64_frames':deep,'exclusive':exclusive.most_common(30),'inclusive':inclusive.most_common(30),'note':'Inclusive costs overlap and stack depth can truncate. Sample shares are not speedup percentages.'}
d['finished']=now();save()
