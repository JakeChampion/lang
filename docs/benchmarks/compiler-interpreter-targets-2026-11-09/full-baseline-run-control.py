from pathlib import Path
import sys,subprocess,os,time,datetime,json,hashlib,signal
root=Path('/workspace/lang-interpreter-timeout-20261109');out=root/'build/interpreter-profile-20261109';label=sys.argv[1];binary=out/('fern-cpu' if label=='first-group-cpu' else 'fern-normal');source=out/'first-group.fern' if label=='first-group-cpu' else root/'tests/stdlib/opus_delimited_test.fern';result=out/(label+'.json');assert not result.exists();env={k:v for k,v in os.environ.items() if not k.startswith('FERN_')};env.update(FERN_PROFILE='0',FERN_LEAKCHECK='0',FERN_RC_CERTIFY='1',FERN_RC_CERTIFY_MEMO='0');args=[str(binary),'-stdlib',str(root/'internal/stdlib'),'-interp',str(source)];d={'name':label,'source_revision':'ebb50c78c0ca714180704352881da7e39efb570f','args':args,'cwd':str(root),'compiler_sha256':hashlib.sha256(binary.read_bytes()).hexdigest(),'input_sha256':hashlib.sha256(source.read_bytes()).hexdigest(),'fern_env':{k:v for k,v in env.items() if k.startswith('FERN_')},'started':datetime.datetime.now(datetime.timezone.utc).isoformat(),'diagnostic_subset':label=='first-group-cpu','budget_seconds':1980,'note':'Baseline diagnosis, not an optimization comparison. Full input remains the validation control. A concurrent diagnostic interpreter may be active.'};result.write_text(json.dumps(d,indent=2)+'\n');start=time.perf_counter();timed_out=False
with (out/(label+'.stdout')).open('wb') as stdout,(out/(label+'.stderr')).open('wb') as stderr:
 p=subprocess.Popen(args,cwd=root,env=env,stdout=stdout,stderr=stderr,start_new_session=True)
 while True:
  pid,status,usage=os.wait4(p.pid,os.WNOHANG)
  if pid:break
  if time.perf_counter()-start>1980:
   timed_out=True;os.killpg(p.pid,signal.SIGTERM);pid,status,usage=os.wait4(p.pid,0);break
  time.sleep(.1)
 p.returncode=os.waitstatus_to_exitcode(status)
d.update(exit_code=p.returncode,wall_seconds=time.perf_counter()-start,cpu_user=usage.ru_utime,cpu_system=usage.ru_stime,peak_rss_kib=usage.ru_maxrss,timed_out=timed_out,finished=datetime.datetime.now(datetime.timezone.utc).isoformat());result.write_text(json.dumps(d,indent=2)+'\n');print(json.dumps(d,indent=2));sys.exit(0 if p.returncode==0 else 1)
