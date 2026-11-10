from pathlib import Path
import os,subprocess,json,hashlib,datetime,time,re,sys
root=Path('/workspace/lang-interpreter-dispatch-20261109');fixed=Path('/workspace/lang-interpreter-timeout-20261109');parent=root/'build/interpreter-lazy-main-qualification-20261110-0710';out=parent/'compilation';out.mkdir();prep=json.loads((parent/'prepare.json').read_text());assert prep.get('finished');path=out/'results.json';state={'baseline_source':prep['baseline_source'],'candidate_source':prep['candidate_source'],'fixed_input_source':'ebb50c78c0ca714180704352881da7e39efb570f','producer_sha256':prep['producer_sha256'],'workload':'Compile the full Opus reference program and its imports to native code. This differs from the earlier checker-compilation workload. Do not pool totals.','note':'No debug variables, profiles or allocation counters. Certification on. Three alternating pairs per cache mode with own primed proof caches. Subset timings finish before compilation controls. Full-corpus interpretation starts afterward.','runs':[]};expected=None

def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
def error(kind,value,trace):
 state['collector_error']=repr(value);save();sys.__excepthook__(kind,value,trace)
sys.excepthook=error
def save():
 temp=path.with_suffix('.tmp');temp.write_text(json.dumps(state,indent=2)+'\n');temp.replace(path)
def run(label,name,memo,report=False):
 global expected
 binary=parent/(name+'-normal');env={k:v for k,v in os.environ.items() if not k.startswith('FERN_')};env.update(FERN_PROFILE='0',FERN_LEAKCHECK='0',FERN_RC_CERTIFY='1',FERN_RC_CERTIFY_MEMO=str(memo))
 if report:env['FERN_IR_VERIFY']='1'
 args=[str(binary),'-target','x86-64-linux','-o',str(out/'opus-reference'),str(fixed/'tests/stdlib/opus_delimited_test.fern'),str(fixed/'internal/stdlib')];d={'label':label,'compiler':name,'args':args,'cwd':str(fixed),'environment':{k:v for k,v in env.items() if k.startswith('FERN_')},'started':datetime.datetime.now(datetime.timezone.utc).isoformat()};state['runs'].append(d);save();start=time.perf_counter_ns()
 with (out/(label+'.stdout')).open('wb') as stdout,(out/(label+'.stderr')).open('wb') as stderr:
  p=subprocess.Popen(args,cwd=fixed,env=env,stdout=stdout,stderr=stderr);pid,status,usage=os.wait4(p.pid,0);p.returncode=os.waitstatus_to_exitcode(status)
 d.update(exit_code=p.returncode,wall_ns=time.perf_counter_ns()-start,cpu_user=usage.ru_utime,cpu_system=usage.ru_stime,peak_rss_kib=usage.ru_maxrss,finished=datetime.datetime.now(datetime.timezone.utc).isoformat());save();assert p.returncode==0
 d['output_sha256']=sha(out/'opus-reference')
 if expected is None:expected=d['output_sha256']
 assert expected==d['output_sha256']
 if report:
  ms=re.findall(r'FERN_RC_CERTIFY: memo hits=(\d+) misses=(\d+)',(out/(label+'.stderr')).read_text());assert ms and all(int(m)==0 for h,m in ms);d.update(cache_hits=sum(int(h) for h,m in ms),cache_misses=0);assert d['cache_hits']>0
 save();print(label,'exit0',flush=True)
save()
for name in ('baseline','candidate'):
 memo=out/('memo-'+('base' if name=='baseline' else 'cand'));run(name+'-prime',name,memo);run(name+'-probe',name,memo,True)
for mode in ('cold','warm'):
 for i in range(3):
  for name in (('baseline','candidate') if i%2==0 else ('candidate','baseline')):run(f'{mode}-{i}-{name}',name,'0' if mode=='cold' else out/('memo-'+('base' if name=='baseline' else 'cand')))
args=[str(out/'opus-reference')];p=subprocess.run(args,capture_output=True,cwd=fixed);(out/'program.stdout').write_bytes(p.stdout);(out/'program.stderr').write_bytes(p.stderr);state['program']={'args':args,'exit_code':p.returncode,'stdout_sha256':sha(out/'program.stdout'),'stderr_sha256':sha(out/'program.stderr')};save();assert p.returncode==0 and b'# fail 0\n' in p.stdout;state['finished']=datetime.datetime.now(datetime.timezone.utc).isoformat();save();print('complete',flush=True)
