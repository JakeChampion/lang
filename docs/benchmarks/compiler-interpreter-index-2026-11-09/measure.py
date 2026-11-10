from pathlib import Path
import os,subprocess,time,json,hashlib,datetime,signal,sys
root=Path('/workspace/lang-interpreter-index-20261109');fixed=Path('/workspace/lang-interpreter-timeout-20261109');old=Path('/workspace/lang-interpreter-dispatch-20261109');out=root/'build/interpreter-index-measure-20261109';out.mkdir(exist_ok=True);mode=sys.argv[1];binary={'skip':old/'build/interpreter-dispatch-20261109/fern-candidate','index':root/'build/interpreter-index-qualified-20261109/fern-index'};env={k:v for k,v in os.environ.items() if not k.startswith('FERN_')};env.update(FERN_PROFILE='0',FERN_LEAKCHECK='0',FERN_RC_CERTIFY='1',FERN_RC_CERTIFY_MEMO='0');state={'baseline_source':'424fe24f4cf2bd1c0c49060215f1fe622ef4d5b2','candidate_source':subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(),'fixed_input_source':'ebb50c78c0ca714180704352881da7e39efb570f','mode':mode,'note':'Production normal binaries, same qualifieda19producer. No debug variables or embedded sources, profiling/counters off, ownership certification on. Go tests and Callgrind can overlap. Full control keeps1980sdiagnosticlimit. Do not infer full CIpackagebudgetpass from direct control alone.','runs':[]};path=out/(mode+'.json');assert not path.exists()
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
def save():
 temp=path.with_suffix('.tmp');temp.write_text(json.dumps(state,indent=2)+'\n');temp.replace(path)
def run(label,name,source):
 args=[str(binary[name]),'-stdlib',str(fixed/'internal/stdlib'),'-interp',str(source)];d={'label':label,'compiler':name,'args':args,'cwd':str(fixed),'binary_sha256':sha(binary[name]),'input_sha256':sha(source),'environment':{k:v for k,v in env.items() if k.startswith('FERN_')},'started':datetime.datetime.now(datetime.timezone.utc).isoformat()};state['runs'].append(d);save();start=time.perf_counter_ns();timeout=False
 with (out/(label+'.stdout')).open('wb') as stdout,(out/(label+'.stderr')).open('wb') as stderr:
  p=subprocess.Popen(args,cwd=fixed,env=env,stdout=stdout,stderr=stderr,start_new_session=True)
  while True:
   pid,status,usage=os.wait4(p.pid,os.WNOHANG)
   if pid:break
   if time.perf_counter_ns()-start>1980*10**9:
    timeout=True;os.killpg(p.pid,signal.SIGTERM);pid,status,usage=os.wait4(p.pid,0);break
   time.sleep(.02)
  p.returncode=os.waitstatus_to_exitcode(status)
 d.update(exit_code=p.returncode,timed_out=timeout,finished=datetime.datetime.now(datetime.timezone.utc).isoformat(),wall_ns=time.perf_counter_ns()-start,cpu_user=usage.ru_utime,cpu_system=usage.ru_stime,peak_rss_kib=usage.ru_maxrss,stdout_sha256=sha(out/(label+'.stdout')),stderr_sha256=sha(out/(label+'.stderr')));save();print(label,d['exit_code'],d['wall_ns']/1e9,flush=True);return d
save()
if mode=='full':
 d=run('full-index','index',fixed/'tests/stdlib/opus_delimited_test.fern');state['finished']=datetime.datetime.now(datetime.timezone.utc).isoformat();save();raise SystemExit(d['exit_code']!=0)
for group,n in [('first',5),('second',3)]:
 rows=[]
 for pair in range(n):
  for name in (('skip','index') if pair%2==0 else ('index','skip')):rows.append(run(f'{group}-{pair}-{name}',name,fixed/f'build/interpreter-profile-20261109/{group}-group.fern'))
 assert all(d['exit_code']==0 for d in rows);assert len(set(d['stdout_sha256'] for d in rows))==1
state['finished']=datetime.datetime.now(datetime.timezone.utc).isoformat();save();print('complete',flush=True)
