from pathlib import Path
import hashlib,json,platform,re,statistics,subprocess,time
root=Path('/workspace/lang-record-field-leaves-current')
out=Path('/workspace/scratch/lang-record-field-leaves/paired-kernel');out.mkdir(exist_ok=True)
cli=Path('/workspace/scratch/lang-record-field-leaves/final-validation/bootstrap/stage3')
baseline=Path('/workspace/scratch/lang-record-field-leaves/kernel-baseline')
candidate=out/'candidate'
compiled=subprocess.run([str(cli),'-target','x86-64-linux','-o',str(candidate),str(root/'docs/benchmarks/simd-evaluators-2026-10-21/kernel.fern'),str(root/'internal/stdlib')],capture_output=True,text=True)
(out/'candidate-build.log').write_text(compiled.stdout+compiled.stderr)
compiled.check_returncode()
runs=[]
for pair in range(4):
 for name,binary in ([('baseline',baseline),('candidate',candidate)] if pair%2==0 else [('candidate',candidate),('baseline',baseline)]):
  started=time.monotonic();r=subprocess.run([str(binary)],capture_output=True,text=True);r.check_returncode()
  (out/f'{pair}-{name}.stdout').write_text(r.stdout);(out/f'{pair}-{name}.stderr').write_text(r.stderr)
  rows=[]
  for line in r.stdout.splitlines():
   m=re.fullmatch(r'(.+): 20 iters [0-9]+ ops/s p50=([0-9]+)ns p99=([0-9]+)ns p99.9=([0-9]+)ns allocs=([0-9]+) fresh_bytes=([0-9]+)',line)
   if not m:raise ValueError(line)
   label,p50,p99,p999,allocs,fresh=m.groups()
   allocs=int(allocs)
   expected={'f64':0,'Dual[f64]':8400000,'DoubleDouble[f64]':129200000}[label] if name=='baseline' else 0
   assert allocs==expected,(name,label,allocs,expected)
   rows.append({'label':label,'p50_ns':int(p50),'p99_ns':int(p99),'p999_ns':int(p999),'allocations':allocs,'allocations_per_element':allocs/400000,'fresh_bytes':int(fresh)})
  assert len(rows)==3
  runs.append({'pair':pair,'variant':name,'seconds':time.monotonic()-started,'rows':rows})
summary={}
for label in ['f64','Dual[f64]','DoubleDouble[f64]']:
 med={name:statistics.median(row['p50_ns'] for run in runs if run['variant']==name for row in run['rows'] if row['label']==label) for name in ['baseline','candidate']}
 summary[label]={'baseline_median_p50_ns':med['baseline'],'candidate_median_p50_ns':med['candidate'],'candidate_over_baseline':med['candidate']/med['baseline']}
report={'host':platform.uname()._asdict(),'method':'Four pairs, alternate order; each executable performs three warmups and twenty timed iterations over 20000 elements. Timings apply to this host only.','source':str(root/'docs/benchmarks/simd-evaluators-2026-10-21/kernel.fern'),'source_sha256':hashlib.sha256((root/'docs/benchmarks/simd-evaluators-2026-10-21/kernel.fern').read_bytes()).hexdigest(),'candidate_compiler_sha256':hashlib.sha256(cli.read_bytes()).hexdigest(),'binary_sha256':{name:hashlib.sha256(binary.read_bytes()).hexdigest() for name,binary in [('baseline',baseline),('candidate',candidate)]},'runs':runs,'summary':summary}
(out/'report.json').write_text(json.dumps(report,indent=2)+'\n')
print(json.dumps(summary,indent=2))
