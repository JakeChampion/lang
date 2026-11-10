from pathlib import Path
import os,time,subprocess,json,datetime,hashlib,signal,sys,re
root=Path('/workspace/lang-interpreter-dispatch-20261109');fixed=Path('/workspace/lang-interpreter-timeout-20261109');parent=root/'build/interpreter-map-qualification-20261110-0330';mode=sys.argv[1];out=parent/mode;out.mkdir();path=out/'results.json'
while True:
 d=json.loads((parent/'prepare.json').read_text())
 if d.get('finished'):break
 time.sleep(1)
state={'baseline_source':d['baseline_source'],'candidate_source':d['candidate_source'],'fixed_input_source':'ebb50c78c0ca714180704352881da7e39efb570f','producer_sha256':d['producer_sha256'],'mode':mode,'note':'Residual shortcut qualification after the upstream Map index merge da3282d0. Same producer, input, stdlib, no debug variables, no embedded sources. Certification on. Subset timings finish before compilation and full-corpus controls start. Earlier NameIndex results do not measure this implementation.','runs':[]}
def save():
 temp=path.with_suffix('.tmp');temp.write_text(json.dumps(state,indent=2)+'\n');temp.replace(path)
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
def error(kind,value,trace):
 state['collector_error']=repr(value);save();sys.__excepthook__(kind,value,trace)
sys.excepthook=error
def run(label,name,source,count=False):
 binary=parent/(name+('-count' if count else '-normal'));env={k:v for k,v in os.environ.items() if not k.startswith('FERN_')};env.update(FERN_PROFILE='0',FERN_LEAKCHECK='1' if count else '0',FERN_RC_CERTIFY='1',FERN_RC_CERTIFY_MEMO='0');args=[str(binary),'-stdlib',str(fixed/'internal/stdlib'),'-interp',str(source)];row={'label':label,'compiler':name,'args':args,'cwd':str(fixed),'binary_sha256':sha(binary),'input_sha256':sha(source),'environment':{k:v for k,v in env.items() if k.startswith('FERN_')},'started':datetime.datetime.now(datetime.timezone.utc).isoformat()};state['runs'].append(row);save();start=time.perf_counter_ns();timeout=False
 with (out/(label+'.stdout')).open('wb') as stdout,(out/(label+'.stderr')).open('wb') as stderr:
  p=subprocess.Popen(args,cwd=fixed,env=env,stdout=stdout,stderr=stderr,start_new_session=True)
  while True:
   pid,status,usage=os.wait4(p.pid,os.WNOHANG)
   if pid:break
   if time.perf_counter_ns()-start>1980*10**9:timeout=True;os.killpg(p.pid,signal.SIGTERM);pid,status,usage=os.wait4(p.pid,0);break
   time.sleep(.02)
 row.update(exit_code=os.waitstatus_to_exitcode(status),timed_out=timeout,wall_ns=time.perf_counter_ns()-start,cpu_user=usage.ru_utime,cpu_system=usage.ru_stime,peak_rss_kib=usage.ru_maxrss,stdout_sha256=sha(out/(label+'.stdout')),finished=datetime.datetime.now(datetime.timezone.utc).isoformat());save();assert row['exit_code']==0
 if count:
  ms=re.findall(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)',(out/(label+'.stderr')).read_text());assert len(ms)==1 and ms[0][0]==ms[0][1] and ms[0][2]=='0';row.update(allocs=int(ms[0][0]),frees=int(ms[0][1]),live_bytes=0);save()
 print(label,'exit0',row['wall_ns']/1e9,flush=True);return row
save()
if mode=='full':run('full-candidate','candidate',fixed/'tests/stdlib/opus_delimited_test.fern')
else:
 for group,n in [('first',5),('second',3)]:
  rows=[]
  for i in range(n):
   for name in (('baseline','candidate') if i%2==0 else ('candidate','baseline')):rows.append(run(f'{group}-{i}-{name}',name,fixed/f'build/interpreter-profile-20261109/{group}-group.fern'))
  assert len(set(r['stdout_sha256'] for r in rows))==1
 for group in ('first','second'):
  rows=[run(f'{group}-count-{name}',name,fixed/f'build/interpreter-profile-20261109/{group}-group.fern',True) for name in ('candidate','baseline')];assert rows[0]['stdout_sha256']==rows[1]['stdout_sha256']
state['finished']=datetime.datetime.now(datetime.timezone.utc).isoformat();save()
