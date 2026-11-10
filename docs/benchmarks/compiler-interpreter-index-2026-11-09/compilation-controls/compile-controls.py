from pathlib import Path
import os,subprocess,json,hashlib,datetime,time,re
root=Path('/workspace/lang-interpreter-index-20261109');fixed=Path('/workspace/lang-interpreter-timeout-20261109');out=root/'build/interpreter-index-compile-controls-20261109';out.mkdir();path=out/'samples.json';binaries={'base':fixed/'build/interpreter-profile-20261109/fern-normal','cand':root/'build/interpreter-index-qualified-20261109/fern-index'};state={'note':'Complete native compilation control for an interpreter-only change. Same fixed checker source and imports. Normal certification, no debug variables, CPU/allocation profiles off. Three alternating pairs per mode. A guest-instruction collector and other controls can overlap. No speedup claim from this control.','baseline_source':'ebb50c78c0ca714180704352881da7e39efb570f','candidate_source':'db4308a127fa82a0a6cf45f1c86851a709f010d4','input_source':'ebb50c78c0ca714180704352881da7e39efb570f','runs':[],'binary_sha256':{k:hashlib.sha256(v.read_bytes()).hexdigest() for k,v in binaries.items()}};expected=None

def save():
 temp=path.with_suffix('.tmp');temp.write_text(json.dumps(state,indent=2)+'\n');temp.replace(path)
def run(label,name,memo,report=False):
 global expected
 env={k:v for k,v in os.environ.items() if not k.startswith('FERN_')};env.update(FERN_PROFILE='0',FERN_LEAKCHECK='0',FERN_RC_CERTIFY='1',FERN_RC_CERTIFY_MEMO=str(memo))
 if report:env['FERN_IR_VERIFY']='1'
 args=[str(binaries[name]),'-target','x86-64-linux','-o',str(out/'checker'),str(fixed/'compiler/checker.fern'),str(fixed/'internal/stdlib')];d={'label':label,'compiler':name,'args':args,'cwd':str(fixed),'environment':{k:v for k,v in env.items() if k.startswith('FERN_')},'started':datetime.datetime.now(datetime.timezone.utc).isoformat()};state['runs'].append(d);save();start=time.perf_counter_ns()
 with (out/(label+'.stdout')).open('wb') as stdout,(out/(label+'.stderr')).open('wb') as stderr:
  p=subprocess.Popen(args,cwd=fixed,env=env,stdout=stdout,stderr=stderr);pid,status,usage=os.wait4(p.pid,0);p.returncode=os.waitstatus_to_exitcode(status)
 d.update(exit_code=p.returncode,wall_ns=time.perf_counter_ns()-start,cpu_user=usage.ru_utime,cpu_system=usage.ru_stime,peak_rss_kib=usage.ru_maxrss,finished=datetime.datetime.now(datetime.timezone.utc).isoformat());assert p.returncode==0;d['output_sha256']=hashlib.sha256((out/'checker').read_bytes()).hexdigest()
 if expected is None:expected=d['output_sha256']
 assert expected==d['output_sha256']
 if report:
  hits=re.findall(r'FERN_RC_CERTIFY: memo hits=(\d+) misses=(\d+)',(out/(label+'.stderr')).read_text());assert hits and sum(int(h) for h,m in hits)>0 and all(int(m)==0 for h,m in hits);d['cache_hits']=sum(int(h) for h,m in hits);d['cache_misses']=0
 save();print(label+' exit0',flush=True)
save()
for name in ('base','cand'):
 memo=out/('memo-'+name);run(name+'-prime',name,memo);run(name+'-probe',name,memo,True)
for mode in ('cold','warm'):
 for pair in range(3):
  for name in (('base','cand') if pair%2==0 else ('cand','base')):run(f'{mode}-{pair}-{name}',name,'0' if mode=='cold' else out/('memo-'+name))
state['finished']=datetime.datetime.now(datetime.timezone.utc).isoformat();save();print('complete',flush=True)
