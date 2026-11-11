#!/usr/bin/env python3
import argparse,gzip,hashlib,io,json,subprocess,tarfile
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--commit');a=p.parse_args()
here=Path(__file__).resolve().parent;root=Path(subprocess.check_output(['git','rev-parse','--show-toplevel'],cwd=here,text=True).strip());rel=here.relative_to(root)
def read(name):
 return subprocess.check_output(['git','show',a.commit+':'+str(rel/name)],cwd=root)if a.commit else(here/name).read_bytes()
def product(name,commit=None):
 return subprocess.check_output(['git','show',(commit or a.commit)+':'+name],cwd=root)if(commit or a.commit)else(root/name).read_bytes()
sha=lambda b:hashlib.sha256(b).hexdigest()
m=json.loads(read(Path('manifest.json')))
for n,h in m['data_sha256'].items():assert sha(read(Path(n)))==h,n
for n,r in m['gzip_raw_identities'].items():
 b=gzip.decompress(read(Path(n)));assert len(b)==r['bytes']and sha(b)==r['sha256'],n
for n,h in m['publication_files'].items():assert sha(product(n))==h,n
for n,h in m['publication_trees'].items():
 if a.commit:assert subprocess.check_output(['git','rev-parse',a.commit+':'+n],cwd=root,text=True).strip()==h,n
 else:subprocess.run(['git','diff','--exit-code',m['source'],'--',n],cwd=root,check=True,stdout=subprocess.DEVNULL)
sources=json.loads(read(Path('source-identities.json')))
for n,r in sources['files'].items():
 b=product(n,sources['source']);assert sha(b)==r['sha256']
 assert hashlib.sha1(b'blob '+str(len(b)).encode()+b'\0'+b).hexdigest()==r['git_blob']
 assert product(n)==b,n
moves=json.loads(read(Path('row-moves.json')));assert len(moves['rows'])==4 and moves['default_tolerance_percent']==1
for file in ['alloc-baseline.txt','cliff-baseline.txt']:
 old=gzip.decompress(read(Path('before-'+file+'.gz')));assert old==product('.github/'+file,moves['source'])
 def rows(b):return {x.split()[0]:(int(x.split()[1]),x.split()[2:])for x in b.decode().splitlines()if x.strip()and not x.lstrip().startswith('#')}
 left,right=rows(old),rows(product('.github/'+file));assert left.keys()==right.keys()
 for key in left:
  assert left[key][1]==right[key][1]
  record=next(x for x in moves['rows']if x['key']==key);assert(left[key][0],right[key][0])==(record['old'],record['new'])
v=json.loads(read(Path('validation.json')));assert v['exit_code']==0 and v['captured_comparator_children']==22
assert b'22 cases pass' in read(Path('canonical-negative-controls.stdout'))
for n,exitcode in [('alloc-old-gate',1),('cliff-old-gate',1),('alloc-new-gate',0),('cliff-new-gate',0)]:
 assert next(x for x in v['runs']if x['label']==n)['exit_code']==exitcode
with tarfile.open(fileobj=io.BytesIO(read(Path('negative-controls.tar.gz'))),mode='r:gz')as t:
 contents={}
 for member in t.getmembers():
  if member.isfile():contents[member.name]=t.extractfile(member).read()
 expected=json.loads(read(Path('negative-controls-members.json')));assert set(contents)==set(expected)
 for name,h in expected.items():assert sha(contents[name])==h,name
 statuses=[json.loads(b)['status']for n,b in contents.items()if n.endswith('/result.json')]
 assert len(statuses)==22 and 0 in statuses and 1 in statuses
rep=json.loads(read(Path('fixture-repro.json')));assert rep['command_exit_code']==1 and rep['skips']==0
fix=json.loads(read(Path('fixture-validation.json')));assert fix['exit_code']==0
tests=next(x for x in fix['runs']if x['label']=='indirect-contracts');assert tests['test_subtest_passes']==17 and tests['skips']==0 and tests['all_original_cases_present']
print(json.dumps({'data_hashes':len(m['data_sha256']),'gzip_identities':len(m['gzip_raw_identities']),'negative_control_children':22,'negative_control_members':len(expected),'original_fixture_cases':15,'fixture_test_results':17,'verified':True}))
