from pathlib import Path
import hashlib,json,os,platform,statistics,subprocess,time
root=Path('/workspace/lang-defer-capture-cells-current');base=Path('/workspace/scratch/lang-defer-capture-cells');out=base/'measure-final';out.mkdir(exist_ok=True)
old=Path('/workspace/scratch/lang-record-field-leaves/baseline-bootstrap/stage3');new=base/'final-fresh-loop-validation/bootstrap/stage3'
affinity=sorted(os.sched_getaffinity(0));os.sched_setaffinity(0,{affinity[0]})
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
def source_hash():
 h=hashlib.sha256()
 for name in sorted(subprocess.check_output(['git','ls-files','compiler','internal/stdlib'],cwd=root,text=True).splitlines()):h.update(name.encode()+b'\0'+(root/name).read_bytes()+b'\0')
 return h.hexdigest()
expected=json.loads((base/'final-fresh-loop-validation/bootstrap/bootstrap.json').read_text())['source_sha256'];assert source_hash()==expected
new_prog=out/'escaping-candidate'
b=subprocess.run([str(new),'-O','-target','x86-64-linux','-o',str(new_prog),str(base/'escaping.fern'),str(root/'internal/stdlib')],capture_output=True,text=True);(out/'candidate-build.log').write_text(b.stdout+b.stderr);b.check_returncode()
mapruns=[]
for n in [25000,50000,100000]:
 for pair in range(4):
  variants=[('baseline',base/'escaping-baseline'),('candidate',new_prog)]
  if pair%2:variants.reverse()
  for name,binary in variants:
   started=time.perf_counter();r=subprocess.run([str(binary),str(n)],capture_output=True,text=True);seconds=time.perf_counter()-started;r.check_returncode();allocations=int(r.stdout.strip())
   if name=='candidate':assert allocations<=32,(n,allocations)
   else:assert allocations>2*n,(n,allocations)
   row={'n':n,'pair':pair,'variant':name,'seconds':seconds,'allocations_during_build':allocations,'stdout':r.stdout,'stderr':r.stderr};mapruns.append(row);print('map',n,pair,name,round(seconds,6),allocations,flush=True)
map_summary={str(n):{name:{'median_seconds':statistics.median(r['seconds'] for r in mapruns if r['n']==n and r['variant']==name),'allocations':sorted(set(r['allocations_during_build'] for r in mapruns if r['n']==n and r['variant']==name))} for name in ['baseline','candidate']} for n in [25000,50000,100000]}
report={'host':platform.uname()._asdict(),'affinity_cpu':affinity[0],'source_sha256':expected,'compiler_sha256':{'baseline':sha(old),'candidate':sha(new)},'program_source_sha256':sha(base/'escaping.fern'),'program_binary_sha256':{'baseline':sha(base/'escaping-baseline'),'candidate':sha(new_prog)},'map_runs':mapruns,'map_summary':map_summary,'compiler_runs':[]}
(out/'report.json').write_text(json.dumps(report,indent=2)+'\n')
for pair in range(4):
 variants=[('baseline',old),('candidate',new)]
 if pair%2:variants.reverse()
 for name,compiler in variants:
  dest=out/(name+'-compiler-output');started=time.perf_counter()
  r=subprocess.run([str(compiler),'-target','x86-64-linux','-o',str(dest),str(root/'compiler/fern.fern'),str(root/'internal/stdlib')],capture_output=True,text=True);elapsed=time.perf_counter()-started
  (out/f'compiler-{pair}-{name}.log').write_text(r.stdout+r.stderr);r.check_returncode()
  binary_sha=sha(dest)
  if name=='candidate':assert binary_sha==sha(new),(binary_sha,sha(new))
  row={'pair':pair,'variant':name,'seconds':elapsed,'output_sha256':binary_sha,'output_bytes':dest.stat().st_size};report['compiler_runs'].append(row)
  print('compiler',pair,name,round(elapsed,6),flush=True);(out/'report.json').write_text(json.dumps(report,indent=2)+'\n')
report['compiler_summary']={name:statistics.median(r['seconds'] for r in report['compiler_runs'] if r['variant']==name) for name in ['baseline','candidate']}
report['compiler_candidate_over_baseline']=report['compiler_summary']['candidate']/report['compiler_summary']['baseline']
assert source_hash()==expected
(out/'report.json').write_text(json.dumps(report,indent=2)+'\n');print(json.dumps({'maps':map_summary,'compiler':report['compiler_summary'],'compiler_ratio':report['compiler_candidate_over_baseline']},indent=2),flush=True)
