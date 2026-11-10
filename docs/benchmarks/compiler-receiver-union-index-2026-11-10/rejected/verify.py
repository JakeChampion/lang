from pathlib import Path
import argparse,gzip,hashlib,json,subprocess
p=argparse.ArgumentParser();p.add_argument('--commit');a=p.parse_args();here=Path(__file__).resolve().parent;repo=Path(subprocess.check_output(['git','rev-parse','--show-toplevel'],cwd=here,text=True).strip());prefix=here.relative_to(repo)
def read(name):return subprocess.check_output(['git','show',a.commit+':'+str(prefix/name)],cwd=repo)if a.commit else(here/name).read_bytes()
def load(name):return json.loads(read(name))
sha=lambda b:hashlib.sha256(b).hexdigest();manifest=load('manifest.json')['sha256']
for name,digest in manifest.items():assert sha(read(name))==digest,name
compressed=load('compression-identities.json');sources=load('source-files.json')
for name,row in sources.items():
 raw=gzip.decompress(read(name))if row['compressed']else read(name);assert sha(raw)==row['original_sha256']and len(raw)==row['original_bytes'],name
 if row['compressed']:assert compressed[name]['original_sha256']==sha(raw)
s=load('summary.json');current=s['current'];assert len(current['instruction_controls'])==8;assert len(current['allocation_controls'])==12;assert len(current['cache_probes'])==4
for w in ['opus','checker']:
 for mode in ['cold','warm']:
  for side in ['baseline','candidate']:assert current['timings'][w][mode][side]['cpu_s']['median']>0
  assert len(current['timings'][w][mode]['paired_cpu_change_percent'])==8
 ir=[r for r in current['instruction_controls']if r['workload']==w];assert len(ir)==4 and all(r['exit_code']==0 and r['output_sha256']==current['outputs'][w]for r in ir)
 assert current['program_controls'][w]['exit_code']==0
assert all(r['allocs']>0 and r['allocs']==r['frees']and r['live_bytes']==0 for r in current['allocation_controls'])
assert all(r['cache_hits']>0 and r['cache_misses']==0 for r in current['cache_probes'])
assert current['program_controls']['opus']['stdout_sha256']=='07c2de202ca09462f396f349cbf99df5b0f044194e9314840de8bf6331c29161'
# Check all retained intermediate raw identities against their earlier local manifest.
old=load('intermediate/manifest-local-final.json');by_original={r['original_path']:(name,r)for name,r in sources.items()}
for rel,digest in old['sha256'].items():
 original='/workspace/lang-checker-union-index-20261110-1540/build/qualification-main-20261110-1635/'+rel;name,row=by_original[original];assert row['original_sha256']==digest,rel
k=s['kernel'];assert k['ordinary_main_six']['test_passes']==6 and k['ordinary_main_six']['test_failures']==0;assert k['negative_slow_timer']['exit_code']==1;assert k['fixed']['exit_code']==0 and all(r['exit_code']==0 for r in k['fixed']['runs'])
origin=load('kernel-control/log-origin.json');job=read('kernel-control/job.log.gz');assert sha(gzip.decompress(job))==origin['tool_decoded_sha256']
print(json.dumps({'data_hashes':len(manifest),'original_sources':len(sources),'raw_gzip_identities':len(compressed),'intermediate_raw_hashes':len(old['sha256']),'current_instruction_runs':8,'current_allocation_runs':12,'kernel_fixed_positive_tests':13,'exit_code':0}))
