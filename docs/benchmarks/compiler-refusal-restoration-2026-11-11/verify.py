from pathlib import Path
import argparse,subprocess,json,hashlib,gzip
p=argparse.ArgumentParser();p.add_argument('--commit',default='HEAD');p.add_argument('--worktree',action='store_true');a=p.parse_args();root=Path(__file__).resolve().parents[3];folder=Path(__file__).resolve().parent.relative_to(root);sha=lambda b:hashlib.sha256(b).hexdigest()
def git(*args):return subprocess.check_output(['git',*args],cwd=root)
def read(path):return(root/path).read_bytes()if a.worktree else git('show',a.commit+':'+str(path))
def data(path):return read(folder/path)
m=json.loads(data('manifest.json'));s=json.loads(data('source-identities.json'));q=json.loads(data('qualification.json'))
for name,h in m['data_sha256'].items():assert sha(data(name))==h,name
for r in m['gzip_identities']:
 b=gzip.decompress(data(r['file']));assert len(b)==r['raw_bytes']and sha(b)==r['raw_sha256'],r['file']
assert m['main']==s['main']=='eb264b32a2775d534109f244b83dabe57258949e';assert m['concurrent_parent']==s['concurrent_parent']=='4e8cbe42176f1be18790297ea0dc83ae5aafb1ad';assert m['published_source']==s['published_source']=='34229724c031302b5147b5132333de7e2397579e'
assert subprocess.run(['git','merge-base','--is-ancestor',s['concurrent_parent'],a.commit],cwd=root).returncode==0
for name,h in s['main_product_trees'].items():
 if a.worktree:assert not git('diff',s['main'],'--',name),name
 else:assert git('rev-parse',a.commit+':'+name).decode().strip()==h,name
assert sha(read(Path(s['harness_path'])))==s['harness_sha256'];assert read(Path(s['harness_path']))==git('show',s['published_source']+':'+s['harness_path'])
for foldername,count,gzcount in [('compiler-checker-cliff-ci-2026-11-11',31,20),('compiler-main-rollback-ci-2026-11-11',72,63)]:
 f=Path('docs/benchmarks')/foldername;old=json.loads(read(f/'manifest.json'));assert len(old['data_sha256'])==count and len(old['gzip_identities'])==gzcount
 for name,h in old['data_sha256'].items():assert sha(read(f/name))==h,(foldername,name)
 for r in old['gzip_identities']:
  b=gzip.decompress(read(f/r['file']));assert len(b)==r['raw_bytes']and sha(b)==r['raw_sha256']
 assert read(f/'verify.py')==git('show',s['published_source']+':'+str(f/'verify.py'))
restored=json.loads(data('restored-files.json'));assert len(restored)==33
for r in restored:assert sha(read(Path(r['path'])))==r['sha256']and git('rev-parse',s['published_source']+':'+r['path']).decode().strip()==r['git_blob']
assert sha(read(Path('.github/cliff-baseline.txt')))==s['cliff_baseline_sha256'];assert read(Path('.github/cliff-baseline.txt'))==git('show',s['published_source']+':.github/cliff-baseline.txt')
assert sha(read(Path('.github/selfhost-driver-sizes.txt')))==s['driver_baseline_sha256']and read(Path('.github/selfhost-driver-sizes.txt'))==git('show',s['main']+':.github/selfhost-driver-sizes.txt')
assert sha(read(Path('bootstrap/stage0.lock')))==s['producer_lock_sha256']
first=json.loads(data('validation/result.json'));resume=json.loads(data('validation-resume/result.json'));audit=json.loads(data('fixture-domain-correction.json'))
assert first['exit_code']==1 and resume['exit_code']==0 and all(r['exit_code']==0 for d in[first,resume]for r in d['runs'])
assert audit['fixture_go_command_exit']==0 and audit['fixture_passes']==16 and audit['fixture_skips']==audit['passed_commands_repeated']==audit['first_native_workload_commands_executed']==0
native=json.loads(data('native-v2/result.json'));assert native['exit_code']==0 and all(r['exit_code']==r.get('expected_exit',0)for r in native['runs'])
assert q['passes']==sum(r.get('passes',0)for d in[first,resume,native]for r in d['runs'])and q['skips']==q['passed_commands_repeated']==0
assert native['normal_sha256']==native['baseline_normal_sha256']==q['normal_sha256']
assert native['stage2_sha256']==native['stage3_sha256']==q['stage2_sha256']
assert q['full_tap_sha256']==sha(gzip.decompress(data('native-v2/opus-program.stdout.gz')))=='07c2de202ca09462f396f349cbf99df5b0f044194e9314840de8bf6331c29161'
negative=[r for r in native['runs']if r.get('expected_exit')==1];assert len(negative)==6
for r in negative:
 label=r['label'];code='E010'if 'e010'in label else'E091';message=(gzip.decompress(data('native-v2/'+label+'.stdout.gz'))+gzip.decompress(data('native-v2/'+label+'.stderr.gz'))).decode();assert 'error['+code+']:'in message and 'error: internal:'not in message
print('Verified',len(m['data_sha256']),'data hashes;',len(m['gzip_identities']),'raw gzip identities; both restored archive hash sets;',q['passes'],'test/subtest passes.')
