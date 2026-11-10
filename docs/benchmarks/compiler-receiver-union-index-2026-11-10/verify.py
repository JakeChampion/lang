from pathlib import Path
import argparse,gzip,hashlib,json,subprocess
p=argparse.ArgumentParser();p.add_argument('--commit');a=p.parse_args();here=Path(__file__).resolve().parent;repo=Path(subprocess.check_output(['git','rev-parse','--show-toplevel'],cwd=here,text=True).strip());prefix=here.relative_to(repo)
def read(name):return subprocess.check_output(['git','show',a.commit+':'+str(prefix/name)],cwd=repo)if a.commit else(here/name).read_bytes()
def load(name):return json.loads(read(name))
sha=lambda b:hashlib.sha256(b).hexdigest();manifest=load('manifest.json')['sha256'];sources=load('source-files.json');compressed=load('compression-identities.json')
for name,digest in manifest.items():assert sha(read(name))==digest,name
for name,r in sources.items():
 raw=gzip.decompress(read(name))if r['compressed']else read(name);assert sha(raw)==r['original_sha256']and len(raw)==r['original_bytes'],name
 if r['compressed']:assert compressed[name]['original_sha256']==sha(raw)
s=load('summary.json');assert s['decision']['decision'].startswith('REJECT')
for section in ['rejected','intermediate']:
 data=s[section];assert len(data['allocation_controls'])==12
 assert all(r['allocs']>0 and r['allocs']==r['frees']and r['live_bytes']==0 for r in data['allocation_controls'])
 for w in ['opus','checker']:
  for mode in ['cold','warm']:assert len(data['timings'][w][mode]['paired_cpu_change_percent'])==8
r=s['rejected']['partial_instruction_control'];assert r['exit_code']==1 and len(r['runs'])==1 and r['runs'][0]['exit_code']==-15
cold=s['rejected']['timings']['checker']['cold'];assert sum(x>0 for x in cold['paired_cpu_change_percent'])==7;assert cold['candidate']['cpu_s']['median']>cold['baseline']['cpu_s']['median']
r=s['retained'];assert r['build']['exit_code']==0 and r['build']['runs'][0]['sha256']==r['build']['runs'][1]['sha256']==r['build']['identical_normal_sha256'];assert r['validation']['exit_code']==r['units']['exit_code']==r['normal_own_source']['exit_code']==r['full_correctness']['exit_code']==r['native_opus']['exit_code']==0
assert r['native_opus']['tap_sha256']=='07c2de202ca09462f396f349cbf99df5b0f044194e9314840de8bf6331c29161'
k=s['kernel'];assert k['ordinary_main_six']['test_passes']==6 and k['negative_slow_timer']['exit_code']==1 and k['fixed']['exit_code']==0;assert all(r['exit_code']==0 for r in k['fixed']['runs'])
old=load('intermediate/manifest-local-final.json');lookup={r['original_path']:r for r in sources.values()}
for name,digest in old['sha256'].items():assert lookup['/workspace/lang-checker-union-index-20261110-1540/build/qualification-main-20261110-1635/'+name]['original_sha256']==digest,name
origin=load('kernel-control/log-origin.json');assert sha(gzip.decompress(read('kernel-control/job.log.gz')))==origin['tool_decoded_sha256']
print(json.dumps({'data_hashes':len(manifest),'original_sources':len(sources),'lossless_gzip_identities':len(compressed),'intermediate_raw_hashes':len(old['sha256']),'decision':'rejected','retained_source':r['source']['retained']if 'source'in r else s['retained']['source']['retained'],'exit_code':0}))
