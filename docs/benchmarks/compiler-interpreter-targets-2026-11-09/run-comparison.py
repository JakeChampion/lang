from pathlib import Path
import os,sys,time,subprocess,json,hashlib,datetime,signal
root=Path('/workspace/lang-interpreter-dispatch-20261109');fixed=Path('/workspace/lang-interpreter-timeout-20261109');out=root/'build/interpreter-dispatch-20261109';base=fixed/'build/interpreter-profile-20261109/fern-normal';cand=out/'fern-candidate';mode=sys.argv[1]
env={k:v for k,v in os.environ.items() if not k.startswith('FERN_')};env.update(FERN_PROFILE='0',FERN_LEAKCHECK='0',FERN_RC_CERTIFY='1',FERN_RC_CERTIFY_MEMO='0')
def digest(p):return hashlib.sha256(p.read_bytes()).hexdigest()
def run(label,binary,source,budget=1980):
 path=out/(label+'.json');assert not path.exists();args=[str(binary),'-stdlib',str(fixed/'internal/stdlib'),'-interp',str(source)];d={'label':label,'args':args,'cwd':str(fixed),'binary_sha256':digest(binary),'input_sha256':digest(source),'environment':{k:v for k,v in env.items() if k.startswith('FERN_')},'started':datetime.datetime.now(datetime.timezone.utc).isoformat(),'budget_seconds':budget,'note':'Concurrent full-corpus control and targeted Go tests may be active. Retain every sample. Diagnostic subsets do not replace the full test.'};path.write_text(json.dumps(d,indent=2)+'\n');start=time.perf_counter_ns();timed_out=False
 with (out/(label+'.stdout')).open('wb') as stdout,(out/(label+'.stderr')).open('wb') as stderr:
  p=subprocess.Popen(args,cwd=fixed,env=env,stdout=stdout,stderr=stderr,start_new_session=True)
  while True:
   pid,status,usage=os.wait4(p.pid,os.WNOHANG)
   if pid:break
   if time.perf_counter_ns()-start>budget*10**9:
    timed_out=True;os.killpg(p.pid,signal.SIGTERM);pid,status,usage=os.wait4(p.pid,0);break
   time.sleep(.01)
  p.returncode=os.waitstatus_to_exitcode(status)
 d.update(exit_code=p.returncode,wall_ns=time.perf_counter_ns()-start,cpu_user=usage.ru_utime,cpu_system=usage.ru_stime,peak_rss_kib=usage.ru_maxrss,timed_out=timed_out,finished=datetime.datetime.now(datetime.timezone.utc).isoformat(),stdout_sha256=digest(out/(label+'.stdout')),stderr_sha256=digest(out/(label+'.stderr')));path.write_text(json.dumps(d,indent=2)+'\n');print(json.dumps(d),flush=True);return d
if mode=='full':
 d=run('full-candidate',cand,fixed/'tests/stdlib/opus_delimited_test.fern');raise SystemExit(d['exit_code']!=0)
if mode=='first-group':
 data=[]
 for i in range(5):
  for name,binary in ([('base',base),('candidate',cand)] if i%2==0 else [('candidate',cand),('base',base)]):
   data.append(run(f'first-group-{i}-{name}',binary,fixed/'build/interpreter-profile-20261109/first-group.fern'))
 (out/'first-group-samples.json').write_text(json.dumps(data,indent=2)+'\n');assert all(d['exit_code']==0 for d in data);assert len(set(d['stdout_sha256'] for d in data))==1
