from pathlib import Path
import argparse,subprocess,json,hashlib,gzip
p=argparse.ArgumentParser();p.add_argument('--commit',default='HEAD');p.add_argument('--worktree',action='store_true');a=p.parse_args()
root=Path(__file__).resolve().parents[3];folder=Path(__file__).resolve().parent.relative_to(root)
sha=lambda b:hashlib.sha256(b).hexdigest()
def git(*args):return subprocess.check_output(['git',*args],cwd=root)
def read(name):return(root/name).read_bytes()if a.worktree else git('show',a.commit+':'+str(name))
def data(name):return read(folder/name)
m=json.loads(data('manifest.json'))
for name,h in m['data_sha256'].items():assert sha(data(name))==h,name
for row in m['gzip_identities']:
 b=gzip.decompress(data(row['file']));assert len(b)==row['raw_bytes']and sha(b)==row['raw_sha256'],row['file']
source=json.loads(data('source-identities.json'));move=json.loads(data('row-move.json'));v=json.loads(data('validation.json'));reuse=json.loads(data('negative-controls-reuse.json'))
assert source['source']==move['source']==m['source']=='2ca148c6757a95ba419a67751dd3598726f07c56'
for name,h in source['product_trees'].items():assert git('rev-parse',a.commit+':'+name).decode().strip()==h,name
for name,entry in source['protocol_files'].items():
 b=read(Path(name));assert sha(b)==entry['sha256'],name
 assert b==gzip.decompress(data('protocol/'+name+'.gz')),name
before=gzip.decompress(data('before-cliff-baseline.txt.gz'));after=gzip.decompress(data('after-cliff-baseline.txt.gz'))
assert read(Path('.github/cliff-baseline.txt'))==after and sha(after)==m['published_baseline_sha256']
def table(b):
 return{r[0]:(int(r[1]),r[2:])for line in b.decode().splitlines()if line.strip()and not line.lstrip().startswith('#')for r in[line.split()]}
old,new=table(before),table(after);assert old.keys()==new.keys()
assert[k for k in old if old[k]!=new[k]]==['x86_64/checker.cliff_count']
assert old['x86_64/checker.cliff_count']==(373694,[])and new['x86_64/checker.cliff_count']==(378075,[])
for k in old:assert old[k][1]==new[k][1]
assert all(line in after.decode().splitlines()for line in before.decode().splitlines()if line.startswith('#'))
assert data('ci-corrective.report')==b'x86_64/checker.cliff_count\t378075\n'
assert move['before']==373694 and move['after']==378075 and move['default_tolerance_percent']==1
assert v['exit_code']==0
assert[(r['label'],r['expected_exit'],r['exit_code'])for r in v['runs']]==[('projected-old-gate',1,1),('projected-new-gate',0,0),('full-old-partial-report',1,1),('full-new-partial-report',1,1)]
for label in ['full-old-partial-report','full-new-partial-report']:
 b=gzip.decompress(data(label+'.stdout.gz'))+gzip.decompress(data(label+'.stderr.gz'));assert b'MISSING: x86_64/checker.cliff_bytes'in b,label
original=git('show',reuse['commit']+':'+reuse['archive']+'/manifest.json');assert sha(original)==reuse['manifest_sha256']
original_m=json.loads(original)
for name,h in original_m['data_sha256'].items():assert sha(git('show',reuse['commit']+':'+reuse['archive']+'/'+name))==h,name
previous=json.loads(git('show',reuse['commit']+':'+reuse['archive']+'/validation.json'))
original_source=json.loads(git('show',reuse['commit']+':'+reuse['archive']+'/source-identities.json'))['source']
for name in ['scripts/ci-check-perf','scripts/flag-lib','scripts/perf-gate-selftest.mjs']:
 assert read(Path(name))==git('show',original_source+':'+name),name
assert previous['exit_code']==0 and previous['captured_comparator_children']==reuse['canonical_control_results']==22
assert reuse['canonical_control_commands_executed']==0 and len(original_m['data_sha256'])==reuse['verified_data_hashes']==37
receipts=json.loads(data('run-and-job-receipts.json'));assert receipts['run']['head_sha']==m['source']
assert{j['id']for j in receipts['jobs']}=={130273056431,130273056388,130273056323}
assert all(j['conclusion']=='success'for j in receipts['jobs'])
assert all(j['head_sha']==m['source'] and j['run_id']==receipts['run']['id']for j in receipts['jobs'])
print(json.dumps({'data_hashes':len(m['data_sha256']),'gzip_identities':len(m['gzip_identities']),'original_controls':22,'original_data_hashes':37,'updated_row':378075,'full_partial_reports_rejected':True,'exit_code':0}))
