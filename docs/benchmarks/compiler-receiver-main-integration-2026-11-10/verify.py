from pathlib import Path
import argparse,json,gzip,hashlib,subprocess
p=argparse.ArgumentParser();p.add_argument('--commit');a=p.parse_args()
root=Path(__file__).resolve().parent;repo=Path(subprocess.check_output(['git','rev-parse','--show-toplevel'],cwd=root,text=True).strip());prefix=root.relative_to(repo)
def raw(n):return subprocess.check_output(['git','show',a.commit+':'+str(prefix/n)],cwd=repo)if a.commit else(root/n).read_bytes()
def load(n):return json.loads(raw(n))
sha=lambda b:hashlib.sha256(b).hexdigest()
m=load('manifest.json')['sha256'];sources=load('source-files.json');c=load('compression-identities.json')
for n,h in m.items():assert sha(raw(n))==h,n
for n,r in sources.items():
 b=gzip.decompress(raw(n))if r['compressed']else raw(n);assert len(b)==r['original_bytes']and sha(b)==r['original_sha256'],n
 if r['compressed']:assert c[n]['original_sha256']==sha(b)
s=load('summary.json')
assert s['native']['test_subtest_passes']==248 and s['units']['test_subtest_passes']==2867 and s['formatter']['test_subtest_passes']==1
assert s['entropy_after']['test_subtest_passes']==58
for n in ['native','units','formatter','entropy_after']:assert s[n]['skips']==0 and not s[n]['failures']
want={'TestSelfHostEntropyAllocationCapacity/x86-64-linux/-1','TestSelfHostEntropyAllocationCapacity/arm64-linux/-1'}
assert {x for x in s['entropy_before']['failures']if x and x.count('/')==2}==want
b=s['build'];assert b['exit_code']==0 and b['runs'][0]['sha256']==b['runs'][1]['sha256']==b['identical_normal_sha256']=='76f026a1ffd7f056d6e862d1dd4f514d4077abc5c9a99c692ebafc7444cf3b9c'
assert all(x['exit_code']==0 for x in s['control']['runs'])and s['control']['exit_code']==0
assert s['native_opus']['exit_code']==s['own_source']['exit_code']==0 and s['native_opus']['tap_sha256']=='07c2de202ca09462f396f349cbf99df5b0f044194e9314840de8bf6331c29161'
for name,identity in s['historical_ci_logs'].items():
 b=gzip.decompress(raw('historical-main-red/'+name));assert len(b)==identity['tool_decoded_utf8_bytes']and sha(b)==identity['tool_decoded_sha256']
for revision in [s['integration'],s['retained']]:assert not subprocess.check_output(['git','diff','--name-only',s['main'],revision,'--','compiler','internal/stdlib'],cwd=repo)
for name,h in s['source_audit']['files'].items():assert sha(subprocess.check_output(['git','show',s['retained']+':'+name],cwd=repo))==h
def events(name):return[json.loads(x)for x in gzip.decompress(raw(name)).decode().splitlines()if x.strip()]
es=events('entropy-fixed/repro.jsonl.gz')
for target in ['x86-64-linux','arm64-linux']:
 for value in ['-1','-2147483648']:assert any(x.get('Action')=='pass'and x.get('Test')=='TestSelfHostEntropyAllocationCapacity/'+target+'/'+value for x in es)
print(json.dumps({'data_hashes':len(m),'source_records':len(sources),'lossless_gzip_identities':len(c),'native_test_passes':248,'entropy_test_passes':58,'reproduced_negative_failures':2,'exit_code':0}))
