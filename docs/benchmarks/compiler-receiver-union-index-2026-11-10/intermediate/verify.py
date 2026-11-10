from pathlib import Path
import argparse,gzip,hashlib,json,subprocess
p=argparse.ArgumentParser();p.add_argument('--commit');a=p.parse_args();here=Path(__file__).resolve().parent;repo=Path(subprocess.check_output(['git','rev-parse','--show-toplevel'],cwd=here,text=True).strip());prefix=here.relative_to(repo)
def read(name):
 return subprocess.check_output(['git','show',a.commit+':'+str(prefix/name)],cwd=repo)if a.commit else(here/name).read_bytes()
def load(name):return json.loads(read(name))
sha=lambda b:hashlib.sha256(b).hexdigest();manifest=load('manifest.json')['sha256']
for name,digest in manifest.items():assert sha(read(name))==digest,name
identities=load('compression-identities.json')
for name,r in identities.items():
 raw=gzip.decompress(read(name));assert sha(raw)==r['original_sha256']and len(raw)==r['original_bytes'],name
s=load('summary.json');assert len(s['instruction_controls'])==8 and all(r['exit_code']==0 for r in s['instruction_controls'])
for w in ['opus','checker']:
 for mode in ['cold','warm']:
  for side in ['baseline','candidate']:assert s['measurements'][w][mode][side]['cpu_s']['median']>0
  assert len(s['measurements'][w][mode]['paired_cpu_change_percent'])==8
 rows=[r for r in s['instruction_controls']if r['workload']==w];assert len({r['output_sha256']for r in rows})==1 and rows[0]['output_sha256']==s['native_outputs'][w]
 assert s['program_controls'][w]['exit_code']==0
assert len(s['allocation_controls'])==12
assert all(r['allocs']>0 and r['allocs']==r['frees']and r['live_bytes']==0 for r in s['allocation_controls'])
assert s['program_controls']['opus']['stdout_sha256']=='07c2de202ca09462f396f349cbf99df5b0f044194e9314840de8bf6331c29161'
print(json.dumps({'data_hashes':len(manifest),'raw_identities':len(identities),'instruction_runs':8,'allocation_runs':12,'outputs':'equal','exit_code':0}))
