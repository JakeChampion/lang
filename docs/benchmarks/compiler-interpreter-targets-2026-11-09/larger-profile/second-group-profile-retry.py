from pathlib import Path
import json,time,subprocess,os,hashlib,datetime,signal
root=Path('/workspace/lang-interpreter-timeout-20261109');out=Path('/workspace/lang-interpreter-dispatch-20261109/build/interpreter-dispatch-20261109');source=root/'build/interpreter-profile-20261109/second-group.fern';assert source.exists()
binary=root/'build/interpreter-profile-20261109/fern-cpu';env={k:v for k,v in os.environ.items() if not k.startswith('FERN_')};env.update(FERN_PROFILE='0',FERN_LEAKCHECK='0',FERN_RC_CERTIFY='1',FERN_RC_CERTIFY_MEMO='0');args=[str(binary),'-stdlib',str(root/'internal/stdlib'),'-interp',str(source)];path=out/'second-group-profile.json';assert not path.exists();d={'args':args,'cwd':str(root),'binary_sha256':hashlib.sha256(binary.read_bytes()).hexdigest(),'source_sha256':hashlib.sha256(source.read_bytes()).hexdigest(),'started':datetime.datetime.now(datetime.timezone.utc).isoformat(),'note':'Diagnostic group1,32records, not full corpus. CPU build counters off. Normal exit required for profile completeness. Started after baseline full-control deadline to limit concurrent host load.','budget_seconds':1980};path.write_text(json.dumps(d,indent=2)+'\n');start=time.perf_counter();timed_out=False
with (out/'second-group-profile.stdout').open('wb') as stdout,(out/'second-group-profile.stderr').open('wb') as stderr:
 p=subprocess.Popen(args,cwd=root,env=env,stdout=stdout,stderr=stderr,start_new_session=True)
 while True:
  pid,status,usage=os.wait4(p.pid,os.WNOHANG)
  if pid:break
  if time.perf_counter()-start>1980:
   timed_out=True;os.killpg(p.pid,signal.SIGTERM);pid,status,usage=os.wait4(p.pid,0);break
  time.sleep(.1)
d.update(exit_code=os.waitstatus_to_exitcode(status),timed_out=timed_out,wall_seconds=time.perf_counter()-start,cpu_user=usage.ru_utime,cpu_system=usage.ru_stime,peak_rss_kib=usage.ru_maxrss,finished=datetime.datetime.now(datetime.timezone.utc).isoformat());path.write_text(json.dumps(d,indent=2)+'\n');print(json.dumps(d))
