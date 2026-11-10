from pathlib import Path
import json, hashlib, shutil, gzip, subprocess, statistics, datetime, re
root=Path('/workspace/lang-interpreter-dispatch-20261109')
src=root/'build/interpreter-main-qualification-20261110-0230'
dst=root/'docs/benchmarks/compiler-interpreter-index-upstream-2026-11-10'
fixed=Path('/workspace/lang-interpreter-timeout-20261109')
assert not dst.exists()
def read(p):return json.loads(p.read_text())
def sha(data):return hashlib.sha256(data).hexdigest()
def dump(p,d):p.parent.mkdir(parents=True,exist_ok=True);p.write_text(json.dumps(d,indent=2)+'\n')
prepare=read(src/'prepare.json');paired=read(src/'subsets/results.json');comp=read(src/'compilation/results.json');full=read(src/'full/results.json')
assert read(src/'collector-result.json')['exit_code']==0
for d in (prepare,paired,comp,full):assert d.get('finished') and not d.get('collector_error')
for d in (paired,comp,full):
 assert d['candidate_source']==prepare['candidate_source'] and d['baseline_source']==prepare['baseline_source']
 assert all(r.get('exit_code')==0 for r in d['runs'])
assert len(paired['runs'])==20 and len(comp['runs'])==16 and len(full['runs'])==1
validation=root/'build/interpreter-main-integration-20261110-0220'
assert read(validation/'tests-result.json')['exit_code']==0
assert read(validation/'source-check-result.json')['exit_code']==0
tests=(validation/'tests.log').read_text()
assert len(re.findall(r'^\s*--- PASS:',tests,re.M))==387
assert not re.search(r'^\s*--- (SKIP|FAIL):',tests,re.M)
for group in ('first','second'):
 rows=[r for r in paired['runs'] if r['label'].startswith(group+'-')]
 assert len({r['stdout_sha256'] for r in rows})==1
 for r in rows:
  if '-count-' in r['label']:assert r['allocs']==r['frees'] and r['live_bytes']==0
assert len({r['output_sha256'] for r in comp['runs']})==1
assert comp['program']['exit_code']==0 and comp['program']['stdout_sha256']==full['runs'][0]['stdout_sha256']
probes=[r for r in comp['runs'] if r['label'].endswith('-probe')]
assert len(probes)==2 and all(r['cache_hits']>0 and r['cache_misses']==0 for r in probes)
for source,expected in [('/workspace/lang-interpreter-qualified-20261110-0230',prepare['candidate_source']),('/workspace/lang-interpreter-main-base-20261110-0220',prepare['baseline_source'])]:
 assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=source,text=True).strip()==expected
 assert not subprocess.check_output(['git','status','--porcelain','--untracked-files=no'],cwd=source,text=True)
dst.mkdir()
compressed={}
def retain(source,target):
 raw=source.read_bytes();out=dst/target;out.parent.mkdir(parents=True,exist_ok=True)
 if any(line.endswith((b' ',b'\t',b'\r')) for line in raw.split(b'\n')):
  out=out.with_name(out.name+'.gz');out.write_bytes(gzip.compress(raw,mtime=0))
  compressed[str(out.relative_to(dst))]={'original_path':str(source),'original_sha256':sha(raw),'original_bytes':len(raw)}
 else:out.write_bytes(raw)
for name in ('prepare.py','measure.py','compile-controls.py','run.sh','archive.py','prepare.json','prepare.log','subsets.log','compilation.log','full.log','parent.log','collector-result.json','identities.json','source-audit.json','baseline-build-controller.py','baseline-build-record.json','baseline-build.stdout','baseline-build.stderr','baseline-plex.stdout','baseline-plex.stderr'):
 retain(src/name,Path(name))
for p in sorted(src.glob('*-build.std*')):retain(p,Path(p.name))
for mode in ('subsets','compilation','full'):
 for p in sorted((src/mode).iterdir()):
  if p.is_file() and (p.suffix in ('.json','.stdout','.stderr')):retain(p,Path(mode)/p.name)
for name in ('first-group.fern','second-group.fern'):
 retain(fixed/'build/interpreter-profile-20261109'/name,Path('inputs')/(name+'.txt'))
for name in ('opus_delimited_test.fern','opus_delimited_vectors.fern'):
 retain(fixed/'tests/stdlib'/name,Path('inputs')/(name+'.txt'))
for name in ('tests.sh','tests.log','tests-result.json','source-check.sh','source-check.log','source-check-result.json'):
 retain(validation/name,Path('validation')/name)
patch=subprocess.check_output(['git','diff',prepare['baseline_source'],prepare['candidate_source'],'--','compiler','internal/testing/e2ecompiler'],cwd=root)
(dst/'implementation.patch.gz').write_bytes(gzip.compress(patch,mtime=0));compressed['implementation.patch.gz']={'original_sha256':sha(patch),'original_bytes':len(patch)}
def stats(rows):
 return {'samples':len(rows),'wall_median_s':statistics.median(r['wall_ns']/1e9 for r in rows),'wall_range_s':[min(r['wall_ns']/1e9 for r in rows),max(r['wall_ns']/1e9 for r in rows)],'cpu_median_s':statistics.median(r['cpu_user']+r['cpu_system'] for r in rows),'peak_rss_median_kib':statistics.median(r['peak_rss_kib'] for r in rows)}
summary={}
for group,n in [('first',5),('second',3)]:
 data={name:stats([r for r in paired['runs'] if r['compiler']==name and r['label'].startswith(group+'-') and '-count-' not in r['label']]) for name in ('baseline','candidate')}
 deltas=[]
 for i in range(n):
  rows={r['compiler']:r for r in paired['runs'] if r['label'].startswith(f'{group}-{i}-')}
  b=rows['baseline']['cpu_user']+rows['baseline']['cpu_system'];c=rows['candidate']['cpu_user']+rows['candidate']['cpu_system'];deltas.append((c/b-1)*100)
 data['paired_cpu_change_percent']=deltas
 data['allocations']={r['compiler']:{k:r[k] for k in ('allocs','frees','live_bytes')} for r in paired['runs'] if r['label'].startswith(group+'-count-')}
 summary[group]=data
for mode in ('cold','warm'):
 summary[mode]={name:stats([r for r in comp['runs'] if r['compiler']==name and r['label'].startswith(mode+'-')]) for name in ('baseline','candidate')}
summary.update(full=full['runs'][0],program=comp['program'],cache_probes=probes)
dump(dst/'summary.json',summary)
dump(dst/'compression-identities.json',compressed)
dump(dst/'manifest.json',{'sha256':{str(p.relative_to(dst)):sha(p.read_bytes()) for p in sorted(dst.rglob('*')) if p.is_file() and p.name!='manifest.json'}})
print(json.dumps({'archive':str(dst),'data_hashes':len(read(dst/'manifest.json')['sha256']),'compressed_records':len(compressed),'summary':summary},indent=2))
